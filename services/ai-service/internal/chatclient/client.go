package chatclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	chatv1 "inlinechat/services/ai-service/internal/gen/chatv1"
)

type Client struct {
	conn    *grpc.ClientConn
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
	ID             uint64
	ConversationID uint64
	SenderType     string
	SenderID       string
	Content        string
	ClientMsgID    string
	CreatedAt      string
	UpdatedAt      string
	Status         string
}

type Conversation struct {
	ID              uint64
	SiteID          string
	VisitorToken    string
	AssignedAgentID uint64
	Status          string
}

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
		gateway: chatv1.NewChatGatewayServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) GetConversation(ctx context.Context, conversationID uint64) (*Conversation, error) {
	resp, err := c.gateway.GetConversation(ctx, &chatv1.GetConversationRequest{Id: conversationID})
	if err != nil {
		return nil, err
	}
	return &Conversation{
		ID:              resp.GetId(),
		SiteID:          resp.GetSiteId(),
		VisitorToken:    resp.GetVisitorToken(),
		AssignedAgentID: resp.GetAssignedAgentId(),
		Status:          resp.GetStatus(),
	}, nil
}

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

func (c *Client) CreateMessage(ctx context.Context, conversationID uint64, reqBody CreateMessageRequest) (*Message, error) {
	resp, err := c.gateway.CreateMessage(ctx, &chatv1.CreateMessageRequest{
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
