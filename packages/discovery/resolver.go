package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type Resolver struct {
	client *clientv3.Client
	prefix string

	// rrState 记录各服务的轮询位置，实现客户端侧简单负载均衡。
	mu      sync.Mutex
	rrState map[string]int
}

// NewResolver 初始化 etcd 客户端，用于服务发现读取。
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

// Resolve 按 service/protocol 查找可用实例；多实例时按轮询返回。
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
