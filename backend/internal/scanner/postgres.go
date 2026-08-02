package scanner

import (
	"context"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQLRepository struct {
	Pool *pgxpool.Pool
}

func (r PostgreSQLRepository) MarkRunning(ctx context.Context, runID string) error {
	return storage.MarkScanRunRunning(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) PendingMeters(
	ctx context.Context,
	runID string,
) ([]storage.ScanMeter, error) {
	return storage.PendingScanMeters(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) Record(
	ctx context.Context,
	runID string,
	meter storage.ScanMeter,
	result provider.Result,
	attempts int,
	duration time.Duration,
	loc *time.Location,
	staleAfter time.Duration,
) error {
	return storage.RecordScanOutcome(
		ctx,
		r.Pool,
		runID,
		meter,
		result,
		attempts,
		duration,
		loc,
		staleAfter,
	)
}

func (r PostgreSQLRepository) Touch(ctx context.Context, runID string) (bool, error) {
	return storage.TouchScanRun(ctx, r.Pool, runID)
}

func (r PostgreSQLRepository) Finish(
	ctx context.Context,
	runID string,
	interrupted bool,
	message string,
) (storage.RunCounters, error) {
	return storage.FinishScanRun(ctx, r.Pool, runID, interrupted, message)
}
