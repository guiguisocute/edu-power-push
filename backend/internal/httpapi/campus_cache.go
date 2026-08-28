package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/rediscache"
)

const (
	campusCacheTTL        = 5 * time.Minute
	campusStaleTTL        = 30 * time.Minute
	campusQueryTimeout    = 10 * time.Second
	campusLockTTL         = 15 * time.Second
	campusLockWait        = 400 * time.Millisecond
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

type sharedHTTPResponse struct {
	Status    int         `json:"status"`
	Header    http.Header `json:"header"`
	Body      []byte      `json:"body"`
	StoredAt  time.Time   `json:"stored_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type campusCacheFlight struct {
	done     chan struct{}
	response cachedHTTPResponse
}

// campusResponseCache 限制重复与对抗式变化的聚合查询。
// L1 保留进程内快路径；Redis L2 让相同自然日窗口跨浏览器、重启和实例共享。
type campusResponseCache struct {
	mu       sync.Mutex
	entries  map[string]cachedHTTPResponse
	inflight map[string]*campusCacheFlight
	slots    chan struct{}
	waiters  chan struct{}
	waitFor  time.Duration
	location *time.Location
	baseCtx  context.Context
	shared   *rediscache.Client
}

func newCampusResponseCache() *campusResponseCache {
	return newCampusResponseCacheWithShared(context.Background(), time.Local, nil)
}

func newCampusResponseCacheWithShared(baseCtx context.Context, location *time.Location, shared *rediscache.Client) *campusResponseCache {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if location == nil {
		location = time.Local
	}
	return &campusResponseCache{
		entries: make(map[string]cachedHTTPResponse), inflight: make(map[string]*campusCacheFlight),
		slots: make(chan struct{}, campusQuerySlots), waiters: make(chan struct{}, campusQueryWaiters),
		waitFor: campusSlotWait, location: location, baseCtx: baseCtx, shared: shared,
	}
}

func (c *campusResponseCache) wrap(next http.HandlerFunc, queryKeys []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := normalizedCampusCacheKeyAt(r.URL.Path, r.URL.Query(), queryKeys, c.location)
		now := time.Now()
		if response, ok := c.fresh(key, now); ok {
			writeCachedCampusResponse(w, response, "HIT", now)
			return
		}

		stale, hasStale := c.stale(key, now)
		sharedKey := c.sharedKey(r.Context(), key)
		if response, ok := c.getShared(r.Context(), sharedKey, now); ok {
			if now.Before(response.expiresAt) {
				c.storeLocal(key, response)
				writeCachedCampusResponse(w, response, "HIT", now)
				return
			}
			if !hasStale || response.storedAt.After(stale.storedAt) {
				stale, hasStale = response, true
			}
		}

		// 软过期立即返回旧值；一个后台刷新者重建新值。
		if hasStale {
			c.refreshStale(next, r, key, sharedKey, stale)
			writeCachedCampusResponse(w, stale, "STALE", now)
			return
		}

		c.mu.Lock()
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
		c.mu.Unlock()

		if !c.acquireSlot(r.Context()) {
			response := campusBusyResponse(requestIDFromContext(r.Context()))
			c.finishFlight(key, flight, response, false)
			w.Header().Set("Retry-After", "2")
			writeCachedCampusResponse(w, response, "BYPASS", now)
			return
		}
		defer func() { <-c.slots }()

		lockToken, lockHeld := c.acquireSharedLock(r.Context(), sharedKey)
		if c.shared != nil && sharedKey != "" && !lockHeld {
			if response, ok := c.waitForShared(r.Context(), sharedKey); ok {
				c.finishFlight(key, flight, response, true)
				writeCachedCampusResponse(w, response, "HIT", time.Now())
				return
			}
		}
		if lockHeld && c.shared != nil && sharedKey != "" {
			defer func() { _ = c.shared.Unlock(context.Background(), sharedKey, lockToken) }()
		}

		ctx, cancel := context.WithTimeout(r.Context(), campusQueryTimeout)
		defer cancel()
		response, cacheable := captureCampusResponse(next, r.WithContext(ctx), now)
		c.finishFlight(key, flight, response, cacheable)
		if cacheable {
			c.storeShared(context.Background(), sharedKey, response)
		}
		writeCachedCampusResponse(w, response, "MISS", now)
	}
}

func (c *campusResponseCache) refreshStale(next http.HandlerFunc, request *http.Request, key, sharedKey string, stale cachedHTTPResponse) {
	c.mu.Lock()
	if _, exists := c.inflight[key]; exists {
		c.mu.Unlock()
		return
	}
	flight := &campusCacheFlight{done: make(chan struct{})}
	c.inflight[key] = flight
	c.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(c.baseCtx, campusQueryTimeout)
		defer cancel()
		if !c.acquireSlot(ctx) {
			c.finishFlight(key, flight, stale, false)
			return
		}
		defer func() { <-c.slots }()
		lockToken, lockHeld := c.acquireSharedLock(ctx, sharedKey)
		if c.shared != nil && sharedKey != "" && !lockHeld {
			c.finishFlight(key, flight, stale, false)
			return
		}
		if lockHeld && c.shared != nil && sharedKey != "" {
			defer func() { _ = c.shared.Unlock(context.Background(), sharedKey, lockToken) }()
		}
		response, cacheable := captureCampusResponse(next, request.Clone(ctx), time.Now())
		c.finishFlight(key, flight, response, cacheable)
		if cacheable {
			c.storeShared(context.Background(), sharedKey, response)
		}
	}()
}

func captureCampusResponse(next http.HandlerFunc, request *http.Request, now time.Time) (cachedHTTPResponse, bool) {
	capture := newBufferedResponseWriter()
	next(capture, request)
	response := capture.response(now)
	cacheable := response.status >= 200 && response.status < 300 && len(response.body) <= maxCampusResponseSize
	return response, cacheable
}

func (c *campusResponseCache) acquireSlot(ctx context.Context) bool {
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

func (c *campusResponseCache) stale(key string, now time.Time) (cachedHTTPResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry, ok && now.Before(entry.expiresAt.Add(campusStaleTTL))
}

func (c *campusResponseCache) finishFlight(key string, flight *campusCacheFlight, response cachedHTTPResponse, cacheable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	flight.response = response
	delete(c.inflight, key)
	if cacheable {
		response.expiresAt = response.storedAt.Add(campusCacheTTL)
		flight.response = response
		c.putLocalLocked(key, response)
	}
	close(flight.done)
}

func (c *campusResponseCache) storeLocal(key string, response cachedHTTPResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocalLocked(key, response)
}

func (c *campusResponseCache) putLocalLocked(key string, response cachedHTTPResponse) {
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

func (c *campusResponseCache) sharedKey(ctx context.Context, key string) string {
	if c.shared == nil {
		return ""
	}
	generation, err := c.shared.Generation(ctx, "campus")
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "campus:" + generation + ":" + hex.EncodeToString(sum[:])
}

func (c *campusResponseCache) getShared(ctx context.Context, key string, now time.Time) (cachedHTTPResponse, bool) {
	if c.shared == nil || key == "" {
		return cachedHTTPResponse{}, false
	}
	encoded, ok, err := c.shared.Get(ctx, key)
	if err != nil || !ok {
		return cachedHTTPResponse{}, false
	}
	var shared sharedHTTPResponse
	if json.Unmarshal(encoded, &shared) != nil || shared.Status < 200 || shared.Status >= 300 || len(shared.Body) > maxCampusResponseSize {
		return cachedHTTPResponse{}, false
	}
	response := cachedHTTPResponse{
		status: shared.Status, header: shared.Header, body: shared.Body,
		storedAt: shared.StoredAt, expiresAt: shared.ExpiresAt,
	}
	return response, now.Before(response.expiresAt.Add(campusStaleTTL))
}

func (c *campusResponseCache) storeShared(ctx context.Context, key string, response cachedHTTPResponse) {
	if c.shared == nil || key == "" {
		return
	}
	if response.expiresAt.IsZero() {
		response.expiresAt = response.storedAt.Add(campusCacheTTL)
	}
	encoded, err := json.Marshal(sharedHTTPResponse{
		Status: response.status, Header: response.header, Body: response.body,
		StoredAt: response.storedAt, ExpiresAt: response.expiresAt,
	})
	if err == nil {
		_ = c.shared.Set(ctx, key, encoded, campusCacheTTL+campusStaleTTL)
	}
}

func (c *campusResponseCache) acquireSharedLock(ctx context.Context, key string) (string, bool) {
	if c.shared == nil || key == "" {
		return "", false
	}
	token, acquired, err := c.shared.TryLock(ctx, key, campusLockTTL)
	return token, err == nil && acquired
}

func (c *campusResponseCache) waitForShared(ctx context.Context, key string) (cachedHTTPResponse, bool) {
	deadline := time.NewTimer(campusLockWait)
	ticker := time.NewTicker(40 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return cachedHTTPResponse{}, false
		case <-deadline.C:
			return cachedHTTPResponse{}, false
		case <-ticker.C:
			if response, ok := c.getShared(ctx, key, time.Now()); ok && time.Now().Before(response.expiresAt) {
				return response, true
			}
		}
	}
}

func (c *campusResponseCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
	if c.shared != nil {
		_ = c.shared.BumpGeneration(context.Background(), "campus")
	}
}

func normalizedCampusCacheKey(path string, values url.Values, keys []string) string {
	return normalizedCampusCacheKeyAt(path, values, keys, time.Local)
}

func normalizedCampusCacheKeyAt(path string, values url.Values, keys []string, location *time.Location) string {
	normalized := make(url.Values, len(keys))
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			normalized.Set(key, canonicalCampusCacheValue(key, value, location))
		}
	}
	return path + "?" + normalized.Encode()
}

func canonicalCampusCacheValue(key, value string, location *time.Location) string {
	if key != "from" && key != "to" {
		return value
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	if location == nil {
		location = time.Local
	}
	local := parsed.In(location)
	day := local.Format("2006-01-02")
	if key == "from" {
		return day
	}
	if local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0 && local.Nanosecond() == 0 {
		return day + ":exclusive"
	}
	return day + ":inclusive"
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
