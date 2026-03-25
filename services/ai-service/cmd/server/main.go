package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	shareddiscovery "inlinechat/packages/discovery"
	httpmiddleware "inlinechat/packages/httpmiddleware"
	"inlinechat/services/ai-service/internal/adminclient"
	"inlinechat/services/ai-service/internal/chatclient"
	"inlinechat/services/ai-service/internal/config"
	"inlinechat/services/ai-service/internal/knowledgebase"
	"inlinechat/services/ai-service/internal/logger"
	"inlinechat/services/ai-service/internal/openai"
	"inlinechat/services/ai-service/internal/pubsub"
	aiservice "inlinechat/services/ai-service/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}

	appLogger, err := logger.New(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger failed: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = appLogger.Sync()
	}()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	ps := pubsub.NewRedisPubSub(redisClient)
	if err := ps.Ping(context.Background()); err != nil {
		appLogger.Fatal("redis unavailable", zap.Error(err))
	}

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
	if _, err := shareddiscovery.ResolveWithRetry(resolver, cfg.AdminServiceName, "grpc", 30*time.Second); err != nil {
		appLogger.Fatal("failed to resolve admin grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.AdminServiceName))
	}

	dialTimeout := time.Duration(cfg.GRPCDialTimeoutSec) * time.Second
	callTimeout := time.Duration(cfg.GRPCCallTimeoutSec) * time.Second
	chatClient, err := chatclient.NewDynamic(resolver, cfg.ChatServiceName, "grpc", dialTimeout)
	if err != nil {
		appLogger.Fatal("failed to connect chat grpc", zap.Error(err))
	}
	defer func() {
		if err := chatClient.Close(); err != nil {
			appLogger.Warn("close chat grpc client failed", zap.Error(err))
		}
	}()

	adminClient, err := adminclient.NewDynamic(resolver, cfg.AdminServiceName, "grpc", dialTimeout)
	if err != nil {
		appLogger.Fatal("failed to connect admin grpc", zap.Error(err))
	}
	defer func() {
		if err := adminClient.Close(); err != nil {
			appLogger.Warn("close admin grpc client failed", zap.Error(err))
		}
	}()

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
		appLogger.Fatal("failed to register ai-service http endpoint to etcd", zap.Error(err))
	}
	defer func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelClose()
		if err := registrar.Close(closeCtx); err != nil {
			appLogger.Warn("failed to unregister ai-service endpoint", zap.Error(err))
		}
	}()

	httpTimeout := time.Duration(cfg.AIHTTPTimeoutMS) * time.Millisecond
	processTimeout := callTimeout
	if processTimeout < httpTimeout+5*time.Second {
		processTimeout = httpTimeout + 5*time.Second
	}
	if processTimeout < 30*time.Second {
		processTimeout = 30 * time.Second
	}
	llmClient := openai.New(cfg.AILLMBaseURL, cfg.AILLMModel, cfg.AILLMAPIKey, httpTimeout)
	embeddingClient := openai.New(cfg.AIEmbeddingBaseURL, cfg.AIEmbeddingModel, cfg.AIEmbeddingAPIKey, httpTimeout)
	kbManager := knowledgebase.New(cfg.AIKBPath, embeddingClient, appLogger)
	if cfg.AIDisableExternalReadiness {
		appLogger.Warn("ai-service external readiness disabled",
			zap.Bool("ai_disable_external_readiness", true),
			zap.String("reason", "skip llm/embedding and knowledge-base readiness for ci or mock environments"),
		)
	} else if _, err := kbManager.Reload(context.Background()); err != nil {
		appLogger.Warn("initial knowledge base load failed", zap.Error(err), zap.String("path", cfg.AIKBPath))
	}

	autoReplySvc := aiservice.NewAutoReplyService(
		redisClient,
		chatClient,
		adminClient,
		kbManager,
		llmClient,
		appLogger,
		callTimeout,
		cfg.AIRetrieveTopK,
		cfg.AIMinSimilarity,
		cfg.AIUnknownReply,
	)

	consumeCtx, stopConsume := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopConsume()
	go func() {
		retryDelay := time.Second
		for {
			err := ps.Consume(consumeCtx, func(_ string, payload []byte) {
				msgCtx, cancel := context.WithTimeout(context.Background(), processTimeout)
				defer cancel()
				if consumeErr := autoReplySvc.HandleEvent(msgCtx, payload); consumeErr != nil {
					appLogger.Warn("process ai message event failed", zap.Error(consumeErr))
				}
			})
			if consumeCtx.Err() != nil {
				return
			}

			appLogger.Warn("ai-service redis consume loop interrupted, retrying",
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
	metrics := httpmiddleware.NewHTTPMetrics("ai-service", nil)
	r.Use(
		httpmiddleware.RequestContext(httpmiddleware.DefaultRequestIDHeader, appLogger),
		httpmiddleware.Recovery(appLogger),
		metrics.Middleware(),
		httpmiddleware.SecurityHeaders(httpmiddleware.SecurityHeadersOptions{}),
	)

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"service": "ai-service", "status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		failures := make(gin.H)
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := redisClient.Ping(checkCtx).Err(); err != nil {
			failures["redis"] = err.Error()
		}
		target, err := resolver.Resolve(checkCtx, cfg.ChatServiceName, "grpc")
		if err != nil {
			failures["chat_grpc"] = err.Error()
		} else if strings.TrimSpace(target) == "" {
			failures["chat_grpc"] = "empty target"
		}
		target, err = resolver.Resolve(checkCtx, cfg.AdminServiceName, "grpc")
		if err != nil {
			failures["admin_grpc"] = err.Error()
		} else if strings.TrimSpace(target) == "" {
			failures["admin_grpc"] = "empty target"
		}
		if !cfg.AIDisableExternalReadiness {
			if err := llmClient.Ready(checkCtx); err != nil {
				failures["llm"] = err.Error()
			}
			if err := embeddingClient.Ready(checkCtx); err != nil {
				failures["embedding"] = err.Error()
			}
			if err := kbManager.Ready(); err != nil {
				failures["knowledge_base"] = err.Error()
			}
		}

		if len(failures) > 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"service": "ai-service",
				"status":  "not_ready",
				"errors":  failures,
			})
			return
		}

		status := kbManager.Status()
		c.JSON(http.StatusOK, gin.H{
			"service":                    "ai-service",
			"status":                     "ready",
			"chunk_count":                status.ChunkCount,
			"loaded_at":                  status.LoadedAt.Format(time.RFC3339Nano),
			"external_readiness_skipped": cfg.AIDisableExternalReadiness,
		})
	})
	r.GET("/metrics", httpmiddleware.MetricsHandler(nil))
	r.POST("/reload", func(c *gin.Context) {
		siteID := strings.TrimSpace(c.Query("site_id"))
		if siteID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "site_id is required"})
			return
		}

		reloadCtx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		status, err := autoReplySvc.ReloadKnowledge(reloadCtx)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"site_id":     siteID,
			"chunk_count": status.ChunkCount,
			"reloaded_at": status.LoadedAt.Format(time.RFC3339Nano),
		})
	})

	httpServer := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		appLogger.Info("ai-service http started", zap.String("port", cfg.HTTPPort))
		appLogger.Info("ai-service discovery registered",
			zap.String("service_name", cfg.ServiceName),
			zap.String("http_endpoint", cfg.ServiceAdvertiseHTTPEndpoint),
			zap.String("chat_grpc_target", chatClient.Target()),
			zap.String("admin_grpc_target", adminClient.Target()),
		)
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-runCtx.Done():
		appLogger.Info("ai-service shutdown signal received")
	case err := <-serverErr:
		appLogger.Error("ai-service server error", zap.Error(err))
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Warn("http shutdown failed", zap.Error(err))
	}
}
