package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"inlinechat/services/chat-service/internal/model"
	"inlinechat/services/chat-service/internal/repository"
)

var (
	ErrConversationNotFound           = errors.New("conversation not found")
	ErrConversationAlreadyClaimed     = errors.New("conversation already claimed by another agent")
	ErrConversationUnassigned         = errors.New("conversation is unassigned")
	ErrConversationClosed             = errors.New("conversation is already closed")
	ErrConversationTransferPending    = errors.New("conversation transfer is pending confirmation")
	ErrConversationTransferNotPending = errors.New("conversation transfer is not pending confirmation")
	ErrMessageNotFound                = errors.New("message not found")
	ErrForbidden                      = errors.New("forbidden")
)

const (
	MessageStatusSent      = "sent"
	MessageStatusDelivered = "delivered"
	MessageStatusRead      = "read"
	MaxMessageContentChars = 2000
)

type ChatService struct {
	conversationRepo   repository.ConversationRepository
	messageRepo        repository.MessageRepository
	txManager          repository.TransactionManager
	outboxRepo         repository.EventOutboxRepository
	outboxNotifier     OutboxEventNotifier
	publisher          MessageEventPublisher
	logger             *zap.Logger
	autoCloseAfter     time.Duration
	autoCloseScheduler *autoCloseScheduler
}

type OutboxEventNotifier interface {
	NotifyOutbox(ctx context.Context) error
}

type outboxNotifyContextKey struct{}

type outboxNotifyState struct {
	shouldNotify bool
}

type MessageEventPublisher interface {
	PublishMessageCreated(ctx context.Context, message *model.Message) error
	PublishMessageStatus(ctx context.Context, conversationID uint64, messageID uint64, status string) error
	PublishMessageStatusRange(ctx context.Context, conversationID uint64, senderType string, upToMessageID uint64, status string) error
	PublishConversationClosed(ctx context.Context, conversationID uint64) error
}

type noopMessageEventPublisher struct{}

func (noopMessageEventPublisher) PublishMessageCreated(context.Context, *model.Message) error {
	return nil
}

func (noopMessageEventPublisher) PublishMessageStatus(context.Context, uint64, uint64, string) error {
	return nil
}

func (noopMessageEventPublisher) PublishMessageStatusRange(context.Context, uint64, string, uint64, string) error {
	return nil
}

func (noopMessageEventPublisher) PublishConversationClosed(context.Context, uint64) error {
	return nil
}

type CreateConversationInput struct {
	SiteID       string
	VisitorToken string
}

type CreateMessageInput struct {
	ConversationID uint64
	SenderType     string
	SenderID       string
	Content        string
	ClientMsgID    string
	VisitorToken   string
}

type ListConversationsInput struct {
	Status          string
	SiteID          string
	AssignedAgentID *uint64
	UnassignedOnly  bool
	Limit           int
	Offset          int
}

type ClaimConversationInput struct {
	ConversationID uint64
	AgentID        uint64
}

type TransferConversationInput struct {
	ConversationID uint64
	ActorAgentID   uint64
	ActorRole      string
	ToAgentID      uint64
}

type ConfirmTransferConversationInput struct {
	ConversationID uint64
	ActorAgentID   uint64
	ActorRole      string
}

type RejectTransferConversationInput struct {
	ConversationID uint64
	ActorAgentID   uint64
	ActorRole      string
}

type CloseConversationInput struct {
	ConversationID uint64
	ActorAgentID   uint64
	ActorRole      string
}

type MarkMessagesReadInput struct {
	ConversationID    uint64
	LastReadMessageID uint64
	ActorType         string
	ActorAgentID      uint64
	VisitorToken      string
}

type MarkMessageDeliveredResult struct {
	Updated bool
	Status  string
}

