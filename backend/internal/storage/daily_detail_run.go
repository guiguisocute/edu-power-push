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

var ErrDailyDetailAlreadyRunning = errors.New("a daily detail run is already active")
var ErrDailyDetailNotRunnable = errors.New("daily detail run is not pending or interrupted")
var ErrDailyDetailScheduleExists = errors.New("today's scheduled daily detail run already exists")
var ErrDailyDetailSettlementRetryExists = errors.New("the settlement retry for this target date already exists")

type DailyDetailRunScope struct {
	Building        string `json:"building,omitempty"`
	Floor           string `json:"floor,omitempty"`
	Meter           string `json:"meter,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	UnpublishedDate string `json:"unpublished_date,omitempty"`
}

type DailyDetailRunSettings struct {
	QPS            float64 `json:"qps"`
	Concurrency    int     `json:"concurrency"`
	RetryMax       int     `json:"retry_max"`
	MonthRetryMax  int     `json:"month_retry_max"`
	TimeoutSeconds int     `json:"timeout_seconds"`
	BootstrapFrom  string  `json:"bootstrap_from,omitempty"`
	Initialization bool    `json:"initialization"`
}

type DailyDetailRunCounters struct {
	InventoryTotal int `json:"inventory_total"`
	ExcludedTotal  int `json:"excluded_total"`
	EligibleTotal  int `json:"eligible_total"`
	ProcessedTotal int `json:"processed_total"`
	ValidTotal     int `json:"valid_total"`
	NoDataTotal    int `json:"no_data_total"`
	PartialTotal   int `json:"partial_total"`
	EmptyTotal     int `json:"empty_total"`
	ErrorTotal     int `json:"error_total"`
	DaysSavedTotal int `json:"days_saved_total"`
	ChangedTotal   int `json:"changed_total"`
}

type DailyDetailRunView struct {
	ID             string                 `json:"id"`
	ParentRunID    *string                `json:"parent_run_id"`
	Trigger        string                 `json:"trigger"`
	Status         string                 `json:"status"`
	Months         []string               `json:"months"`
	StartedAt      time.Time              `json:"started_at"`
	FinishedAt     *time.Time             `json:"finished_at"`
	HeartbeatAt    *time.Time             `json:"heartbeat_at"`
	QPS            float64                `json:"qps"`
	Concurrency    int                    `json:"concurrency"`
	Limit          *int                   `json:"limit"`
	Initialization bool                   `json:"initialization"`
	Counters       DailyDetailRunCounters `json:"counters"`
	ErrorMessage   *string                `json:"error_message,omitempty"`
}

type DailyDetailResultView struct {
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
	MonthsValid     int       `json:"months_valid"`
	MonthsNoData    int       `json:"months_no_data"`
	MonthsError     int       `json:"months_error"`
	DaysSaved       int       `json:"days_saved"`
	ChangedCount    int       `json:"changed_count"`
	ErrorCode       *string   `json:"error_code,omitempty"`
	ErrorMessage    *string   `json:"error_message,omitempty"`
}

func DailyDetailsInitialized(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var initialized bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM daily_detail_runs
			WHERE (
				status='completed'
				AND COALESCE((config_snapshot->>'initialization')::boolean,false)
				AND scope='{}'::jsonb
				AND processed_total=eligible_total AND error_total=0 AND partial_total=0 AND empty_total=0
			) OR (
				trigger='import' AND status IN ('completed','completed_with_errors')
				AND COALESCE((config_snapshot->>'baseline_accepted')::boolean,false)
			)
		)
	`).Scan(&initialized)
	return initialized, err
}

func CreateDailyDetailRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	trigger string,
	parentRunID *string,
	months []string,
	scope DailyDetailRunScope,
	settings DailyDetailRunSettings,
) (string, error) {
	monthDates, err := parseBillMonths(months)
	if err != nil {
		return "", err
	}
	if scope.UnpublishedDate != "" {
		if _, err := time.Parse("2006-01-02", scope.UnpublishedDate); err != nil {
			return "", fmt.Errorf("invalid unpublished date %q", scope.UnpublishedDate)
		}
	}
	var runID string
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if trigger == "schedule" {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(SELECT 1 FROM daily_detail_runs
				WHERE trigger='schedule' AND started_at::date=current_date)
			`).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrDailyDetailScheduleExists
			}
		}
		if trigger == "retry" && scope.UnpublishedDate != "" {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(SELECT 1 FROM daily_detail_runs
				WHERE trigger='retry' AND scope->>'unpublished_date'=$1)
			`, scope.UnpublishedDate).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrDailyDetailSettlementRetryExists
			}
		}
		var inventoryTotal, excludedTotal int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE active AND excluded)
			FROM meters
		`).Scan(&inventoryTotal, &excludedTotal); err != nil {
			return err
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
			INSERT INTO daily_detail_runs (
				parent_run_id,trigger,status,months,scope,config_snapshot,
				inventory_total,excluded_total,heartbeat_at
			) VALUES ($1::uuid,$2,'pending',$3,$4,$5,$6,$7,now())
			RETURNING id::text
		`, nullableUUID(parentRunID), trigger, monthDates, scopeJSON, settingsJSON,
			inventoryTotal, excludedTotal).Scan(&runID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "daily_detail_runs_one_active" {
				return ErrDailyDetailAlreadyRunning
			}
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO daily_detail_run_meters (run_id,meter_id,ordinal)
			SELECT $1::uuid,id,row_number() OVER (ORDER BY building,floor,room,meter_no)::integer
			FROM meters
			WHERE active AND NOT excluded
			  AND ($2='' OR building=$2) AND ($3='' OR floor=$3)
			  AND ($5='' OR meter_no=$5)
			  AND (NULLIF($6,'')::date IS NULL OR NOT EXISTS (
				SELECT 1 FROM daily_usages u
				WHERE u.meter_id=meters.id
				  AND u.usage_date=NULLIF($6,'')::date AND u.has_upstream_data
			  ))
			ORDER BY building,floor,room,meter_no LIMIT NULLIF($4,0)
		`, runID, scope.Building, scope.Floor, scope.Limit, scope.Meter, scope.UnpublishedDate)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 && scope.UnpublishedDate == "" {
			return errors.New("daily detail scope contains no eligible meters")
		}
		_, err = tx.Exec(ctx, `UPDATE daily_detail_runs SET eligible_total=$2 WHERE id=$1::uuid`, runID, tag.RowsAffected())
		return err
	})
	return runID, err
}

func CountDailyDetailScope(ctx context.Context, pool *pgxpool.Pool, scope DailyDetailRunScope) (DailyDetailRunCounters, error) {
	var counters DailyDetailRunCounters
	err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE active AND excluded),
			LEAST(count(*) FILTER (WHERE active AND NOT excluded
				AND ($1='' OR building=$1) AND ($2='' OR floor=$2) AND ($3='' OR meter_no=$3)
				AND (NULLIF($5,'')::date IS NULL OR NOT EXISTS (
					SELECT 1 FROM daily_usages u
					WHERE u.meter_id=meters.id
					  AND u.usage_date=NULLIF($5,'')::date AND u.has_upstream_data
				))),
				COALESCE(NULLIF($4,0),2147483647))::integer
		FROM meters
	`, scope.Building, scope.Floor, scope.Meter, scope.Limit, scope.UnpublishedDate).Scan(
		&counters.InventoryTotal, &counters.ExcludedTotal, &counters.EligibleTotal,
	)
	return counters, err
}

func CreateRetryDailyDetailRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	parentRunID string,
	statuses []string,
	settings DailyDetailRunSettings,
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
			INSERT INTO daily_detail_runs (
				parent_run_id,trigger,status,months,scope,config_snapshot,
				inventory_total,excluded_total,heartbeat_at
			)
			SELECT parent.id,'retry','pending',parent.months,
				jsonb_build_object('retry_statuses',to_jsonb($2::text[])),$3,$4,$5,now()
			FROM daily_detail_runs parent
			WHERE parent.id=$1::uuid
			  AND parent.trigger<>'import'
			  AND parent.status IN ('completed','completed_with_errors','failed')
			RETURNING id::text
		`, parentRunID, statuses, settingsJSON, inventoryTotal, excludedTotal).Scan(&runID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "daily_detail_runs_one_active" {
				return ErrDailyDetailAlreadyRunning
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("terminal parent daily detail run: %w", pgx.ErrNoRows)
			}
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO daily_detail_run_meters (run_id,meter_id,ordinal)
			SELECT $1::uuid,dr.meter_id,
				row_number() OVER (ORDER BY m.building,m.floor,m.room,m.meter_no)::integer
			FROM daily_detail_results dr JOIN meters m ON m.id=dr.meter_id
			WHERE dr.run_id=$2::uuid AND dr.status=ANY($3::text[])
			ORDER BY m.building,m.floor,m.room,m.meter_no
		`, runID, parentRunID, statuses)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("parent daily detail run has no results matching retry statuses")
		}
		_, err = tx.Exec(ctx, `UPDATE daily_detail_runs SET eligible_total=$2 WHERE id=$1::uuid`, runID, tag.RowsAffected())
		return err
	})
	return runID, err
}

