package httpapi

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

func TestRankingCacheHitsUntilScheduledUpdate(t *testing.T) {
	cache := newRankingResponseCache()
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	loads := 0
	load := func() (storage.RankingView, error) {
		loads++
		return storage.RankingView{Period: "day", NextUpdateAt: now.Add(time.Hour)}, nil
	}
	if _, status, err := cache.get(context.Background(), "day", now, load); err != nil || status != "MISS" {
		t.Fatalf("first get status=%s err=%v", status, err)
	}
	if _, status, err := cache.get(context.Background(), "day", now.Add(30*time.Minute), load); err != nil || status != "HIT" {
		t.Fatalf("cached get status=%s err=%v", status, err)
	}
	if loads != 1 {
		t.Fatalf("loads=%d, want 1", loads)
	}
	if _, status, err := cache.get(context.Background(), "day", now.Add(time.Hour), load); err != nil || status != "MISS" {
		t.Fatalf("expired get status=%s err=%v", status, err)
	}
	if loads != 2 {
		t.Fatalf("loads=%d after expiry, want 2", loads)
	}
}

func TestRankingCacheCoalescesConcurrentMissesAndInvalidates(t *testing.T) {
	cache := newRankingResponseCache()
	now := time.Now()
	started := make(chan struct{})
	release := make(chan struct{})
	var loads atomic.Int32
	load := func() (storage.RankingView, error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-release
		return storage.RankingView{NextUpdateAt: now.Add(time.Hour)}, nil
	}

	var wg sync.WaitGroup
	statuses := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, status, err := cache.get(context.Background(), "same", now, load)
			if err != nil {
				t.Errorf("get: %v", err)
			}
			statuses <- status
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(statuses)
	if loads.Load() != 1 {
		t.Fatalf("concurrent loads=%d, want 1", loads.Load())
	}
	cache.invalidate()
	_, status, err := cache.get(context.Background(), "same", now, func() (storage.RankingView, error) {
		loads.Add(1)
		return storage.RankingView{NextUpdateAt: now.Add(time.Hour)}, nil
	})
	if err != nil || status != "MISS" || loads.Load() != 2 {
		t.Fatalf("after invalidate status=%s loads=%d err=%v", status, loads.Load(), err)
	}
}
