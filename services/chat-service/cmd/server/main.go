package main

import (
	"context"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	shareddiscovery "inlinechat/packages/discovery"
	httpmiddleware "inlinechat/packages/httpmiddleware"
	"inlinechat/services/chat-service/internal/config"
	chatv1 "inlinechat/services/chat-service/internal/gen/chatv1"
	"inlinechat/services/chat-service/internal/grpcserver"
	"inlinechat/services/chat-service/internal/handler"
	"inlinechat/services/chat-service/internal/logger"
	"inlinechat/services/chat-service/internal/pubsub"
	"inlinechat/services/chat-service/internal/repository"
	"inlinechat/services/chat-service/internal/service"
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
	registerCtx, cancelRegister := context.WithTimeout(context.Background(), etcdDialTimeout)
	registrar, err := shareddiscovery.Register(registerCtx, shareddiscovery.RegisterRequest{
		Prefix:       cfg.DiscoveryPrefix,
		ServiceName:  cfg.ServiceName,
		Protocol:     "grpc",
		InstanceID:   cfg.ServiceInstanceID,
		Endpoint:     cfg.ServiceAdvertiseGRPCEndpoint,
		TTLSeconds:   int64(cfg.ETCDRegisterTTLSec),
		ETCDEndpoint: cfg.ETCDEndpoints,
		DialTimeout:  etcdDialTimeout,
		Logger:       appLogger,
	})
	cancelRegister()
	if err != nil {
		appLogger.Fatal("failed to register grpc endpoint to etcd", zap.Error(err))
	}
	defer func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelClose()
		if err := registrar.Close(closeCtx); err != nil {
			appLogger.Warn("failed to unregister grpc endpoint from etcd", zap.Error(err))
		}
	}()

	db, err := gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{})
	if err != nil {
		appLogger.Fatal("failed to connect mysql", zap.Error(err))
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		appLogger.Fatal("failed to connect redis", zap.Error(err))
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			appLogger.Warn("close redis client failed", zap.Error(err))
		}
	}()

	conversationRepo := repository.NewConversationRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	messagePublisher := pubsub.NewRedisMessagePublisher(redisClient, 2*time.Second)
	autoCloseAfter := time.Duration(cfg.AutoCloseAfterSec) * time.Second
	chatSvc := service.New(conversationRepo, messageRepo, appLogger, messagePublisher, autoCloseAfter)
	if err := chatSvc.StartAutoCloseScheduler(context.Background()); err != nil {
		appLogger.Fatal("failed to bootstrap auto-close scheduler", zap.Error(err))
	}
	defer chatSvc.StopAutoCloseScheduler()
	h := handler.NewHTTPHandler(chatSvc)

	r := gin.New()
	r.Use(httpmiddleware.RequestContext(httpmiddleware.DefaultRequestIDHeader, appLogger), httpmiddleware.Recovery(appLogger))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": "chat-service",
			"status":  "ok",
		})
	})

	v1 := r.Group("/v1")
	h.RegisterRoutes(v1)

	httpServer := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		appLogger.Fatal("failed to listen grpc port", zap.Error(err), zap.String("port", cfg.GRPCPort))
	}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(grpcserver.LoggingInterceptor(appLogger)))
	chatv1.RegisterChatInternalServiceServer(grpcServer, grpcserver.New(chatSvc))
	chatv1.RegisterChatGatewayServiceServer(grpcServer, grpcserver.NewGateway(chatSvc))

	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 2)
	go func() {
		appLogger.Info("chat-service http started", zap.String("port", cfg.HTTPPort))
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()
	go func() {
		appLogger.Info("chat-service grpc started", zap.String("port", cfg.GRPCPort))
		appLogger.Info("chat-service discovery registered",
			zap.String("service_name", cfg.ServiceName),
			zap.String("grpc_endpoint", cfg.ServiceAdvertiseGRPCEndpoint),
		)
		if err := grpcServer.Serve(grpcListener); err != nil {
			serverErr <- err
		}
	}()

	select {
	case <-runCtx.Done():
		appLogger.Info("chat-service shutdown signal received")
	case err := <-serverErr:
		appLogger.Error("chat-service server error", zap.Error(err))
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Warn("http shutdown failed", zap.Error(err))
	}
	grpcServer.GracefulStop()
}
