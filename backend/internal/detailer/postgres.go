package detailer

import (
	"context"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQLRepository struct{ Pool *pgxpool.Pool }

func (r PostgreSQLRepository) MarkRunning(ctx context.Context, runID string) error {
	return storage.MarkDailyDetailRunRunning(ctx, r.Pool, runID)
}
func (r PostgreSQLRepository) Months(ctx context.Context, runID string) ([]string, error) {
	return storage.DailyDetailRunMonths(ctx, r.Pool, runID)
}
func (r PostgreSQLRepository) PendingMeters(ctx context.Context, runID string) ([]storage.ScanMeter, error) {
	return storage.PendingDailyDetailMeters(ctx, r.Pool, runID)
}
func (r PostgreSQLRepository) Record(ctx context.Context, runID string, meter storage.ScanMeter, result provider.DailyDetailsResult, duration time.Duration) error {
	return storage.RecordDailyDetailOutcome(ctx, r.Pool, runID, meter, result, duration)
}
func (r PostgreSQLRepository) Touch(ctx context.Context, runID string) (bool, error) {
	return storage.TouchDailyDetailRun(ctx, r.Pool, runID)
}
func (r PostgreSQLRepository) Finish(ctx context.Context, runID string, interrupted bool, message string) (storage.DailyDetailRunCounters, error) {
	return storage.FinishDailyDetailRun(ctx, r.Pool, runID, interrupted, message)
}
