package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"inlinechat/services/chat-service/internal/model"
	"inlinechat/services/chat-service/internal/repository"
)

const autoCloseMutationTimeout = 5 * time.Second

type autoCloseTimerEntry struct {
	timer *time.Timer
	dueAt time.Time
}

type autoCloseScheduler struct {
	mu     sync.Mutex
	timers map[uint64]autoCloseTimerEntry
	onDue  func(conversationID uint64, dueAt time.Time)
}

func newAutoCloseScheduler(onDue func(conversationID uint64, dueAt time.Time)) *autoCloseScheduler {
	return &autoCloseScheduler{
		timers: make(map[uint64]autoCloseTimerEntry),
		onDue:  onDue,
	}
}

func (s *autoCloseScheduler) Schedule(conversationID uint64, dueAt time.Time) {
	if conversationID == 0 || s == nil {
		return
	}

	s.mu.Lock()
	if current, ok := s.timers[conversationID]; ok {
		if current.dueAt.Equal(dueAt) {
			s.mu.Unlock()
			return
		}
		current.timer.Stop()
		delete(s.timers, conversationID)
	}

	delay := time.Until(dueAt)
	if delay < 0 {
		delay = 0
	}

	timer := time.AfterFunc(delay, func() {
		s.fire(conversationID, dueAt)
	})
	s.timers[conversationID] = autoCloseTimerEntry{
		timer: timer,
		dueAt: dueAt,
	}
	s.mu.Unlock()
}

func (s *autoCloseScheduler) Cancel(conversationID uint64) {
	if conversationID == 0 || s == nil {
		return
	}

	s.mu.Lock()
	if current, ok := s.timers[conversationID]; ok {
		current.timer.Stop()
		delete(s.timers, conversationID)
	}
	s.mu.Unlock()
}

func (s *autoCloseScheduler) Stop() {
	if s == nil {
		return
	}

	s.mu.Lock()
	for conversationID, current := range s.timers {
		current.timer.Stop()
		delete(s.timers, conversationID)
	}
	s.mu.Unlock()
}

func (s *autoCloseScheduler) fire(conversationID uint64, dueAt time.Time) {
	s.mu.Lock()
	current, ok := s.timers[conversationID]
	if !ok || !current.dueAt.Equal(dueAt) {
		s.mu.Unlock()
		return
	}
	delete(s.timers, conversationID)
	s.mu.Unlock()

	if s.onDue != nil {
		s.onDue(conversationID, dueAt)
	}
}

func (s *ChatService) StartAutoCloseScheduler(ctx context.Context) error {
	if s.autoCloseScheduler == nil {
		return nil
	}

	const batchSize = 200
	conversationIDs := make([]uint64, 0, batchSize)

	for offset := 0; ; offset += batchSize {
		conversations, err := s.conversationRepo.List(ctx, repository.ListConversationsFilter{
			Status: "open",
			Limit:  batchSize,
			Offset: offset,
		})
		if err != nil {
			return err
		}
		if len(conversations) == 0 {
			break
		}
		for i := range conversations {
			conversationIDs = append(conversationIDs, conversations[i].ID)
		}
		if len(conversations) < batchSize {
			break
		}
	}

	var scheduledCount int

	for _, conversationID := range conversationIDs {
		latest, err := s.messageRepo.GetLatestByConversationExcludingSystem(ctx, conversationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return err
		}
		if latest.SenderType != "agent" {
			continue
		}

		deadline := latest.CreatedAt.Add(s.autoCloseAfter)
		if deadline.After(time.Now()) {
			s.autoCloseScheduler.Schedule(conversationID, deadline)
			scheduledCount++
			continue
		}

		if err := s.autoCloseConversationByTimeout(ctx, conversationID); err != nil {
			return err
		}
	}

	s.logger.Info("auto-close scheduler bootstrapped", zap.Int("scheduled_count", scheduledCount))
	return nil
}

func (s *ChatService) StopAutoCloseScheduler() {
	if s.autoCloseScheduler != nil {
		s.autoCloseScheduler.Stop()
	}
}

func (s *ChatService) onMessageCreatedForAutoClose(message *model.Message) {
	if s.autoCloseScheduler == nil || message == nil {
		return
	}

	switch message.SenderType {
	case "agent":
		s.autoCloseScheduler.Schedule(message.ConversationID, message.CreatedAt.Add(s.autoCloseAfter))
	case "visitor":
		s.autoCloseScheduler.Cancel(message.ConversationID)
	}
}

func (s *ChatService) autoCloseConversationByTimeout(ctx context.Context, conversationID uint64) error {
	if conversationID == 0 {
		return nil
	}

	var closed bool
	var rescheduleAt *time.Time
	err := s.withEventTransaction(ctx, func(txCtx context.Context) error {
		_, mutateErr := s.conversationRepo.Mutate(txCtx, conversationID, func(conversation *model.Conversation) (bool, error) {
			if conversation.Status != "open" {
				return false, nil
			}

			latest, latestErr := s.messageRepo.GetLatestByConversationExcludingSystem(txCtx, conversation.ID)
			if latestErr != nil {
				if errors.Is(latestErr, repository.ErrNotFound) {
					return false, nil
				}
				return false, latestErr
			}
			if latest.SenderType != "agent" {
				return false, nil
			}

			now := time.Now()
			deadline := latest.CreatedAt.Add(s.autoCloseAfter)
			if deadline.After(now) {
				next := deadline
				rescheduleAt = &next
				return false, nil
			}

			conversation.Status = "closed"
			conversation.ClosedAt = &now
			if conversation.AssignedAgentID != nil {
				closedBy := *conversation.AssignedAgentID
				conversation.ClosedByAgentID = &closedBy
			} else {
				conversation.ClosedByAgentID = nil
			}
			closed = true
			return true, nil
		})
		if mutateErr != nil {
			if errors.Is(mutateErr, repository.ErrNotFound) {
				return nil
			}
			return mutateErr
		}

		if closed {
			if emitErr := s.emitConversationClosed(txCtx, conversationID); emitErr != nil {
				return emitErr
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if rescheduleAt != nil && s.autoCloseScheduler != nil {
		s.autoCloseScheduler.Schedule(conversationID, *rescheduleAt)
		return nil
	}
	if !closed {
		return nil
	}

	if s.autoCloseScheduler != nil {
		s.autoCloseScheduler.Cancel(conversationID)
	}
	s.logger.Info("conversation auto closed due to visitor inactivity",
		zap.Uint64("conversation_id", conversationID),
		zap.Duration("inactivity", s.autoCloseAfter),
	)
	return nil
}

func (s *ChatService) onAutoCloseDue(conversationID uint64, dueAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), autoCloseMutationTimeout)
	defer cancel()

	if err := s.autoCloseConversationByTimeout(ctx, conversationID); err != nil {
		s.logger.Warn("auto close due conversation failed",
			zap.Error(err),
			zap.Uint64("conversation_id", conversationID),
			zap.Time("due_at", dueAt),
		)
	}
}
