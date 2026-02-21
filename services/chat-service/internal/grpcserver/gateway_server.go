package grpcserver

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatv1 "inlinechat/services/chat-service/internal/gen/chatv1"
	"inlinechat/services/chat-service/internal/model"
	"inlinechat/services/chat-service/internal/service"
)

type ChatGatewayServer struct {
	chatv1.UnimplementedChatGatewayServiceServer
	chatService *service.ChatService
}

func NewGateway(chatService *service.ChatService) *ChatGatewayServer {
	return &ChatGatewayServer{chatService: chatService}
}

func (s *ChatGatewayServer) CreateConversation(ctx context.Context, req *chatv1.CreateConversationRequest) (*chatv1.Conversation, error) {
	if strings.TrimSpace(req.GetSiteId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "site_id is required")
	}
	if strings.TrimSpace(req.GetVisitorToken()) == "" {
		return nil, status.Error(codes.InvalidArgument, "visitor_token is required")
	}

	conversation, err := s.chatService.CreateConversation(ctx, service.CreateConversationInput{
		SiteID:       req.GetSiteId(),
		VisitorToken: req.GetVisitorToken(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return toConversationPB(conversation), nil
}

func (s *ChatGatewayServer) GetConversation(ctx context.Context, req *chatv1.GetConversationRequest) (*chatv1.Conversation, error) {
	if req.GetId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	conversation, err := s.chatService.GetConversation(ctx, req.GetId())
	if err != nil {
		return nil, mapError(err)
	}
	return toConversationPB(conversation), nil
}

func (s *ChatGatewayServer) ListConversations(ctx context.Context, req *chatv1.ListConversationsRequest) (*chatv1.ListConversationsResponse, error) {
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}

	offset := int(req.GetOffset())
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "offset must be greater than or equal to 0")
	}

	var assignedAgentID *uint64
	if req.GetAssignedAgentId() > 0 {
		assignedAgentID = new(uint64)
		*assignedAgentID = req.GetAssignedAgentId()
	}

	items, err := s.chatService.ListConversations(ctx, service.ListConversationsInput{
		Status:          req.GetStatus(),
		SiteID:          req.GetSiteId(),
		AssignedAgentID: assignedAgentID,
		UnassignedOnly:  req.GetUnassignedOnly(),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &chatv1.ListConversationsResponse{
		Items: make([]*chatv1.Conversation, 0, len(items)),
	}
	for i := range items {
		conversation := items[i]
		resp.Items = append(resp.Items, toConversationPB(&conversation))
	}

	return resp, nil
}

func (s *ChatGatewayServer) CreateMessage(ctx context.Context, req *chatv1.CreateMessageRequest) (*chatv1.Message, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	if strings.TrimSpace(req.GetSenderType()) == "" {
		return nil, status.Error(codes.InvalidArgument, "sender_type is required")
	}
	if strings.TrimSpace(req.GetContent()) == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}
	if strings.TrimSpace(req.GetClientMsgId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "client_msg_id is required")
	}

	message, err := s.chatService.CreateMessage(ctx, service.CreateMessageInput{
		ConversationID: req.GetConversationId(),
		SenderType:     req.GetSenderType(),
		SenderID:       req.GetSenderId(),
		Content:        req.GetContent(),
		ClientMsgID:    req.GetClientMsgId(),
		VisitorToken:   req.GetVisitorToken(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	return toMessagePB(message), nil
}

func (s *ChatGatewayServer) ListMessages(ctx context.Context, req *chatv1.ListMessagesRequest) (*chatv1.ListMessagesResponse, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	items, err := s.chatService.ListMessages(ctx, req.GetConversationId(), limit, req.GetBeforeId())
	if err != nil {
		return nil, mapError(err)
	}

	resp := &chatv1.ListMessagesResponse{
		Items: make([]*chatv1.Message, 0, len(items)),
	}
	for i := range items {
		msg := items[i]
		resp.Items = append(resp.Items, toMessagePB(&msg))
	}
	return resp, nil
}

func (s *ChatGatewayServer) MarkMessagesRead(ctx context.Context, req *chatv1.MarkMessagesReadRequest) (*chatv1.MarkMessagesReadResponse, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	if req.GetLastReadMessageId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "last_read_message_id is required")
	}
	if strings.TrimSpace(req.GetActorType()) == "" {
		return nil, status.Error(codes.InvalidArgument, "actor_type is required")
	}

	updatedCount, err := s.chatService.MarkMessagesRead(ctx, service.MarkMessagesReadInput{
		ConversationID:    req.GetConversationId(),
		LastReadMessageID: req.GetLastReadMessageId(),
		ActorType:         req.GetActorType(),
		ActorAgentID:      req.GetActorAgentId(),
		VisitorToken:      req.GetVisitorToken(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &chatv1.MarkMessagesReadResponse{
		UpdatedCount: updatedCount,
	}, nil
}

func (s *ChatGatewayServer) ClaimConversation(ctx context.Context, req *chatv1.ClaimConversationRequest) (*chatv1.Conversation, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	if req.GetAgentId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	conversation, err := s.chatService.ClaimConversation(ctx, service.ClaimConversationInput{
		ConversationID: req.GetConversationId(),
		AgentID:        req.GetAgentId(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return toConversationPB(conversation), nil
}

func (s *ChatGatewayServer) TransferConversation(ctx context.Context, req *chatv1.TransferConversationRequest) (*chatv1.Conversation, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	if req.GetActorAgentId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "actor_agent_id is required")
	}
	if req.GetToAgentId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "to_agent_id is required")
	}
	if strings.TrimSpace(req.GetActorRole()) == "" {
		return nil, status.Error(codes.InvalidArgument, "actor_role is required")
	}

	conversation, err := s.chatService.TransferConversation(ctx, service.TransferConversationInput{
		ConversationID: req.GetConversationId(),
		ActorAgentID:   req.GetActorAgentId(),
		ActorRole:      req.GetActorRole(),
		ToAgentID:      req.GetToAgentId(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return toConversationPB(conversation), nil
}

func (s *ChatGatewayServer) ConfirmTransferConversation(ctx context.Context, req *chatv1.ConfirmTransferConversationRequest) (*chatv1.Conversation, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	if req.GetActorAgentId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "actor_agent_id is required")
	}
	if strings.TrimSpace(req.GetActorRole()) == "" {
		return nil, status.Error(codes.InvalidArgument, "actor_role is required")
	}

	conversation, err := s.chatService.ConfirmTransferConversation(ctx, service.ConfirmTransferConversationInput{
		ConversationID: req.GetConversationId(),
		ActorAgentID:   req.GetActorAgentId(),
		ActorRole:      req.GetActorRole(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return toConversationPB(conversation), nil
}

func (s *ChatGatewayServer) CloseConversation(ctx context.Context, req *chatv1.CloseConversationRequest) (*chatv1.Conversation, error) {
	if req.GetConversationId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "conversation_id is required")
	}
	if req.GetActorAgentId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "actor_agent_id is required")
	}
	if strings.TrimSpace(req.GetActorRole()) == "" {
		return nil, status.Error(codes.InvalidArgument, "actor_role is required")
	}

	conversation, err := s.chatService.CloseConversation(ctx, service.CloseConversationInput{
		ConversationID: req.GetConversationId(),
		ActorAgentID:   req.GetActorAgentId(),
		ActorRole:      req.GetActorRole(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return toConversationPB(conversation), nil
}

func toConversationPB(conversation *model.Conversation) *chatv1.Conversation {
	var assignedAgentID uint64
	if conversation.AssignedAgentID != nil {
		assignedAgentID = *conversation.AssignedAgentID
	}

	var closedAt string
	if conversation.ClosedAt != nil {
		closedAt = conversation.ClosedAt.Format(time.RFC3339Nano)
	}

	var closedByAgentID uint64
	if conversation.ClosedByAgentID != nil {
		closedByAgentID = *conversation.ClosedByAgentID
	}

	var pendingTransferToAgentID uint64
	if conversation.PendingTransferToAgentID != nil {
		pendingTransferToAgentID = *conversation.PendingTransferToAgentID
	}

	var pendingTransferFromAgentID uint64
	if conversation.PendingTransferFromAgentID != nil {
		pendingTransferFromAgentID = *conversation.PendingTransferFromAgentID
	}

	var pendingTransferRequestedAt string
	if conversation.PendingTransferRequestedAt != nil {
		pendingTransferRequestedAt = conversation.PendingTransferRequestedAt.Format(time.RFC3339Nano)
	}

	return &chatv1.Conversation{
		Id:                         conversation.ID,
		SiteId:                     conversation.SiteID,
		VisitorToken:               conversation.VisitorToken,
		Status:                     conversation.Status,
		CreatedAt:                  conversation.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:                  conversation.UpdatedAt.Format(time.RFC3339Nano),
		AssignedAgentId:            assignedAgentID,
		ClosedAt:                   closedAt,
		ClosedByAgentId:            closedByAgentID,
		PendingTransferToAgentId:   pendingTransferToAgentID,
		PendingTransferFromAgentId: pendingTransferFromAgentID,
		PendingTransferRequestedAt: pendingTransferRequestedAt,
	}
}

func toMessagePB(message *model.Message) *chatv1.Message {
	return &chatv1.Message{
		Id:             message.ID,
		ConversationId: message.ConversationID,
		SenderType:     message.SenderType,
		SenderId:       message.SenderID,
		Content:        message.Content,
		ClientMsgId:    message.ClientMsgID,
		CreatedAt:      message.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      message.UpdatedAt.Format(time.RFC3339Nano),
		Status:         message.Status,
	}
}