func New(conversationRepo repository.ConversationRepository, messageRepo repository.MessageRepository, logger *zap.Logger, publisher MessageEventPublisher, autoCloseAfter time.Duration) *ChatService {
	if publisher == nil {
		publisher = noopMessageEventPublisher{}
	}

	svc := &ChatService{
		conversationRepo: conversationRepo,
		messageRepo:      messageRepo,
		publisher:        publisher,
		logger:           logger,
		autoCloseAfter:   autoCloseAfter,
	}

	if autoCloseAfter > 0 {
		svc.autoCloseScheduler = newAutoCloseScheduler(func(conversationID uint64, dueAt time.Time) {
			svc.onAutoCloseDue(conversationID, dueAt)
		})
	}

	return svc
}

func (s *ChatService) EnableEventOutbox(txManager repository.TransactionManager, outboxRepo repository.EventOutboxRepository) {
	if txManager == nil || outboxRepo == nil {
		return
	}
	s.txManager = txManager
	s.outboxRepo = outboxRepo
}

func (s *ChatService) SetOutboxNotifier(notifier OutboxEventNotifier) {
	s.outboxNotifier = notifier
}

func (s *ChatService) eventOutboxEnabled() bool {
	return s.txManager != nil && s.outboxRepo != nil
}

func (s *ChatService) withEventTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.eventOutboxEnabled() {
		return fn(ctx)
	}
	notifyState := &outboxNotifyState{}
	txCtx := context.WithValue(ctx, outboxNotifyContextKey{}, notifyState)
	if err := s.txManager.WithTransaction(txCtx, fn); err != nil {
		return err
	}
	if notifyState.shouldNotify && s.outboxNotifier != nil {
		if err := s.outboxNotifier.NotifyOutbox(ctx); err != nil {
			s.logger.Warn("notify outbox dispatcher failed", zap.Error(err))
		}
	}
	return nil
}

func (s *ChatService) CreateConversation(ctx context.Context, input CreateConversationInput) (*model.Conversation, error) {
	input.SiteID = strings.TrimSpace(input.SiteID)
	input.VisitorToken = strings.TrimSpace(input.VisitorToken)
	if input.SiteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}
	if input.VisitorToken == "" {
		return nil, fmt.Errorf("visitor_token is required")
	}

	existing, err := s.conversationRepo.GetLatestOpenBySiteVisitor(ctx, input.SiteID, input.VisitorToken)
	if err == nil {
		return existing, nil
	}
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	conversation := &model.Conversation{
		SiteID:       input.SiteID,
		VisitorToken: input.VisitorToken,
		Status:       "open",
	}

	if err := s.conversationRepo.Create(ctx, conversation); err != nil {
		return nil, err
	}

	return conversation, nil
}

func (s *ChatService) GetConversation(ctx context.Context, id uint64) (*model.Conversation, error) {
	conversation, err := s.conversationRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return conversation, nil
}

func (s *ChatService) ListConversations(ctx context.Context, input ListConversationsInput) ([]model.Conversation, error) {
	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 200 {
		return nil, fmt.Errorf("invalid limit")
	}
	if input.Offset < 0 {
		return nil, fmt.Errorf("invalid offset")
	}
	if input.Status != "" && input.Status != "open" && input.Status != "closed" {
		return nil, fmt.Errorf("invalid status")
	}

	return s.conversationRepo.List(ctx, repository.ListConversationsFilter{
		Status:          input.Status,
		SiteID:          input.SiteID,
		AssignedAgentID: input.AssignedAgentID,
		UnassignedOnly:  input.UnassignedOnly,
		Limit:           input.Limit,
		Offset:          input.Offset,
	})
}

