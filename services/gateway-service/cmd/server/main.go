package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"inlinechat/services/gateway-service/internal/config"
	"inlinechat/services/gateway-service/internal/discovery"
	"inlinechat/services/gateway-service/internal/grpcclient"
	"inlinechat/services/gateway-service/internal/handler"
	"inlinechat/services/gateway-service/internal/logger"
	"inlinechat/services/gateway-service/internal/middleware"
	"inlinechat/services/gateway-service/internal/proxy"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	appLogger, err := logger.New(cfg.LogLevel)
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = appLogger.Sync()
	}()

	etcdDialTimeout := time.Duration(cfg.ETCDDialTimeoutSec) * time.Second
	resolver, err := discovery.NewResolver(cfg.ETCDEndpoints, etcdDialTimeout, cfg.DiscoveryPrefix)
	if err != nil {
		appLogger.Fatal("failed to create etcd resolver", zap.Error(err))
	}
	defer func() {
		if err := resolver.Close(); err != nil {
			appLogger.Warn("failed to close etcd resolver", zap.Error(err))
		}
	}()

	chatTarget, err := resolveWithRetry(resolver, cfg.ChatServiceName, "grpc", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve chat-service grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.ChatServiceName))
	}
	authTarget, err := resolveWithRetry(resolver, cfg.AuthServiceName, "grpc", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve auth-service grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.AuthServiceName))
	}
	adminTarget, err := resolveWithRetry(resolver, cfg.AdminServiceName, "grpc", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve admin-service grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.AdminServiceName))
	}
	realtimeTarget, err := resolveWithRetry(resolver, cfg.RealtimeServiceName, "http", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve realtime-service http target from etcd", zap.Error(err), zap.String("service_name", cfg.RealtimeServiceName))
	}

	realtimeProxy, err := proxy.NewReverseProxy(realtimeTarget, "", cfg.RequestIDHeader, appLogger)
	if err != nil {
		appLogger.Fatal("invalid realtime proxy target", zap.Error(err), zap.String("target", realtimeTarget))
	}

	dialTimeout := time.Duration(cfg.GRPCDialTimeoutSec) * time.Second
	callTimeout := time.Duration(cfg.GRPCCallTimeoutSec) * time.Second
	clients, err := grpcclient.New(chatTarget, authTarget, adminTarget, dialTimeout)
	if err != nil {
		appLogger.Fatal("failed to connect grpc upstream", zap.Error(err))
	}
	defer func() {
		if err := clients.Close(); err != nil {
			appLogger.Warn("failed to close grpc upstream connections", zap.Error(err))
		}
	}()

	appLogger.Info("resolved upstream endpoints via etcd",
		zap.String("chat_grpc_target", chatTarget),
		zap.String("auth_grpc_target", authTarget),
		zap.String("admin_grpc_target", adminTarget),
		zap.String("realtime_http_target", realtimeTarget),
	)

	httpHandler := handler.NewHTTPHandler(clients, callTimeout)

	r := gin.New()
	r.Use(middleware.RequestContext(cfg.RequestIDHeader, appLogger), middleware.Recovery(appLogger))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"service": "gateway-service", "status": "ok"})
	})
	r.GET("/app/customer", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/customer/")
	})
	r.GET("/app/agent", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/agent/")
	})
	r.GET("/app/staff-login", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/staff-login/")
	})
	r.GET("/app/widget", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/widget/")
	})
	r.GET("/app/admin", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/admin/")
	})
	r.GET("/app/demo", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/demo/")
	})
	r.StaticFS("/app/customer", gin.Dir("./public/customer", false))
	r.StaticFS("/app/agent", gin.Dir("./public/agent", false))
	r.StaticFS("/app/staff-login", gin.Dir("./public/staff-login", false))
	r.StaticFS("/app/widget", gin.Dir("./public/widget", false))
	r.StaticFS("/app/admin", gin.Dir("./public/admin", false))
	r.StaticFS("/app/demo", gin.Dir("./public/demo", false))
	r.StaticFS("/sdk", gin.Dir("./public/sdk", false))

	httpHandler.RegisterRoutes(r)

	r.Any("/ws/*path", gin.WrapH(realtimeProxy))
	r.NoRoute(middleware.NoRouteHandler())
	r.NoMethod(middleware.NoMethodHandler())

	appLogger.Info("gateway-service started", zap.String("port", cfg.HTTPPort))
	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		appLogger.Fatal("gateway-service exited", zap.Error(err))
	}
}

func resolveWithRetry(resolver *discovery.Resolver, serviceName string, protocol string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		target, err := resolver.Resolve(ctx, serviceName, protocol)
		cancel()
		if err == nil {
			return target, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("resolve %s/%s timeout: %w", serviceName, protocol, lastErr)
}
