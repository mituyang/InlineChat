package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/signal"
	"strconv"
	"strings"
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

	if _, err := shareddiscovery.ResolveWithRetry(resolver, cfg.ChatServiceName, "grpc", 30*time.Second); err != nil {
		appLogger.Fatal("failed to resolve chat grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.ChatServiceName))
	}
	chatClient, err := chatclient.NewDynamic(resolver, cfg.ChatServiceName, "grpc", dialTimeout)
	if err != nil {
		appLogger.Fatal("failed to connect chat grpc", zap.Error(err), zap.String("service_name", cfg.ChatServiceName))
	}
	defer func() {
		if err := chatClient.Close(); err != nil {
			appLogger.Warn("close chat grpc client failed", zap.Error(err))
		}
	}()
	wsHandler := ws.NewHandler(
		hub,
		chatClient,
		cfg.AllowedOrigins,
		callTimeout,
		cfg.JWTSecret,
		cfg.JWTIssuer,
		appLogger,
	)

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
		zap.String("chat_grpc_target", chatClient.Target()),
		zap.String("realtime_http_advertise", cfg.ServiceAdvertiseHTTPEndpoint),
		zap.String("service_name", cfg.ServiceName),
	)

	consumeCtx, stopConsume := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopConsume()
	go func() {
		retryDelay := time.Second
		for {
			err := publisher.Consume(consumeCtx, func(conversationID string, payload []byte) {
				eventType, ok := parseMessageEventType(payload)
				if !ok {
					return
				}
				if eventType == "message.status" || eventType == "conversation.closed" || eventType == "conversation.status" {
					_ = hub.Broadcast(conversationID, payload, "")
					return
				}
				if eventType != "message.new" {
					return
				}

				eventConversationID, messageID, senderType, ok := parseMessageNewEvent(payload)
				if !ok {
					return
				}

				delivered := hub.Broadcast(conversationID, payload, senderType)
				if !delivered || senderType == "" || messageID == 0 {
					return
				}

				targetConversationID := eventConversationID
				if targetConversationID == 0 {
					id, err := strconv.ParseUint(conversationID, 10, 64)
					if err != nil {
						appLogger.Warn("skip mark delivered due to invalid conversation id",
							zap.String("conversation_id", conversationID),
							zap.Error(err),
						)
						return
					}
					targetConversationID = id
				}

				ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
				defer cancel()
				if _, err := chatClient.MarkMessageDelivered(ctx, targetConversationID, messageID); err != nil {
					appLogger.Warn("mark message delivered failed",
						zap.Error(err),
						zap.Uint64("conversation_id", targetConversationID),
						zap.Uint64("message_id", messageID),
					)
				}
			})
			if consumeCtx.Err() != nil {
				return
			}

			appLogger.Warn("redis consume loop interrupted, retrying",
				zap.Error(err),
				zap.Duration("retry_after", retryDelay),
			)
			timer := time.NewTimer(retryDelay)
			select {
			case <-consumeCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if retryDelay < 5*time.Second {
				retryDelay *= 2
			}
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

type messageNewEvent struct {
	Type    string `json:"type"`
	Payload struct {
		ConversationID uint64 `json:"conversation_id"`
		Message        struct {
			ID         uint64 `json:"id"`
			SenderType string `json:"sender_type"`
		} `json:"message"`
	} `json:"payload"`
}

type messageEventTypeEnvelope struct {
	Type string `json:"type"`
}

func parseMessageEventType(payload []byte) (string, bool) {
	var env messageEventTypeEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", false
	}
	eventType := strings.ToLower(strings.TrimSpace(env.Type))
	if eventType == "" {
		return "", false
	}
	return eventType, true
}

func parseMessageNewEvent(payload []byte) (conversationID uint64, messageID uint64, senderType string, ok bool) {
	var event messageNewEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return 0, 0, "", false
	}
	if event.Type != "message.new" {
		return 0, 0, "", false
	}

	senderType = strings.ToLower(strings.TrimSpace(event.Payload.Message.SenderType))
	if senderType != "visitor" && senderType != "agent" {
		senderType = ""
	}

	return event.Payload.ConversationID, event.Payload.Message.ID, senderType, true
}
