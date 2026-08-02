package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

type Querier interface {
	QueryMeter(context.Context, string, provider.QueryOptions) provider.Result
}

type Repository interface {
	MarkRunning(context.Context, string) error
	PendingMeters(context.Context, string) ([]storage.ScanMeter, error)
	Record(
		context.Context,
		string,
		storage.ScanMeter,
		provider.Result,
		int,
		time.Duration,
		*time.Location,
		time.Duration,
	) error
	// Touch 写心跳。返回 true 表示已请求取消。
	Touch(context.Context, string) (bool, error)
	Finish(context.Context, string, bool, string) (storage.RunCounters, error)
}

type Config struct {
	Concurrency    int
	QPS            float64
	AcquireRequest func(context.Context) (func(), error)
	RetryMax       int
	BaseDelay      time.Duration
	MaxDelay       time.Duration
	StaleAfter     time.Duration
	Location       *time.Location
	ProgressEvery  int
	// QuietProgress 关闭逐条进度日志，避免单表刷新淹没 API 日志。
	QuietProgress bool
	// HeartbeatEvery 兼作取消发现延迟。留空取 defaultHeartbeatEvery。
	HeartbeatEvery time.Duration
}

const defaultHeartbeatEvery = 15 * time.Second

type Runner struct {
	querier Querier
	store   Repository
	config  Config
	logger  *slog.Logger
}

func New(
	querier Querier,
	store Repository,
	config Config,
	logger *slog.Logger,
) (*Runner, error) {
	if querier == nil || store == nil {
		return nil, errors.New("scanner querier and repository are required")
	}
	if config.Concurrency < 1 {
		return nil, errors.New("scanner concurrency must be positive")
	}
	if config.QPS <= 0 {
		return nil, errors.New("scanner QPS must be positive")
	}
	if config.RetryMax < 0 {
		return nil, errors.New("scanner retry max cannot be negative")
	}
	if config.BaseDelay <= 0 || config.MaxDelay < config.BaseDelay {
		return nil, errors.New("scanner retry delays are invalid")
	}
	if config.Location == nil {
		config.Location = time.UTC
	}
	if config.ProgressEvery < 1 {
		config.ProgressEvery = 50
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{querier: querier, store: store, config: config, logger: logger}, nil
}

func (r *Runner) Run(ctx context.Context, runID string) (storage.RunCounters, error) {
	if err := r.store.MarkRunning(ctx, runID); err != nil {
		return storage.RunCounters{}, err
	}
	meters, err := r.store.PendingMeters(ctx, runID)
	if err != nil {
		return r.finishAfterError(runID, err)
	}
	if len(meters) == 0 {
		return r.store.Finish(ctx, runID, false, "")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	limiter := rate.NewLimiter(rate.Limit(r.config.QPS), 1)
	jobs := make(chan storage.ScanMeter)
	errCh := make(chan error, 1)
	var processed atomic.Int64
	var workers sync.WaitGroup

	for workerID := 0; workerID < r.config.Concurrency; workerID++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for meter := range jobs {
				if runCtx.Err() != nil {
					return
				}
				result, attempts, elapsed := r.queryWithRetry(runCtx, limiter, meter.MeterNo)
				if err := r.store.Record(
					runCtx,
					runID,
					meter,
					result,
					attempts,
					elapsed,
					r.config.Location,
					r.config.StaleAfter,
				); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				done := processed.Add(1)
				if !r.config.QuietProgress &&
					(done%int64(r.config.ProgressEvery) == 0 || done == int64(len(meters))) {
					r.logger.Info(
						"scan progress",
						"run_id", runID,
						"processed", done,
						"pending_at_start", len(meters),
					)
				}
			}
		}()
	}

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		beat := r.config.HeartbeatEvery
		if beat <= 0 {
			beat = defaultHeartbeatEvery
		}
		ticker := time.NewTicker(beat)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				canceled, err := r.store.Touch(runCtx, runID)
				if err != nil {
					r.logger.Warn("scan heartbeat failed", "run_id", runID, "error", err)
				}
				// 取消请求由 API 进程写库。
				// 掐断 runCtx 后停止投喂新表。
				// 在途表跑完再收尾，不硬砍连接。
				if canceled {
					r.logger.Warn("scan run canceled by operator", "run_id", runID)
					cancel()
					return
				}
			}
		}
	}()

sendLoop:
	for _, meter := range meters {
		select {
		case <-runCtx.Done():
			break sendLoop
		case jobs <- meter:
		}
	}
	close(jobs)
	workers.Wait()
	cancel()
	<-heartbeatDone

	var runErr error
	select {
	case runErr = <-errCh:
	default:
		runErr = ctx.Err()
	}
	if runErr != nil {
		return r.finishAfterError(runID, runErr)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCancel()
	return r.store.Finish(cleanupCtx, runID, false, "")
}

func (r *Runner) queryWithRetry(
	ctx context.Context,
	limiter *rate.Limiter,
	meter string,
) (provider.Result, int, time.Duration) {
	started := time.Now()
	var result provider.Result
	for attempt := 1; attempt <= r.config.RetryMax+1; attempt++ {
		// 余额 QPS 表示每秒电表尝试次数。
		// QueryMeter 内每次 HTTP 另走 AcquireRequest 全局上限。
		if err := limiter.Wait(ctx); err != nil {
			return canceledResult(meter, err), attempt, time.Since(started)
		}
		result = r.querier.QueryMeter(ctx, meter, provider.QueryOptions{
			MaxAttempts: 1, AcquireRequest: r.config.AcquireRequest,
		})
		if result.OK || result.Status == provider.StatusEmpty || result.Status == provider.StatusParseError {
			return result, attempt, time.Since(started)
		}
		if attempt > r.config.RetryMax {
			return result, attempt, time.Since(started)
		}
		delay := r.config.BaseDelay * time.Duration(1<<(attempt-1))
		if delay > r.config.MaxDelay {
			delay = r.config.MaxDelay
		}
		delay = time.Duration(float64(delay) * (0.5 + rand.Float64()))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return canceledResult(meter, ctx.Err()), attempt, time.Since(started)
		case <-timer.C:
		}
	}
	return result, r.config.RetryMax + 1, time.Since(started)
}

func (r *Runner) finishAfterError(runID string, runErr error) (storage.RunCounters, error) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	counters, finishErr := r.store.Finish(cleanupCtx, runID, true, runErr.Error())
	if finishErr != nil {
		return storage.RunCounters{}, fmt.Errorf("%v; also failed to mark scan interrupted: %w", runErr, finishErr)
	}
	return counters, runErr
}

func canceledResult(meter string, err error) provider.Result {
	message := "scan canceled"
	if err != nil {
		message = err.Error()
	}
	return provider.Result{
		QueriedAt: time.Now(),
		Meter:     meter,
		Status:    provider.StatusError,
		Errors:    []string{message},
	}
}
