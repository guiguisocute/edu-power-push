package rediscache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const operationTimeout = 150 * time.Millisecond

var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

// Client 是可选的共享缓存。Redis 不可用时，调用方必须回退到本机缓存和 PostgreSQL。
type Client struct {
	redis  *redis.Client
	prefix string
}

func New(address, password string, database, poolSize int, prefix string) *Client {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}
	if poolSize < 1 {
		poolSize = 16
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), ":")
	if prefix == "" {
		prefix = "edu-power"
	}
	return &Client{
		redis: redis.NewClient(&redis.Options{
			Addr:                  address,
			Password:              password,
			DB:                    database,
			PoolSize:              poolSize,
			MinIdleConns:          1,
			DialTimeout:           500 * time.Millisecond,
			ReadTimeout:           operationTimeout,
			WriteTimeout:          operationTimeout,
			PoolTimeout:           operationTimeout,
			MaxRetries:            -1,
			ContextTimeoutEnabled: true,
		}),
		prefix: prefix,
	}
}

func (c *Client) Close() error {
	if c == nil || c.redis == nil {
		return nil
	}
	return c.redis.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.redis == nil {
		return errors.New("redis cache is disabled")
	}
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	return c.redis.Ping(ctx).Err()
}

func (c *Client) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c == nil || c.redis == nil {
		return nil, false, nil
	}
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	value, err := c.redis.Get(ctx, c.key(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	return value, err == nil, err
}

func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.redis == nil {
		return nil
	}
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	return c.redis.Set(ctx, c.key(key), value, ttl).Err()
}

// Generation 返回某一数据域的共享代数。失效只递增代数，旧响应由 TTL 自动回收。
func (c *Client) Generation(ctx context.Context, domain string) (string, error) {
	if c == nil || c.redis == nil {
		return "local", nil
	}
	key := c.key("generation:" + domain)
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	value, err := c.redis.Get(ctx, key).Result()
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, redis.Nil) {
		return "", err
	}
	created, err := c.redis.SetNX(ctx, key, "1", 0).Result()
	if err != nil {
		return "", err
	}
	if created {
		return "1", nil
	}
	return c.redis.Get(ctx, key).Result()
}

func (c *Client) BumpGeneration(ctx context.Context, domain string) error {
	if c == nil || c.redis == nil {
		return nil
	}
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	return c.redis.Incr(ctx, c.key("generation:"+domain)).Err()
}

// TryLock 用带随机令牌的短锁抑制跨 API 实例的缓存击穿。
func (c *Client) TryLock(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	if c == nil || c.redis == nil {
		return "", true, nil
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", false, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	acquired, err := c.redis.SetNX(ctx, c.key("lock:"+key), token, ttl).Result()
	return token, acquired, err
}

func (c *Client) Unlock(ctx context.Context, key, token string) error {
	if c == nil || c.redis == nil || token == "" {
		return nil
	}
	ctx, cancel := cacheContext(ctx)
	defer cancel()
	return unlockScript.Run(ctx, c.redis, []string{c.key("lock:" + key)}, token).Err()
}

func (c *Client) key(key string) string {
	return c.prefix + ":" + strings.TrimLeft(key, ":")
}

func cacheContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, operationTimeout)
}
