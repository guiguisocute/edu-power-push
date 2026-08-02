package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RunnableScan struct {
	RunID string
	// Trigger 区分运维显式任务与定时触发。
	Trigger     string
	QPS         float64
	Concurrency int
	RetryMax    int
}

type RunnableBill struct {
	RunID string
	// Trigger 区分运维显式任务与定时触发。
	Trigger       string
	QPS           float64
	Concurrency   int
	RetryMax      int
	MonthRetryMax int
}

type RunnableDailyDetail struct {
	RunID string
	// Trigger 区分运维显式任务与定时触发。
	Trigger       string
	QPS           float64
	Concurrency   int
	RetryMax      int
	MonthRetryMax int
}

func UpsertWorkerHeartbeat(ctx context.Context, pool *pgxpool.Pool, workerName, instanceID string, metadata []byte) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO worker_heartbeats (worker_name,instance_id,heartbeat_at,metadata)
		VALUES ($1,$2,now(),$3)
		ON CONFLICT (worker_name) DO UPDATE SET
			instance_id=EXCLUDED.instance_id,heartbeat_at=EXCLUDED.heartbeat_at,metadata=EXCLUDED.metadata
	`, workerName, instanceID, metadata)
	return err
}

func FindPendingScan(ctx context.Context, pool *pgxpool.Pool) (RunnableScan, error) {
	return scanSettingsRow(pool.QueryRow(ctx, `
		SELECT id::text, trigger, COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0)
		FROM scan_runs
		WHERE status='pending' AND scope->>'meter' IS NULL
		ORDER BY started_at LIMIT 1
	`))
}

// FindInterruptedScan 仅在无 pending/running 余额扫描占槽时返回可恢复批量扫描。
// MarkScanRunRunning 为最终原子认领。并发 worker 安全。
// 单表手动刷新由 API 进程跑完。worker 禁止接管。
func FindInterruptedScan(ctx context.Context, pool *pgxpool.Pool) (RunnableScan, error) {
	return scanSettingsRow(pool.QueryRow(ctx, `
		SELECT id::text, trigger, COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0)
		FROM scan_runs
		WHERE status='interrupted' AND scope->>'meter' IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM scan_runs active
			WHERE active.status IN ('pending','running') AND active.scope->>'meter' IS NULL
		  )
		ORDER BY started_at, id
		LIMIT 1
	`))
}

func InterruptStaleScan(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration) (RunnableScan, error) {
	return scanSettingsRow(pool.QueryRow(ctx, `
		WITH stale AS (
			SELECT id FROM scan_runs
			WHERE status='running' AND scope->>'meter' IS NULL
			  AND heartbeat_at < now() - $1::interval
			ORDER BY heartbeat_at LIMIT 1 FOR UPDATE SKIP LOCKED
		), changed AS (
			UPDATE scan_runs r SET status='interrupted',finished_at=NULL,
				error_message='worker heartbeat expired; recovery requested'
			FROM stale WHERE r.id=stale.id
			RETURNING r.*
		)
		SELECT id::text, trigger, COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0)
		FROM changed
	`, fmt.Sprintf("%f seconds", staleAfter.Seconds())))
}

// FailStaleMeterRefreshes 清理孤儿单表刷新。
// 单表刷新由 API 进程跑完。进程重启会留下永久 running 行。
// 不占槽位。但使运行态失真。过期直接判失败。不进重试队列。
func FailStaleMeterRefreshes(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE scan_runs
		SET status='failed', finished_at=now(),
			error_message='meter refresh was orphaned by an API restart'
		WHERE status IN ('pending','running') AND scope->>'meter' IS NOT NULL
		  AND heartbeat_at < now() - $1::interval
	`, fmt.Sprintf("%f seconds", staleAfter.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("fail stale meter refreshes: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanSettingsRow(row pgx.Row) (RunnableScan, error) {
	var scan RunnableScan
	err := row.Scan(&scan.RunID, &scan.Trigger, &scan.QPS, &scan.Concurrency, &scan.RetryMax)
	return scan, err
}

func FindPendingBill(ctx context.Context, pool *pgxpool.Pool) (RunnableBill, error) {
	return billSettingsRow(pool.QueryRow(ctx, `
		SELECT id::text,trigger,COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0),
			COALESCE((config_snapshot->>'month_retry_max')::integer,0)
		FROM bill_runs
		WHERE status='pending'
		ORDER BY started_at LIMIT 1
	`))
}

func FindInterruptedBill(ctx context.Context, pool *pgxpool.Pool) (RunnableBill, error) {
	return billSettingsRow(pool.QueryRow(ctx, `
		SELECT id::text,trigger,COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0),
			COALESCE((config_snapshot->>'month_retry_max')::integer,0)
		FROM bill_runs
		WHERE status='interrupted'
		  AND NOT EXISTS (SELECT 1 FROM bill_runs WHERE status IN ('pending','running'))
		ORDER BY started_at,id LIMIT 1
	`))
}

func InterruptStaleBill(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration) (RunnableBill, error) {
	return billSettingsRow(pool.QueryRow(ctx, `
		WITH stale AS (
			SELECT id FROM bill_runs WHERE status='running' AND heartbeat_at<now()-$1::interval
			ORDER BY heartbeat_at LIMIT 1 FOR UPDATE SKIP LOCKED
		), changed AS (
			UPDATE bill_runs r SET status='interrupted',finished_at=NULL,
				error_message='worker heartbeat expired; recovery requested'
			FROM stale WHERE r.id=stale.id RETURNING r.*
		)
		SELECT id::text,trigger,COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0),
			COALESCE((config_snapshot->>'month_retry_max')::integer,0)
		FROM changed
	`, fmt.Sprintf("%f seconds", staleAfter.Seconds())))
}

func billSettingsRow(row pgx.Row) (RunnableBill, error) {
	var bill RunnableBill
	err := row.Scan(&bill.RunID, &bill.Trigger, &bill.QPS, &bill.Concurrency, &bill.RetryMax, &bill.MonthRetryMax)
	return bill, err
}

func FindPendingDailyDetail(ctx context.Context, pool *pgxpool.Pool) (RunnableDailyDetail, error) {
	return dailyDetailSettingsRow(pool.QueryRow(ctx, `
		SELECT id::text,trigger,COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0),
			COALESCE((config_snapshot->>'month_retry_max')::integer,0)
		FROM daily_detail_runs WHERE status='pending' AND trigger<>'import'
		ORDER BY started_at LIMIT 1
	`))
}

func FindInterruptedDailyDetail(ctx context.Context, pool *pgxpool.Pool) (RunnableDailyDetail, error) {
	return dailyDetailSettingsRow(pool.QueryRow(ctx, `
		SELECT id::text,trigger,COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0),
			COALESCE((config_snapshot->>'month_retry_max')::integer,0)
		FROM daily_detail_runs WHERE status='interrupted' AND trigger<>'import'
		  AND NOT EXISTS (SELECT 1 FROM daily_detail_runs WHERE status IN ('pending','running'))
		ORDER BY started_at,id LIMIT 1
	`))
}

func InterruptStaleDailyDetail(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration) (RunnableDailyDetail, error) {
	return dailyDetailSettingsRow(pool.QueryRow(ctx, `
		WITH stale AS (
			SELECT id FROM daily_detail_runs WHERE status='running' AND trigger<>'import' AND heartbeat_at<now()-$1::interval
			ORDER BY heartbeat_at LIMIT 1 FOR UPDATE SKIP LOCKED
		), changed AS (
			UPDATE daily_detail_runs r SET status='interrupted',finished_at=NULL,
				error_message='worker heartbeat expired; recovery requested'
			FROM stale WHERE r.id=stale.id RETURNING r.*
		)
		SELECT id::text,trigger,COALESCE((config_snapshot->>'qps')::double precision,0),
			COALESCE((config_snapshot->>'concurrency')::integer,0),
			COALESCE((config_snapshot->>'retry_max')::integer,0),
			COALESCE((config_snapshot->>'month_retry_max')::integer,0)
		FROM changed
	`, fmt.Sprintf("%f seconds", staleAfter.Seconds())))
}

func dailyDetailSettingsRow(row pgx.Row) (RunnableDailyDetail, error) {
	var detail RunnableDailyDetail
	err := row.Scan(&detail.RunID, &detail.Trigger, &detail.QPS, &detail.Concurrency, &detail.RetryMax, &detail.MonthRetryMax)
	return detail, err
}
