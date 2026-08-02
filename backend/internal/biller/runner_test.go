package biller

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

type fakeBillQuerier struct{ calls int }

func (q *fakeBillQuerier) QueryMonthlyBills(_ context.Context, meter string, months []string, _ provider.MonthlyQueryOptions) provider.MonthlyBillsResult {
	q.calls++
	return provider.MonthlyBillsResult{
		QueriedAt: time.Now(), Meter: meter, OK: true, Status: provider.MonthlyStatusValid,
		Attempts: 1, MonthsRequested: len(months),
		Bills: []provider.MonthlyBillMonth{{Month: months[0], Status: "data", Data: provider.BillData{StartKWh: "1", EndKWh: "2", UsageKWh: "1", CostYuan: "0.6"}}},
	}
}

type fakeBillRepository struct{ recorded, finished int }

func (*fakeBillRepository) MarkRunning(context.Context, string) error { return nil }
func (*fakeBillRepository) Months(context.Context, string) ([]string, error) {
	return []string{"2026-07"}, nil
}
func (*fakeBillRepository) PendingMeters(context.Context, string) ([]storage.ScanMeter, error) {
	return []storage.ScanMeter{{ID: "id", MeterNo: "meter", Ordinal: 1}}, nil
}
func (r *fakeBillRepository) Record(context.Context, string, storage.ScanMeter, provider.MonthlyBillsResult, time.Duration) error {
	r.recorded++
	return nil
}
func (*fakeBillRepository) Touch(context.Context, string) (bool, error) { return false, nil }
func (r *fakeBillRepository) Finish(context.Context, string, bool, string) (storage.BillRunCounters, error) {
	r.finished++
	return storage.BillRunCounters{ProcessedTotal: r.recorded}, nil
}

func TestVisibleMonths(t *testing.T) {
	got := VisibleMonths(time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC), time.UTC)
	if len(got) != 19 || got[0] != "2026-07" || got[6] != "2026-01" || got[7] != "2025-12" || got[18] != "2025-01" {
		t.Fatalf("VisibleMonths() = %#v", got)
	}
}

func TestRunnerRecordsMeter(t *testing.T) {
	querier := &fakeBillQuerier{}
	repository := &fakeBillRepository{}
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
