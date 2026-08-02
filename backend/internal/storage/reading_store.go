package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/statistics"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type preparedOutcome struct {
	Status       string
	ErrorCode    string
	ErrorMessage string
	QueriedAt    time.Time
	ReadingTime  *time.Time
	PrepaidYuan  *string
	SubsidyYuan  *string
	TotalYuan    *string
	TotalKWh     *string
	MeterStatus  string
	ChargeType   string
	Freshness    string
	ReadingHash  string
}

func RecordScanOutcome(
	ctx context.Context,
	pool *pgxpool.Pool,
	runID string,
	meter ScanMeter,
	result provider.Result,
	attempts int,
	duration time.Duration,
	loc *time.Location,
	staleAfter time.Duration,
) error {
	outcome := prepareOutcome(result, loc, staleAfter)
	if attempts < 1 {
		attempts = 1
	}
	durationMS := duration.Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var readingID string
		duplicate := false
		if outcome.ReadingTime != nil && outcome.TotalKWh != nil &&
			(outcome.Status == "valid" || outcome.Status == "stale") {
			var existingPrepaid, existingSubsidy, existingTotal, existingKWh string
			err := tx.QueryRow(ctx, `
				SELECT
					id::text,
					COALESCE(prepaid_yuan::text, ''),
					COALESCE(subsidy_yuan::text, ''),
					COALESCE(total_yuan::text, ''),
					total_kwh::text
				FROM meter_readings
				WHERE meter_id = $1::uuid AND reading_time = $2
			`, meter.ID, *outcome.ReadingTime).Scan(
				&readingID,
				&existingPrepaid,
				&existingSubsidy,
				&existingTotal,
				&existingKWh,
			)
			switch {
			case err == nil && readingValuesMatch(
				outcome,
				existingPrepaid,
				existingSubsidy,
				existingTotal,
				existingKWh,
			):
				duplicate = true
				if _, updateErr := tx.Exec(ctx, `
					UPDATE meter_readings
					SET
						meter_status = COALESCE(NULLIF($2, ''), meter_status),
						charge_type = COALESCE(NULLIF($3, ''), charge_type),
						freshness = $4
					WHERE id = $1::uuid
				`, readingID, outcome.MeterStatus, outcome.ChargeType, outcome.Freshness); updateErr != nil {
					return fmt.Errorf("refresh duplicate reading metadata: %w", updateErr)
				}
			case err == nil:
				outcome.Status = "parse_error"
				outcome.ErrorCode = "same_timestamp_conflict"
				outcome.ErrorMessage = "upstream returned different values for an existing reading_time"
				payload, _ := json.Marshal(map[string]any{
					"reading_time": *outcome.ReadingTime,
				})
				if _, insertErr := tx.Exec(ctx, `
					INSERT INTO anomaly_events (
						type, severity, meter_id, reading_id, scan_run_id, payload
					)
					VALUES (
						'same_timestamp_conflict', 'critical', $1::uuid, $2::uuid, $3::uuid, $4
					)
					ON CONFLICT DO NOTHING
				`, meter.ID, readingID, runID, payload); insertErr != nil {
					return fmt.Errorf("record timestamp conflict: %w", insertErr)
				}
			case errors.Is(err, pgx.ErrNoRows):
				// 使用 ON CONFLICT，禁止裸 INSERT。
				// 手动刷新与批量扫描可同时命中同一 (meter_id, reading_time)。
				// 冲突由唯一约束裁决。失败方按重复读数处理。避免 23505 整事务回滚。
				insertErr := tx.QueryRow(ctx, `
					INSERT INTO meter_readings (
						meter_id, reading_time, observed_at,
						prepaid_yuan, subsidy_yuan, total_yuan, total_kwh,
						meter_status, charge_type, freshness,
						source, source_ref, reading_hash
					)
					VALUES (
						$1::uuid, $2, $3,
						$4, $5, $6, $7,
						$8, $9, $10,
						'live_scan', $11, $12
					)
					ON CONFLICT (meter_id, reading_time) DO NOTHING
					RETURNING id::text
				`,
					meter.ID,
					*outcome.ReadingTime,
					outcome.QueriedAt,
					nullableString(outcome.PrepaidYuan),
					nullableString(outcome.SubsidyYuan),
					nullableString(outcome.TotalYuan),
					nullableString(outcome.TotalKWh),
					nullableText(outcome.MeterStatus),
					nullableText(outcome.ChargeType),
					outcome.Freshness,
					runID,
					outcome.ReadingHash,
				).Scan(&readingID)
				switch {
				case insertErr == nil:
					if err := createDeltaForReading(ctx, tx, runID, meter.ID, readingID, *outcome.ReadingTime, *outcome.TotalKWh); err != nil {
						return err
					}
				case errors.Is(insertErr, pgx.ErrNoRows):
					// 并发另一方先落库。delta 也由它创建。
					duplicate = true
					if err := tx.QueryRow(ctx, `
						SELECT id::text FROM meter_readings
						WHERE meter_id = $1::uuid AND reading_time = $2
					`, meter.ID, *outcome.ReadingTime).Scan(&readingID); err != nil {
						return fmt.Errorf("load concurrently inserted reading: %w", err)
					}
				default:
					return fmt.Errorf("insert live reading: %w", insertErr)
				}
			default:
				return fmt.Errorf("look up existing reading: %w", err)
			}

			if outcome.Freshness == "stale" && readingID != "" {
				payload, _ := json.Marshal(map[string]any{
					"reading_time": *outcome.ReadingTime,
					"observed_at":  outcome.QueriedAt,
				})
				if _, err := tx.Exec(ctx, `
					INSERT INTO anomaly_events (
						type, severity, meter_id, reading_id, scan_run_id, payload
					)
					VALUES ('stale_reading', 'warning', $1::uuid, $2::uuid, $3::uuid, $4)
					ON CONFLICT DO NOTHING
				`, meter.ID, readingID, runID, payload); err != nil {
					return fmt.Errorf("record stale reading: %w", err)
				}
			}
		}
		if outcome.Status == "parse_error" && outcome.ErrorCode != "same_timestamp_conflict" {
			payload, _ := json.Marshal(map[string]any{
				"error_code":    outcome.ErrorCode,
				"error_message": outcome.ErrorMessage,
			})
			if _, err := tx.Exec(ctx, `
				INSERT INTO anomaly_events (type,severity,meter_id,scan_run_id,payload)
				VALUES ('parse_drift','warning',$1::uuid,$2::uuid,$3)
			`, meter.ID, runID, payload); err != nil {
				return fmt.Errorf("record parse drift: %w", err)
			}
		}

		tag, err := tx.Exec(ctx, `
			INSERT INTO scan_results (
				run_id, meter_id, status, attempts, duration_ms, queried_at,
				reading_id, duplicate_reading, error_code, error_message
			)
			VALUES (
				$1::uuid, $2::uuid, $3, $4, $5, $6,
				$7::uuid, $8, NULLIF($9, ''), NULLIF($10, '')
			)
			ON CONFLICT (run_id, meter_id) DO NOTHING
		`,
			runID,
			meter.ID,
			outcome.Status,
			attempts,
			durationMS,
			outcome.QueriedAt,
			nullableUUIDString(readingID),
			duplicate,
			outcome.ErrorCode,
			outcome.ErrorMessage,
		)
		if err != nil {
			return fmt.Errorf("insert scan result: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE scan_runs
			SET
				heartbeat_at = now(),
				processed_total = processed_total + 1,
				valid_total = valid_total + CASE WHEN $2 = 'valid' THEN 1 ELSE 0 END,
				stale_total = stale_total + CASE WHEN $2 = 'stale' THEN 1 ELSE 0 END,
				empty_total = empty_total + CASE WHEN $2 = 'empty' THEN 1 ELSE 0 END,
				error_total = error_total + CASE WHEN $2 IN ('error', 'canceled') THEN 1 ELSE 0 END,
				parse_error_total = parse_error_total + CASE WHEN $2 = 'parse_error' THEN 1 ELSE 0 END,
				duplicate_reading_total = duplicate_reading_total + CASE WHEN $3 THEN 1 ELSE 0 END
			WHERE id = $1::uuid
		`, runID, outcome.Status, duplicate); err != nil {
			return fmt.Errorf("advance scan counters: %w", err)
		}
		return nil
	})
}

func createDeltaForReading(
	ctx context.Context,
	tx pgx.Tx,
	runID, meterID, currentReadingID string,
	currentTime time.Time,
	currentKWh string,
) error {
	var laterID string
	var laterTime time.Time
	err := tx.QueryRow(ctx, `
		SELECT id::text, reading_time
		FROM meter_readings
		WHERE meter_id=$1::uuid AND reading_time>$2
		ORDER BY reading_time DESC LIMIT 1
	`, meterID, currentTime).Scan(&laterID, &laterTime)
	if err == nil {
		payload, _ := json.Marshal(map[string]any{
			"current_reading_id": currentReadingID,
			"current_time":       currentTime,
			"later_reading_id":   laterID,
			"later_time":         laterTime,
		})
		if _, err := tx.Exec(ctx, `
			INSERT INTO anomaly_events (type,severity,meter_id,reading_id,scan_run_id,payload)
			VALUES ('time_regression','warning',$1::uuid,$2::uuid,$3::uuid,$4)
			ON CONFLICT DO NOTHING
		`, meterID, currentReadingID, runID, payload); err != nil {
			return fmt.Errorf("record time regression anomaly: %w", err)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("look for later reading: %w", err)
	}

	var previousID, previousKWh string
	var previousTime time.Time
	err = tx.QueryRow(ctx, `
		SELECT id::text, reading_time, total_kwh::text
		FROM meter_readings
		WHERE meter_id = $1::uuid AND reading_time < $2
		ORDER BY reading_time DESC
		LIMIT 1
	`, meterID, currentTime).Scan(&previousID, &previousTime, &previousKWh)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load previous reading: %w", err)
	}
	delta, err := statistics.CalculateDelta(previousKWh, currentKWh, previousTime, currentTime)
	if err != nil {
		return fmt.Errorf("calculate reading delta: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO consumption_deltas (
			meter_id, previous_reading_id, current_reading_id,
			from_time, to_time, delta_kwh, status
		)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7)
		ON CONFLICT (current_reading_id) DO NOTHING
	`, meterID, previousID, currentReadingID, previousTime, currentTime, delta.Value, delta.Status); err != nil {
		return fmt.Errorf("insert consumption delta: %w", err)
	}
	if delta.Status == statistics.DeltaNegativeReset {
		payload, _ := json.Marshal(map[string]any{
			"previous_reading_id": previousID,
			"current_reading_id":  currentReadingID,
			"from_time":           previousTime,
			"to_time":             currentTime,
			"delta_kwh":           delta.Value,
		})
		if _, err := tx.Exec(ctx, `
			INSERT INTO anomaly_events (
				type, severity, meter_id, reading_id, scan_run_id, payload
			)
			VALUES ('negative_reset', 'critical', $1::uuid, $2::uuid, $3::uuid, $4)
			ON CONFLICT DO NOTHING
		`, meterID, currentReadingID, runID, payload); err != nil {
			return fmt.Errorf("record negative delta anomaly: %w", err)
		}
	}
	return nil
}

func prepareOutcome(
	result provider.Result,
	loc *time.Location,
	staleAfter time.Duration,
) preparedOutcome {
	queriedAt := result.QueriedAt
	if queriedAt.IsZero() {
		queriedAt = time.Now()
	}
	outcome := preparedOutcome{
		Status:    string(result.Status),
		QueriedAt: queriedAt,
	}
	if outcome.Status == "" {
		outcome.Status = "error"
	}
	if len(result.Errors) > 0 {
		outcome.ErrorMessage = truncateText(result.Errors[0], 2000)
	}
	if !result.OK {
		outcome.ErrorCode = "upstream_" + outcome.Status
		return outcome
	}

	readingTime, err := parseUpstreamTime(result.Balance.ReadingTime, loc)
	if err != nil {
		outcome.Status = "parse_error"
		outcome.ErrorCode = "invalid_reading_time"
		outcome.ErrorMessage = err.Error()
		return outcome
	}
	outcome.ReadingTime = &readingTime
	outcome.PrepaidYuan = canonicalDecimal(result.Balance.PrepaidYuan)
	outcome.SubsidyYuan = canonicalDecimal(result.Balance.SubsidyYuan)
	outcome.TotalYuan = canonicalDecimal(result.Balance.TotalYuan)
	outcome.TotalKWh = canonicalDecimal(result.Balance.TotalKWh)
	if outcome.TotalKWh == nil || (outcome.PrepaidYuan == nil && outcome.TotalYuan == nil) {
		outcome.Status = "parse_error"
		outcome.ErrorCode = "invalid_numeric_reading"
		outcome.ErrorMessage = "valid upstream result is missing a numeric balance or cumulative kWh"
		outcome.ReadingTime = nil
		return outcome
	}
	outcome.MeterStatus = strings.TrimSpace(result.Balance.MeterStatus)
	outcome.ChargeType = strings.TrimSpace(result.Balance.ChargeType)
	outcome.Freshness = "fresh"
	outcome.Status = "valid"
	if staleAfter > 0 && queriedAt.Sub(readingTime) > staleAfter {
		outcome.Freshness = "stale"
		outcome.Status = "stale"
	}
	parts := []string{
		derefString(outcome.PrepaidYuan),
		derefString(outcome.SubsidyYuan),
		derefString(outcome.TotalYuan),
		derefString(outcome.TotalKWh),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	outcome.ReadingHash = hex.EncodeToString(sum[:])
	return outcome
}

func readingValuesMatch(outcome preparedOutcome, prepaid, subsidy, total, kwh string) bool {
	return prepaid == derefString(outcome.PrepaidYuan) &&
		subsidy == derefString(outcome.SubsidyYuan) &&
		total == derefString(outcome.TotalYuan) &&
		kwh == derefString(outcome.TotalKWh)
}

func canonicalDecimal(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || value == "undefined" {
		return nil
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil
	}
	canonical := number.FloatString(4)
	return &canonical
}

func parseUpstreamTime(value string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano} {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported reading_time %q", value)
}

func nullableUUIDString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncateText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
