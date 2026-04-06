package adminclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1 "inlinechat/services/realtime-service/internal/gen/adminv1"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  adminv1.AdminGatewayServiceClient
}

type Site struct {
	SiteID  string
	Status  string
	Domain  string
	Domains []string
}

// New 建立到 admin-service 的 gRPC 连接。
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

// GetSiteBySiteID 查询站点信息，用于对话链路上的站点存在性与状态校验。
func (c *Client) GetSiteBySiteID(ctx context.Context, siteID string) (*Site, error) {
	resp, err := c.rpc.GetSiteBySiteID(ctx, &adminv1.GetSiteBySiteIDRequest{SiteId: siteID})
	if err != nil {
		return nil, err
	}
	return &Site{
		SiteID:  resp.GetSiteId(),
		Status:  resp.GetStatus(),
		Domain:  resp.GetDomain(),
		Domains: append([]string(nil), resp.GetDomains()...),
	}, nil
}
