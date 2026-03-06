package authclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "inlinechat/services/realtime-service/internal/gen/authv1"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  authv1.AuthGatewayServiceClient
}

type MeResult struct {
	AgentID uint64
	Email   string
	Role    string
	Exp     int64
	SiteID  string
}

// New 建立到 auth-service 的 gRPC 连接。
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
		rpc:  authv1.NewAuthGatewayServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Me 调用 auth-service 校验 token 并返回身份声明。
func (c *Client) Me(ctx context.Context, authorization string) (*MeResult, error) {
	resp, err := c.rpc.Me(ctx, &authv1.MeRequest{Authorization: authorization})
	if err != nil {
		return nil, err
	}
	return &MeResult{
		AgentID: resp.GetAgentId(),
		Email:   resp.GetEmail(),
		Role:    resp.GetRole(),
		Exp:     resp.GetExp(),
		SiteID:  resp.GetSiteId(),
	}, nil
}
