package detailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

type Querier interface {
	QueryDailyDetails(context.Context, string, []string, provider.DailyDetailQueryOptions) provider.DailyDetailsResult
}

type Repository interface {
	MarkRunning(context.Context, string) error
	Months(context.Context, string) ([]string, error)
	PendingMeters(context.Context, string) ([]storage.ScanMeter, error)
	Record(context.Context, string, storage.ScanMeter, provider.DailyDetailsResult, time.Duration) error
	// Touch 写心跳。返回 true 表示已请求取消。
	Touch(context.Context, string) (bool, error)
	Finish(context.Context, string, bool, string) (storage.DailyDetailRunCounters, error)
}

type Config struct {
	Concurrency    int
	QPS            float64
	AcquireRequest func(context.Context) (func(), error)
	RetryMax       int
	MonthRetryMax  int
	ProgressEvery  int
	Location       *time.Location
}

type Runner struct {
	querier Querier
	store   Repository
	config  Config
	logger  *slog.Logger
}

func New(querier Querier, store Repository, config Config, logger *slog.Logger) (*Runner, error) {
	if querier == nil || store == nil {
		return nil, errors.New("daily detail querier and repository are required")
	}
	if config.Concurrency < 1 || config.QPS <= 0 {
		return nil, errors.New("daily detail concurrency and QPS must be positive")
	}
	if config.RetryMax < 0 || config.MonthRetryMax < 0 {
		return nil, errors.New("daily detail retry maxima cannot be negative")
	}
	if config.ProgressEvery < 1 {
		config.ProgressEvery = 25
	}
	if config.Location == nil {
		config.Location = time.UTC
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{querier: querier, store: store, config: config, logger: logger}, nil
}

func BootstrapMonths(now time.Time, loc *time.Location, from string) ([]string, error) {
	if loc == nil {
		loc = time.UTC
	}
	start, err := time.ParseInLocation("2006-01", from, loc)
	if err != nil {
		return nil, fmt.Errorf("invalid bootstrap month %q: %w", from, err)
	}
	current := now.In(loc)
	end := time.Date(current.Year(), current.Month(), 1, 0, 0, 0, 0, loc)
	if start.After(end) {
		return nil, errors.New("daily detail bootstrap month is after the current month")
	}
	months := []string{}
	for cursor := start; !cursor.After(end); cursor = cursor.AddDate(0, 1, 0) {
		months = append(months, cursor.Format("2006-01"))
	}
	return months, nil
}

func IncrementalMonths(now time.Time, loc *time.Location) []string {
	if loc == nil {
		loc = time.UTC
	}
	current := now.In(loc)
	months := []string{current.Format("2006-01")}
	if current.Day() <= 3 {
		months = append(months, current.AddDate(0, -1, 0).Format("2006-01"))
	}
	return months
}

func (r *Runner) Run(ctx context.Context, runID string) (storage.DailyDetailRunCounters, error) {
	if err := r.store.MarkRunning(ctx, runID); err != nil {
		return storage.DailyDetailRunCounters{}, err
	}
	months, err := r.store.Months(ctx, runID)
	if err != nil {
		return r.finishAfterError(runID, err)
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
				started := time.Now()
				result := r.querier.QueryDailyDetails(runCtx, meter.MeterNo, months, provider.DailyDetailQueryOptions{
					MaxAttempts: r.config.RetryMax + 1, MonthMaxAttempts: r.config.MonthRetryMax + 1,
					Location: r.config.Location, BeforeRequest: limiter.Wait, AcquireRequest: r.config.AcquireRequest,
				})
				if err := r.store.Record(runCtx, runID, meter, result, time.Since(started)); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				done := processed.Add(1)
				if done%int64(r.config.ProgressEvery) == 0 || done == int64(len(meters)) {
					r.logger.Info("daily detail progress", "run_id", runID, "processed", done, "pending_at_start", len(meters))
				}
			}
		}()
	}

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				canceled, err := r.store.Touch(runCtx, runID)
				if err != nil {
					r.logger.Warn("daily detail heartbeat failed", "run_id", runID, "error", err)
				}
				// 取消请求由 API 进程写库。
				// 掐断 runCtx 后停止投喂新表。
				// 在途表跑完再收尾，不硬砍连接。
				if canceled {
					r.logger.Warn("daily detail run canceled by operator", "run_id", runID)
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

func (r *Runner) finishAfterError(runID string, runErr error) (storage.DailyDetailRunCounters, error) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	counters, finishErr := r.store.Finish(cleanupCtx, runID, true, runErr.Error())
	if finishErr != nil {
		return storage.DailyDetailRunCounters{}, fmt.Errorf("%v; also failed to checkpoint daily detail run: %w", runErr, finishErr)
	}
	return counters, runErr
}
