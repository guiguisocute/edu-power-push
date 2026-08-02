package biller

import (
	"context"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQLRepository struct{ Pool *pgxpool.Pool }

func (r PostgreSQLRepository) MarkRunning(ctx context.Context, runID string) error {
	return storage.MarkBillRunRunning(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) Months(ctx context.Context, runID string) ([]string, error) {
	return storage.BillRunMonths(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) PendingMeters(ctx context.Context, runID string) ([]storage.ScanMeter, error) {
	return storage.PendingBillMeters(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) Record(ctx context.Context, runID string, meter storage.ScanMeter, result provider.MonthlyBillsResult, duration time.Duration) error {
	return storage.RecordBillOutcome(ctx, r.Pool, runID, meter, result, duration)
}

func (r PostgreSQLRepository) Touch(ctx context.Context, runID string) (bool, error) {
	return storage.TouchBillRun(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) Finish(ctx context.Context, runID string, interrupted bool, message string) (storage.BillRunCounters, error) {
	return storage.FinishBillRun(ctx, r.Pool, runID, interrupted, message)
}
