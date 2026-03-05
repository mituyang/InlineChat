package chatclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	chatv1 "inlinechat/services/realtime-service/internal/gen/chatv1"
)

type Client struct {
	conn    *grpc.ClientConn
	rpc     chatv1.ChatInternalServiceClient
	gateway chatv1.ChatGatewayServiceClient
}

type CreateMessageRequest struct {
	SenderType   string
	SenderID     string
	Content      string
	ClientMsgID  string
	VisitorToken string
}

type ListMessagesInput struct {
	Limit    int
	BeforeID uint64
}

type Message struct {
	ID             uint64 `json:"id"`
	ConversationID uint64 `json:"conversation_id"`
	SenderType     string `json:"sender_type"`
	SenderID       string `json:"sender_id,omitempty"`
	Content        string `json:"content"`
	ClientMsgID    string `json:"client_msg_id"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	Status         string `json:"status"`
}

type Conversation struct {
	ID              uint64
	VisitorToken    string
	AssignedAgentID uint64
	Status          string
}

type MarkMessageDeliveredResult struct {
	Updated bool
	Status  string
}

// New 建立到 chat-service 的 gRPC 连接。
func New(target string, dialTimeout time.Duration) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}

	return &Client{
		conn:    conn,
		rpc:     chatv1.NewChatInternalServiceClient(conn),
		gateway: chatv1.NewChatGatewayServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) CreateMessage(ctx context.Context, conversationID uint64, reqBody CreateMessageRequest) (*Message, error) {
	resp, err := c.rpc.CreateMessage(ctx, &chatv1.CreateMessageRequest{
		ConversationId: conversationID,
		SenderType:     reqBody.SenderType,
		SenderId:       reqBody.SenderID,
		Content:        reqBody.Content,
		ClientMsgId:    reqBody.ClientMsgID,
		VisitorToken:   reqBody.VisitorToken,
	})
	if err != nil {
		return nil, err
	}

	return &Message{
		ID:             resp.GetId(),
		ConversationID: resp.GetConversationId(),
		SenderType:     resp.GetSenderType(),
		SenderID:       resp.GetSenderId(),
		Content:        resp.GetContent(),
		ClientMsgID:    resp.GetClientMsgId(),
		CreatedAt:      resp.GetCreatedAt(),
		UpdatedAt:      resp.GetUpdatedAt(),
		Status:         resp.GetStatus(),
	}, nil
}

// MarkMessageDelivered 用于 realtime 确认消息已投递到客户端后回写状态。
func (c *Client) MarkMessageDelivered(ctx context.Context, conversationID uint64, messageID uint64) (*MarkMessageDeliveredResult, error) {
	resp, err := c.rpc.MarkMessageDelivered(ctx, &chatv1.MarkMessageDeliveredRequest{
		ConversationId: conversationID,
		MessageId:      messageID,
	})
	if err != nil {
		return nil, err
	}

	return &MarkMessageDeliveredResult{
		Updated: resp.GetUpdated(),
		Status:  resp.GetStatus(),
	}, nil
}

// GetConversation 拉取会话归属信息，用于 WS 连接鉴权。
func (c *Client) GetConversation(ctx context.Context, conversationID uint64) (*Conversation, error) {
	resp, err := c.gateway.GetConversation(ctx, &chatv1.GetConversationRequest{Id: conversationID})
	if err != nil {
		return nil, err
	}
	return &Conversation{
		ID:              resp.GetId(),
		VisitorToken:    resp.GetVisitorToken(),
		AssignedAgentID: resp.GetAssignedAgentId(),
		Status:          resp.GetStatus(),
	}, nil
}

// ListMessages 用于 WS 重连回放历史消息。
func (c *Client) ListMessages(ctx context.Context, conversationID uint64, in ListMessagesInput) ([]*Message, error) {
	resp, err := c.gateway.ListMessages(ctx, &chatv1.ListMessagesRequest{
		ConversationId: conversationID,
		Limit:          int32(in.Limit),
		BeforeId:       in.BeforeID,
	})
	if err != nil {
		return nil, err
	}

	items := resp.GetItems()
	out := make([]*Message, 0, len(items))
	for i := range items {
		item := items[i]
		out = append(out, &Message{
			ID:             item.GetId(),
			ConversationID: item.GetConversationId(),
			SenderType:     item.GetSenderType(),
			SenderID:       item.GetSenderId(),
			Content:        item.GetContent(),
			ClientMsgID:    item.GetClientMsgId(),
			CreatedAt:      item.GetCreatedAt(),
			UpdatedAt:      item.GetUpdatedAt(),
			Status:         item.GetStatus(),
		})
	}
	return out, nil
}
