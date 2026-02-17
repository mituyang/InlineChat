package main

import (
	"context"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"inlinechat/services/auth-service/internal/config"
	"inlinechat/services/auth-service/internal/discovery"
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
	registrar, err := discovery.Register(registerCtx, discovery.RegisterRequest{
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

	repo := repository.NewAgentRepository(db)
	authSvc := service.New(
		repo,
		cfg.JWTSecret,
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

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"service": "auth-service", "status": "ok"})
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
