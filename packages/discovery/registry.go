package discovery

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type Registrar struct {
	// Registrar 维护 etcd 租约与 keepalive 生命周期。
	client      *clientv3.Client
	leaseID     clientv3.LeaseID
	key         string
	endpoint    string
	ttlSeconds  int64
	dialTimeout time.Duration
	done        chan struct{}
	stopCtx     context.Context
	stop        context.CancelFunc
	logger      *zap.Logger
	mu          sync.RWMutex
}

type RegisterRequest struct {
	Prefix       string
	ServiceName  string
	Protocol     string
	InstanceID   string
	Endpoint     string
	TTLSeconds   int64
	ETCDEndpoint []string
	DialTimeout  time.Duration
	Logger       *zap.Logger
}

// Register 把服务实例写入 etcd，并通过 keepalive 保持租约存活。
func Register(ctx context.Context, req RegisterRequest) (*Registrar, error) {
	if req.TTLSeconds <= 0 {
		return nil, fmt.Errorf("ttl must be greater than 0")
	}
	if req.DialTimeout <= 0 {
		return nil, fmt.Errorf("dial timeout must be greater than 0")
	}

	prefix := normalizePrefix(req.Prefix)
	serviceName := strings.TrimSpace(req.ServiceName)
	protocol := strings.TrimSpace(req.Protocol)
	instanceID := strings.TrimSpace(req.InstanceID)
	endpoint := strings.TrimSpace(req.Endpoint)
	if serviceName == "" || protocol == "" || endpoint == "" {
		return nil, fmt.Errorf("service_name protocol endpoint are required")
	}
	if instanceID == "" {
		host, _ := os.Hostname()
		instanceID = fmt.Sprintf("%s-%d", strings.TrimSpace(host), time.Now().UnixNano())
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   normalizeEndpoints(req.ETCDEndpoint),
		DialTimeout: req.DialTimeout,
	})
	if err != nil {
		return nil, err
	}

	leaseResp, err := client.Grant(ctx, req.TTLSeconds)
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	key := fmt.Sprintf("%s/%s/%s/%s", prefix, serviceName, protocol, instanceID)
	if _, err := client.Put(ctx, key, endpoint, clientv3.WithLease(leaseResp.ID)); err != nil {
		_ = client.Close()
		return nil, err
	}

	keepCtx, cancelKeep := context.WithCancel(context.Background())
	ch, err := client.KeepAlive(keepCtx, leaseResp.ID)
	if err != nil {
		cancelKeep()
		_ = client.Close()
		return nil, err
	}

	reg := &Registrar{
		client:      client,
		leaseID:     leaseResp.ID,
		key:         key,
		endpoint:    endpoint,
		ttlSeconds:  req.TTLSeconds,
		dialTimeout: req.DialTimeout,
		done:        make(chan struct{}),
		stopCtx:     keepCtx,
		stop:        cancelKeep,
		logger:      req.Logger,
	}

	go reg.keepAliveLoop(ch)

	return reg, nil
}

// Close 主动删除注册键并回收租约，通常在进程退出时调用。
func (r *Registrar) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.stop != nil {
		r.stop()
	}
	if r.done != nil {
		select {
		case <-r.done:
		case <-time.After(1200 * time.Millisecond):
		}
	}
	if r.client == nil {
		return nil
	}

	leaseID := r.currentLeaseID()
	var closeErr error
	if _, err := r.client.Delete(ctx, r.key); err != nil {
		closeErr = err
	}
	if leaseID != clientv3.NoLease {
		if _, err := r.client.Revoke(ctx, leaseID); err != nil && !isLeaseNotFound(err) && closeErr == nil {
			closeErr = err
		}
	}
	if err := r.client.Close(); err != nil && closeErr == nil {
		closeErr = err
	}
	return closeErr
}

func (r *Registrar) keepAliveLoop(ch <-chan *clientv3.LeaseKeepAliveResponse) {
	defer close(r.done)

	retryDelay := 500 * time.Millisecond
	for {
		if !r.consumeKeepAlive(ch) {
			return
		}

		if r.logger != nil {
			r.logger.Warn("etcd keepalive channel closed", zap.String("key", r.key))
		}

		for {
			if !sleepWithContext(r.stopCtx, retryDelay) {
				return
			}

			nextCh, err := r.reRegister()
			if err == nil {
				if r.logger != nil {
					r.logger.Info("etcd registration recovered", zap.String("key", r.key))
				}
				ch = nextCh
				retryDelay = 500 * time.Millisecond
				break
			}

			if r.logger != nil {
				r.logger.Warn("etcd registration recovery failed", zap.String("key", r.key), zap.Error(err))
			}
			if retryDelay < 5*time.Second {
				retryDelay *= 2
				if retryDelay > 5*time.Second {
					retryDelay = 5 * time.Second
				}
			}
		}
	}
}

func (r *Registrar) consumeKeepAlive(ch <-chan *clientv3.LeaseKeepAliveResponse) bool {
	for {
		select {
		case <-r.stopCtx.Done():
			return false
		case _, ok := <-ch:
			if !ok {
				return r.stopCtx.Err() == nil
			}
		}
	}
}

func (r *Registrar) reRegister() (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	opCtx, cancel := context.WithTimeout(r.stopCtx, r.dialTimeout)
	defer cancel()

	leaseResp, err := r.client.Grant(opCtx, r.ttlSeconds)
	if err != nil {
		return nil, err
	}
	if _, err := r.client.Put(opCtx, r.key, r.endpoint, clientv3.WithLease(leaseResp.ID)); err != nil {
		return nil, err
	}
	ch, err := r.client.KeepAlive(r.stopCtx, leaseResp.ID)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.leaseID = leaseResp.ID
	r.mu.Unlock()
	return ch, nil
}

func (r *Registrar) currentLeaseID() clientv3.LeaseID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.leaseID
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func isLeaseNotFound(err error) bool {
	return rpctypes.Error(err) == rpctypes.ErrLeaseNotFound
}
