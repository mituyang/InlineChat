package discovery

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type Registrar struct {
	// Registrar 维护 etcd 租约与 keepalive 生命周期。
	client      *clientv3.Client
	leaseID     clientv3.LeaseID
	key         string
	cancelAlive context.CancelFunc
	done        chan struct{}
	logger      *zap.Logger
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
		cancelAlive: cancelKeep,
		done:        make(chan struct{}),
		logger:      req.Logger,
	}

	go func() {
		defer close(reg.done)
		for {
			select {
			case <-keepCtx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					if reg.logger != nil {
						reg.logger.Warn("etcd keepalive channel closed", zap.String("key", reg.key))
					}
					return
				}
			}
		}
	}()

	return reg, nil
}

// Close 主动删除注册键并回收租约，通常在进程退出时调用。
func (r *Registrar) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.cancelAlive != nil {
		r.cancelAlive()
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

	var closeErr error
	if _, err := r.client.Delete(ctx, r.key); err != nil {
		closeErr = err
	}
	if _, err := r.client.Revoke(ctx, r.leaseID); err != nil && closeErr == nil {
		closeErr = err
	}
	if err := r.client.Close(); err != nil && closeErr == nil {
		closeErr = err
	}
	return closeErr
}
