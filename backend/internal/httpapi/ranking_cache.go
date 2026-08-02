package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

const maxRankingCacheEntries = 4096

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
// 同一冷 key 仅允许一个请求查库。其余等待同一结果。
type rankingResponseCache struct {
	mu         sync.Mutex
	entries    map[string]rankingCacheEntry
	inflight   map[string]*rankingCacheFlight
	generation uint64
}

func newRankingResponseCache() *rankingResponseCache {
	return &rankingResponseCache{
		entries:  make(map[string]rankingCacheEntry),
		inflight: make(map[string]*rankingCacheFlight),
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

	value, err := load()

	c.mu.Lock()
	flight.value, flight.err = value, err
	delete(c.inflight, key)
	if err == nil && generation == c.generation && now.Before(value.NextUpdateAt) {
		if len(c.entries) >= maxRankingCacheEntries {
			clear(c.entries)
		}
		c.entries[key] = rankingCacheEntry{value: value, expiresAt: value.NextUpdateAt}
	}
	close(flight.done)
	c.mu.Unlock()
	return value, "MISS", err
}

// invalidate 清除身份文案与本人位置缓存。
// generation 防止失效后旧查询结果回填。
func (c *rankingResponseCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.generation++
	c.mu.Unlock()
}
