package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ApplyLegacySnapshot(
	ctx context.Context,
	pool *pgxpool.Pool,
	plan importer.SnapshotPlan,
) (SnapshotApplyResult, error) {
	if len(plan.Records) == 0 {
		return SnapshotApplyResult{}, errors.New("snapshot plan has no records")
	}
	if len(plan.Errors) > 0 {
		return SnapshotApplyResult{}, fmt.Errorf("snapshot plan has %d document errors", len(plan.Errors))
	}

	result := SnapshotApplyResult{
		Total:      plan.Total,
		Valid:      plan.Valid,
		Stale:      plan.Stale,
		Empty:      plan.Empty,
		Error:      plan.Error,
		ParseError: plan.ParseError,
	}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id::text
			FROM scan_runs
			WHERE config_snapshot ->> 'source' = 'legacy_import'
			  AND config_snapshot ->> 'source_hash' = $1
			ORDER BY started_at DESC
			LIMIT 1
		`, plan.SourceHash).Scan(&result.RunID)
		if err == nil {
			result.Idempotent = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check existing snapshot import: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE snapshot_stage (
				building text NOT NULL,
				floor text NOT NULL,
				room text NOT NULL,
				meter_no text NOT NULL,
				status text NOT NULL,
				attempts integer NOT NULL,
				queried_at timestamptz NOT NULL,
				reading_time timestamptz,
				prepaid_yuan numeric(18, 4),
				subsidy_yuan numeric(18, 4),
				total_yuan numeric(18, 4),
				total_kwh numeric(20, 4),
				freshness text,
				error_message text,
				reading_hash char(64)
			) ON COMMIT DROP
		`); err != nil {
			return fmt.Errorf("create snapshot staging table: %w", err)
		}
		rows := make([][]any, 0, len(plan.Records))
		for _, record := range plan.Records {
			rows = append(rows, []any{
				record.Building,
				record.Floor,
				record.Room,
				record.MeterNo,
				record.Status,
				record.Attempts,
				record.QueriedAt,
				nullableTime(record.ReadingTime),
				nullableString(record.PrepaidYuan),
				nullableString(record.SubsidyYuan),
				nullableString(record.TotalYuan),
				nullableString(record.TotalKWh),
				nullableText(record.Freshness),
				nullableText(record.Error),
				nullableText(record.ReadingHash),
			})
		}
		if _, err := tx.CopyFrom(
			ctx,
			pgx.Identifier{"snapshot_stage"},
			[]string{
				"building", "floor", "room", "meter_no", "status", "attempts", "queried_at",
				"reading_time", "prepaid_yuan", "subsidy_yuan", "total_yuan", "total_kwh",
				"freshness", "error_message", "reading_hash",
			},
			pgx.CopyFromRows(rows),
		); err != nil {
			return fmt.Errorf("copy snapshot staging rows: %w", err)
		}

		var unmatched int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM snapshot_stage s
			LEFT JOIN meters m ON m.meter_no = s.meter_no
			WHERE m.id IS NULL
		`).Scan(&unmatched); err != nil {
			return fmt.Errorf("count unmatched snapshot meters: %w", err)
		}
		if unmatched > 0 {
			return fmt.Errorf("snapshot contains %d meters not present in inventory", unmatched)
		}

		var inventoryTotal, excludedTotal, eligibleTotal int
		if err := tx.QueryRow(ctx, `
			SELECT
				count(*) FILTER (WHERE active),
				count(*) FILTER (WHERE active AND excluded),
				count(*) FILTER (WHERE active AND NOT excluded)
			FROM meters
		`).Scan(&inventoryTotal, &excludedTotal, &eligibleTotal); err != nil {
			return fmt.Errorf("count inventory for snapshot run: %w", err)
		}
		runStatus := "completed"
		if plan.Empty+plan.Error+plan.ParseError > 0 {
			runStatus = "completed_with_errors"
		}
		configJSON, err := json.Marshal(map[string]any{
			"source":      "legacy_import",
			"source_name": plan.SourceName,
			"source_hash": plan.SourceHash,
		})
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO scan_runs (
				trigger, status, config_snapshot, started_at, finished_at, heartbeat_at,
				inventory_total, excluded_total, eligible_total, processed_total,
				valid_total, stale_total, empty_total, error_total, parse_error_total
			)
			VALUES (
				'manual', $1, $2, $3, $3, $3,
				$4, $5, $6, $7, $8, $9, $10, $11, $12
			)
			RETURNING id::text
		`, runStatus, configJSON, plan.SourceUpdatedAt,
			inventoryTotal, excludedTotal, eligibleTotal, plan.Total,
			plan.Valid, plan.Stale, plan.Empty, plan.Error, plan.ParseError,
		).Scan(&result.RunID); err != nil {
			return fmt.Errorf("create legacy scan run: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO anomaly_events (
				type, severity, meter_id, scan_run_id, payload
			)
			SELECT
				'same_timestamp_conflict',
				'critical',
				m.id,
				$1::uuid,
				jsonb_build_object(
					'reading_time', s.reading_time,
					'existing_hash', r.reading_hash,
					'incoming_hash', s.reading_hash,
					'source', 'legacy_import'
				)
			FROM snapshot_stage s
			JOIN meters m ON m.meter_no = s.meter_no
			JOIN meter_readings r ON r.meter_id = m.id AND r.reading_time = s.reading_time
			WHERE s.status IN ('valid', 'stale')
			  AND (
				r.prepaid_yuan IS DISTINCT FROM s.prepaid_yuan OR
				r.subsidy_yuan IS DISTINCT FROM s.subsidy_yuan OR
				r.total_yuan IS DISTINCT FROM s.total_yuan OR
				r.total_kwh IS DISTINCT FROM s.total_kwh
			  )
		`, result.RunID); err != nil {
			return fmt.Errorf("record reading conflicts: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO meter_readings (
				meter_id, reading_time, observed_at, prepaid_yuan, subsidy_yuan,
				total_yuan, total_kwh, freshness, source, source_ref, reading_hash
			)
			SELECT
				m.id, s.reading_time, s.queried_at, s.prepaid_yuan, s.subsidy_yuan,
				s.total_yuan, s.total_kwh, s.freshness, 'legacy_import', $1, s.reading_hash
			FROM snapshot_stage s
			JOIN meters m ON m.meter_no = s.meter_no
			WHERE s.status IN ('valid', 'stale')
			ON CONFLICT (meter_id, reading_time) DO NOTHING
		`, result.RunID); err != nil {
			return fmt.Errorf("insert legacy readings: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO scan_results (
				run_id, meter_id, status, attempts, duration_ms, queried_at,
				reading_id, error_code, error_message
			)
			SELECT
				$1::uuid,
				m.id,
				s.status,
				s.attempts,
				0,
				s.queried_at,
				r.id,
				CASE WHEN s.status IN ('error', 'parse_error', 'empty') THEN 'legacy_' || s.status ELSE NULL END,
				s.error_message
			FROM snapshot_stage s
			JOIN meters m ON m.meter_no = s.meter_no
			LEFT JOIN meter_readings r ON r.meter_id = m.id AND r.reading_time = s.reading_time
		`, result.RunID); err != nil {
			return fmt.Errorf("insert legacy scan results: %w", err)
		}
		return nil
	})
	if err != nil {
		return SnapshotApplyResult{}, err
	}
	return result, nil
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return *value
}