func (s *ChatService) CreateMessage(ctx context.Context, input CreateMessageInput) (*model.Message, error) {
	if input.SenderType != "visitor" && input.SenderType != "agent" && input.SenderType != "system" {
		return nil, fmt.Errorf("invalid sender_type")
	}
	if strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("content is required")
	}
	if utf8.RuneCountInString(input.Content) > MaxMessageContentChars {
		return nil, fmt.Errorf("invalid content: too long (max %d characters)", MaxMessageContentChars)
	}
	var (
		message *model.Message
		created bool
	)

	err := s.withEventTransaction(ctx, func(txCtx context.Context) error {
		conversation, err := s.conversationRepo.GetByID(txCtx, input.ConversationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrConversationNotFound
			}
			return err
		}

		if conversation.Status == "closed" {
			return ErrConversationClosed
		}

		if input.SenderType == "visitor" {
			input.VisitorToken = strings.TrimSpace(input.VisitorToken)
			if input.VisitorToken == "" {
				return fmt.Errorf("visitor_token is required")
			}
			if conversation.VisitorToken != input.VisitorToken {
				return fmt.Errorf("visitor token does not match conversation")
			}
		}

		if input.SenderType == "agent" {
			input.SenderID = strings.TrimSpace(input.SenderID)
			if input.SenderID == "" {
				return fmt.Errorf("sender_id is required for agent sender_type")
			}
			senderAgentID, parseErr := strconv.ParseUint(input.SenderID, 10, 64)
			if parseErr != nil || senderAgentID == 0 {
				return fmt.Errorf("invalid sender_id")
			}
			if conversation.AssignedAgentID == nil {
				return ErrConversationUnassigned
			}
			if *conversation.AssignedAgentID != senderAgentID {
				return ErrForbidden
			}
		}

		existing, err := s.messageRepo.GetByClientMsgID(txCtx, input.ConversationID, input.ClientMsgID)
		if err == nil {
			message = existing
			return nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return err
		}

		message = &model.Message{
			ConversationID: input.ConversationID,
			SenderType:     input.SenderType,
			SenderID:       input.SenderID,
			Content:        input.Content,
			ClientMsgID:    input.ClientMsgID,
			Status:         MessageStatusSent,
		}

		if err := s.messageRepo.Create(txCtx, message); err != nil {
			if isDuplicateMessageErr(err) {
				existing, findErr := s.messageRepo.GetByClientMsgID(txCtx, input.ConversationID, input.ClientMsgID)
				if findErr == nil {
					message = existing
					return nil
				}
				if !errors.Is(findErr, repository.ErrNotFound) {
					return findErr
				}
			}
			return err
		}

		if err := s.emitMessageCreated(txCtx, message); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}

	if created {
		s.logger.Info("message created",
			zap.Uint64("conversation_id", input.ConversationID),
			zap.Uint64("message_id", message.ID),
		)
		s.onMessageCreatedForAutoClose(message)
	}

	return message, nil
}

func (s *ChatService) MarkMessageDelivered(ctx context.Context, conversationID uint64, messageID uint64) (MarkMessageDeliveredResult, error) {
	if conversationID == 0 {
		return MarkMessageDeliveredResult{}, fmt.Errorf("conversation_id is required")
	}
	if messageID == 0 {
		return MarkMessageDeliveredResult{}, fmt.Errorf("message_id is required")
	}

	var result MarkMessageDeliveredResult
	err := s.withEventTransaction(ctx, func(txCtx context.Context) error {
		message, err := s.messageRepo.GetByID(txCtx, conversationID, messageID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrMessageNotFound
			}
			return err
		}

		switch message.Status {
		case MessageStatusRead, MessageStatusDelivered:
			result = MarkMessageDeliveredResult{Updated: false, Status: message.Status}
			return nil
		}

		updated, err := s.messageRepo.MarkDelivered(txCtx, conversationID, messageID)
		if err != nil {
			return err
		}
		if updated {
			if err := s.emitMessageStatus(txCtx, conversationID, messageID, MessageStatusDelivered); err != nil {
				return err
			}
			result = MarkMessageDeliveredResult{Updated: true, Status: MessageStatusDelivered}
			return nil
		}

		latest, err := s.messageRepo.GetByID(txCtx, conversationID, messageID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrMessageNotFound
			}
			return err
		}
		result = MarkMessageDeliveredResult{Updated: false, Status: latest.Status}
		return nil
	})
	if err != nil {
		return MarkMessageDeliveredResult{}, err
	}
	return result, nil
}

