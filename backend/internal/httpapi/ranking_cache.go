package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/rediscache"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

const (
	maxRankingCacheEntries = 4096
	rankingLockTTL         = 15 * time.Second
	rankingLockWait        = 400 * time.Millisecond
)

type rankingCacheEntry struct {
	value     storage.RankingView
	expiresAt time.Time
}

type rankingCacheFlight struct {
	done  chan struct{}
	value storage.RankingView
	err   error
}

// rankingResponseCache 保存完整且已按查看者权限脱敏的响应。
// key 含 viewer ID 与 reveal 权限。禁止跨账号共享 self / is_self。
// L1 保留单进程毫秒级命中；Redis L2 跨重启、跨实例共享同一查看者的结果。
type rankingResponseCache struct {
	mu         sync.Mutex
	entries    map[string]rankingCacheEntry
	inflight   map[string]*rankingCacheFlight
	generation uint64
	shared     *rediscache.Client
}

func newRankingResponseCache() *rankingResponseCache {
	return newRankingResponseCacheWithShared(nil)
}

func newRankingResponseCacheWithShared(shared *rediscache.Client) *rankingResponseCache {
	return &rankingResponseCache{
		entries: make(map[string]rankingCacheEntry), inflight: make(map[string]*rankingCacheFlight),
		shared: shared,
	}
}

func (c *rankingResponseCache) get(
	ctx context.Context,
	key string,
	now time.Time,
	load func() (storage.RankingView, error),
) (storage.RankingView, string, error) {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		if now.Before(entry.expiresAt) {
			c.mu.Unlock()
			return entry.value, "HIT", nil
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()

	sharedKey := c.sharedKey(ctx, key)
	if value, ok := c.getShared(ctx, sharedKey, now); ok {
		c.storeLocal(key, value)
		return value, "HIT", nil
	}

	c.mu.Lock()
	if flight, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-flight.done:
			return flight.value, "HIT", flight.err
		case <-ctx.Done():
			return storage.RankingView{}, "MISS", ctx.Err()
		}
	}
	flight := &rankingCacheFlight{done: make(chan struct{})}
	generation := c.generation
	c.inflight[key] = flight
	c.mu.Unlock()

	lockToken, lockHeld := c.acquireSharedLock(ctx, sharedKey)
	if c.shared != nil && sharedKey != "" && !lockHeld {
		if value, ok := c.waitForShared(ctx, sharedKey, now); ok {
			c.finish(key, flight, value, nil, generation, now)
			return value, "HIT", nil
		}
	}
	if lockHeld && c.shared != nil && sharedKey != "" {
		defer func() { _ = c.shared.Unlock(context.Background(), sharedKey, lockToken) }()
	}

	value, err := load()
	c.finish(key, flight, value, err, generation, now)
	if err == nil && sharedKey != "" && now.Before(value.NextUpdateAt) {
		if encoded, encodeErr := json.Marshal(value); encodeErr == nil {
			_ = c.shared.Set(ctx, sharedKey, encoded, value.NextUpdateAt.Sub(now))
		}
	}
	return value, "MISS", err
}

func (c *rankingResponseCache) getShared(ctx context.Context, key string, now time.Time) (storage.RankingView, bool) {
	if c.shared == nil || key == "" {
		return storage.RankingView{}, false
	}
	encoded, ok, err := c.shared.Get(ctx, key)
	if err != nil || !ok {
		return storage.RankingView{}, false
	}
	var value storage.RankingView
	if json.Unmarshal(encoded, &value) != nil || !now.Before(value.NextUpdateAt) {
		return storage.RankingView{}, false
	}
	return value, true
}

func (c *rankingResponseCache) sharedKey(ctx context.Context, key string) string {
	if c.shared == nil {
		return ""
	}
	generation, err := c.shared.Generation(ctx, "ranking")
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "ranking:" + generation + ":" + hex.EncodeToString(sum[:])
}

func (c *rankingResponseCache) acquireSharedLock(ctx context.Context, key string) (string, bool) {
	if c.shared == nil || key == "" {
		return "", false
	}
	token, acquired, err := c.shared.TryLock(ctx, key, rankingLockTTL)
	return token, err == nil && acquired
}

func (c *rankingResponseCache) waitForShared(ctx context.Context, key string, now time.Time) (storage.RankingView, bool) {
	deadline := time.NewTimer(rankingLockWait)
	ticker := time.NewTicker(40 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return storage.RankingView{}, false
		case <-deadline.C:
			return storage.RankingView{}, false
		case <-ticker.C:
			if value, ok := c.getShared(ctx, key, now); ok {
				return value, true
			}
		}
	}
}

func (c *rankingResponseCache) finish(key string, flight *rankingCacheFlight, value storage.RankingView, err error, generation uint64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	flight.value, flight.err = value, err
	delete(c.inflight, key)
	if err == nil && generation == c.generation && now.Before(value.NextUpdateAt) {
		if len(c.entries) >= maxRankingCacheEntries {
			clear(c.entries)
		}
		c.entries[key] = rankingCacheEntry{value: value, expiresAt: value.NextUpdateAt}
	}
	close(flight.done)
}

func (c *rankingResponseCache) storeLocal(key string, value storage.RankingView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxRankingCacheEntries {
		clear(c.entries)
	}
	c.entries[key] = rankingCacheEntry{value: value, expiresAt: value.NextUpdateAt}
}

// invalidate 清除身份文案与本人位置缓存。
// generation 防止失效后旧查询结果回填；Redis 代数避免扫描删除旧键。
func (c *rankingResponseCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.generation++
	c.mu.Unlock()
	if c.shared != nil {
		_ = c.shared.BumpGeneration(context.Background(), "ranking")
	}
}
