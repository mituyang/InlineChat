package discovery

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type Resolver struct {
	client *clientv3.Client
	prefix string

	mu      sync.Mutex
	rrState map[string]int
}

func NewResolver(endpoints []string, dialTimeout time.Duration, prefix string) (*Resolver, error) {
	normalized := normalizeEndpoints(endpoints)
	if len(normalized) == 0 {
		return nil, fmt.Errorf("etcd endpoints are required")
	}
	if dialTimeout <= 0 {
		return nil, fmt.Errorf("etcd dial timeout must be greater than 0")
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   normalized,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return nil, err
	}

	return &Resolver{
		client:  cli,
		prefix:  normalizePrefix(prefix),
		rrState: make(map[string]int),
	}, nil
}

func (r *Resolver) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *Resolver) Resolve(ctx context.Context, serviceName string, protocol string) (string, error) {
	serviceName = strings.TrimSpace(serviceName)
	protocol = strings.TrimSpace(protocol)
	if serviceName == "" || protocol == "" {
		return "", fmt.Errorf("service name and protocol are required")
	}

	keyPrefix := fmt.Sprintf("%s/%s/%s/", r.prefix, serviceName, protocol)
	resp, err := r.client.Get(ctx, keyPrefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	if err != nil {
		return "", err
	}

	endpoints := make([]string, 0, len(resp.Kvs))
	for _, item := range resp.Kvs {
		value := strings.TrimSpace(string(item.Value))
		if value != "" {
			endpoints = append(endpoints, value)
		}
	}
	if len(endpoints) == 0 {
		return "", fmt.Errorf("no endpoint found for %s/%s", serviceName, protocol)
	}
	if len(endpoints) == 1 {
		return endpoints[0], nil
	}

	stateKey := serviceName + "/" + protocol
	r.mu.Lock()
	idx := r.rrState[stateKey] % len(endpoints)
	r.rrState[stateKey]++
	r.mu.Unlock()
	return endpoints[idx], nil
}

type Registrar struct {
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
	if _, err := r.client.Delete(ctx, r.key); err != nil && closeErr == nil {
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

func normalizeEndpoints(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(item)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizePrefix(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "/inlinechat/services"
	}
	value = "/" + strings.Trim(value, "/")
	return value
}
