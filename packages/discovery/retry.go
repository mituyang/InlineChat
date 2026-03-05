package discovery

import (
	"context"
	"fmt"
	"time"
)

type endpointResolver interface {
	Resolve(ctx context.Context, serviceName string, protocol string) (string, error)
}

// ResolveWithRetry 在限定总超时内重复解析，缓冲服务启动抖动。
func ResolveWithRetry(resolver endpointResolver, serviceName string, protocol string, timeout time.Duration) (string, error) {
	return resolveWithRetryWithPolicy(resolver, serviceName, protocol, timeout, 2*time.Second, 500*time.Millisecond)
}

func resolveWithRetryWithPolicy(
	resolver endpointResolver,
	serviceName string,
	protocol string,
	timeout time.Duration,
	attemptTimeout time.Duration,
	retryInterval time.Duration,
) (string, error) {
	if timeout <= 0 {
		return "", fmt.Errorf("timeout must be greater than 0")
	}
	if attemptTimeout <= 0 {
		return "", fmt.Errorf("attempt timeout must be greater than 0")
	}
	if retryInterval <= 0 {
		return "", fmt.Errorf("retry interval must be greater than 0")
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		target, err := resolver.Resolve(ctx, serviceName, protocol)
		cancel()
		if err == nil {
			return target, nil
		}

		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		sleepFor := retryInterval
		if sleepFor > remaining {
			sleepFor = remaining
		}
		time.Sleep(sleepFor)
	}

	return "", fmt.Errorf("resolve %s/%s timeout: %w", serviceName, protocol, lastErr)
}
