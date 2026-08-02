package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrScanAlreadyRunning = errors.New("a campus scan is already running")
var ErrScanNotRunnable = errors.New("scan run is not pending or interrupted")
var ErrScanScopeEmpty = errors.New("scan scope contains no eligible meters")

type ScanScope struct {
	Building string `json:"building,omitempty"`
	Floor    string `json:"floor,omitempty"`
	// BoundOnly 为活跃用户优先批量集合。仍是批量扫描。
	// 与全校、账单、日明细采集共享上游安全通道。
	BoundOnly bool `json:"bound_only,omitempty"`
	// Meter 保留给已认证单表刷新。运维扫描 API 不从请求 JSON 接受该字段。
	Meter string `json:"meter,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// SingleMeter 区分单表手动刷新与批量扫描。
// 判定口径必须与 scan_runs 上 scope->>'meter' 部分索引一致。
func (s ScanScope) SingleMeter() bool { return s.Meter != "" }

type ScanSettings struct {
	QPS            float64 `json:"qps"`
	Concurrency    int     `json:"concurrency"`
	RetryMax       int     `json:"retry_max"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

type ScanMeter struct {
	ID       string
	MeterNo  string
	Building string
	Floor    string
	Room     string
	Ordinal  int
}

type RunCounters struct {
	InventoryTotal        int `json:"inventory_total"`
	ExcludedTotal         int `json:"excluded_total"`
	EligibleTotal         int `json:"eligible_total"`
	ProcessedTotal        int `json:"processed_total"`
	ValidTotal            int `json:"valid_total"`
	StaleTotal            int `json:"stale_total"`
	EmptyTotal            int `json:"empty_total"`
	ErrorTotal            int `json:"error_total"`
	ParseErrorTotal       int `json:"parse_error_total"`
	DuplicateReadingTotal int `json:"duplicate_reading_total"`
}

func CreateScanRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	trigger string,
	parentRunID *string,
	scope ScanScope,
	settings ScanSettings,
) (string, error) {
	var runID string
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var inventoryTotal, excludedTotal int
		if err := tx.QueryRow(ctx, `
			SELECT
				count(*) FILTER (WHERE active),
				count(*) FILTER (WHERE active AND excluded)
			FROM meters
		`).Scan(&inventoryTotal, &excludedTotal); err != nil {
			return fmt.Errorf("count scan inventory: %w", err)
		}
		scopeJSON, err := json.Marshal(scope)
		if err != nil {
			return err
		}
		settingsJSON, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO scan_runs (
				parent_run_id, trigger, status, scope, config_snapshot,
				inventory_total, excluded_total, heartbeat_at
			)
			VALUES ($1::uuid, $2, 'pending', $3, $4, $5, $6, now())
			RETURNING id::text
		`, nullableUUID(parentRunID), trigger, scopeJSON, settingsJSON, inventoryTotal, excludedTotal).Scan(&runID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
				pgErr.ConstraintName == "scan_runs_one_active_school" {
				return ErrScanAlreadyRunning
			}
			return fmt.Errorf("create scan run: %w", err)
		}

		tag, err := tx.Exec(ctx, `
			INSERT INTO scan_run_meters (run_id, meter_id, ordinal)
			SELECT $1::uuid, selected.id, selected.ordinal
			FROM (
				SELECT
					m.id,
					row_number() OVER (
						ORDER BY m.building, m.floor, m.room, m.meter_no
					)::integer AS ordinal
				FROM meters m
				WHERE m.active
				  AND NOT m.excluded
				  AND ($2 = '' OR m.building = $2)
				  AND ($3 = '' OR m.floor = $3)
				  AND ($5 = '' OR m.meter_no = $5)
				  AND (NOT $6::boolean OR EXISTS (
					SELECT 1
					FROM user_meter_bindings b
					JOIN user_accounts u ON u.id = b.user_id
					WHERE b.meter_id = m.id
					  AND b.unbound_at IS NULL
					  AND u.status = 'active'
				  ))
				ORDER BY m.building, m.floor, m.room, m.meter_no
				LIMIT NULLIF($4, 0)
			) selected
		`, runID, scope.Building, scope.Floor, scope.Limit, scope.Meter, scope.BoundOnly)
		if err != nil {
			return fmt.Errorf("freeze scan meter set: %w", err)
		}
		eligible := int(tag.RowsAffected())
		if eligible == 0 {
			return ErrScanScopeEmpty
		}
		if _, err := tx.Exec(ctx, `
			UPDATE scan_runs SET eligible_total = $2 WHERE id = $1::uuid
		`, runID, eligible); err != nil {
			return fmt.Errorf("update eligible scan count: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return runID, nil
}

func CountScanScope(ctx context.Context, pool *pgxpool.Pool, scope ScanScope) (RunCounters, error) {
	var counters RunCounters
	err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE active),
			count(*) FILTER (WHERE active AND excluded),
			LEAST(
				count(*) FILTER (
					WHERE active AND NOT excluded
					  AND ($1 = '' OR building = $1)
					  AND ($2 = '' OR floor = $2)
					  AND ($4 = '' OR meter_no = $4)
					  AND (NOT $5::boolean OR EXISTS (
						SELECT 1
						FROM user_meter_bindings b
						JOIN user_accounts u ON u.id = b.user_id
						WHERE b.meter_id = meters.id
						  AND b.unbound_at IS NULL
						  AND u.status = 'active'
					  ))
				),
				COALESCE(NULLIF($3, 0), 2147483647)
			)::integer
		FROM meters
	`, scope.Building, scope.Floor, scope.Limit, scope.Meter, scope.BoundOnly).Scan(
		&counters.InventoryTotal, &counters.ExcludedTotal, &counters.EligibleTotal,
	)
	return counters, err
}

func CreateRetryScanRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	parentRunID string,
	statuses []string,
	settings ScanSettings,
) (string, error) {
	if len(statuses) == 0 {
		return "", errors.New("at least one retry status is required")
	}
	var runID string
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var inventoryTotal, excludedTotal int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE active AND excluded)
			FROM meters
		`).Scan(&inventoryTotal, &excludedTotal); err != nil {
			return err
		}
		settingsJSON, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO scan_runs (
				parent_run_id, trigger, status, scope, config_snapshot,
				inventory_total, excluded_total, heartbeat_at
			)
			SELECT $1::uuid, 'retry', 'pending',
				jsonb_build_object('retry_statuses', to_jsonb($2::text[])), $3,
				$4, $5, now()
			WHERE EXISTS (SELECT 1 FROM scan_runs WHERE id=$1::uuid)
			RETURNING id::text
		`, parentRunID, statuses, settingsJSON, inventoryTotal, excludedTotal).Scan(&runID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "scan_runs_one_active_school" {
				return ErrScanAlreadyRunning
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("parent scan run: %w", pgx.ErrNoRows)
			}
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO scan_run_meters (run_id, meter_id, ordinal)
			SELECT $1::uuid, sr.meter_id,
				row_number() OVER (ORDER BY m.building,m.floor,m.room,m.meter_no)::integer
			FROM scan_results sr JOIN meters m ON m.id=sr.meter_id
			WHERE sr.run_id=$2::uuid AND sr.status=ANY($3::text[])
			ORDER BY m.building,m.floor,m.room,m.meter_no
		`, runID, parentRunID, statuses)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("parent run has no results matching retry statuses")
		}
		_, err = tx.Exec(ctx, `UPDATE scan_runs SET eligible_total=$2 WHERE id=$1::uuid`, runID, tag.RowsAffected())
		return err
	})
	return runID, err
}

func MarkScanRunRunning(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE scan_runs
			SET status = 'running', heartbeat_at = now()
			WHERE id = $1::uuid AND status IN ('pending', 'interrupted')
		`, runID)
		if err != nil {
			return fmt.Errorf("mark scan run running: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrScanNotRunnable
		}
		return nil
	})
}

func PendingScanMeters(
	ctx context.Context,
	pool *pgxpool.Pool,
	runID string,
) ([]ScanMeter, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.id::text, m.meter_no, m.building, m.floor, m.room, rm.ordinal
		FROM scan_run_meters rm
		JOIN meters m ON m.id = rm.meter_id
		LEFT JOIN scan_results sr ON sr.run_id = rm.run_id AND sr.meter_id = rm.meter_id
		WHERE rm.run_id = $1::uuid AND sr.run_id IS NULL
		ORDER BY rm.ordinal
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list pending scan meters: %w", err)
	}
	defer rows.Close()
	meters := make([]ScanMeter, 0)
	for rows.Next() {
		var meter ScanMeter
		if err := rows.Scan(
			&meter.ID,
			&meter.MeterNo,
			&meter.Building,
			&meter.Floor,
			&meter.Room,
			&meter.Ordinal,
		); err != nil {
			return nil, err
		}
		meters = append(meters, meter)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return meters, nil
}

func FinishScanRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	runID string,
	interrupted bool,
	message string,
) (RunCounters, error) {
	status := "completed"
	if interrupted {
		status = "interrupted"
	}
	var counters RunCounters
	err := pool.QueryRow(ctx, `
		WITH counts AS (
			SELECT
				count(*)::integer AS processed,
				count(*) FILTER (WHERE status = 'valid')::integer AS valid,
				count(*) FILTER (WHERE status = 'stale')::integer AS stale,
				count(*) FILTER (WHERE status = 'empty')::integer AS empty,
				count(*) FILTER (WHERE status IN ('error', 'canceled'))::integer AS errors,
				count(*) FILTER (WHERE status = 'parse_error')::integer AS parse_errors,
				count(*) FILTER (WHERE duplicate_reading)::integer AS duplicates
			FROM scan_results
			WHERE run_id = $1::uuid
		), updated AS (
			UPDATE scan_runs r
			SET
				status = CASE
					-- 取消优先于 interrupted：canceled 不在恢复逻辑的捞取范围内，
					-- 写成 interrupted 会被 worker 起来后原样续跑。
					WHEN r.cancel_requested THEN 'canceled'
					WHEN $2 = 'interrupted' THEN 'interrupted'
					WHEN counts.empty + counts.errors + counts.parse_errors > 0 THEN 'completed_with_errors'
					ELSE 'completed'
				END,
				finished_at = now(),
				heartbeat_at = now(),
				processed_total = counts.processed,
				valid_total = counts.valid,
				stale_total = counts.stale,
				empty_total = counts.empty,
				error_total = counts.errors,
				parse_error_total = counts.parse_errors,
				duplicate_reading_total = counts.duplicates,
				error_message = NULLIF($3, '')
			FROM counts
			WHERE r.id = $1::uuid
			RETURNING
				r.inventory_total, r.excluded_total, r.eligible_total, r.processed_total,
				r.valid_total, r.stale_total, r.empty_total, r.error_total,
				r.parse_error_total, r.duplicate_reading_total
		)
		SELECT * FROM updated
	`, runID, status, message).Scan(
		&counters.InventoryTotal,
		&counters.ExcludedTotal,
		&counters.EligibleTotal,
		&counters.ProcessedTotal,
		&counters.ValidTotal,
		&counters.StaleTotal,
		&counters.EmptyTotal,
		&counters.ErrorTotal,
		&counters.ParseErrorTotal,
		&counters.DuplicateReadingTotal,
	)
	if err != nil {
		return RunCounters{}, fmt.Errorf("finish scan run: %w", err)
	}
	return counters, nil
}

// TouchScanRun 打心跳并返回是否已请求取消。
// 心跳按固定节奏访问该行。取消信号复用该路径。不增加查询。
func TouchScanRun(ctx context.Context, pool *pgxpool.Pool, runID string) (bool, error) {
	var canceled bool
	err := pool.QueryRow(ctx, `
		UPDATE scan_runs SET heartbeat_at = now()
		WHERE id = $1::uuid AND status = 'running'
		RETURNING cancel_requested
	`, runID).Scan(&canceled)
	if errors.Is(err, pgx.ErrNoRows) {
		// run 已非 running。其他路径已收尾。不是错误。
		return false, nil
	}
	return canceled, err
}

// ErrRunNotCancelable 表示 run 已收尾。取消无意义。
var ErrRunNotCancelable = errors.New("run is already finished")

// RequestScanRunCancel 仅写请求标记。worker 负责掐断循环并写终态。
// API 与 worker 为两进程。靠本行通信。
func RequestScanRunCancel(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	return requestRunCancel(ctx, pool, "scan_runs", runID)
}

// RequestBillRunCancel 见 RequestScanRunCancel。
func RequestBillRunCancel(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	return requestRunCancel(ctx, pool, "bill_runs", runID)
}

// RequestDailyDetailRunCancel 见 RequestScanRunCancel。
func RequestDailyDetailRunCancel(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	return requestRunCancel(ctx, pool, "daily_detail_runs", runID)
}

// table 仅来自上方三个固定字面量。禁止外部输入。
func requestRunCancel(ctx context.Context, pool *pgxpool.Pool, table, runID string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE `+table+` SET cancel_requested = true
		WHERE id = $1::uuid AND status IN ('pending', 'running', 'interrupted')
	`, runID)
	if err != nil {
		return fmt.Errorf("request run cancel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRunNotCancelable
	}
	return nil
}

func nullableUUID(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}
