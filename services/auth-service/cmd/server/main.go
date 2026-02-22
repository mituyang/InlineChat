package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	shareddiscovery "inlinechat/packages/discovery"
	httpmiddleware "inlinechat/packages/httpmiddleware"
	"inlinechat/services/auth-service/internal/config"
	authv1 "inlinechat/services/auth-service/internal/gen/authv1"
	"inlinechat/services/auth-service/internal/grpcserver"
	"inlinechat/services/auth-service/internal/handler"
	"inlinechat/services/auth-service/internal/logger"
	"inlinechat/services/auth-service/internal/repository"
	"inlinechat/services/auth-service/internal/service"
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
	sqlDB, err := db.DB()
	if err != nil {
		appLogger.Fatal("failed to get mysql sql.DB", zap.Error(err))
	}
	sqlDB.SetMaxOpenConns(cfg.MySQLMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MySQLMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.MySQLConnMaxLifetimeSec) * time.Second)
	sqlDB.SetConnMaxIdleTime(time.Duration(cfg.MySQLConnMaxIdleTimeSec) * time.Second)
	queryTimeout := time.Duration(cfg.MySQLQueryTimeoutMS) * time.Millisecond

	repo := repository.NewAgentRepository(db, queryTimeout)
	authSvc := service.New(
		repo,
		cfg.JWTSecret,
		cfg.JWTPreviousSecret,
		cfg.JWTIssuer,
		cfg.JWTExpire,
		cfg.BCryptCost,
		cfg.SuperAdminEmail,
		cfg.SuperAdminPassword,
		cfg.SuperAdminDisplayName,
	)
	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 10*time.Second)
	if err := authSvc.EnsureSuperAdmin(ensureCtx); err != nil {
		cancelEnsure()
		appLogger.Fatal("failed to ensure super admin account", zap.Error(err))
	}
	cancelEnsure()
	appLogger.Info("super admin account ensured", zap.String("email", cfg.SuperAdminEmail))
	h := handler.NewHTTPHandler(authSvc)
	metrics := httpmiddleware.NewHTTPMetrics("auth-service", nil)

	r := gin.New()
	r.Use(
		httpmiddleware.RequestContext(httpmiddleware.DefaultRequestIDHeader, appLogger),
		httpmiddleware.Recovery(appLogger),
		metrics.Middleware(),
		httpmiddleware.SecurityHeaders(httpmiddleware.SecurityHeadersOptions{}),
	)

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"service": "auth-service", "status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 1500*time.Millisecond)
		defer cancel()
		if err := sqlDB.PingContext(checkCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"service": "auth-service",
				"status":  "not_ready",
				"errors": gin.H{
					"mysql": err.Error(),
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"service": "auth-service", "status": "ready"})
	})
	r.GET("/metrics", httpmiddleware.MetricsHandler(nil))

	v1 := r.Group("/v1")
	h.RegisterRoutes(v1)

	httpServer := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		appLogger.Fatal("failed to listen grpc port", zap.Error(err), zap.String("port", cfg.GRPCPort))
	}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(grpcserver.LoggingInterceptor(appLogger)))
	authv1.RegisterAuthGatewayServiceServer(grpcServer, grpcserver.New(authSvc))

	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 2)
	go func() {
		appLogger.Info("auth-service http started", zap.String("port", cfg.HTTPPort))
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()
	go func() {
		appLogger.Info("auth-service grpc started", zap.String("port", cfg.GRPCPort))
		appLogger.Info("auth-service discovery registered",
			zap.String("service_name", cfg.ServiceName),
			zap.String("grpc_endpoint", cfg.ServiceAdvertiseGRPCEndpoint),
		)
		if err := grpcServer.Serve(grpcListener); err != nil {
			serverErr <- err
		}
	}()

	select {
	case <-runCtx.Done():
		appLogger.Info("auth-service shutdown signal received")
	case err := <-serverErr:
		appLogger.Error("auth-service server error", zap.Error(err))
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Warn("http shutdown failed", zap.Error(err))
	}
	grpcServer.GracefulStop()
}
