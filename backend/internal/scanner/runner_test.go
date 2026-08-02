package scanner

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

type fakeQuerier struct {
	mu      sync.Mutex
	results []provider.Result
	calls   int
}

func (q *fakeQuerier) QueryMeter(
	_ context.Context,
	meter string,
	_ provider.QueryOptions,
) provider.Result {
	q.mu.Lock()
	defer q.mu.Unlock()
	index := q.calls
	q.calls++
	if index >= len(q.results) {
		index = len(q.results) - 1
	}
	result := q.results[index]
	result.Meter = meter
	result.QueriedAt = time.Now()
	return result
}

type fakeRepository struct {
	meter            storage.ScanMeter
	recorded         int
	recordedAttempts int
	finished         bool
	cancelRequested  bool
	pending          int
	recordDelay      time.Duration
	mu               sync.Mutex
}

func (r *fakeRepository) MarkRunning(context.Context, string) error {
	return nil
}

func (r *fakeRepository) PendingMeters(context.Context, string) ([]storage.ScanMeter, error) {
	if r.pending > 1 {
		meters := make([]storage.ScanMeter, r.pending)
		for i := range meters {
			meters[i] = r.meter
		}
		return meters, nil
	}
	return []storage.ScanMeter{r.meter}, nil
}

func (r *fakeRepository) Record(
	_ context.Context,
	_ string,
	_ storage.ScanMeter,
	_ provider.Result,
	attempts int,
	_ time.Duration,
	_ *time.Location,
	_ time.Duration,
) error {
	if r.recordDelay > 0 {
		time.Sleep(r.recordDelay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recorded++
	r.recordedAttempts = attempts
	return nil
}

func (r *fakeRepository) Touch(context.Context, string) (bool, error) {
	return r.cancelRequested, nil
}

func (r *fakeRepository) Finish(
	context.Context,
	string,
	bool,
	string,
) (storage.RunCounters, error) {
	r.finished = true
	return storage.RunCounters{ProcessedTotal: r.recorded}, nil
}

func TestRunnerRetriesTransientFailure(t *testing.T) {
	querier := &fakeQuerier{results: []provider.Result{
		{Status: provider.StatusError, Errors: []string{"temporary"}},
		{
			OK:     true,
			Status: provider.StatusValid,
			Balance: provider.Balance{
				TotalYuan:   "1",
				TotalKWh:    "2",
				ReadingTime: "2026-07-26 18:00:00",
			},
		},
	}}
	repository := &fakeRepository{
		meter: storage.ScanMeter{ID: "id", MeterNo: "meter", Ordinal: 1},
	}
	runner, err := New(querier, repository, Config{
		Concurrency:   1,
		QPS:           1000,
		RetryMax:      2,
		BaseDelay:     time.Millisecond,
		MaxDelay:      2 * time.Millisecond,
		StaleAfter:    36 * time.Hour,
		Location:      time.UTC,
		ProgressEvery: 1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	counters, err := runner.Run(context.Background(), "run")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if querier.calls != 2 || repository.recordedAttempts != 2 {
		t.Fatalf("calls=%d attempts=%d", querier.calls, repository.recordedAttempts)
	}
	if counters.ProcessedTotal != 1 || !repository.finished {
		t.Fatalf("counters=%+v finished=%v", counters, repository.finished)
	}
}

func TestRunnerDoesNotRetryEmpty(t *testing.T) {
	querier := &fakeQuerier{results: []provider.Result{
		{Status: provider.StatusEmpty, Errors: []string{"empty"}},
	}}
	repository := &fakeRepository{
		meter: storage.ScanMeter{ID: "id", MeterNo: "meter", Ordinal: 1},
	}
	runner, _ := New(querier, repository, Config{
		Concurrency: 1,
		QPS:         1000,
		RetryMax:    3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := runner.Run(context.Background(), "run"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if querier.calls != 1 || repository.recordedAttempts != 1 {
		t.Fatalf("calls=%d attempts=%d", querier.calls, repository.recordedAttempts)
	}
}

// 取消经心跳读取；毫秒级心跳与多表断言 run 中途停止。
func TestRunnerStopsWhenCancelRequested(t *testing.T) {
	querier := &fakeQuerier{results: []provider.Result{{
		OK:     true,
		Status: provider.StatusValid,
		Balance: provider.Balance{
			TotalYuan: "1", TotalKWh: "2", ReadingTime: "2026-07-26 18:00:00",
		},
	}}}
	const total = 1000
	repository := &fakeRepository{
		meter:           storage.ScanMeter{ID: "id", MeterNo: "meter", Ordinal: 1},
		pending:         total,
		recordDelay:     time.Millisecond,
		cancelRequested: true,
	}
	runner, err := New(querier, repository, Config{
		Concurrency:    1,
		QPS:            100000,
		RetryMax:       0,
		BaseDelay:      time.Millisecond,
		MaxDelay:       time.Millisecond,
		Location:       time.UTC,
		ProgressEvery:  10000,
		QuietProgress:  true,
		HeartbeatEvery: time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := runner.Run(context.Background(), "run"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	repository.mu.Lock()
	processed := repository.recorded
	repository.mu.Unlock()
	if processed >= total {
		t.Fatalf("cancel did not stop the run: processed %d of %d", processed, total)
	}
	if !repository.finished {
		t.Fatal("a canceled run must still be finalized, otherwise recovery resumes it")
	}
}
