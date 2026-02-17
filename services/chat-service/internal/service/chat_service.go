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
	ErrForbidden                  = errors.New("forbidden")
)

type ChatService struct {
	conversationRepo repository.ConversationRepository
	messageRepo      repository.MessageRepository
	publisher        MessageEventPublisher
	logger           *zap.Logger
}

type MessageEventPublisher interface {
	PublishMessageCreated(ctx context.Context, message *model.Message) error
}

type noopMessageEventPublisher struct{}

func (noopMessageEventPublisher) PublishMessageCreated(context.Context, *model.Message) error {
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

	message := &model.Message{
		ConversationID: input.ConversationID,
		SenderType:     input.SenderType,
		SenderID:       input.SenderID,
		Content:        input.Content,
		ClientMsgID:    input.ClientMsgID,
	}

	if err := s.messageRepo.Create(ctx, message); err != nil {
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
