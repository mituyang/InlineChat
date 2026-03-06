package grpcserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatv1 "inlinechat/services/chat-service/internal/gen/chatv1"
	"inlinechat/services/chat-service/internal/service"
)

type ChatInternalServer struct {
	chatv1.UnimplementedChatInternalServiceServer
	// internal 接口面向 realtime 等内部服务，收敛消息写入与状态推进。
	chatService *service.ChatService
}

func New(chatService *service.ChatService) *ChatInternalServer {
	return &ChatInternalServer{chatService: chatService}
}

// CreateMessage 提供内部发消息入口，参数更严格，强调幂等 client_msg_id。
func (s *ChatInternalServer) CreateMessage(ctx context.Context, req *chatv1.CreateMessageRequest) (*chatv1.CreateMessageResponse, error) {
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

	return &chatv1.CreateMessageResponse{
		Id:             message.ID,
		ConversationId: message.ConversationID,
		SenderType:     message.SenderType,
		SenderId:       message.SenderID,
		Content:        message.Content,
		ClientMsgId:    message.ClientMsgID,
		CreatedAt:      message.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      message.UpdatedAt.Format(time.RFC3339Nano),
		Status:         message.Status,
	}, nil
}

// mapError 将领域错误映射到 gRPC code，减少调用方分支复杂度。
func mapError(err error) error {
	if errors.Is(err, service.ErrConversationNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, service.ErrMessageNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, service.ErrForbidden) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if errors.Is(err, service.ErrConversationAlreadyClaimed) {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	if errors.Is(err, service.ErrConversationUnassigned) ||
		errors.Is(err, service.ErrConversationClosed) ||
		errors.Is(err, service.ErrConversationTransferPending) ||
		errors.Is(err, service.ErrConversationTransferNotPending) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "invalid"), strings.Contains(msg, "required"):
		return status.Error(codes.InvalidArgument, err.Error())
	case strings.Contains(msg, "match conversation"):
		return status.Error(codes.PermissionDenied, err.Error())
	case strings.Contains(msg, "duplicate"), strings.Contains(msg, "unique"):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
