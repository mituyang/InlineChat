package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	shareddiscovery "inlinechat/packages/discovery"
	httpmiddleware "inlinechat/packages/httpmiddleware"
	"inlinechat/services/gateway-service/internal/aiclient"
	"inlinechat/services/gateway-service/internal/config"
	"inlinechat/services/gateway-service/internal/grpcclient"
	"inlinechat/services/gateway-service/internal/handler"
	"inlinechat/services/gateway-service/internal/logger"
	"inlinechat/services/gateway-service/internal/middleware"
	"inlinechat/services/gateway-service/internal/proxy"
	"inlinechat/services/gateway-service/internal/ratelimit"
)

func main() {
	// 1) 加载配置与日志器，失败时直接退出，避免半初始化状态继续提供服务。
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

	// 2) 初始化 etcd 解析器，网关的 gRPC 上游与 WS 上游都通过它做动态发现。
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
		appLogger.Fatal("failed to resolve chat-service grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.ChatServiceName))
	}
	authTarget, err := shareddiscovery.ResolveWithRetry(resolver, cfg.AuthServiceName, "grpc", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve auth-service grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.AuthServiceName))
	}
	adminTarget, err := shareddiscovery.ResolveWithRetry(resolver, cfg.AdminServiceName, "grpc", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve admin-service grpc target from etcd", zap.Error(err), zap.String("service_name", cfg.AdminServiceName))
	}
	initialRealtimeTarget, err := shareddiscovery.ResolveWithRetry(resolver, cfg.RealtimeServiceName, "http", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve realtime-service http target from etcd", zap.Error(err), zap.String("service_name", cfg.RealtimeServiceName))
	}
	initialAITarget, err := shareddiscovery.ResolveWithRetry(resolver, cfg.AIServiceName, "http", 30*time.Second)
	if err != nil {
		appLogger.Fatal("failed to resolve ai-service http target from etcd", zap.Error(err), zap.String("service_name", cfg.AIServiceName))
	}

	// 3) /ws 走动态反向代理，每次请求实时解析 realtime-service 的最新地址。
	realtimeProxy, err := proxy.NewDynamicReverseProxy(func(ctx context.Context) (string, error) {
		return resolver.Resolve(ctx, cfg.RealtimeServiceName, "http")
	}, "", cfg.RequestIDHeader, appLogger)
	if err != nil {
		appLogger.Fatal("invalid realtime dynamic proxy", zap.Error(err))
	}

	dialTimeout := time.Duration(cfg.GRPCDialTimeoutSec) * time.Second
	callTimeout := time.Duration(cfg.GRPCCallTimeoutSec) * time.Second
	clients, err := grpcclient.NewDynamic(
		resolver,
		cfg.ChatServiceName,
		cfg.AuthServiceName,
		cfg.AdminServiceName,
		dialTimeout,
	)
	if err != nil {
		appLogger.Fatal("failed to connect grpc upstream", zap.Error(err))
	}
	defer func() {
		if err := clients.Close(); err != nil {
			appLogger.Warn("failed to close grpc upstream connections", zap.Error(err))
		}
	}()

	aiClient, err := aiclient.NewDynamic(resolver, cfg.AIServiceName, callTimeout)
	if err != nil {
		appLogger.Fatal("failed to init ai-service client", zap.Error(err))
	}

	appLogger.Info("resolved upstream endpoints via etcd",
		zap.String("chat_grpc_target", chatTarget),
		zap.String("auth_grpc_target", authTarget),
		zap.String("admin_grpc_target", adminTarget),
		zap.String("realtime_http_target", initialRealtimeTarget),
		zap.String("ai_http_target", initialAITarget),
	)

	limitTTL := time.Duration(cfg.RateLimitKeyTTLMins) * time.Minute
	loginLimiter := ratelimit.New(cfg.LoginRateLimitPerMin, cfg.LoginRateLimitBurst, limitTTL, 100000)
	visitorLimiter := ratelimit.New(cfg.VisitorRateLimitPerMin, cfg.VisitorRateLimitBurst, limitTTL, 200000)
	agentLimiter := ratelimit.New(cfg.AgentRateLimitPerMin, cfg.AgentRateLimitBurst, limitTTL, 120000)
	adminLimiter := ratelimit.New(cfg.AdminRateLimitPerMin, cfg.AdminRateLimitBurst, limitTTL, 80000)

	// 4) 限流默认本地计数；配置了 Redis 时切换为分布式计数（带熔断降级）。
	var rateLimitRedis *redis.Client
	if redisAddr := strings.TrimSpace(cfg.RateLimitRedisAddr); redisAddr != "" {
		rateLimitRedis = redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: cfg.RateLimitRedisPassword,
			DB:       cfg.RateLimitRedisDB,
		})
		pingCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		pingErr := rateLimitRedis.Ping(pingCtx).Err()
		cancel()
		if pingErr != nil {
			appLogger.Warn("rate limit redis unavailable, fallback to local limiter",
				zap.Error(pingErr),
				zap.String("redis_addr", redisAddr),
			)
			if closeErr := rateLimitRedis.Close(); closeErr != nil {
				appLogger.Warn("close rate limit redis client failed", zap.Error(closeErr))
			}
			rateLimitRedis = nil
		} else {
			counter := ratelimit.NewRedisCounter(rateLimitRedis)
			timeout := time.Duration(cfg.RateLimitRedisTimeout) * time.Millisecond
			circuitOpenWindow := time.Duration(cfg.RateLimitRedisCircuitOpenSec) * time.Second
			loginLimiter.EnableDistributedCounterWithCircuit(counter, cfg.RateLimitRedisPrefix+":login", time.Minute, timeout, cfg.RateLimitRedisFailThreshold, circuitOpenWindow)
			visitorLimiter.EnableDistributedCounterWithCircuit(counter, cfg.RateLimitRedisPrefix+":visitor", time.Minute, timeout, cfg.RateLimitRedisFailThreshold, circuitOpenWindow)
			agentLimiter.EnableDistributedCounterWithCircuit(counter, cfg.RateLimitRedisPrefix+":agent", time.Minute, timeout, cfg.RateLimitRedisFailThreshold, circuitOpenWindow)
			adminLimiter.EnableDistributedCounterWithCircuit(counter, cfg.RateLimitRedisPrefix+":admin", time.Minute, timeout, cfg.RateLimitRedisFailThreshold, circuitOpenWindow)
			appLogger.Info("rate limit distributed mode enabled",
				zap.String("redis_addr", redisAddr),
				zap.String("prefix", cfg.RateLimitRedisPrefix),
				zap.Int("fail_threshold", cfg.RateLimitRedisFailThreshold),
				zap.Duration("circuit_open_window", circuitOpenWindow),
			)
		}
	}
	if rateLimitRedis != nil {
		defer func() {
			if err := rateLimitRedis.Close(); err != nil {
				appLogger.Warn("close rate limit redis client failed", zap.Error(err))
			}
		}()
	}

	// 5) 统一注册 HTTP API、静态资源和 WS 代理入口。
	httpHandler := handler.NewHTTPHandler(clients, callTimeout)
	httpHandler.SetRateLimiters(loginLimiter, visitorLimiter)
	httpHandler.SetStaffRateLimiters(agentLimiter, adminLimiter)
	httpHandler.SetAIClient(aiClient)
	metrics := httpmiddleware.NewHTTPMetrics("gateway-service", nil)

	r := gin.New()
	r.Use(
		middleware.RequestContext(cfg.RequestIDHeader, appLogger),
		middleware.Recovery(appLogger),
		metrics.Middleware(),
		httpmiddleware.SecurityHeaders(httpmiddleware.SecurityHeadersOptions{}),
	)

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"service": "gateway-service", "status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		type upstreamCheck struct {
			name     string
			protocol string
			key      string
		}
		checks := []upstreamCheck{
			{name: cfg.ChatServiceName, protocol: "grpc", key: "chat_grpc"},
			{name: cfg.AuthServiceName, protocol: "grpc", key: "auth_grpc"},
			{name: cfg.AdminServiceName, protocol: "grpc", key: "admin_grpc"},
			{name: cfg.RealtimeServiceName, protocol: "http", key: "realtime_http"},
			{name: cfg.AIServiceName, protocol: "http", key: "ai_http"},
		}
		upstreams := make(gin.H, len(checks))
		failures := make(gin.H)
		for _, item := range checks {
			checkCtx, cancel := context.WithTimeout(c.Request.Context(), 800*time.Millisecond)
			target, err := resolver.Resolve(checkCtx, item.name, item.protocol)
			cancel()
			if err != nil {
				failures[item.key] = err.Error()
				continue
			}
			target = strings.TrimSpace(target)
			if target == "" {
				failures[item.key] = "empty target"
				continue
			}
			upstreams[item.key] = target
		}
		if len(failures) > 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"service":   "gateway-service",
				"status":    "not_ready",
				"upstreams": upstreams,
				"errors":    failures,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"service":   "gateway-service",
			"status":    "ready",
			"upstreams": upstreams,
		})
	})
	r.GET("/metrics", httpmiddleware.MetricsHandler(nil))
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
	r.GET("/app/api-docs", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/app/api-docs/")
	})

	registerStaticRoute(r, appLogger, "/app/customer", []string{"./public/customer", "./apps/customer-console", "../../apps/customer-console"})
	registerStaticRoute(r, appLogger, "/app/agent", []string{"./public/agent", "./apps/agent-console", "../../apps/agent-console"})
	registerStaticRoute(r, appLogger, "/app/staff-login", []string{"./public/staff-login", "./apps/staff-login", "../../apps/staff-login"})
	widgetDir, err := resolveStaticDir("./public/widget", "./apps/widget-chat", "../../apps/widget-chat")
	if err != nil {
		appLogger.Warn("widget route disabled due to missing directory", zap.Error(err))
	} else {
		mountWidgetRoute(r, appLogger, httpHandler, widgetDir)
	}
	registerStaticRoute(r, appLogger, "/app/admin", []string{"./public/admin", "./apps/admin-console", "../../apps/admin-console"})
	registerStaticRoute(r, appLogger, "/app/demo", []string{"./public/demo", "./apps/demo-site", "../../apps/demo-site"})
	registerStaticRoute(r, appLogger, "/app/api-docs", []string{"./public/api-docs", "./apps/api-docs", "../../apps/api-docs"})
	registerStaticRoute(r, appLogger, "/sdk", []string{"./public/sdk", "./apps/widget-sdk", "../../apps/widget-sdk"})
	registerStaticRoute(r, appLogger, "/docs/backend", []string{"./docs/backend", "../../docs/backend"})

	httpHandler.RegisterRoutes(r)

	// /ws/* 不在网关落业务，直接转发到 realtime-service。
	r.Any("/ws/*path", gin.WrapH(realtimeProxy))
	r.NoRoute(middleware.NoRouteHandler())
	r.NoMethod(middleware.NoMethodHandler())

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
		appLogger.Info("gateway-service started", zap.String("port", cfg.HTTPPort))
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-runCtx.Done():
		appLogger.Info("gateway-service shutdown signal received")
	case err := <-serverErr:
		appLogger.Error("gateway-service server error", zap.Error(err))
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Warn("gateway-service shutdown failed", zap.Error(err))
	}
}

