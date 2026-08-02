package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDailyDetailImportExists = errors.New("this daily-detail source file was already imported")

type DailyDetailImportResult struct {
	RunID                 string   `json:"run_id"`
	Status                string   `json:"status"`
	SourceSHA256          string   `json:"source_sha256"`
	SourceLines           int      `json:"source_lines"`
	MatchedMeters         int      `json:"matched_meters"`
	UnmatchedMeters       int      `json:"unmatched_meters"`
	UnmatchedMeterSamples []string `json:"unmatched_meter_samples,omitempty"`
	ImportedMonthCells    int      `json:"imported_month_cells"`
	RetainedFailedCells   int      `json:"retained_failed_cells"`
	ActualDayRows         int      `json:"actual_day_rows"`
	CoveredDayRows        int      `json:"covered_day_rows"`
	ZeroFilledDayRows     int      `json:"zero_filled_day_rows"`
	UsageRowsWritten      int64    `json:"usage_rows_written"`
	RevisionRows          int      `json:"revision_rows"`
}

// ApplyDailyDetailImport 将已审计 JSONL 写入规范日表。
// 全部写库在一个事务中完成。
// 爬取失败与未匹配表号保留为审计缺口。禁止当作零用电覆盖。
func ApplyDailyDetailImport(
	ctx context.Context,
	pool *pgxpool.Pool,
	r io.Reader,
	plan *importer.DailyDetailPlan,
	loc *time.Location,
	acceptPartial bool,
	provenance map[string]string,
) (result DailyDetailImportResult, err error) {
	if plan == nil || loc == nil {
		return result, errors.New("daily detail import requires an audit plan and timezone")
	}
	if plan.UnresolvedFailedCells > 0 && !acceptPartial {
		return result, fmt.Errorf("daily detail import has %d unresolved month cells; pass --accept-partial after review", plan.UnresolvedFailedCells)
	}
	result = DailyDetailImportResult{
		SourceSHA256: plan.SHA256, SourceLines: plan.TotalLines,
		RetainedFailedCells: plan.UnresolvedFailedCells,
	}
	runID, err := createDailyDetailImportRun(ctx, pool, plan, acceptPartial, provenance)
	if err != nil {
		return result, err
	}
	result.RunID = runID
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `
			UPDATE daily_detail_runs SET status='failed',finished_at=now(),heartbeat_at=now(),error_message=$2
			WHERE id=$1::uuid AND status='running'
		`, runID, truncateImportError(err.Error(), 2000))
	}()

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE daily_detail_import_month_stage (
				line_no bigint PRIMARY KEY,
				meter_no text NOT NULL,
				month date NOT NULL,
				observed_at timestamptz NOT NULL,
				covered_through date NOT NULL,
				month_status text NOT NULL,
				raw_row_count integer NOT NULL,
				days_with_usage integer NOT NULL,
				content_hash text NOT NULL,
				UNIQUE (meter_no,month)
			) ON COMMIT DROP;
			CREATE TEMP TABLE daily_detail_import_day_stage (
				line_no bigint NOT NULL,
				usage_date date NOT NULL,
				usage_kwh text NOT NULL,
				cost_yuan text NOT NULL,
				PRIMARY KEY (line_no,usage_date)
			) ON COMMIT DROP;
			CREATE TEMP TABLE daily_detail_import_failure_stage (
				line_no bigint PRIMARY KEY,
				meter_no text NOT NULL,
				month date NOT NULL,
				observed_at timestamptz NOT NULL,
				error_message text NOT NULL,
				UNIQUE (meter_no,month)
			) ON COMMIT DROP;
		`); err != nil {
			return err
		}

		monthRows := make([][]any, 0, 300)
		dayRows := make([][]any, 0, 12000)
		failureRows := make([][]any, 0, 100)
		flush := func() error {
			if len(monthRows) > 0 {
				if _, err := tx.CopyFrom(ctx, pgx.Identifier{"daily_detail_import_month_stage"},
					[]string{"line_no", "meter_no", "month", "observed_at", "covered_through", "month_status", "raw_row_count", "days_with_usage", "content_hash"},
					pgx.CopyFromRows(monthRows)); err != nil {
					return fmt.Errorf("copy import month stage: %w", err)
				}
				monthRows = monthRows[:0]
			}
			if len(dayRows) > 0 {
				if _, err := tx.CopyFrom(ctx, pgx.Identifier{"daily_detail_import_day_stage"},
					[]string{"line_no", "usage_date", "usage_kwh", "cost_yuan"},
					pgx.CopyFromRows(dayRows)); err != nil {
					return fmt.Errorf("copy import day stage: %w", err)
				}
				dayRows = dayRows[:0]
			}
			if len(failureRows) > 0 {
				if _, err := tx.CopyFrom(ctx, pgx.Identifier{"daily_detail_import_failure_stage"},
					[]string{"line_no", "meter_no", "month", "observed_at", "error_message"},
					pgx.CopyFromRows(failureRows)); err != nil {
					return fmt.Errorf("copy import failure stage: %w", err)
				}
				failureRows = failureRows[:0]
			}
			return nil
		}

		if err := importer.StreamDailyDetailPlan(r, plan, loc, func(record importer.DailyDetailRecord) error {
			if !record.OK {
				failureRows = append(failureRows, []any{
					int64(record.LineNo), record.MeterNo, record.Month, record.ObservedAt, record.Error,
				})
			} else {
				status := "no_data"
				if len(record.Days) > 0 {
					status = "valid"
				}
				monthRows = append(monthRows, []any{
					int64(record.LineNo), record.MeterNo, record.Month, record.ObservedAt,
					record.CoveredThrough, status, record.RawRowCount, len(record.Days), record.ContentHash,
				})
				for _, day := range record.Days {
					dayRows = append(dayRows, []any{int64(record.LineNo), day.Date, day.UsageKWh, day.CostYuan})
				}
			}
			if len(monthRows) >= 300 || len(dayRows) >= 12000 || len(failureRows) >= 300 {
				return flush()
			}
			return nil
		}); err != nil {
			return err
		}
		if err := flush(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			CREATE INDEX ON daily_detail_import_month_stage (meter_no,month);
			CREATE INDEX ON daily_detail_import_day_stage (line_no,usage_date);
			CREATE INDEX ON daily_detail_import_failure_stage (meter_no,month);
			ANALYZE daily_detail_import_month_stage;
			ANALYZE daily_detail_import_day_stage;
			ANALYZE daily_detail_import_failure_stage;
		`); err != nil {
			return err
		}

		if err := tx.QueryRow(ctx, `
			WITH source_meters AS (
				SELECT meter_no FROM daily_detail_import_month_stage
				UNION SELECT meter_no FROM daily_detail_import_failure_stage
			)
			SELECT count(*) FILTER (WHERE m.id IS NOT NULL),count(*) FILTER (WHERE m.id IS NULL)
			FROM source_meters s LEFT JOIN meters m ON m.meter_no=s.meter_no
		`).Scan(&result.MatchedMeters, &result.UnmatchedMeters); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			WITH source_meters AS (
				SELECT meter_no FROM daily_detail_import_month_stage
				UNION SELECT meter_no FROM daily_detail_import_failure_stage
			)
			SELECT s.meter_no FROM source_meters s LEFT JOIN meters m ON m.meter_no=s.meter_no
			WHERE m.id IS NULL ORDER BY s.meter_no LIMIT 20
		`)
		if err != nil {
			return err
		}
		result.UnmatchedMeterSamples, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
			var meter string
			err := row.Scan(&meter)
			return meter, err
		})
		if err != nil {
			return err
		}
		if result.UnmatchedMeters > 0 && !acceptPartial {
			return fmt.Errorf("daily detail import has %d meter numbers absent from inventory", result.UnmatchedMeters)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO daily_detail_run_meters (run_id,meter_id,ordinal)
			SELECT $1::uuid,id,row_number() OVER (ORDER BY building,floor,room,meter_no)::integer
			FROM meters WHERE meter_no IN (
				SELECT meter_no FROM daily_detail_import_month_stage
				UNION SELECT meter_no FROM daily_detail_import_failure_stage
			)
			ORDER BY building,floor,room,meter_no
		`, runID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE daily_detail_import_incoming ON COMMIT DROP AS
			SELECT m.id AS meter_id,s.line_no,g.day::date AS usage_date,
				COALESCE(d.usage_kwh::numeric,0)::numeric(20,4) AS usage_kwh,
				COALESCE(d.cost_yuan::numeric,0)::numeric(18,4) AS cost_yuan,
				(d.usage_date IS NOT NULL) AS has_upstream_data,
				s.observed_at,
				CASE WHEN d.usage_date IS NULL THEN '{}'::jsonb
					ELSE jsonb_build_object('sj',to_char(d.usage_date,'YYYY-MM-DD'),'ydl',d.usage_kwh,'ydje',d.cost_yuan) END AS raw_payload
			FROM daily_detail_import_month_stage s
			JOIN meters m ON m.meter_no=s.meter_no
			CROSS JOIN LATERAL generate_series(s.month,s.covered_through,interval '1 day') g(day)
			LEFT JOIN daily_detail_import_day_stage d ON d.line_no=s.line_no AND d.usage_date=g.day::date;
			CREATE UNIQUE INDEX ON daily_detail_import_incoming (meter_id,usage_date);
			ANALYZE daily_detail_import_incoming;
		`); err != nil {
			return fmt.Errorf("materialize canonical daily import: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM daily_detail_import_incoming`).Scan(&result.CoveredDayRows); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM daily_detail_import_day_stage`).Scan(&result.ActualDayRows); err != nil {
			return err
		}
		result.ZeroFilledDayRows = result.CoveredDayRows - result.ActualDayRows

		if _, err := tx.Exec(ctx, `
			INSERT INTO daily_usage_revisions (run_id,meter_id,usage_date,previous_values,current_values)
			SELECT $1::uuid,i.meter_id,i.usage_date,
				jsonb_build_object('ydl',u.usage_kwh,'ydje',u.cost_yuan,'has_data',u.has_upstream_data),
				jsonb_build_object('ydl',i.usage_kwh,'ydje',i.cost_yuan,'has_data',i.has_upstream_data)
			FROM daily_detail_import_incoming i
			JOIN daily_usages u ON u.meter_id=i.meter_id AND u.usage_date=i.usage_date
			WHERE u.observed_at<=i.observed_at
			  AND (u.usage_kwh IS DISTINCT FROM i.usage_kwh OR u.cost_yuan IS DISTINCT FROM i.cost_yuan
			       OR u.has_upstream_data IS DISTINCT FROM i.has_upstream_data)
			ON CONFLICT DO NOTHING
		`, runID); err != nil {
			return fmt.Errorf("record imported daily usage revisions: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM daily_usage_revisions WHERE run_id=$1::uuid`, runID).Scan(&result.RevisionRows); err != nil {
			return err
		}

		tag, err := tx.Exec(ctx, `
			INSERT INTO daily_usages (
				meter_id,usage_date,usage_kwh,cost_yuan,has_upstream_data,
				observed_at,source_run_id,raw_payload
			)
			SELECT meter_id,usage_date,usage_kwh,cost_yuan,has_upstream_data,
				observed_at,$1::uuid,raw_payload
			FROM daily_detail_import_incoming
			ON CONFLICT (meter_id,usage_date) DO UPDATE SET
				usage_kwh=EXCLUDED.usage_kwh,cost_yuan=EXCLUDED.cost_yuan,
				has_upstream_data=EXCLUDED.has_upstream_data,observed_at=EXCLUDED.observed_at,
				source_run_id=EXCLUDED.source_run_id,raw_payload=EXCLUDED.raw_payload,updated_at=now()
			WHERE daily_usages.observed_at<=EXCLUDED.observed_at
			  AND (daily_usages.usage_kwh IS DISTINCT FROM EXCLUDED.usage_kwh
			    OR daily_usages.cost_yuan IS DISTINCT FROM EXCLUDED.cost_yuan
			    OR daily_usages.has_upstream_data IS DISTINCT FROM EXCLUDED.has_upstream_data)
		`, runID)
		if err != nil {
			return fmt.Errorf("upsert imported daily usages: %w", err)
		}
		result.UsageRowsWritten = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `
			INSERT INTO daily_detail_months (
				meter_id,month,covered_through,status,raw_row_count,days_with_usage,
				observed_at,source_run_id
			)
			SELECT m.id,s.month,s.covered_through,s.month_status,s.raw_row_count,
				s.days_with_usage,s.observed_at,$1::uuid
			FROM daily_detail_import_month_stage s JOIN meters m ON m.meter_no=s.meter_no
			ON CONFLICT (meter_id,month) DO UPDATE SET
				covered_through=GREATEST(daily_detail_months.covered_through,EXCLUDED.covered_through),
				status=EXCLUDED.status,raw_row_count=EXCLUDED.raw_row_count,
				days_with_usage=EXCLUDED.days_with_usage,observed_at=EXCLUDED.observed_at,
				source_run_id=EXCLUDED.source_run_id,updated_at=now()
			WHERE daily_detail_months.observed_at<=EXCLUDED.observed_at
			  AND (daily_detail_months.covered_through IS DISTINCT FROM
			         GREATEST(daily_detail_months.covered_through,EXCLUDED.covered_through)
			    OR daily_detail_months.status IS DISTINCT FROM EXCLUDED.status
			    OR daily_detail_months.raw_row_count IS DISTINCT FROM EXCLUDED.raw_row_count
			    OR daily_detail_months.days_with_usage IS DISTINCT FROM EXCLUDED.days_with_usage)
		`, runID); err != nil {
			return fmt.Errorf("upsert imported detail month coverage: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM daily_detail_import_month_stage s
			JOIN meters m ON m.meter_no=s.meter_no
		`).Scan(&result.ImportedMonthCells); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			WITH success AS (
				SELECT m.id meter_id,count(*)::integer months,
					count(*) FILTER (WHERE s.month_status='valid')::integer valid_months,
					count(*) FILTER (WHERE s.month_status='no_data')::integer no_data_months,
					sum((s.covered_through-s.month)+1)::integer days_saved,max(s.observed_at) queried_at
				FROM daily_detail_import_month_stage s JOIN meters m ON m.meter_no=s.meter_no GROUP BY m.id
			), failures AS (
				SELECT m.id meter_id,count(*)::integer months,max(f.observed_at) queried_at,min(f.error_message) error_message
				FROM daily_detail_import_failure_stage f JOIN meters m ON m.meter_no=f.meter_no GROUP BY m.id
			), combined AS (
				SELECT rm.meter_id,COALESCE(s.months,0) success_months,COALESCE(s.valid_months,0) valid_months,
					COALESCE(s.no_data_months,0) no_data_months,COALESCE(f.months,0) error_months,
					COALESCE(s.days_saved,0) days_saved,GREATEST(s.queried_at,f.queried_at) queried_at,f.error_message
				FROM daily_detail_run_meters rm LEFT JOIN success s ON s.meter_id=rm.meter_id
				LEFT JOIN failures f ON f.meter_id=rm.meter_id WHERE rm.run_id=$1::uuid
			)
			INSERT INTO daily_detail_results (
				run_id,meter_id,status,attempts,duration_ms,queried_at,months_requested,
				months_valid,months_no_data,months_error,days_saved,changed_count,error_code,error_message
			)
			SELECT $1::uuid,c.meter_id,
				CASE WHEN error_months>0 AND success_months>0 THEN 'partial'
					WHEN error_months>0 THEN 'error' WHEN valid_months>0 THEN 'valid' ELSE 'no_data' END,
				1,0,c.queried_at,success_months+error_months,valid_months,no_data_months,error_months,
				days_saved,(SELECT count(*) FROM daily_usage_revisions r WHERE r.run_id=$1::uuid AND r.meter_id=c.meter_id),
				CASE WHEN error_months>0 THEN 'source_export_gap' END,c.error_message
			FROM combined c
		`, runID); err != nil {
			return fmt.Errorf("record daily detail import results: %w", err)
		}

		status := "completed"
		if plan.UnresolvedFailedCells > 0 || result.UnmatchedMeters > 0 {
			status = "completed_with_errors"
		}
		messageParts := []string{}
		if plan.UnresolvedFailedCells > 0 {
			messageParts = append(messageParts, fmt.Sprintf("source retained %d unresolved month cells", plan.UnresolvedFailedCells))
		}
		if result.UnmatchedMeters > 0 {
			messageParts = append(messageParts, fmt.Sprintf("skipped %d unmatched meter numbers", result.UnmatchedMeters))
		}
		scopePatch, _ := json.Marshal(map[string]any{
			"matched_meters": result.MatchedMeters, "unmatched_meters": result.UnmatchedMeters,
			"imported_month_cells": result.ImportedMonthCells, "declared_failed_month_cells": plan.DeclaredFailedCells,
			"unresolved_failed_month_cells": plan.UnresolvedFailedCells,
			"jsonl_failed_month_cells":      plan.UniqueFailedCells, "superseded_failed_lines": plan.SupersededFailedLines,
			"missing_meter_cells": plan.MissingMeterCells,
			"actual_day_rows":     result.ActualDayRows, "covered_day_rows": result.CoveredDayRows,
		})
		if _, err := tx.Exec(ctx, dailyDetailCountersSQL+`
			UPDATE daily_detail_runs r SET status=$2,finished_at=now(),heartbeat_at=now(),
				eligible_total=c.processed,processed_total=c.processed,valid_total=c.valid,
				no_data_total=c.no_data,partial_total=c.partial,empty_total=c.empty,error_total=c.errors,
				days_saved_total=c.days_saved,changed_total=c.changed,error_message=NULLIF($3,''),
				scope=scope || $4::jsonb
			FROM counts c WHERE r.id=$1::uuid
		`, runID, status, strings.Join(messageParts, "; "), scopePatch); err != nil {
			return err
		}
		result.Status = status
		return nil
	})
	if err != nil {
		return result, err
	}
	// 批量导入已写入历史 daily_usages。全校空房除数由其汇总。
	if err := RefreshCampusRollup(ctx, pool); err != nil {
		return result, fmt.Errorf("rebuild campus rollup after daily detail import: %w", err)
	}
	if err := RefreshRollupsForDailyDetailRun(ctx, pool, result.RunID); err != nil {
		return result, fmt.Errorf("rebuild consumption rollups after daily detail import: %w", err)
	}
	return result, nil
}

func createDailyDetailImportRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	plan *importer.DailyDetailPlan,
	acceptPartial bool,
	provenance map[string]string,
) (string, error) {
	monthDates, err := parseBillMonths(plan.Months)
	if err != nil {
		return "", err
	}
	var existing string
	err = pool.QueryRow(ctx, `
		SELECT id::text FROM daily_detail_runs
		WHERE trigger='import' AND scope->>'source_sha256'=$1
		  AND status IN ('running','completed','completed_with_errors')
		ORDER BY started_at DESC LIMIT 1
	`, plan.SHA256).Scan(&existing)
	if err == nil {
		return "", fmt.Errorf("%w: run %s", ErrDailyDetailImportExists, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var inventory, excluded int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE active),count(*) FILTER (WHERE active AND excluded) FROM meters
	`).Scan(&inventory, &excluded); err != nil {
		return "", err
	}
	scope := map[string]any{
		"source_file": plan.SourceName, "source_sha256": plan.SHA256,
		"source_lines": plan.TotalLines,
	}
	for key, value := range provenance {
		if strings.TrimSpace(value) != "" {
			scope[key] = value
		}
	}
	settings := map[string]any{
		"initialization": true, "baseline_accepted": acceptPartial || plan.UnresolvedFailedCells == 0,
		"source_type": "electricdetail_jsonl", "strict_validation": true,
	}
	scopeJSON, _ := json.Marshal(scope)
	settingsJSON, _ := json.Marshal(settings)
	var runID string
	err = pool.QueryRow(ctx, `
		INSERT INTO daily_detail_runs (
			trigger,status,months,scope,config_snapshot,inventory_total,excluded_total,heartbeat_at
		) VALUES ('import','running',$1,$2,$3,$4,$5,now()) RETURNING id::text
	`, monthDates, scopeJSON, settingsJSON, inventory, excluded).Scan(&runID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "daily_detail_runs_one_active" {
			return "", ErrDailyDetailAlreadyRunning
		}
		return "", err
	}
	return runID, nil
}

func truncateImportError(message string, max int) string {
	message = strings.TrimSpace(message)
	if len(message) <= max {
		return message
	}
	return message[:max]
}
