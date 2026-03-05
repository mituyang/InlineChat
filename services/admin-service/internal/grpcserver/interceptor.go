package grpcserver

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func LoggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		// 统一打点 gRPC 调用耗时与状态，便于排障与容量观察。
		start := time.Now()
		resp, err := handler(ctx, req)
		cost := time.Since(start)

		if err != nil {
			st, _ := status.FromError(err)
			logger.Warn(
				"grpc request failed",
				zap.String("method", info.FullMethod),
				zap.String("code", st.Code().String()),
				zap.Duration("latency", cost),
				zap.Error(err),
			)
			return nil, err
		}

		logger.Info(
			"grpc request handled",
			zap.String("method", info.FullMethod),
			zap.Duration("latency", cost),
		)
		return resp, nil
	}
}
