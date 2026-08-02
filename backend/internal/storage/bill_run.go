package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBillAlreadyRunning = errors.New("a monthly bill run is already active")
var ErrBillNotRunnable = errors.New("bill run is not pending or interrupted")
var ErrBillScheduleExists = errors.New("this month's scheduled bill run already exists")

type BillRunScope struct {
	Building string `json:"building,omitempty"`
	Floor    string `json:"floor,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type BillRunSettings struct {
	QPS            float64 `json:"qps"`
	Concurrency    int     `json:"concurrency"`
	RetryMax       int     `json:"retry_max"`
	MonthRetryMax  int     `json:"month_retry_max"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

type BillRunCounters struct {
	InventoryTotal      int `json:"inventory_total"`
	ExcludedTotal       int `json:"excluded_total"`
	EligibleTotal       int `json:"eligible_total"`
	ProcessedTotal      int `json:"processed_total"`
	ValidTotal          int `json:"valid_total"`
	PartialTotal        int `json:"partial_total"`
	NoDataTotal         int `json:"no_data_total"`
	EmptyTotal          int `json:"empty_total"`
	ErrorTotal          int `json:"error_total"`
	MonthDataTotal      int `json:"month_data_total"`
	MonthNoDataTotal    int `json:"month_no_data_total"`
	MonthPartialTotal   int `json:"month_partial_total"`
	MonthErrorTotal     int `json:"month_error_total"`
	CanonicalSavedTotal int `json:"canonical_saved_total"`
	ChangedTotal        int `json:"changed_total"`
}

type BillRunView struct {
	ID           string          `json:"id"`
	ParentRunID  *string         `json:"parent_run_id"`
	Trigger      string          `json:"trigger"`
	Status       string          `json:"status"`
	Mode         string          `json:"mode"`
	Months       []string        `json:"months"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	HeartbeatAt  *time.Time      `json:"heartbeat_at"`
	QPS          float64         `json:"qps"`
	Concurrency  int             `json:"concurrency"`
	Limit        *int            `json:"limit"`
	Counters     BillRunCounters `json:"counters"`
	ErrorMessage *string         `json:"error_message,omitempty"`
}

type BillResultView struct {
	RunID           string    `json:"run_id"`
	Meter           string    `json:"meter"`
	Building        string    `json:"building"`
	Floor           string    `json:"floor"`
	Room            string    `json:"room"`
	Status          string    `json:"status"`
	Attempts        int       `json:"attempts"`
	DurationMS      int64     `json:"duration_ms"`
	QueriedAt       time.Time `json:"queried_at"`
	MonthsRequested int       `json:"months_requested"`
	MonthsWithData  int       `json:"months_with_data"`
	MonthsNoData    int       `json:"months_no_data"`
	MonthsPartial   int       `json:"months_partial"`
	MonthsError     int       `json:"months_error"`
	CanonicalSaved  int       `json:"canonical_saved"`
	ChangedCount    int       `json:"changed_count"`
	ErrorCode       *string   `json:"error_code,omitempty"`
	ErrorMessage    *string   `json:"error_message,omitempty"`
}

type BillRevisionView struct {
	ID               string         `json:"id"`
	RunID            string         `json:"run_id"`
	Meter            string         `json:"meter"`
	Month            string         `json:"month"`
	PreviousValues   map[string]any `json:"previous_values"`
	CurrentValues    map[string]any `json:"current_values"`
	DetectedAt       time.Time      `json:"detected_at"`
	Acknowledged     bool           `json:"acknowledged"`
	AcknowledgedAt   *time.Time     `json:"acknowledged_at"`
	AcknowledgedNote *string        `json:"acknowledged_note"`
}

func CreateBillRun(ctx context.Context, pool *pgxpool.Pool, trigger string, parentRunID *string, months []string, scope BillRunScope, settings BillRunSettings) (string, error) {
	monthDates, err := parseBillMonths(months)
	if err != nil {
		return "", err
	}
	var runID string
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if trigger == "schedule" {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM bill_runs
					WHERE (trigger='schedule' OR scope='{}'::jsonb)
					  AND started_at>=date_trunc('month',now())
					  AND started_at<date_trunc('month',now())+interval '1 month'
				)
			`).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrBillScheduleExists
			}
		}
		var inventoryTotal, excludedTotal int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE active AND excluded)
			FROM meters
		`).Scan(&inventoryTotal, &excludedTotal); err != nil {
			return fmt.Errorf("count bill inventory: %w", err)
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
			INSERT INTO bill_runs (
				parent_run_id,trigger,status,mode,months,scope,config_snapshot,
				inventory_total,excluded_total,heartbeat_at
			) VALUES ($1::uuid,$2,'pending','visible_months',$3,$4,$5,$6,$7,now())
			RETURNING id::text
		`, nullableUUID(parentRunID), trigger, monthDates, scopeJSON, settingsJSON, inventoryTotal, excludedTotal).Scan(&runID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "bill_runs_one_active_school" {
				return ErrBillAlreadyRunning
			}
			return fmt.Errorf("create bill run: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO bill_run_meters (run_id,meter_id,ordinal)
			SELECT $1::uuid, selected.id, selected.ordinal FROM (
				SELECT m.id, row_number() OVER (ORDER BY m.building,m.floor,m.room,m.meter_no)::integer ordinal
				FROM meters m
				WHERE m.active AND NOT m.excluded
				  AND ($2='' OR m.building=$2) AND ($3='' OR m.floor=$3)
				ORDER BY m.building,m.floor,m.room,m.meter_no
				LIMIT NULLIF($4,0)
			) selected
		`, runID, scope.Building, scope.Floor, scope.Limit)
		if err != nil {
			return fmt.Errorf("freeze bill meter set: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return errors.New("bill scope contains no eligible meters")
		}
		_, err = tx.Exec(ctx, `UPDATE bill_runs SET eligible_total=$2 WHERE id=$1::uuid`, runID, tag.RowsAffected())
		return err
	})
	return runID, err
}

func CountBillScope(ctx context.Context, pool *pgxpool.Pool, scope BillRunScope, _ int) (BillRunCounters, error) {
	var counters BillRunCounters
	err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE active AND excluded),
			LEAST(count(*) FILTER (WHERE active AND NOT excluded
				AND ($1='' OR building=$1) AND ($2='' OR floor=$2)),
				COALESCE(NULLIF($3,0),2147483647))::integer
		FROM meters
	`, scope.Building, scope.Floor, scope.Limit).Scan(&counters.InventoryTotal, &counters.ExcludedTotal, &counters.EligibleTotal)
	return counters, err
}

func CreateRetryBillRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	parentRunID string,
	statuses []string,
	settings BillRunSettings,
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
			INSERT INTO bill_runs (
				parent_run_id,trigger,status,mode,months,scope,config_snapshot,
				inventory_total,excluded_total,heartbeat_at
			)
			SELECT parent.id,'retry','pending','visible_months',parent.months,
				jsonb_build_object('retry_statuses',to_jsonb($2::text[])),$3,$4,$5,now()
			FROM bill_runs parent
			WHERE parent.id=$1::uuid
			  AND parent.status IN ('completed','completed_with_errors','failed')
			RETURNING id::text
		`, parentRunID, statuses, settingsJSON, inventoryTotal, excludedTotal).Scan(&runID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "bill_runs_one_active_school" {
				return ErrBillAlreadyRunning
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("terminal parent bill run: %w", pgx.ErrNoRows)
			}
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO bill_run_meters (run_id,meter_id,ordinal)
			SELECT $1::uuid,br.meter_id,
				row_number() OVER (ORDER BY m.building,m.floor,m.room,m.meter_no)::integer
			FROM bill_results br JOIN meters m ON m.id=br.meter_id
			WHERE br.run_id=$2::uuid AND br.status=ANY($3::text[])
			ORDER BY m.building,m.floor,m.room,m.meter_no
		`, runID, parentRunID, statuses)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("parent bill run has no results matching retry statuses")
		}
		_, err = tx.Exec(ctx, `UPDATE bill_runs SET eligible_total=$2 WHERE id=$1::uuid`, runID, tag.RowsAffected())
		return err
	})
	return runID, err
}

