package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/scanner"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5"
)

const (
	// 单表刷新使用独立上游通道。不再与批量扫描抢槽位。
	// 并发上限防止全量扫描期间压垮上游。每表 30 秒一次。
	// 8 个并发位覆盖正常点击密度。
	meterRefreshConcurrency = 8
	// 等位超时。超时后降级。优先短暂等待而非直接返回上游不可用。
	meterRefreshQueueWait = 2 * time.Second
)

type meterRefreshGate struct {
	mu       sync.Mutex
	cooldown time.Duration
	next     map[string]time.Time
	now      func() time.Time
}

func newMeterRefreshGate(cooldown time.Duration) *meterRefreshGate {
	return &meterRefreshGate{cooldown: cooldown, next: make(map[string]time.Time), now: time.Now}
}

func (g *meterRefreshGate) take(meter string) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if next := g.next[meter]; now.Before(next) {
		return next, false
	}
	next := now.Add(g.cooldown)
	g.next[meter] = next
	return next, true
}

// acquireRefreshSlot 占用一个手动刷新通道槽位。
// 拿不到则返回 false，调用方必须降级。release 必须调用一次。
func (s *Server) acquireRefreshSlot(ctx context.Context) (func(), bool) {
	if s.refreshSlots == nil {
		return func() {}, true
	}
	timer := time.NewTimer(meterRefreshQueueWait)
	defer timer.Stop()
	select {
	case s.refreshSlots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-s.refreshSlots }) }, true
	case <-ctx.Done():
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

type meterRefreshResponse struct {
	Meter         string                     `json:"meter"`
	Status        string                     `json:"status"`
	Latest        *storage.LatestReadingView `json:"latest"`
	RefreshedAt   time.Time                  `json:"refreshed_at"`
	NextAllowedAt time.Time                  `json:"next_allowed_at"`
	Availability  string                     `json:"availability"`
	Quality       storage.Quality            `json:"quality"`
}

func (s *Server) refreshCurrentUserMeter(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatedUser(w, r)
	if !ok || !s.requireBoundMeter(w, r, user) {
		return
	}
	s.refreshOneMeter(w, r, user.Meter.Meter)
}

func (s *Server) refreshOperatorMeter(w http.ResponseWriter, r *http.Request) {
	meter := r.PathValue("meter")
	if !meterPattern.MatchString(meter) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_meter", "meter must contain 6 to 32 digits")
		return
	}
	s.refreshOneMeter(w, r, meter)
}

func (s *Server) refreshOneMeter(w http.ResponseWriter, r *http.Request, meter string) {
	now := time.Now()
	before, err := storage.GetMeterOverview(r.Context(), s.pool, meter, now)
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeError(w, r, http.StatusNotFound, "meter_not_found", "meter was not found")
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}

	nextAllowedAt, allowed := s.refreshGate.take(meter)
	if !allowed {
		seconds := int(time.Until(nextAllowedAt).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds+1))
		s.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "meter refresh is cooling down")
		return
	}

	release, ok := s.acquireRefreshSlot(r.Context())
	if !ok {
		// 通道满员。此路径唯一返回缓存读数。与批量扫描无关。
		s.writeMeterRefresh(w, before, "upstream_unavailable", now, nextAllowedAt)
		return
	}
	defer release()

	runner, err := scanner.New(s.upstream, scanner.PostgreSQLRepository{Pool: s.pool}, scanner.Config{
		Concurrency:    1,
		QPS:            1,
		AcquireRequest: s.acquireUpstreamRequest,
		RetryMax:       0,
		BaseDelay:      s.cfg.Scan.RetryBaseDelay,
		MaxDelay:       s.cfg.Scan.RetryMaxDelay,
		StaleAfter:     s.cfg.Scan.StaleAfter,
		Location:       s.cfg.App.Timezone,
		ProgressEvery:  1,
		QuietProgress:  true,
	}, s.logger)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "refresh_unavailable", "meter refresh is not configured")
		return
	}
	settings := storage.ScanSettings{
		QPS: 1, Concurrency: 1, RetryMax: 0,
		TimeoutSeconds: int(s.cfg.Scan.Timeout.Seconds()),
	}
	runID, err := storage.CreateScanRun(
		r.Context(), s.pool, "manual", nil,
		storage.ScanScope{Meter: meter, Limit: 1}, settings,
	)
	if errors.Is(err, storage.ErrScanAlreadyRunning) {
		// 单表 scope 已排除在批量余额唯一索引外（000021_manual_refresh_lane）。
		// 正常不会到此。留作兜底，禁止把库层意外变成 500。
		s.logger.Warn("meter refresh unexpectedly hit a scan-run uniqueness conflict", "meter", meter, "error", err)
		s.writeMeterRefresh(w, before, "upstream_unavailable", now, nextAllowedAt)
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	counters, err := runner.Run(r.Context(), runID)
	if err != nil {
		if errors.Is(err, storage.ErrScanNotRunnable) || errors.Is(err, context.Canceled) {
			s.writeMeterRefresh(w, before, "upstream_unavailable", time.Now(), nextAllowedAt)
			return
		}
		s.databaseError(w, r, err)
		return
	}

	after, err := storage.GetMeterOverview(r.Context(), s.pool, meter, time.Now())
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	status := "upstream_unavailable"
	if counters.ValidTotal+counters.StaleTotal > 0 {
		status = "refreshed"
		if counters.DuplicateReadingTotal > 0 {
			status = "cached"
		}
	}
	rollupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := storage.RefreshRollupsForRun(rollupCtx, s.pool, runID); err != nil {
		s.logger.Warn("refresh on-demand rollups", "run_id", runID, "error", err)
	}
	s.campusCache.invalidate()
	s.writeMeterRefresh(w, after, status, time.Now(), nextAllowedAt)
}

func (s *Server) writeMeterRefresh(
	w http.ResponseWriter,
	overview storage.MeterOverviewView,
	status string,
	refreshedAt time.Time,
	nextAllowedAt time.Time,
) {
	s.writeJSON(w, http.StatusOK, meterRefreshResponse{
		Meter: overview.Meter, Status: status, Latest: overview.Latest,
		RefreshedAt: refreshedAt, NextAllowedAt: nextAllowedAt,
		Availability: overview.Availability, Quality: overview.Quality,
	})
}