func (s *ChatService) MarkMessagesRead(ctx context.Context, input MarkMessagesReadInput) (uint64, error) {
	if input.ConversationID == 0 {
		return 0, fmt.Errorf("conversation_id is required")
	}
	if input.LastReadMessageID == 0 {
		return 0, fmt.Errorf("last_read_message_id is required")
	}

	actorType, err := normalizeActorType(input.ActorType)
	if err != nil {
		return 0, err
	}

	var updatedRows int64
	err = s.withEventTransaction(ctx, func(txCtx context.Context) error {
		conversation, err := s.conversationRepo.GetByID(txCtx, input.ConversationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrConversationNotFound
			}
			return err
		}

		targetSenderType := "visitor"
		if actorType == "visitor" {
			if strings.TrimSpace(input.VisitorToken) == "" {
				return fmt.Errorf("visitor_token is required")
			}
			if conversation.VisitorToken != input.VisitorToken {
				return fmt.Errorf("visitor token does not match conversation")
			}
			targetSenderType = "agent"
		} else {
			if input.ActorAgentID == 0 {
				return fmt.Errorf("actor_agent_id is required")
			}
		}

		rows, err := s.messageRepo.MarkReadByConversationAndSender(txCtx, input.ConversationID, targetSenderType, input.LastReadMessageID)
		if err != nil {
			return err
		}
		if rows < 0 {
			rows = 0
		}
		if rows > 0 {
			if err := s.emitMessageStatusRange(txCtx, input.ConversationID, targetSenderType, input.LastReadMessageID, MessageStatusRead); err != nil {
				return err
			}
		}
		updatedRows = rows
		return nil
	})
	if err != nil {
		return 0, err
	}
	return uint64(updatedRows), nil
}

func isDuplicateMessageErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique") || strings.Contains(msg, "1062")
}

func (s *ChatService) ListMessages(ctx context.Context, conversationID uint64, limit int, beforeID uint64) ([]model.Message, error) {
	_, err := s.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return s.messageRepo.ListByConversation(ctx, conversationID, limit, beforeID)
}

func (s *ChatService) AutoCloseInactiveConversations(ctx context.Context, inactivity time.Duration) (int, error) {
	if inactivity <= 0 {
		return 0, fmt.Errorf("inactivity must be greater than 0")
	}

	const batchSize = 200
	cutoff := time.Now().Add(-inactivity)
	openConversationIDs := make([]uint64, 0, batchSize)

	for offset := 0; ; offset += batchSize {
		conversations, err := s.conversationRepo.List(ctx, repository.ListConversationsFilter{
			Status: "open",
			Limit:  batchSize,
			Offset: offset,
		})
		if err != nil {
			return 0, err
		}
		if len(conversations) == 0 {
			break
		}
		for i := range conversations {
			openConversationIDs = append(openConversationIDs, conversations[i].ID)
		}
		if len(conversations) < batchSize {
			break
		}
	}

	var closedCount int
	for _, conversationID := range openConversationIDs {
		latestMessage, err := s.messageRepo.GetLatestByConversationExcludingSystem(ctx, conversationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return closedCount, err
		}
		if latestMessage.SenderType != "agent" || latestMessage.CreatedAt.After(cutoff) {
			continue
		}

		changed := false
		err = s.withEventTransaction(ctx, func(txCtx context.Context) error {
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
				if latest.SenderType != "agent" || latest.CreatedAt.After(cutoff) {
					return false, nil
				}

				now := time.Now()
				conversation.Status = "closed"
				conversation.ClosedAt = &now
				if conversation.AssignedAgentID != nil {
					closedBy := *conversation.AssignedAgentID
					conversation.ClosedByAgentID = &closedBy
				} else {
					conversation.ClosedByAgentID = nil
				}
				changed = true
				return true, nil
			})
			if mutateErr != nil {
				if errors.Is(mutateErr, repository.ErrNotFound) {
					return nil
				}
				return mutateErr
			}

			if changed {
				if emitErr := s.emitConversationClosed(txCtx, conversationID); emitErr != nil {
					return emitErr
				}
			}
			return nil
		})
		if err != nil {
			return closedCount, err
		}
		if changed {
			closedCount++
			s.logger.Info("conversation auto closed due to visitor inactivity",
				zap.Uint64("conversation_id", conversationID),
				zap.Duration("inactivity", inactivity),
			)
		}
	}

	return closedCount, nil
}

