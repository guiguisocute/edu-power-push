package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	campusCacheTTL        = 2 * time.Minute
	campusStaleTTL        = 5 * time.Minute
	campusQueryTimeout    = 10 * time.Second
	maxCampusCacheEntries = 256
	maxCampusResponseSize = 4 << 20
	// 看板可同时打开多个聚合端点。四个 DB 槽位覆盖正常突发。
	// 槽位数远低于连接池上限。
	campusQuerySlots = 4
	// 不同冷未命中可短暂排队。有界队列禁止攻击者用合法过滤耗尽资源。
	campusQueryWaiters = 32
	campusSlotWait     = 5 * time.Second
)

type cachedHTTPResponse struct {
	status    int
	header    http.Header
	body      []byte
	storedAt  time.Time
	expiresAt time.Time
}

type campusCacheFlight struct {
	done     chan struct{}
	response cachedHTTPResponse
}

// campusResponseCache 限制重复与对抗式变化的聚合查询。
// 相同未命中共享一次结果。仅 campusQuerySlots 个不同未命中可并发查库。
// 条目上限禁止合法过滤把防护本身变成内存耗尽路径。
type campusResponseCache struct {
	mu       sync.Mutex
	entries  map[string]cachedHTTPResponse
	inflight map[string]*campusCacheFlight
	slots    chan struct{}
	waiters  chan struct{}
	waitFor  time.Duration
}

func newCampusResponseCache() *campusResponseCache {
	return &campusResponseCache{
		entries: make(map[string]cachedHTTPResponse), inflight: make(map[string]*campusCacheFlight),
		slots: make(chan struct{}, campusQuerySlots), waiters: make(chan struct{}, campusQueryWaiters),
		waitFor: campusSlotWait,
	}
}

func (c *campusResponseCache) wrap(next http.HandlerFunc, queryKeys []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := normalizedCampusCacheKey(r.URL.Path, r.URL.Query(), queryKeys)
		now := time.Now()
		if response, ok := c.fresh(key, now); ok {
			writeCachedCampusResponse(w, response, "HIT", now)
			return
		}

		c.mu.Lock()
		if response, ok := c.entries[key]; ok && now.Before(response.expiresAt) {
			c.mu.Unlock()
			writeCachedCampusResponse(w, response, "HIT", now)
			return
		}
		if flight, ok := c.inflight[key]; ok {
			c.mu.Unlock()
			select {
			case <-flight.done:
				writeCachedCampusResponse(w, flight.response, "COALESCED", time.Now())
			case <-r.Context().Done():
				writeStandaloneError(w, http.StatusServiceUnavailable, "campus_busy", "campus aggregate request was canceled", requestIDFromContext(r.Context()))
			}
			return
		}
		flight := &campusCacheFlight{done: make(chan struct{})}
		c.inflight[key] = flight
		stale, hasStale := c.entries[key]
		hasStale = hasStale && now.Before(stale.expiresAt.Add(campusStaleTTL))
		c.mu.Unlock()

		if !c.acquireSlot(r.Context()) {
			if hasStale {
				c.finishFlight(key, flight, stale, false)
				writeCachedCampusResponse(w, stale, "STALE", now)
				return
			}
			response := campusBusyResponse(requestIDFromContext(r.Context()))
			c.finishFlight(key, flight, response, false)
			w.Header().Set("Retry-After", "2")
			writeCachedCampusResponse(w, response, "BYPASS", now)
			return
		}
		defer func() { <-c.slots }()

		ctx, cancel := context.WithTimeout(r.Context(), campusQueryTimeout)
		defer cancel()
		capture := newBufferedResponseWriter()
		next(capture, r.WithContext(ctx))
		response := capture.response(now)
		cacheable := response.status >= 200 && response.status < 300 && len(response.body) <= maxCampusResponseSize
		c.finishFlight(key, flight, response, cacheable)
		writeCachedCampusResponse(w, response, "MISS", now)
	}
}

func (c *campusResponseCache) acquireSlot(ctx context.Context) bool {
	// 快路径：普通流量多数不进入等待队列。
	select {
	case c.slots <- struct{}{}:
		return true
	default:
	}
	select {
	case c.waiters <- struct{}{}:
		defer func() { <-c.waiters }()
	default:
		return false
	}
	timer := time.NewTimer(c.waitFor)
	defer timer.Stop()
	select {
	case c.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (c *campusResponseCache) fresh(key string, now time.Time) (cachedHTTPResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry, ok && now.Before(entry.expiresAt)
}

func (c *campusResponseCache) finishFlight(key string, flight *campusCacheFlight, response cachedHTTPResponse, cacheable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	flight.response = response
	delete(c.inflight, key)
	if cacheable {
		response.expiresAt = response.storedAt.Add(campusCacheTTL)
		flight.response = response
		if len(c.entries) >= maxCampusCacheEntries {
			oldestKey := ""
			var oldest time.Time
			for candidateKey, candidate := range c.entries {
				if oldestKey == "" || candidate.storedAt.Before(oldest) {
					oldestKey, oldest = candidateKey, candidate.storedAt
				}
			}
			delete(c.entries, oldestKey)
		}
		c.entries[key] = response
	}
	close(flight.done)
}

func normalizedCampusCacheKey(path string, values url.Values, keys []string) string {
	normalized := make(url.Values, len(keys))
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			normalized.Set(key, value)
		}
	}
	return path + "?" + normalized.Encode()
}

type bufferedResponseWriter struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header), status: http.StatusOK}
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }
func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status, w.wroteHeader = status, true
}
func (w *bufferedResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(body)
}
func (w *bufferedResponseWriter) response(now time.Time) cachedHTTPResponse {
	return cachedHTTPResponse{status: w.status, header: w.header.Clone(), body: bytes.Clone(w.body.Bytes()), storedAt: now}
}

func writeCachedCampusResponse(w http.ResponseWriter, response cachedHTTPResponse, cacheStatus string, now time.Time) {
	for key, values := range response.header {
		if key == "Cache-Control" || key == "Content-Length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if response.status >= 200 && response.status < 300 {
		w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Campus-Cache", cacheStatus)
	if !response.storedAt.IsZero() {
		age := int(now.Sub(response.storedAt).Seconds())
		if age < 0 {
			age = 0
		}
		w.Header().Set("Age", strconv.Itoa(age))
	}
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body)
}

func campusBusyResponse(requestID string) cachedHTTPResponse {
	body := []byte(`{"error":{"code":"campus_busy","message":"campus aggregate capacity is busy; retry shortly","request_id":"` + requestID + `"}}`)
	return cachedHTTPResponse{
		status: http.StatusServiceUnavailable, header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		body: body, storedAt: time.Now(),
	}
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
