package grpcclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
)

type Clients struct {
	Chat  chatv1.ChatGatewayServiceClient
	Auth  authv1.AuthGatewayServiceClient
	Admin adminv1.AdminGatewayServiceClient

	chatConn  *grpc.ClientConn
	authConn  *grpc.ClientConn
	adminConn *grpc.ClientConn

	chatConnManager  *serviceConn
	authConnManager  *serviceConn
	adminConnManager *serviceConn
}

// New 基于固定地址创建 chat/auth/admin 三个 gRPC 客户端。
func New(chatTarget string, authTarget string, adminTarget string, dialTimeout time.Duration) (*Clients, error) {
	chatConn, err := dial(chatTarget, dialTimeout)
	if err != nil {
		return nil, err
	}

	authConn, err := dial(authTarget, dialTimeout)
	if err != nil {
		_ = chatConn.Close()
		return nil, err
	}

	adminConn, err := dial(adminTarget, dialTimeout)
	if err != nil {
		_ = chatConn.Close()
		_ = authConn.Close()
		return nil, err
	}

	return &Clients{
		Chat:      chatv1.NewChatGatewayServiceClient(chatConn),
		Auth:      authv1.NewAuthGatewayServiceClient(authConn),
		Admin:     adminv1.NewAdminGatewayServiceClient(adminConn),
		chatConn:  chatConn,
		authConn:  authConn,
		adminConn: adminConn,
	}, nil
}

// Close 按顺序关闭动态连接管理器与静态连接。
func (c *Clients) Close() error {
	var closeErr error

	if c.chatConnManager != nil {
		if err := c.chatConnManager.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if c.authConnManager != nil {
		if err := c.authConnManager.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if c.adminConnManager != nil {
		if err := c.adminConnManager.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}

	if c.chatConn != nil {
		if err := c.chatConn.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if c.authConn != nil {
		if err := c.authConn.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if c.adminConn != nil {
		if err := c.adminConn.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}

	return closeErr
}

// dial 使用阻塞拨号确保启动阶段就暴露依赖不可用问题。
func dial(target string, dialTimeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	return grpc.DialContext(
		ctx,
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
}
