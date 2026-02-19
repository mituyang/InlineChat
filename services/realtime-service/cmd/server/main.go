package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	shareddiscovery "inlinechat/packages/discovery"
	httpmiddleware "inlinechat/packages/httpmiddleware"
	"inlinechat/services/realtime-service/internal/chatclient"
	"inlinechat/services/realtime-service/internal/config"
	"inlinechat/services/realtime-service/internal/logger"
	"inlinechat/services/realtime-service/internal/pubsub"
	"inlinechat/services/realtime-service/internal/ws"
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

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	publisher := pubsub.NewRedisPubSub(redisClient)
	if err := publisher.Ping(context.Background()); err != nil {
		appLogger.Fatal("redis unavailable", zap.Error(err))
	}

	hub := ws.NewHub()
	dialTimeout := time.Duration(cfg.ChatGRPCDialTimeout) * time.Second
	callTimeout := time.Duration(cfg.ChatGRPCCallTimeout) * time.Second
	etcdDialTimeout := time.Duration(cfg.ETCDDialTimeoutSec) * time.Second

	resolver, err := shareddiscovery.NewResolver(cfg.ETCDEndpoints, etcdDialTimeout, cfg.DiscoveryPrefix)
	if err != nil {
		appLogger.Fatal("failed to create etcd resolver", zap.Error(err))
	}
	defer func() {
		if err := resolver.Close(); err != nil {
			appLogger.Warn("failed to close etcd resolver", zap.Error(err))
		}
	}()

	chatTarget, err := shareddiscovery.ResolveWithRetry(resolver, cfg.ChatServiceName, "grpc", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve chat grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.ChatServiceName))
	}

	chatClient, err := chatclient.New(chatTarget, dialTimeout)
	if err != nil {
		appLogger.Fatal("failed to connect chat grpc", zap.Error(err), zap.String("target", chatTarget))
	}
	defer func() {
		if err := chatClient.Close(); err != nil {
			appLogger.Warn("close chat grpc client failed", zap.Error(err))
		}
	}()
	wsHandler := ws.NewHandler(hub, chatClient, cfg.AllowedOrigins, callTimeout, appLogger)

	registerCtx, cancelRegister := context.WithTimeout(context.Background(), etcdDialTimeout)
	registrar, err := shareddiscovery.Register(registerCtx, shareddiscovery.RegisterRequest{
		Prefix:       cfg.DiscoveryPrefix,
		ServiceName:  cfg.ServiceName,
		Protocol:     "http",
		InstanceID:   cfg.ServiceInstanceID,
		Endpoint:     cfg.ServiceAdvertiseHTTPEndpoint,
		TTLSeconds:   int64(cfg.ETCDRegisterTTLSec),
		ETCDEndpoint: cfg.ETCDEndpoints,
		DialTimeout:  etcdDialTimeout,
		Logger:       appLogger,
	})
	cancelRegister()
	if err != nil {
		appLogger.Fatal("failed to register realtime http endpoint to etcd", zap.Error(err))
	}
	defer func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelClose()
		if err := registrar.Close(closeCtx); err != nil {
			appLogger.Warn("failed to unregister realtime endpoint from etcd", zap.Error(err))
		}
	}()

	appLogger.Info("resolved and registered discovery endpoints",
		zap.String("chat_grpc_target", chatTarget),
		zap.String("realtime_http_advertise", cfg.ServiceAdvertiseHTTPEndpoint),
		zap.String("service_name", cfg.ServiceName),
	)

	consumeCtx, stopConsume := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopConsume()
	go func() {
		err := publisher.Consume(consumeCtx, func(conversationID string, payload []byte) {
			hub.Broadcast(conversationID, payload)
		})
		if err != nil && consumeCtx.Err() == nil {
			appLogger.Error("redis consume loop exited", zap.Error(err))
		}
	}()

	r := gin.New()
	r.Use(httpmiddleware.RequestContext(httpmiddleware.DefaultRequestIDHeader, appLogger), httpmiddleware.Recovery(appLogger))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"service": "realtime-service", "status": "ok"})
	})

	r.GET("/ws/:conversation_id", wsHandler.Serve)

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		appLogger.Info("realtime-service started", zap.String("port", cfg.HTTPPort))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appLogger.Fatal("realtime-service exited", zap.Error(err))
		}
	}()

	<-consumeCtx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
