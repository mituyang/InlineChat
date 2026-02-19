package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"inlinechat/services/chat-service/internal/model"
	"inlinechat/services/chat-service/internal/repository"
)

var (
	ErrConversationNotFound       = errors.New("conversation not found")
	ErrConversationAlreadyClaimed = errors.New("conversation already claimed by another agent")
	ErrConversationUnassigned     = errors.New("conversation is unassigned")
	ErrConversationClosed         = errors.New("conversation is already closed")
	ErrMessageNotFound            = errors.New("message not found")
	ErrForbidden                  = errors.New("forbidden")
)

const (
	MessageStatusSent      = "sent"
	MessageStatusDelivered = "delivered"
	MessageStatusRead      = "read"
)

type ChatService struct {
	conversationRepo repository.ConversationRepository
	messageRepo      repository.MessageRepository
	publisher        MessageEventPublisher
	logger           *zap.Logger
}

type MessageEventPublisher interface {
	PublishMessageCreated(ctx context.Context, message *model.Message) error
	PublishMessageStatus(ctx context.Context, conversationID uint64, messageID uint64, status string) error
	PublishMessageStatusRange(ctx context.Context, conversationID uint64, senderType string, upToMessageID uint64, status string) error
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

func New(conversationRepo repository.ConversationRepository, messageRepo repository.MessageRepository, logger *zap.Logger, publisher MessageEventPublisher) *ChatService {
	if publisher == nil {
		publisher = noopMessageEventPublisher{}
	}

	return &ChatService{
		conversationRepo: conversationRepo,
		messageRepo:      messageRepo,
		publisher:        publisher,
		logger:           logger,
	}
}

func (s *ChatService) CreateConversation(ctx context.Context, input CreateConversationInput) (*model.Conversation, error) {
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
	conversation, err := s.conversationRepo.GetByID(ctx, input.ConversationID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}

	if input.SenderType == "visitor" && input.VisitorToken != "" && conversation.VisitorToken != input.VisitorToken {
		return nil, fmt.Errorf("visitor token does not match conversation")
	}

	existing, err := s.messageRepo.GetByClientMsgID(ctx, input.ConversationID, input.ClientMsgID)
	if err == nil {
		return existing, nil
	}
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	message := &model.Message{
		ConversationID: input.ConversationID,
		SenderType:     input.SenderType,
		SenderID:       input.SenderID,
		Content:        input.Content,
		ClientMsgID:    input.ClientMsgID,
		Status:         MessageStatusSent,
	}

	if err := s.messageRepo.Create(ctx, message); err != nil {
		if isDuplicateMessageErr(err) {
			existing, findErr := s.messageRepo.GetByClientMsgID(ctx, input.ConversationID, input.ClientMsgID)
			if findErr == nil {
				return existing, nil
			}
			if !errors.Is(findErr, repository.ErrNotFound) {
				return nil, findErr
			}
		}
		return nil, err
	}

	s.logger.Info("message created",
		zap.Uint64("conversation_id", input.ConversationID),
		zap.Uint64("message_id", message.ID),
	)
	if err := s.publisher.PublishMessageCreated(ctx, message); err != nil {
		s.logger.Warn("publish message.new event failed",
			zap.Error(err),
			zap.Uint64("conversation_id", input.ConversationID),
			zap.Uint64("message_id", message.ID),
		)
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

	message, err := s.messageRepo.GetByID(ctx, conversationID, messageID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MarkMessageDeliveredResult{}, ErrMessageNotFound
		}
		return MarkMessageDeliveredResult{}, err
	}

	switch message.Status {
	case MessageStatusRead, MessageStatusDelivered:
		return MarkMessageDeliveredResult{Updated: false, Status: message.Status}, nil
	}

	updated, err := s.messageRepo.MarkDelivered(ctx, conversationID, messageID)
	if err != nil {
		return MarkMessageDeliveredResult{}, err
	}
	if updated {
		if err := s.publisher.PublishMessageStatus(ctx, conversationID, messageID, MessageStatusDelivered); err != nil {
			s.logger.Warn("publish message.status delivered event failed",
				zap.Error(err),
				zap.Uint64("conversation_id", conversationID),
				zap.Uint64("message_id", messageID),
			)
		}
		return MarkMessageDeliveredResult{Updated: true, Status: MessageStatusDelivered}, nil
	}

	latest, err := s.messageRepo.GetByID(ctx, conversationID, messageID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MarkMessageDeliveredResult{}, ErrMessageNotFound
		}
		return MarkMessageDeliveredResult{}, err
	}
	return MarkMessageDeliveredResult{Updated: false, Status: latest.Status}, nil
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

	conversation, err := s.conversationRepo.GetByID(ctx, input.ConversationID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return 0, ErrConversationNotFound
		}
		return 0, err
	}

	targetSenderType := "visitor"
	if actorType == "visitor" {
		if strings.TrimSpace(input.VisitorToken) == "" {
			return 0, fmt.Errorf("visitor_token is required")
		}
		if conversation.VisitorToken != input.VisitorToken {
			return 0, fmt.Errorf("visitor token does not match conversation")
		}
		targetSenderType = "agent"
	} else {
		if input.ActorAgentID == 0 {
			return 0, fmt.Errorf("actor_agent_id is required")
		}
	}

	rows, err := s.messageRepo.MarkReadByConversationAndSender(ctx, input.ConversationID, targetSenderType, input.LastReadMessageID)
	if err != nil {
		return 0, err
	}
	if rows < 0 {
		rows = 0
	}
	if rows > 0 {
		if err := s.publisher.PublishMessageStatusRange(ctx, input.ConversationID, targetSenderType, input.LastReadMessageID, MessageStatusRead); err != nil {
			s.logger.Warn("publish message.status read-range event failed",
				zap.Error(err),
				zap.Uint64("conversation_id", input.ConversationID),
				zap.String("sender_type", targetSenderType),
				zap.Uint64("up_to_message_id", input.LastReadMessageID),
				zap.Int64("updated_count", rows),
			)
		}
	}
	return uint64(rows), nil
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

	conversation, err := s.conversationRepo.Mutate(ctx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
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

		agentID := input.ToAgentID
		conversation.AssignedAgentID = &agentID
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

	conversation, err := s.conversationRepo.Mutate(ctx, input.ConversationID, func(conversation *model.Conversation) (bool, error) {
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
