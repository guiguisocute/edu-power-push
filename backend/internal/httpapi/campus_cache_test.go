package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCampusCacheCanonicalizesEquivalentNaturalDayWindows(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"from", "to", "granularity"}
	first, _ := url.Parse("/api/v1/campus/series?from=2026-08-01T01:02:03%2B08:00&to=2026-08-27T12:34:01%2B08:00&granularity=day")
	second, _ := url.Parse("/api/v1/campus/series?from=2026-08-01T22:59:59%2B08:00&to=2026-08-27T23:59:59%2B08:00&granularity=day")
	if a, b := normalizedCampusCacheKeyAt(first.Path, first.Query(), keys, location), normalizedCampusCacheKeyAt(second.Path, second.Query(), keys, location); a != b {
		t.Fatalf("same SQL day window produced different cache keys:\n%s\n%s", a, b)
	}

	exclusive, _ := url.Parse("/api/v1/campus/series?from=2026-08-01T00:00:00%2B08:00&to=2026-08-27T00:00:00%2B08:00&granularity=day")
	if a, b := normalizedCampusCacheKeyAt(first.Path, first.Query(), keys, location), normalizedCampusCacheKeyAt(exclusive.Path, exclusive.Query(), keys, location); a == b {
		t.Fatal("inclusive current day and exclusive midnight windows shared a cache key")
	}
}

func TestCampusCacheNormalizesUnknownParametersAndCaches(t *testing.T) {
	cache := newCampusResponseCache()
	var calls atomic.Int32
	handler := cache.wrap(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"call":%d}`, calls.Add(1))
	}, []string{"from", "to"})

	first := httptest.NewRecorder()
	handler(first, httptest.NewRequest(http.MethodGet, "/api/v1/campus/summary?from=a&to=b&cache_bust=1", nil))
	second := httptest.NewRecorder()
	handler(second, httptest.NewRequest(http.MethodGet, "/api/v1/campus/summary?to=b&from=a&cache_bust=2", nil))

	if calls.Load() != 1 || first.Header().Get("X-Campus-Cache") != "MISS" || second.Header().Get("X-Campus-Cache") != "HIT" {
		t.Fatalf("calls=%d first=%s second=%s", calls.Load(), first.Header().Get("X-Campus-Cache"), second.Header().Get("X-Campus-Cache"))
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("cached body=%q, want %q", second.Body.String(), first.Body.String())
	}
}

func TestCampusCacheCoalescesIdenticalMisses(t *testing.T) {
	cache := newCampusResponseCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	handler := cache.wrap(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, []string{"from"})

	var wg sync.WaitGroup
	statuses := make(chan string, 2)
	run := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodGet, "/api/v1/campus/summary?from=a", nil))
			statuses <- response.Header().Get("X-Campus-Cache")
		}()
	}
	run()
	<-started
	run()
	// 给第二个请求时间，使其在释放前观察到飞行中条目。
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(statuses)
	if calls.Load() != 1 {
		t.Fatalf("database-equivalent calls=%d, want 1", calls.Load())
	}
	seen := map[string]bool{}
	for status := range statuses {
		seen[status] = true
	}
	if !seen["MISS"] || !seen["COALESCED"] {
		t.Fatalf("cache statuses=%v", seen)
	}
}

func TestCampusCacheQueuesNormalBurstAndBoundsOverflow(t *testing.T) {
	cache := newCampusResponseCache()
	cache.slots = make(chan struct{}, 1)
	cache.waiters = make(chan struct{}, 1)
	cache.waitFor = time.Second
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	handler := cache.wrap(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, []string{"from"})

	firstDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/api/v1/campus/summary?from=first", nil))
		firstDone <- response.Code
	}()
	<-started

	secondDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/api/v1/campus/summary?from=second", nil))
		secondDone <- response.Code
	}()
	deadline := time.Now().Add(time.Second)
	for len(cache.waiters) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(cache.waiters) != 1 {
		t.Fatal("second cold query did not enter the bounded waiter queue")
	}

	overflow := httptest.NewRecorder()
	handler(overflow, httptest.NewRequest(http.MethodGet, "/api/v1/campus/summary?from=overflow", nil))
	if overflow.Code != http.StatusServiceUnavailable || overflow.Header().Get("Retry-After") != "2" {
		t.Fatalf("overflow status=%d headers=%v body=%s", overflow.Code, overflow.Header(), overflow.Body.String())
	}
	close(release)
	if code := <-firstDone; code != http.StatusOK {
		t.Fatalf("first status=%d", code)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("queued dashboard query did not acquire the released slot")
	}
	if code := <-secondDone; code != http.StatusOK {
		t.Fatalf("second status=%d", code)
	}
}
