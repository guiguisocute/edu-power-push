package detailer

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

func TestDetailMonthSelection(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	months, err := BootstrapMonths(time.Date(2026, 7, 28, 0, 0, 0, 0, loc), loc, "2025-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 19 || months[0] != "2025-01" || months[18] != "2026-07" {
		t.Fatalf("bootstrap months=%v", months)
	}
	got := IncrementalMonths(time.Date(2026, 8, 2, 0, 30, 0, 0, loc), loc)
	if len(got) != 2 || got[0] != "2026-08" || got[1] != "2026-07" {
		t.Fatalf("incremental months=%v", got)
	}
	got = IncrementalMonths(time.Date(2026, 8, 4, 0, 30, 0, 0, loc), loc)
	if len(got) != 1 || got[0] != "2026-08" {
		t.Fatalf("incremental months after grace=%v", got)
	}
}

type fakeDetailQuerier struct{ calls int }

func (q *fakeDetailQuerier) QueryDailyDetails(_ context.Context, meter string, months []string, _ provider.DailyDetailQueryOptions) provider.DailyDetailsResult {
	q.calls++
	return provider.DailyDetailsResult{
		QueriedAt: time.Now(), Meter: meter, OK: true, Status: provider.DailyDetailStatusValid,
		Attempts: 1, MonthsRequested: len(months), Months: []provider.DailyDetailMonth{{
			Month: months[0], Status: "valid", CoveredThrough: "2026-07-27",
			Days: []provider.DailyUsageDay{{Date: "2026-07-27", UsageKWh: "9.70", CostYuan: "6.01"}},
		}},
	}
}

type fakeDetailRepository struct{ recorded, finished int }

func (*fakeDetailRepository) MarkRunning(context.Context, string) error { return nil }
func (*fakeDetailRepository) Months(context.Context, string) ([]string, error) {
	return []string{"2026-07"}, nil
}
func (*fakeDetailRepository) PendingMeters(context.Context, string) ([]storage.ScanMeter, error) {
	return []storage.ScanMeter{{ID: "id", MeterNo: "meter", Ordinal: 1}}, nil
}
func (r *fakeDetailRepository) Record(context.Context, string, storage.ScanMeter, provider.DailyDetailsResult, time.Duration) error {
	r.recorded++
	return nil
}
func (*fakeDetailRepository) Touch(context.Context, string) (bool, error) { return false, nil }
func (r *fakeDetailRepository) Finish(context.Context, string, bool, string) (storage.DailyDetailRunCounters, error) {
	r.finished++
	return storage.DailyDetailRunCounters{ProcessedTotal: r.recorded}, nil
}

func TestRunnerRecordsMeter(t *testing.T) {
	querier := &fakeDetailQuerier{}
	repository := &fakeDetailRepository{}
	runner, err := New(querier, repository, Config{Concurrency: 1, QPS: 1000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	counters, err := runner.Run(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if querier.calls != 1 || repository.recorded != 1 || repository.finished != 1 || counters.ProcessedTotal != 1 {
		t.Fatalf("calls=%d recorded=%d finished=%d counters=%+v", querier.calls, repository.recorded, repository.finished, counters)
	}
}