func (s *ChatService) ClaimConversation(ctx context.Context, input ClaimConversationInput) (*model.Conversation, error) {
	if input.ConversationID == 0 {
		return nil, fmt.Errorf("conversation_id is required")
	}
	if input.AgentID == 0 {
		return nil, fmt.Errorf("agent_id is required")
	}

	conversation, err := s.conversationRepo.Mutate(ctx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
		if conversation.Status != "open" {
			return false, ErrConversationClosed
		}
		if conversation.AssignedAgentID != nil {
			if *conversation.AssignedAgentID == input.AgentID {
				return false, nil
			}
			return false, ErrConversationAlreadyClaimed
		}

		agentID := input.AgentID
		conversation.AssignedAgentID = &agentID
		conversation.PendingTransferToAgentID = nil
		conversation.PendingTransferFromAgentID = nil
		conversation.PendingTransferRequestedAt = nil
		return true, nil
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return conversation, nil
}

func (s *ChatService) TransferConversation(ctx context.Context, input TransferConversationInput) (*model.Conversation, error) {
	if input.ConversationID == 0 {
		return nil, fmt.Errorf("conversation_id is required")
	}
	if input.ActorAgentID == 0 {
		return nil, fmt.Errorf("actor_agent_id is required")
	}
	if input.ToAgentID == 0 {
		return nil, fmt.Errorf("to_agent_id is required")
	}

	actorRole, err := normalizeActorRole(input.ActorRole)
	if err != nil {
		return nil, err
	}

	changed := false
	var (
		targetAgentID uint64
		conversation  *model.Conversation
	)
	err = s.withEventTransaction(ctx, func(txCtx context.Context) error {
		var mutateErr error
		conversation, mutateErr = s.conversationRepo.Mutate(txCtx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
			if conversation.Status != "open" {
				return false, ErrConversationClosed
			}
			if conversation.AssignedAgentID == nil {
				return false, ErrConversationUnassigned
			}

			isAdmin := actorRole == "admin" || actorRole == "super_admin"
			if !isAdmin && *conversation.AssignedAgentID != input.ActorAgentID {
				return false, ErrForbidden
			}
			if *conversation.AssignedAgentID == input.ToAgentID {
				return false, nil
			}
			if conversation.PendingTransferToAgentID != nil {
				if *conversation.PendingTransferToAgentID == input.ToAgentID {
					return false, nil
				}
				return false, ErrConversationTransferPending
			}

			pendingTo := input.ToAgentID
			owner := *conversation.AssignedAgentID
			now := time.Now()

			conversation.PendingTransferToAgentID = &pendingTo
			conversation.PendingTransferFromAgentID = &owner
			conversation.PendingTransferRequestedAt = &now

			targetAgentID = pendingTo
			changed = true
			return true, nil
		})
		if mutateErr != nil {
			if errors.Is(mutateErr, repository.ErrNotFound) {
				return ErrConversationNotFound
			}
			return mutateErr
		}

		if changed {
			if err := s.publishTransferSystemMessage(txCtx, conversation.ID, "正在转接客服%s，等待对方确认", targetAgentID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return conversation, nil
}

func (s *ChatService) ConfirmTransferConversation(ctx context.Context, input ConfirmTransferConversationInput) (*model.Conversation, error) {
	if input.ConversationID == 0 {
		return nil, fmt.Errorf("conversation_id is required")
	}
	if input.ActorAgentID == 0 {
		return nil, fmt.Errorf("actor_agent_id is required")
	}

	actorRole, err := normalizeActorRole(input.ActorRole)
	if err != nil {
		return nil, err
	}

	changed := false
	var (
		targetAgentID uint64
		conversation  *model.Conversation
	)
	err = s.withEventTransaction(ctx, func(txCtx context.Context) error {
		var mutateErr error
		conversation, mutateErr = s.conversationRepo.Mutate(txCtx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
			if conversation.Status != "open" {
				return false, ErrConversationClosed
			}
			if conversation.PendingTransferToAgentID == nil {
				return false, ErrConversationTransferNotPending
			}

			isAdmin := actorRole == "admin" || actorRole == "super_admin"
			if !isAdmin && *conversation.PendingTransferToAgentID != input.ActorAgentID {
				return false, ErrForbidden
			}

			acceptedBy := *conversation.PendingTransferToAgentID
			conversation.AssignedAgentID = &acceptedBy
			conversation.PendingTransferToAgentID = nil
			conversation.PendingTransferFromAgentID = nil
			conversation.PendingTransferRequestedAt = nil

			targetAgentID = acceptedBy
			changed = true
			return true, nil
		})
		if mutateErr != nil {
			if errors.Is(mutateErr, repository.ErrNotFound) {
				return ErrConversationNotFound
			}
			return mutateErr
		}

		if changed {
			if err := s.publishTransferSystemMessage(txCtx, conversation.ID, "成功转接客服%s", targetAgentID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return conversation, nil
}

func (s *ChatService) RejectTransferConversation(ctx context.Context, input RejectTransferConversationInput) (*model.Conversation, error) {
	if input.ConversationID == 0 {
		return nil, fmt.Errorf("conversation_id is required")
	}
	if input.ActorAgentID == 0 {
		return nil, fmt.Errorf("actor_agent_id is required")
	}

	actorRole, err := normalizeActorRole(input.ActorRole)
	if err != nil {
		return nil, err
	}

	changed := false
	var (
		ownerAgentID  uint64
		targetAgentID uint64
		conversation  *model.Conversation
	)
	err = s.withEventTransaction(ctx, func(txCtx context.Context) error {
		var mutateErr error
		conversation, mutateErr = s.conversationRepo.Mutate(txCtx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
			if conversation.Status != "open" {
				return false, ErrConversationClosed
			}
			if conversation.PendingTransferToAgentID == nil {
				return false, ErrConversationTransferNotPending
			}

			isAdmin := actorRole == "admin" || actorRole == "super_admin"
			if !isAdmin && *conversation.PendingTransferToAgentID != input.ActorAgentID {
				return false, ErrForbidden
			}

			targetAgentID = *conversation.PendingTransferToAgentID
			if conversation.PendingTransferFromAgentID != nil {
				ownerAgentID = *conversation.PendingTransferFromAgentID
			} else if conversation.AssignedAgentID != nil {
				ownerAgentID = *conversation.AssignedAgentID
			}

			conversation.PendingTransferToAgentID = nil
			conversation.PendingTransferFromAgentID = nil
			conversation.PendingTransferRequestedAt = nil
			changed = true
			return true, nil
		})
		if mutateErr != nil {
			if errors.Is(mutateErr, repository.ErrNotFound) {
				return ErrConversationNotFound
			}
			return mutateErr
		}

		if changed {
			var content string
			if ownerAgentID > 0 {
				content = fmt.Sprintf("客服%s拒绝转接，当前会话继续由客服%s接待", formatAgentID4(targetAgentID), formatAgentID4(ownerAgentID))
			} else {
				content = fmt.Sprintf("客服%s拒绝转接", formatAgentID4(targetAgentID))
			}
			if err := s.publishSystemMessage(txCtx, conversation.ID, content); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return conversation, nil
}

func (s *ChatService) CloseConversation(ctx context.Context, input CloseConversationInput) (*model.Conversation, error) {
	if input.ConversationID == 0 {
		return nil, fmt.Errorf("conversation_id is required")
	}
	if input.ActorAgentID == 0 {
		return nil, fmt.Errorf("actor_agent_id is required")
	}

	actorRole, err := normalizeActorRole(input.ActorRole)
	if err != nil {
		return nil, err
	}

	changed := false
	var conversation *model.Conversation
	err = s.withEventTransaction(ctx, func(txCtx context.Context) error {
		var mutateErr error
		conversation, mutateErr = s.conversationRepo.Mutate(txCtx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
			if conversation.Status == "closed" {
				return false, nil
			}

			isAdmin := actorRole == "admin" || actorRole == "super_admin"
			if !isAdmin {
				if conversation.AssignedAgentID == nil || *conversation.AssignedAgentID != input.ActorAgentID {
					return false, ErrForbidden
				}
			}

			now := time.Now()
			closedBy := input.ActorAgentID
			conversation.Status = "closed"
			conversation.ClosedAt = &now
			conversation.ClosedByAgentID = &closedBy
			conversation.PendingTransferToAgentID = nil
			conversation.PendingTransferFromAgentID = nil
			conversation.PendingTransferRequestedAt = nil
			changed = true
			return true, nil
		})
		if mutateErr != nil {
			if errors.Is(mutateErr, repository.ErrNotFound) {
				return ErrConversationNotFound
			}
			return mutateErr
		}
		if changed {
			if err := s.emitConversationClosed(txCtx, conversation.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if conversation.Status == "closed" && s.autoCloseScheduler != nil {
		s.autoCloseScheduler.Cancel(conversation.ID)
	}
	return conversation, nil
}

func (s *ChatService) emitMessageCreated(ctx context.Context, message *model.Message) error {
	if message == nil {
		return nil
	}
	if !s.eventOutboxEnabled() {
		if err := s.publisher.PublishMessageCreated(ctx, message); err != nil {
			s.logger.Warn("publish message.new event failed",
				zap.Error(err),
				zap.Uint64("conversation_id", message.ConversationID),
				zap.Uint64("message_id", message.ID),
			)
		}
		return nil
	}

	payload := map[string]any{
		"type": "message.new",
		"payload": map[string]any{
			"conversation_id": message.ConversationID,
			"message": map[string]any{
				"id":              message.ID,
				"conversation_id": message.ConversationID,
				"sender_type":     message.SenderType,
				"sender_id":       message.SenderID,
				"content":         message.Content,
				"client_msg_id":   message.ClientMsgID,
				"status":          message.Status,
				"created_at":      message.CreatedAt.Format(time.RFC3339Nano),
				"updated_at":      message.UpdatedAt.Format(time.RFC3339Nano),
			},
		},
	}
	return s.enqueueOutboxEvent(ctx, message.ConversationID, "message.new", payload)
}

func (s *ChatService) emitMessageStatus(ctx context.Context, conversationID uint64, messageID uint64, status string) error {
	if conversationID == 0 || messageID == 0 {
		return nil
	}
	if !s.eventOutboxEnabled() {
		if err := s.publisher.PublishMessageStatus(ctx, conversationID, messageID, status); err != nil {
			s.logger.Warn("publish message.status event failed",
				zap.Error(err),
				zap.Uint64("conversation_id", conversationID),
				zap.Uint64("message_id", messageID),
				zap.String("status", status),
			)
		}
		return nil
	}

	payload := map[string]any{
		"type": "message.status",
		"payload": map[string]any{
			"conversation_id": conversationID,
			"message_id":      messageID,
			"status":          status,
		},
	}
	return s.enqueueOutboxEvent(ctx, conversationID, "message.status", payload)
}

func (s *ChatService) emitMessageStatusRange(ctx context.Context, conversationID uint64, senderType string, upToMessageID uint64, status string) error {
	if conversationID == 0 || upToMessageID == 0 {
		return nil
	}
	if !s.eventOutboxEnabled() {
		if err := s.publisher.PublishMessageStatusRange(ctx, conversationID, senderType, upToMessageID, status); err != nil {
			s.logger.Warn("publish message.status range event failed",
				zap.Error(err),
				zap.Uint64("conversation_id", conversationID),
				zap.String("sender_type", senderType),
				zap.Uint64("up_to_message_id", upToMessageID),
				zap.String("status", status),
			)
		}
		return nil
	}

	payload := map[string]any{
		"type": "message.status",
		"payload": map[string]any{
			"conversation_id":  conversationID,
			"sender_type":      senderType,
			"up_to_message_id": upToMessageID,
			"status":           status,
		},
	}
	return s.enqueueOutboxEvent(ctx, conversationID, "message.status", payload)
}

func (s *ChatService) emitConversationClosed(ctx context.Context, conversationID uint64) error {
	if conversationID == 0 {
		return nil
	}
	if !s.eventOutboxEnabled() {
		if err := s.publisher.PublishConversationClosed(ctx, conversationID); err != nil {
			s.logger.Warn("publish conversation.closed event failed",
				zap.Error(err),
				zap.Uint64("conversation_id", conversationID),
			)
		}
		return nil
	}

	payload := map[string]any{
		"type": "conversation.closed",
		"payload": map[string]any{
			"conversation_id": conversationID,
			"status":          "closed",
		},
	}
	return s.enqueueOutboxEvent(ctx, conversationID, "conversation.closed", payload)
}

func (s *ChatService) enqueueOutboxEvent(ctx context.Context, conversationID uint64, eventType string, payload map[string]any) error {
	if !s.eventOutboxEnabled() {
		return nil
	}
	if conversationID == 0 || strings.TrimSpace(eventType) == "" {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload failed: %w", err)
	}
	event := &model.EventOutbox{
		ConversationID: conversationID,
		EventType:      strings.TrimSpace(eventType),
		Payload:        string(raw),
		Status:         model.OutboxStatusPending,
	}
	if err := s.outboxRepo.Create(ctx, event); err != nil {
		return fmt.Errorf("create outbox event failed: %w", err)
	}
	if ctx != nil {
		if notifyState, ok := ctx.Value(outboxNotifyContextKey{}).(*outboxNotifyState); ok && notifyState != nil {
			notifyState.shouldNotify = true
		}
	}
	return nil
}

func (s *ChatService) publishTransferSystemMessage(ctx context.Context, conversationID uint64, template string, agentID uint64) error {
	return s.publishSystemMessage(ctx, conversationID, fmt.Sprintf(template, formatAgentID4(agentID)))
}

func (s *ChatService) publishSystemMessage(ctx context.Context, conversationID uint64, content string) error {
	if conversationID == 0 {
		return nil
	}

	msg := &model.Message{
		ConversationID: conversationID,
		SenderType:     "system",
		Content:        content,
		ClientMsgID:    systemClientMsgID(conversationID),
		Status:         MessageStatusSent,
	}
	if err := s.messageRepo.Create(ctx, msg); err != nil {
		return fmt.Errorf("create transfer system message failed: %w", err)
	}
	if err := s.emitMessageCreated(ctx, msg); err != nil {
		return fmt.Errorf("emit transfer system message failed: %w", err)
	}
	return nil
}

func systemClientMsgID(conversationID uint64) string {
	return fmt.Sprintf("sys_transfer_%d_%d", conversationID, time.Now().UTC().UnixNano())
}

func formatAgentID4(agentID uint64) string {
	if agentID > 0 && agentID <= 9999 {
		return fmt.Sprintf("%04d", agentID)
	}
	return strconv.FormatUint(agentID, 10)
}

func normalizeActorRole(role string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "agent" && role != "admin" && role != "super_admin" {
		return "", fmt.Errorf("invalid actor_role")
	}
	return role, nil
}

func normalizeActorType(actorType string) (string, error) {
	actorType = strings.ToLower(strings.TrimSpace(actorType))
	if actorType != "agent" && actorType != "visitor" {
		return "", fmt.Errorf("invalid actor_type")
	}
	return actorType, nil
}