func MarkBillRunRunning(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE bill_runs SET status='running',heartbeat_at=now(),finished_at=NULL
			WHERE id=$1::uuid AND status IN ('pending','interrupted')
		`, runID)
		if err != nil {
			return fmt.Errorf("mark bill run running: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrBillNotRunnable
		}
		return nil
	})
}

func PendingBillMeters(ctx context.Context, pool *pgxpool.Pool, runID string) ([]ScanMeter, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.id::text,m.meter_no,m.building,m.floor,m.room,rm.ordinal
		FROM bill_run_meters rm JOIN meters m ON m.id=rm.meter_id
		LEFT JOIN bill_results br ON br.run_id=rm.run_id AND br.meter_id=rm.meter_id
		WHERE rm.run_id=$1::uuid AND br.run_id IS NULL ORDER BY rm.ordinal
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	meters := []ScanMeter{}
	for rows.Next() {
		var meter ScanMeter
		if err := rows.Scan(&meter.ID, &meter.MeterNo, &meter.Building, &meter.Floor, &meter.Room, &meter.Ordinal); err != nil {
			return nil, err
		}
		meters = append(meters, meter)
	}
	return meters, rows.Err()
}

func BillRunMonths(ctx context.Context, pool *pgxpool.Pool, runID string) ([]string, error) {
	var months []time.Time
	if err := pool.QueryRow(ctx, `SELECT months FROM bill_runs WHERE id=$1::uuid`, runID).Scan(&months); err != nil {
		return nil, err
	}
	out := make([]string, len(months))
	for i, month := range months {
		out[i] = month.Format("2006-01")
	}
	return out, nil
}

func RecordBillOutcome(ctx context.Context, pool *pgxpool.Pool, runID string, meter ScanMeter, result provider.MonthlyBillsResult, duration time.Duration) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		dataCount, noDataCount, partialCount, errorCount, savedCount, changedCount := 0, 0, 0, 0, 0, 0
		recordErrors := append([]string(nil), result.Errors...)
		for _, bill := range result.Bills {
			month, err := time.Parse("2006-01", bill.Month)
			if err != nil {
				return fmt.Errorf("invalid bill month %q: %w", bill.Month, err)
			}
			status := bill.Status
			observationError := bill.ErrorMessage
			values := []*string{
				canonicalDecimal(bill.Data.StartKWh), canonicalDecimal(bill.Data.EndKWh),
				canonicalDecimal(bill.Data.UsageKWh), canonicalDecimal(bill.Data.CostYuan),
			}
			if status == "data" {
				for _, value := range values {
					if value == nil {
						status = "partial"
						observationError = "invalid or missing numeric field"
						recordErrors = append(recordErrors, bill.Month+": "+observationError)
						break
					}
				}
			}
			switch status {
			case "data":
				dataCount++
			case "no_data":
				noDataCount++
			case "partial":
				partialCount++
			default:
				status = "error"
				errorCount++
			}
			raw, _ := json.Marshal(bill.Data)
			if _, err := tx.Exec(ctx, `
				INSERT INTO monthly_bill_observations (
					run_id,meter_id,month,status,start_kwh,end_kwh,usage_kwh,cost_yuan,
					upstream_message,upstream_period,error_message,observed_at,raw_payload
				) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),$12,$13)
			`, runID, meter.ID, month, status, values[0], values[1], values[2], values[3],
				bill.Data.Message, bill.Data.Period, observationError, result.QueriedAt, raw); err != nil {
				return fmt.Errorf("record monthly bill observation: %w", err)
			}
			if status != "data" {
				continue
			}

			var existed, changed bool
			if err := tx.QueryRow(ctx, `
				SELECT count(*)>0, COALESCE(bool_or(
					start_kwh IS DISTINCT FROM $3::numeric OR end_kwh IS DISTINCT FROM $4::numeric OR
					usage_kwh IS DISTINCT FROM $5::numeric OR cost_yuan IS DISTINCT FROM $6::numeric
				),false)
				FROM monthly_bills WHERE meter_id=$1::uuid AND month=$2
			`, meter.ID, month, *values[0], *values[1], *values[2], *values[3]).Scan(&existed, &changed); err != nil {
				return err
			}
			if existed && changed {
				if _, err := tx.Exec(ctx, `
					INSERT INTO bill_revisions (run_id,meter_id,month,previous_values,current_values)
					SELECT $1::uuid,$2::uuid,$3,
						jsonb_build_object('qcdl',start_kwh,'qmdl',end_kwh,'ydl',usage_kwh,'ydje',cost_yuan),
						jsonb_build_object('qcdl',$4::numeric,'qmdl',$5::numeric,'ydl',$6::numeric,'ydje',$7::numeric)
					FROM monthly_bills WHERE meter_id=$2::uuid AND month=$3
				`, runID, meter.ID, month, *values[0], *values[1], *values[2], *values[3]); err != nil {
					return fmt.Errorf("record bill revision: %w", err)
				}
				changedCount++
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO monthly_bills (meter_id,month,start_kwh,end_kwh,usage_kwh,cost_yuan,observed_at,source)
				VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,'live_query')
				ON CONFLICT (meter_id,month) DO UPDATE SET
					start_kwh=EXCLUDED.start_kwh,end_kwh=EXCLUDED.end_kwh,usage_kwh=EXCLUDED.usage_kwh,
					cost_yuan=EXCLUDED.cost_yuan,observed_at=EXCLUDED.observed_at,source=EXCLUDED.source
			`, meter.ID, month, *values[0], *values[1], *values[2], *values[3], result.QueriedAt); err != nil {
				return fmt.Errorf("upsert canonical monthly bill: %w", err)
			}
			savedCount++
		}

		status := string(result.Status)
		if !result.OK {
			if status != "empty" {
				status = "error"
			}
		} else {
			switch {
			case errorCount+partialCount > 0:
				status = "partial"
			case dataCount == 0:
				status = "no_data"
			default:
				status = "valid"
			}
		}
		errorMessage := strings.Join(recordErrors, "; ")
		var errorCode any
		if errorMessage != "" {
			errorCode = "upstream_bill_query"
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO bill_results (
				run_id,meter_id,status,attempts,duration_ms,queried_at,months_requested,
				months_with_data,months_no_data,months_partial,months_error,canonical_saved,
				changed_count,error_code,error_message
			) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,''))
		`, runID, meter.ID, status, maxInt(result.Attempts, 1), duration.Milliseconds(), result.QueriedAt,
			result.MonthsRequested, dataCount, noDataCount, partialCount, errorCount, savedCount, changedCount,
			errorCode, errorMessage)
		return err
	})
}

func FinishBillRun(ctx context.Context, pool *pgxpool.Pool, runID string, interrupted bool, message string) (BillRunCounters, error) {
	requestedStatus := "completed"
	if interrupted {
		requestedStatus = "interrupted"
	}
	var counters BillRunCounters
	err := pool.QueryRow(ctx, `
		WITH counts AS (
			SELECT count(*)::integer processed,
				count(*) FILTER (WHERE status='valid')::integer valid,
				count(*) FILTER (WHERE status='partial')::integer partial,
				count(*) FILTER (WHERE status='no_data')::integer no_data,
				count(*) FILTER (WHERE status='empty')::integer empty,
				count(*) FILTER (WHERE status IN ('error','canceled'))::integer errors,
				COALESCE(sum(months_with_data),0)::integer month_data,
				COALESCE(sum(months_no_data),0)::integer month_no_data,
				COALESCE(sum(months_partial),0)::integer month_partial,
				COALESCE(sum(months_error),0)::integer month_error,
				COALESCE(sum(canonical_saved),0)::integer canonical_saved,
				COALESCE(sum(changed_count),0)::integer changed
			FROM bill_results WHERE run_id=$1::uuid
		), updated AS (
			UPDATE bill_runs r SET
				status=CASE
					-- 取消优先：canceled 是终态，interrupted 会被恢复逻辑续跑。
					WHEN r.cancel_requested THEN 'canceled'
					WHEN $2='interrupted' THEN 'interrupted'
					WHEN counts.partial+counts.empty+counts.errors>0 THEN 'completed_with_errors'
					ELSE 'completed' END,
				finished_at=now(),heartbeat_at=now(),processed_total=counts.processed,
				valid_total=counts.valid,partial_total=counts.partial,no_data_total=counts.no_data,
				empty_total=counts.empty,error_total=counts.errors,month_data_total=counts.month_data,
				month_no_data_total=counts.month_no_data,month_partial_total=counts.month_partial,
				month_error_total=counts.month_error,canonical_saved_total=counts.canonical_saved,
				changed_total=counts.changed,error_message=NULLIF($3,'')
			FROM counts WHERE r.id=$1::uuid
			RETURNING r.inventory_total,r.excluded_total,r.eligible_total,r.processed_total,
				r.valid_total,r.partial_total,r.no_data_total,r.empty_total,r.error_total,
				r.month_data_total,r.month_no_data_total,r.month_partial_total,r.month_error_total,
				r.canonical_saved_total,r.changed_total
		) SELECT * FROM updated
	`, runID, requestedStatus, message).Scan(
		&counters.InventoryTotal, &counters.ExcludedTotal, &counters.EligibleTotal, &counters.ProcessedTotal,
		&counters.ValidTotal, &counters.PartialTotal, &counters.NoDataTotal, &counters.EmptyTotal,
		&counters.ErrorTotal, &counters.MonthDataTotal, &counters.MonthNoDataTotal,
		&counters.MonthPartialTotal, &counters.MonthErrorTotal, &counters.CanonicalSavedTotal, &counters.ChangedTotal,
	)
	return counters, err
}

// TouchBillRun 打心跳并返回是否已请求取消。见 TouchScanRun。
func TouchBillRun(ctx context.Context, pool *pgxpool.Pool, runID string) (bool, error) {
	var canceled bool
	err := pool.QueryRow(ctx, `
		WITH counts AS (
			SELECT count(*)::integer processed,
				count(*) FILTER (WHERE status='valid')::integer valid,
				count(*) FILTER (WHERE status='partial')::integer partial,
				count(*) FILTER (WHERE status='no_data')::integer no_data,
				count(*) FILTER (WHERE status='empty')::integer empty,
				count(*) FILTER (WHERE status IN ('error','canceled'))::integer errors,
				COALESCE(sum(months_with_data),0)::integer month_data,
				COALESCE(sum(months_no_data),0)::integer month_no_data,
				COALESCE(sum(months_partial),0)::integer month_partial,
				COALESCE(sum(months_error),0)::integer month_error,
				COALESCE(sum(canonical_saved),0)::integer canonical_saved,
				COALESCE(sum(changed_count),0)::integer changed
			FROM bill_results WHERE run_id=$1::uuid
		)
		UPDATE bill_runs r SET
			heartbeat_at=now(),processed_total=counts.processed,
			valid_total=counts.valid,partial_total=counts.partial,no_data_total=counts.no_data,
			empty_total=counts.empty,error_total=counts.errors,month_data_total=counts.month_data,
			month_no_data_total=counts.month_no_data,month_partial_total=counts.month_partial,
			month_error_total=counts.month_error,canonical_saved_total=counts.canonical_saved,
			changed_total=counts.changed
		FROM counts WHERE r.id=$1::uuid AND r.status='running'
		RETURNING r.cancel_requested
	`, runID).Scan(&canceled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return canceled, err
}

func ListBillRuns(ctx context.Context, pool *pgxpool.Pool, status string, limit, offset int) ([]BillRunView, error) {
	rows, err := pool.Query(ctx, billRunSelect+` WHERE ($1='' OR r.status=$1) ORDER BY r.started_at DESC,r.id DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []BillRunView{}
	for rows.Next() {
		item, err := scanBillRunRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func GetBillRun(ctx context.Context, pool *pgxpool.Pool, id string) (BillRunView, error) {
	return scanBillRunRow(pool.QueryRow(ctx, billRunSelect+` WHERE r.id=$1::uuid`, id))
}

const billRunSelect = `
	SELECT r.id::text,r.parent_run_id::text,r.trigger,r.status,r.mode,
		ARRAY(SELECT to_char(x,'YYYY-MM') FROM unnest(r.months) x),
		r.started_at,r.finished_at,r.heartbeat_at,
		COALESCE((r.config_snapshot->>'qps')::double precision,0),
		COALESCE((r.config_snapshot->>'concurrency')::integer,0),(r.scope->>'limit')::integer,
		r.inventory_total,r.excluded_total,r.eligible_total,r.processed_total,r.valid_total,
		r.partial_total,r.no_data_total,r.empty_total,r.error_total,r.month_data_total,
		r.month_no_data_total,r.month_partial_total,r.month_error_total,r.canonical_saved_total,
		r.changed_total,r.error_message FROM bill_runs r`

func scanBillRunRow(row rowScanner) (BillRunView, error) {
	var item BillRunView
	err := row.Scan(&item.ID, &item.ParentRunID, &item.Trigger, &item.Status, &item.Mode, &item.Months,
		&item.StartedAt, &item.FinishedAt, &item.HeartbeatAt, &item.QPS, &item.Concurrency, &item.Limit,
		&item.Counters.InventoryTotal, &item.Counters.ExcludedTotal, &item.Counters.EligibleTotal,
		&item.Counters.ProcessedTotal, &item.Counters.ValidTotal, &item.Counters.PartialTotal,
		&item.Counters.NoDataTotal, &item.Counters.EmptyTotal, &item.Counters.ErrorTotal,
		&item.Counters.MonthDataTotal, &item.Counters.MonthNoDataTotal, &item.Counters.MonthPartialTotal,
		&item.Counters.MonthErrorTotal, &item.Counters.CanonicalSavedTotal, &item.Counters.ChangedTotal,
		&item.ErrorMessage)
	return item, err
}

func ListBillResults(ctx context.Context, pool *pgxpool.Pool, runID, status string, limit, offset int) ([]BillResultView, error) {
	rows, err := pool.Query(ctx, `
		SELECT br.run_id::text,m.meter_no,m.building,m.floor,m.room,br.status,br.attempts,
			br.duration_ms,br.queried_at,br.months_requested,br.months_with_data,br.months_no_data,
			br.months_partial,br.months_error,br.canonical_saved,br.changed_count,br.error_code,br.error_message
		FROM bill_results br JOIN meters m ON m.id=br.meter_id
		WHERE br.run_id=$1::uuid AND ($2='' OR br.status=$2)
		ORDER BY br.created_at,m.meter_no LIMIT $3 OFFSET $4
	`, runID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []BillResultView{}
	for rows.Next() {
		var item BillResultView
		if err := rows.Scan(&item.RunID, &item.Meter, &item.Building, &item.Floor, &item.Room,
			&item.Status, &item.Attempts, &item.DurationMS, &item.QueriedAt, &item.MonthsRequested,
			&item.MonthsWithData, &item.MonthsNoData, &item.MonthsPartial, &item.MonthsError,
			&item.CanonicalSaved, &item.ChangedCount, &item.ErrorCode, &item.ErrorMessage); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func ListBillRevisions(ctx context.Context, pool *pgxpool.Pool, acknowledged *bool, limit, offset int) ([]BillRevisionView, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.id::text,r.run_id::text,m.meter_no,to_char(r.month,'YYYY-MM'),
			r.previous_values,r.current_values,r.detected_at,r.acknowledged_at IS NOT NULL,
			r.acknowledged_at,r.acknowledged_note
		FROM bill_revisions r JOIN meters m ON m.id=r.meter_id
		WHERE ($1::boolean IS NULL OR (r.acknowledged_at IS NOT NULL)=$1)
		ORDER BY r.detected_at DESC,r.id DESC LIMIT $2 OFFSET $3
	`, acknowledged, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []BillRevisionView{}
	for rows.Next() {
		var item BillRevisionView
		if err := rows.Scan(&item.ID, &item.RunID, &item.Meter, &item.Month, &item.PreviousValues,
			&item.CurrentValues, &item.DetectedAt, &item.Acknowledged, &item.AcknowledgedAt,
			&item.AcknowledgedNote); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func AcknowledgeBillRevision(ctx context.Context, pool *pgxpool.Pool, id string, acknowledged bool, note string) (BillRevisionView, error) {
	row := pool.QueryRow(ctx, `
		WITH changed AS (
			UPDATE bill_revisions SET
				acknowledged_at=CASE WHEN $2 THEN now() ELSE NULL END,
				acknowledged_note=NULLIF($3,'')
			WHERE id=$1::uuid RETURNING *
		)
		SELECT r.id::text,r.run_id::text,m.meter_no,to_char(r.month,'YYYY-MM'),
			r.previous_values,r.current_values,r.detected_at,r.acknowledged_at IS NOT NULL,
			r.acknowledged_at,r.acknowledged_note
		FROM changed r JOIN meters m ON m.id=r.meter_id
	`, id, acknowledged, note)
	var item BillRevisionView
	err := row.Scan(&item.ID, &item.RunID, &item.Meter, &item.Month, &item.PreviousValues,
		&item.CurrentValues, &item.DetectedAt, &item.Acknowledged, &item.AcknowledgedAt,
		&item.AcknowledgedNote)
	return item, err
}

func UnacknowledgedBillRevisionCount(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var count int
	err := pool.QueryRow(ctx, `SELECT count(*)::integer FROM bill_revisions WHERE acknowledged_at IS NULL`).Scan(&count)
	return count, err
}

func parseBillMonths(months []string) ([]time.Time, error) {
	if len(months) == 0 {
		return nil, errors.New("at least one bill month is required")
	}
	seen := map[string]bool{}
	out := make([]time.Time, 0, len(months))
	for _, value := range months {
		if seen[value] {
			return nil, fmt.Errorf("duplicate bill month %q", value)
		}
		month, err := time.Parse("2006-01", value)
		if err != nil {
			return nil, fmt.Errorf("invalid bill month %q", value)
		}
		seen[value] = true
		out = append(out, month)
	}
	return out, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