func registerStaticRoute(r *gin.Engine, appLogger *zap.Logger, route string, candidates []string) {
	// 依次尝试候选目录，兼容容器内 public 目录与本地源码目录两种运行方式。
	dir, err := resolveStaticDir(candidates...)
	if err != nil {
		appLogger.Warn("static route disabled due to missing directory", zap.String("route", route), zap.Error(err))
		return
	}

	absDir := dir
	if v, err := filepath.Abs(dir); err == nil {
		absDir = v
	}

	appLogger.Info("static route mounted", zap.String("route", route), zap.String("dir", absDir))
	r.StaticFS(route, gin.Dir(dir, false))
}

func mountWidgetRoute(r *gin.Engine, appLogger *zap.Logger, httpHandler *handler.HTTPHandler, widgetDir string) {
	absDir := widgetDir
	if v, err := filepath.Abs(widgetDir); err == nil {
		absDir = v
	}

	indexHTML, readErr := os.ReadFile(filepath.Join(widgetDir, "index.html"))
	if readErr != nil {
		appLogger.Warn("widget entry disabled due to missing index.html", zap.String("dir", absDir), zap.Error(readErr))
	} else {
		httpHandler.SetWidgetIndexHTML(indexHTML)
	}

	fileServer := http.StripPrefix("/app/widget", http.FileServer(gin.Dir(widgetDir, false)))
	serveWidget := func(c *gin.Context) {
		if path := strings.TrimSpace(c.Param("filepath")); path == "" || path == "/" {
			httpHandler.ServeWidgetApp(c)
			return
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}

	r.GET("/app/widget/*filepath", serveWidget)
	r.HEAD("/app/widget/*filepath", serveWidget)
	appLogger.Info("widget route mounted", zap.String("route", "/app/widget/*filepath"), zap.String("dir", absDir))
}

func resolveStaticDir(candidates ...string) (string, error) {
	for _, dir := range candidates {
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no static directory matched candidates=%v", candidates)
}
