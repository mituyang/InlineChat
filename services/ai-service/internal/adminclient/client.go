package adminclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1 "inlinechat/services/ai-service/internal/gen/adminv1"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  adminv1.AdminGatewayServiceClient
}

type SiteAIConfig struct {
	SiteID    string
	Enabled   bool
	ReplyMode string
	UpdatedAt string
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
		rpc:  adminv1.NewAdminGatewayServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) GetSiteAIConfig(ctx context.Context, siteID string) (*SiteAIConfig, error) {
	resp, err := c.rpc.GetSiteAIConfig(ctx, &adminv1.GetSiteAIConfigRequest{SiteId: siteID})
	if err != nil {
		return nil, err
	}
	return &SiteAIConfig{
		SiteID:    resp.GetSiteId(),
		Enabled:   resp.GetEnabled(),
		ReplyMode: resp.GetReplyMode(),
		UpdatedAt: resp.GetUpdatedAt(),
	}, nil
}
