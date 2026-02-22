package locker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var releaseConversationLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

type RedisConversationLocker struct {
	client        *redis.Client
	prefix        string
	retryInterval time.Duration
}

func NewRedisConversationLocker(client *redis.Client, prefix string, retryInterval time.Duration) *RedisConversationLocker {
	if strings.TrimSpace(prefix) == "" {
		prefix = "chat:conversation:create:lock"
	}
	if retryInterval <= 0 {
		retryInterval = 25 * time.Millisecond
	}
	return &RedisConversationLocker{
		client:        client,
		prefix:        prefix,
		retryInterval: retryInterval,
	}
}

func (l *RedisConversationLocker) Lock(ctx context.Context, key string, ttl time.Duration) (func(context.Context) error, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("redis conversation locker is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("lock key is required")
	}
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	token, err := buildLockToken()
	if err != nil {
		return nil, err
	}

	lockKey := l.prefix + ":" + key
	for {
		acquired, setErr := l.client.SetNX(ctx, lockKey, token, ttl).Result()
		if setErr != nil {
			return nil, setErr
		}
		if acquired {
			break
		}

		timer := time.NewTimer(l.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	return func(unlockCtx context.Context) error {
		if unlockCtx == nil {
			unlockCtx = context.Background()
		}
		_, unlockErr := releaseConversationLockScript.Run(unlockCtx, l.client, []string{lockKey}, token).Result()
		if unlockErr == redis.Nil {
			return nil
		}
		return unlockErr
	}, nil
}

func buildLockToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