func MarkDailyDetailRunRunning(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE daily_detail_runs SET status='running',heartbeat_at=now(),finished_at=NULL
			WHERE id=$1::uuid AND status IN ('pending','interrupted')
		`, runID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrDailyDetailNotRunnable
		}
		return nil
	})
}

func DailyDetailRunMonths(ctx context.Context, pool *pgxpool.Pool, runID string) ([]string, error) {
	var months []time.Time
	if err := pool.QueryRow(ctx, `SELECT months FROM daily_detail_runs WHERE id=$1::uuid`, runID).Scan(&months); err != nil {
		return nil, err
	}
	out := make([]string, len(months))
	for i, month := range months {
		out[i] = month.Format("2006-01")
	}
	return out, nil
}

func PendingDailyDetailMeters(ctx context.Context, pool *pgxpool.Pool, runID string) ([]ScanMeter, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.id::text,m.meter_no,m.building,m.floor,m.room,rm.ordinal
		FROM daily_detail_run_meters rm JOIN meters m ON m.id=rm.meter_id
		LEFT JOIN daily_detail_results r ON r.run_id=rm.run_id AND r.meter_id=rm.meter_id
		WHERE rm.run_id=$1::uuid AND r.run_id IS NULL ORDER BY rm.ordinal
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

func RecordDailyDetailOutcome(
	ctx context.Context,
	pool *pgxpool.Pool,
	runID string,
	meter ScanMeter,
	result provider.DailyDetailsResult,
	duration time.Duration,
) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		validMonths, noDataMonths, errorMonths, daysSaved, changedCount := 0, 0, 0, 0, 0
		recordErrors := append([]string(nil), result.Errors...)
		for _, detail := range result.Months {
			if detail.Status != "valid" && detail.Status != "no_data" {
				errorMonths++
				if detail.ErrorMessage != "" {
					recordErrors = append(recordErrors, detail.Month+": "+detail.ErrorMessage)
				}
				continue
			}
			month, err := time.Parse("2006-01", detail.Month)
			if err != nil {
				return err
			}
			if detail.CoveredThrough == "" {
				// 月初第一天该月尚无已结算日。
				noDataMonths++
				continue
			}
			covered, err := time.Parse("2006-01-02", detail.CoveredThrough)
			if err != nil || covered.Before(month) || !covered.Before(month.AddDate(0, 1, 0)) {
				return fmt.Errorf("invalid covered_through %q for %s", detail.CoveredThrough, detail.Month)
			}
			payload := make([]map[string]any, 0, len(detail.Days))
			for _, day := range detail.Days {
				usage, cost := canonicalDecimal(day.UsageKWh), canonicalDecimal(day.CostYuan)
				if usage == nil || cost == nil {
					return fmt.Errorf("invalid daily detail numeric value for %s", day.Date)
				}
				payload = append(payload, map[string]any{"sj": day.Date, "ydl": *usage, "ydje": *cost})
			}
			payloadJSON, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			incomingSQL := `
				WITH payload AS (
					SELECT (x->>'sj')::date usage_date,(x->>'ydl')::numeric usage_kwh,
						(x->>'ydje')::numeric cost_yuan,x raw_payload
					FROM jsonb_array_elements($4::jsonb) x
				), incoming AS (
					SELECT d::date usage_date,COALESCE(p.usage_kwh,0)::numeric(20,4) usage_kwh,
						COALESCE(p.cost_yuan,0)::numeric(18,4) cost_yuan,
						(p.usage_date IS NOT NULL) has_data,COALESCE(p.raw_payload,'{}'::jsonb) raw_payload
					FROM generate_series($2::date,$3::date,interval '1 day') d
					LEFT JOIN payload p ON p.usage_date=d::date
				)`
			tag, err := tx.Exec(ctx, incomingSQL+`
				INSERT INTO daily_usage_revisions (run_id,meter_id,usage_date,previous_values,current_values)
				SELECT $1::uuid,$5::uuid,i.usage_date,
					jsonb_build_object('ydl',u.usage_kwh,'ydje',u.cost_yuan,'has_data',u.has_upstream_data),
					jsonb_build_object('ydl',i.usage_kwh,'ydje',i.cost_yuan,'has_data',i.has_data)
				FROM incoming i JOIN daily_usages u ON u.meter_id=$5::uuid AND u.usage_date=i.usage_date
				WHERE u.usage_kwh IS DISTINCT FROM i.usage_kwh OR u.cost_yuan IS DISTINCT FROM i.cost_yuan
				   OR u.has_upstream_data IS DISTINCT FROM i.has_data
				ON CONFLICT DO NOTHING
			`, runID, month, covered, payloadJSON, meter.ID)
			if err != nil {
				return fmt.Errorf("record daily usage revisions: %w", err)
			}
			changedCount += int(tag.RowsAffected())
			/* 定时运行重读整月。无值比较会重写全月每日每表。
			   相同行保留旧 updated_at 与 source_run_id。
			   rollup 刷新可从真实变更起算，不必从月初。
			   raw_payload 仅作写侧溯源。不参与比较。
			   导入路径数字格式不同，比较会永不匹配。 */
			if _, err := tx.Exec(ctx, incomingSQL+`
				INSERT INTO daily_usages (
					meter_id,usage_date,usage_kwh,cost_yuan,has_upstream_data,
					observed_at,source_run_id,raw_payload
				)
				SELECT $5::uuid,usage_date,usage_kwh,cost_yuan,has_data,$6,$1::uuid,raw_payload
				FROM incoming
				ON CONFLICT (meter_id,usage_date) DO UPDATE SET
					usage_kwh=EXCLUDED.usage_kwh,cost_yuan=EXCLUDED.cost_yuan,
					has_upstream_data=EXCLUDED.has_upstream_data,observed_at=EXCLUDED.observed_at,
					source_run_id=EXCLUDED.source_run_id,raw_payload=EXCLUDED.raw_payload,updated_at=now()
				WHERE daily_usages.observed_at<=EXCLUDED.observed_at
				  AND (daily_usages.usage_kwh IS DISTINCT FROM EXCLUDED.usage_kwh
				    OR daily_usages.cost_yuan IS DISTINCT FROM EXCLUDED.cost_yuan
				    OR daily_usages.has_upstream_data IS DISTINCT FROM EXCLUDED.has_upstream_data)
			`, runID, month, covered, payloadJSON, meter.ID, result.QueriedAt); err != nil {
				return fmt.Errorf("upsert daily usages: %w", err)
			}
			monthStatus := detail.Status
			if _, err := tx.Exec(ctx, `
				INSERT INTO daily_detail_months (
					meter_id,month,covered_through,status,raw_row_count,days_with_usage,
					observed_at,source_run_id
				) VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8::uuid)
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
			`, meter.ID, month, covered, monthStatus, detail.RawRowCount, len(detail.Days), result.QueriedAt, runID); err != nil {
				return err
			}
			daysSaved += int(covered.Sub(month).Hours()/24) + 1
			if detail.Status == "valid" {
				validMonths++
			} else {
				noDataMonths++
			}
		}

		status := string(result.Status)
		if !result.OK {
			if status != "empty" {
				status = "error"
			}
		} else if errorMonths > 0 {
			status = "partial"
		} else if validMonths == 0 {
			status = "no_data"
		} else {
			status = "valid"
		}
		errorMessage := strings.Join(recordErrors, "; ")
		_, err := tx.Exec(ctx, `
			INSERT INTO daily_detail_results (
				run_id,meter_id,status,attempts,duration_ms,queried_at,months_requested,
				months_valid,months_no_data,months_error,days_saved,changed_count,error_code,error_message
			) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,''))
		`, runID, meter.ID, status, maxInt(result.Attempts, 1), duration.Milliseconds(), result.QueriedAt,
			result.MonthsRequested, validMonths, noDataMonths, errorMonths, daysSaved, changedCount,
			nullableTextIf(errorMessage != "", "upstream_daily_detail"), errorMessage)
		return err
	})
}

func nullableTextIf(condition bool, value string) any {
	if condition {
		return value
	}
	return nil
}

// TouchDailyDetailRun 打心跳并返回是否已请求取消。见 TouchScanRun。
func TouchDailyDetailRun(ctx context.Context, pool *pgxpool.Pool, runID string) (bool, error) {
	var canceled bool
	err := pool.QueryRow(ctx, dailyDetailCountersSQL+`
		UPDATE daily_detail_runs r SET heartbeat_at=now(),processed_total=c.processed,
			valid_total=c.valid,no_data_total=c.no_data,partial_total=c.partial,
			empty_total=c.empty,error_total=c.errors,days_saved_total=c.days_saved,
			changed_total=c.changed
		FROM counts c WHERE r.id=$1::uuid AND r.status='running'
		RETURNING r.cancel_requested
	`, runID).Scan(&canceled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return canceled, err
}

func FinishDailyDetailRun(ctx context.Context, pool *pgxpool.Pool, runID string, interrupted bool, message string) (DailyDetailRunCounters, error) {
	requested := "completed"
	if interrupted {
		requested = "interrupted"
	}
	var counters DailyDetailRunCounters
	err := pool.QueryRow(ctx, dailyDetailCountersSQL+`
		, updated AS (
			UPDATE daily_detail_runs r SET status=CASE
				-- 取消优先：canceled 是终态，interrupted 会被恢复逻辑续跑。
				WHEN r.cancel_requested THEN 'canceled'
				WHEN $2='interrupted' THEN 'interrupted'
				WHEN c.partial+c.empty+c.errors>0 THEN 'completed_with_errors' ELSE 'completed' END,
				finished_at=now(),heartbeat_at=now(),processed_total=c.processed,
				valid_total=c.valid,no_data_total=c.no_data,partial_total=c.partial,
				empty_total=c.empty,error_total=c.errors,days_saved_total=c.days_saved,
				changed_total=c.changed,error_message=NULLIF($3,'')
			FROM counts c WHERE r.id=$1::uuid RETURNING r.*
		)
		SELECT inventory_total,excluded_total,eligible_total,processed_total,valid_total,
			no_data_total,partial_total,empty_total,error_total,days_saved_total,changed_total
		FROM updated
	`, runID, requested, message).Scan(
		&counters.InventoryTotal, &counters.ExcludedTotal, &counters.EligibleTotal,
		&counters.ProcessedTotal, &counters.ValidTotal, &counters.NoDataTotal,
		&counters.PartialTotal, &counters.EmptyTotal, &counters.ErrorTotal,
		&counters.DaysSavedTotal, &counters.ChangedTotal,
	)
	return counters, err
}

const dailyDetailCountersSQL = `
	WITH counts AS (
		SELECT count(*)::integer processed,
			count(*) FILTER (WHERE status='valid')::integer valid,
			count(*) FILTER (WHERE status='no_data')::integer no_data,
			count(*) FILTER (WHERE status='partial')::integer partial,
			count(*) FILTER (WHERE status='empty')::integer empty,
			count(*) FILTER (WHERE status IN ('error','canceled'))::integer errors,
			COALESCE(sum(days_saved),0)::integer days_saved,
			COALESCE(sum(changed_count),0)::integer changed
		FROM daily_detail_results WHERE run_id=$1::uuid
	) `

func ListDailyDetailRuns(ctx context.Context, pool *pgxpool.Pool, status string, limit, offset int) ([]DailyDetailRunView, error) {
	rows, err := pool.Query(ctx, dailyDetailRunSelect+`
		WHERE ($1='' OR r.status=$1) ORDER BY r.started_at DESC,r.id DESC LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DailyDetailRunView{}
	for rows.Next() {
		item, err := scanDailyDetailRunRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func GetDailyDetailRun(ctx context.Context, pool *pgxpool.Pool, id string) (DailyDetailRunView, error) {
	return scanDailyDetailRunRow(pool.QueryRow(ctx, dailyDetailRunSelect+` WHERE r.id=$1::uuid`, id))
}

const dailyDetailRunSelect = `
	SELECT r.id::text,r.parent_run_id::text,r.trigger,r.status,
		ARRAY(SELECT to_char(x,'YYYY-MM') FROM unnest(r.months) x),
		r.started_at,r.finished_at,r.heartbeat_at,
		COALESCE((r.config_snapshot->>'qps')::double precision,0),
		COALESCE((r.config_snapshot->>'concurrency')::integer,0),(r.scope->>'limit')::integer,
		COALESCE((r.config_snapshot->>'initialization')::boolean,false),
		r.inventory_total,r.excluded_total,r.eligible_total,r.processed_total,r.valid_total,
		r.no_data_total,r.partial_total,r.empty_total,r.error_total,r.days_saved_total,
		r.changed_total,r.error_message FROM daily_detail_runs r`

func scanDailyDetailRunRow(row rowScanner) (DailyDetailRunView, error) {
	var item DailyDetailRunView
	err := row.Scan(&item.ID, &item.ParentRunID, &item.Trigger, &item.Status, &item.Months,
		&item.StartedAt, &item.FinishedAt, &item.HeartbeatAt, &item.QPS, &item.Concurrency,
		&item.Limit, &item.Initialization, &item.Counters.InventoryTotal, &item.Counters.ExcludedTotal,
		&item.Counters.EligibleTotal, &item.Counters.ProcessedTotal, &item.Counters.ValidTotal,
		&item.Counters.NoDataTotal, &item.Counters.PartialTotal, &item.Counters.EmptyTotal,
		&item.Counters.ErrorTotal, &item.Counters.DaysSavedTotal, &item.Counters.ChangedTotal,
		&item.ErrorMessage)
	return item, err
}

func ListDailyDetailResults(ctx context.Context, pool *pgxpool.Pool, runID, status string, limit, offset int) ([]DailyDetailResultView, error) {
	rows, err := pool.Query(ctx, `
		SELECT dr.run_id::text,m.meter_no,m.building,m.floor,m.room,dr.status,dr.attempts,
			dr.duration_ms,dr.queried_at,dr.months_requested,dr.months_valid,dr.months_no_data,
			dr.months_error,dr.days_saved,dr.changed_count,dr.error_code,dr.error_message
		FROM daily_detail_results dr JOIN meters m ON m.id=dr.meter_id
		WHERE dr.run_id=$1::uuid AND ($2='' OR dr.status=$2)
		ORDER BY dr.created_at,m.meter_no LIMIT $3 OFFSET $4
	`, runID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DailyDetailResultView{}
	for rows.Next() {
		var item DailyDetailResultView
		if err := rows.Scan(&item.RunID, &item.Meter, &item.Building, &item.Floor, &item.Room,
			&item.Status, &item.Attempts, &item.DurationMS, &item.QueriedAt, &item.MonthsRequested,
			&item.MonthsValid, &item.MonthsNoData, &item.MonthsError, &item.DaysSaved,
			&item.ChangedCount, &item.ErrorCode, &item.ErrorMessage); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
