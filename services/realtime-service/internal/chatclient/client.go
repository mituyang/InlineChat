package chatclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	chatv1 "inlinechat/services/realtime-service/internal/gen/chatv1"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  chatv1.ChatInternalServiceClient
}

type CreateMessageRequest struct {
	SenderType   string
	SenderID     string
	Content      string
	ClientMsgID  string
	VisitorToken string
}

type Message struct {
	ID             uint64 `json:"id"`
	ConversationID uint64 `json:"conversation_id"`
	SenderType     string `json:"sender_type"`
	SenderID       string `json:"sender_id,omitempty"`
	Content        string `json:"content"`
	ClientMsgID    string `json:"client_msg_id"`
	CreatedAt      string `json:"created_at"`
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
		conn: conn,
		rpc:  chatv1.NewChatInternalServiceClient(conn),
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
		if st, ok := status.FromError(err); ok {
			return nil, fmt.Errorf("chat-service grpc error [%s]: %s", st.Code().String(), st.Message())
		}
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
	}, nil
}
