package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListScanRuns 仅列批量运行。单表手动刷新也写入 scan_runs。
// 混入会冲没批量运行史。单次刷新用 GetScanRun 或 scan_results 查询。
func ListScanRuns(ctx context.Context, pool *pgxpool.Pool, status string, limit, offset int) ([]ScanRunView, error) {
	rows, err := pool.Query(ctx, scanRunSelect+`
		WHERE ($1 = '' OR r.status = $1) AND r.scope->>'meter' IS NULL
		ORDER BY r.started_at DESC, r.id DESC
		LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRunRows(rows)
}

func GetScanRun(ctx context.Context, pool *pgxpool.Pool, id string) (ScanRunView, error) {
	row := pool.QueryRow(ctx, scanRunSelect+` WHERE r.id = $1::uuid`, id)
	return scanRunRow(row)
}

const scanRunSelect = `
	SELECT r.id::text, r.parent_run_id::text, r.trigger, r.status,
		r.started_at, r.finished_at, r.heartbeat_at,
		COALESCE((r.config_snapshot->>'qps')::double precision, 0),
		COALESCE((r.config_snapshot->>'concurrency')::integer, 0),
		(r.scope->>'limit')::integer,
		COALESCE((r.scope->>'bound_only')::boolean, false),
		r.inventory_total, r.excluded_total, r.eligible_total, r.processed_total,
		r.valid_total, r.stale_total, r.empty_total, r.error_total,
		r.parse_error_total, r.duplicate_reading_total
	FROM scan_runs r`

type rowScanner interface{ Scan(...any) error }

func scanRunRow(row rowScanner) (ScanRunView, error) {
	var item ScanRunView
	err := row.Scan(
		&item.ID, &item.ParentRunID, &item.Trigger, &item.Status,
		&item.StartedAt, &item.FinishedAt, &item.HeartbeatAt,
		&item.QPS, &item.Concurrency, &item.Limit, &item.BoundOnly,
		&item.Counters.InventoryTotal, &item.Counters.ExcludedTotal,
		&item.Counters.EligibleTotal, &item.Counters.ProcessedTotal,
		&item.Counters.ValidTotal, &item.Counters.StaleTotal,
		&item.Counters.EmptyTotal, &item.Counters.ErrorTotal,
		&item.Counters.ParseErrorTotal, &item.Counters.DuplicateReadingTotal,
	)
	return item, err
}

func scanRunRows(rows pgx.Rows) ([]ScanRunView, error) {
	items := make([]ScanRunView, 0)
	for rows.Next() {
		item, err := scanRunRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func ListScanResults(ctx context.Context, pool *pgxpool.Pool, runID, status string, limit, offset int) ([]ScanResultView, error) {
	rows, err := pool.Query(ctx, `
		SELECT sr.run_id::text, m.meter_no, m.building, m.floor, m.room,
			sr.status, sr.attempts, sr.duration_ms, sr.queried_at,
			sr.reading_id::text, mr.reading_time, sr.error_code, sr.error_message
		FROM scan_results sr
		JOIN meters m ON m.id = sr.meter_id
		LEFT JOIN meter_readings mr ON mr.id = sr.reading_id
		WHERE sr.run_id = $1::uuid AND ($2 = '' OR sr.status = $2)
		ORDER BY sr.created_at, m.meter_no
		LIMIT $3 OFFSET $4`, runID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ScanResultView, 0)
	for rows.Next() {
		var item ScanResultView
		if err := rows.Scan(
			&item.RunID, &item.Meter, &item.Building, &item.Floor, &item.Room,
			&item.Status, &item.Attempts, &item.DurationMS, &item.QueriedAt,
			&item.ReadingID, &item.ReadingTime, &item.ErrorCode, &item.ErrorMessage,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func ListAnomalies(ctx context.Context, pool *pgxpool.Pool, acknowledged *bool, anomalyType string, limit, offset int) ([]AnomalyView, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id::text, a.type, a.severity, m.meter_no, a.scan_run_id::text,
			a.detected_at, a.acknowledged_at IS NOT NULL, a.acknowledged_at,
			a.acknowledged_note, a.payload
		FROM anomaly_events a
		LEFT JOIN meters m ON m.id = a.meter_id
		WHERE ($1::boolean IS NULL OR (a.acknowledged_at IS NOT NULL) = $1)
		  AND ($2 = '' OR a.type = $2)
		ORDER BY CASE a.severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
			a.detected_at DESC, a.id DESC
		LIMIT $3 OFFSET $4`, acknowledged, anomalyType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AnomalyView, 0)
	for rows.Next() {
		var item AnomalyView
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Severity, &item.Meter, &item.ScanRunID,
			&item.DetectedAt, &item.Acknowledged, &item.AcknowledgedAt,
			&item.Note, &item.Details,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func AcknowledgeAnomaly(ctx context.Context, pool *pgxpool.Pool, id string, acknowledged bool, note string) (AnomalyView, error) {
	row := pool.QueryRow(ctx, `
		WITH changed AS (
			UPDATE anomaly_events
			SET acknowledged_at = CASE WHEN $2 THEN now() ELSE NULL END,
				acknowledged_note = NULLIF($3, '')
			WHERE id = $1::uuid
			RETURNING *
		)
		SELECT a.id::text, a.type, a.severity, m.meter_no, a.scan_run_id::text,
			a.detected_at, a.acknowledged_at IS NOT NULL, a.acknowledged_at,
			a.acknowledged_note, a.payload
		FROM changed a LEFT JOIN meters m ON m.id = a.meter_id`, id, acknowledged, note)
	var item AnomalyView
	err := row.Scan(
		&item.ID, &item.Type, &item.Severity, &item.Meter, &item.ScanRunID,
		&item.DetectedAt, &item.Acknowledged, &item.AcknowledgedAt, &item.Note, &item.Details,
	)
	return item, err
}

func CountOpenCriticalAnomaliesForRun(ctx context.Context, pool *pgxpool.Pool, runID string) (int, error) {
	var count int
	err := pool.QueryRow(ctx, `
		SELECT count(*)::integer FROM anomaly_events
		WHERE scan_run_id=$1::uuid AND severity='critical' AND acknowledged_at IS NULL
	`, runID).Scan(&count)
	return count, err
}

func ListInventoryImports(ctx context.Context, pool *pgxpool.Pool) ([]InventoryImportView, error) {
	rows, err := pool.Query(ctx, `
		SELECT id::text, source_name, source_hash, source_updated_at, imported_at, status, counts
		FROM inventory_imports ORDER BY imported_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]InventoryImportView, 0)
	for rows.Next() {
		var item InventoryImportView
		if err := rows.Scan(&item.ID, &item.SourceName, &item.SourceHash, &item.SourceUpdatedAt, &item.ImportedAt, &item.Status, &item.Counts); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetCampusScopes 仅返回平台支持且 eligible 电表的楼栋楼层。
// 口径与 campus/series、campus/rankings 分母一致。
// 楼层取自 meters.floor。前端可直接透传为查询参数。
func GetCampusScopes(ctx context.Context, pool *pgxpool.Pool) (CampusScopes, error) {
	scopes := CampusScopes{Campuses: []ScopeCampus{}}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(max(updated_at), now()) FROM meters
		WHERE active AND NOT excluded AND building<>$1
	`, nonPlatformBillingBuilding).Scan(&scopes.UpdatedAt); err != nil {
		return scopes, err
	}
	rows, err := pool.Query(ctx, `
		SELECT campus, building, floor, count(*)
		FROM meters WHERE active AND NOT excluded AND building<>$1
		GROUP BY campus, building, floor
		ORDER BY campus, building, floor`, nonPlatformBillingBuilding)
	if err != nil {
		return scopes, err
	}
	defer rows.Close()
	for rows.Next() {
		var campus, building, floor string
		var rooms int
		if err := rows.Scan(&campus, &building, &floor, &rooms); err != nil {
			return scopes, err
		}
		ci := findOrAddScopeCampus(&scopes.Campuses, campus)
		buildings := &scopes.Campuses[ci].Buildings
		bi := len(*buildings) - 1
		if bi < 0 || (*buildings)[bi].Name != building {
			*buildings = append(*buildings, ScopeBuilding{Name: building, Floors: []string{}})
			bi = len(*buildings) - 1
		}
		(*buildings)[bi].Floors = append((*buildings)[bi].Floors, floor)
		(*buildings)[bi].RoomCount += rooms
	}
	return scopes, rows.Err()
}

func findOrAddScopeCampus(items *[]ScopeCampus, name string) int {
	if len(*items) > 0 && (*items)[len(*items)-1].Name == name {
		return len(*items) - 1
	}
	*items = append(*items, ScopeCampus{Name: name, Buildings: []ScopeBuilding{}})
	return len(*items) - 1
}

func GetInventoryTree(ctx context.Context, pool *pgxpool.Pool, includeExcluded bool) (InventoryTree, error) {
	var tree InventoryTree
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(max(updated_at), now()), count(*) FILTER (WHERE active),
			count(*) FILTER (WHERE active AND excluded) FROM meters
	`).Scan(&tree.UpdatedAt, &tree.InventoryTotal, &tree.ExcludedTotal); err != nil {
		return tree, err
	}
	rows, err := pool.Query(ctx, `
		SELECT campus, building, floor, room, meter_no, excluded
		FROM meters WHERE active AND ($1 OR NOT excluded)
		ORDER BY campus, building, floor, room, meter_no`, includeExcluded)
	if err != nil {
		return tree, err
	}
	defer rows.Close()
	for rows.Next() {
		var campus, building, floor string
		var room InventoryRoom
		if err := rows.Scan(&campus, &building, &floor, &room.Name, &room.Meter, &room.Excluded); err != nil {
			return tree, err
		}
		ci := findOrAddCampus(&tree.Campuses, campus)
		bi := findOrAddBuilding(&tree.Campuses[ci].Buildings, building)
		fi := findOrAddFloor(&tree.Campuses[ci].Buildings[bi].Floors, floor)
		tree.Campuses[ci].Buildings[bi].Floors[fi].Rooms = append(tree.Campuses[ci].Buildings[bi].Floors[fi].Rooms, room)
	}
	return tree, rows.Err()
}

func findOrAddCampus(items *[]InventoryCampus, name string) int {
	if len(*items) > 0 && (*items)[len(*items)-1].Name == name {
		return len(*items) - 1
	}
	*items = append(*items, InventoryCampus{Name: name, Buildings: []InventoryBuilding{}})
	return len(*items) - 1
}

func findOrAddBuilding(items *[]InventoryBuilding, name string) int {
	if len(*items) > 0 && (*items)[len(*items)-1].Name == name {
		return len(*items) - 1
	}
	*items = append(*items, InventoryBuilding{Name: name, Floors: []InventoryFloor{}})
	return len(*items) - 1
}

func findOrAddFloor(items *[]InventoryFloor, name string) int {
	if len(*items) > 0 && (*items)[len(*items)-1].Name == name {
		return len(*items) - 1
	}
	*items = append(*items, InventoryFloor{Name: name, Rooms: []InventoryRoom{}})
	return len(*items) - 1
}

func GetMeterOverview(ctx context.Context, pool *pgxpool.Pool, meter string, now time.Time) (MeterOverviewView, error) {
	var result MeterOverviewView
	var meterID string
	var active, excluded bool
	if err := pool.QueryRow(ctx, `
		SELECT id::text, meter_no, building, floor, room, active, excluded
		FROM meters WHERE meter_no = $1`, meter).Scan(
		&meterID, &result.Meter, &result.Location.Building, &result.Location.Floor,
		&result.Location.Room, &active, &excluded,
	); err != nil {
		return result, err
	}
	var latest LatestReadingView
	err := pool.QueryRow(ctx, `
		SELECT reading_time, observed_at, COALESCE(prepaid_yuan, 0)::text,
			subsidy_yuan::text, COALESCE(total_yuan, 0)::text, total_kwh::text,
			COALESCE(meter_status, ''), freshness
		FROM meter_readings WHERE meter_id = $1::uuid
		ORDER BY reading_time DESC LIMIT 1`, meterID).Scan(
		&latest.ReadingTime, &latest.ObservedAt, &latest.PrepaidYuan, &latest.SubsidyYuan,
		&latest.TotalYuan, &latest.TotalKWH, &latest.MeterStatus, &latest.Freshness,
	)
	if err == nil {
		result.Latest = &latest
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	var recent *string
	var covered, anomalies int
	if err := pool.QueryRow(ctx, `
		SELECT sum(usage_kwh)::text, count(*),
			(SELECT count(*) FROM anomaly_events WHERE meter_id = $1::uuid AND acknowledged_at IS NULL)
		FROM effective_daily_consumption
		WHERE meter_id = $1::uuid AND usage_date >= ($2::timestamptz)::date`,
		meterID, now.AddDate(0, 0, -6)).Scan(&recent, &covered, &anomalies); err != nil {
		return result, err
	}
	result.Recent7DKWH = recent
	result.Quality = Quality{Eligible: 1, Anomalies: anomalies}
	if result.Latest != nil {
		result.Quality.Covered = 1
		if result.Latest.Freshness == "stale" {
			result.Quality.Stale = 1
		}
	}
	result.Quality.CoverageRatio = float64(result.Quality.Covered)
	switch {
	case !active || excluded || result.Latest == nil:
		result.Availability = "unavailable"
	case covered == 0:
		result.Availability = "insufficient_history"
	case result.Quality.Stale > 0 || anomalies > 0:
		result.Availability = "partial"
	default:
		result.Availability = "ready"
	}
	return result, nil
}

func ListMeterBills(ctx context.Context, pool *pgxpool.Pool, meter, fromMonth, toMonth string) ([]MonthlyBillView, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meters WHERE meter_no=$1)`, meter).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}
	rows, err := pool.Query(ctx, `
		SELECT to_char(b.month, 'YYYY-MM'), COALESCE(b.start_kwh,0)::text,
			COALESCE(b.end_kwh,0)::text, COALESCE(b.usage_kwh,0)::text,
			COALESCE(b.cost_yuan,0)::text
		FROM monthly_bills b JOIN meters m ON m.id=b.meter_id
		WHERE m.meter_no=$1
		  AND ($2='' OR b.month >= ($2 || '-01')::date)
		  AND ($3='' OR b.month <= ($3 || '-01')::date)
		ORDER BY b.month DESC`, meter, fromMonth, toMonth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]MonthlyBillView, 0)
	for rows.Next() {
		var item MonthlyBillView
		if err := rows.Scan(&item.Month, &item.StartKWH, &item.EndKWH, &item.UsageKWH, &item.CostYuan); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func qualityForScope(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, building, floor string) (Quality, error) {
	var q Quality
	err := pool.QueryRow(ctx, `
		WITH eligible AS (
			SELECT id FROM meters WHERE active AND NOT excluded
			  AND building<>$5 AND ($3='' OR building=$3) AND ($4='' OR floor=$4)
		), bounds AS (
			SELECT ($1::timestamptz)::date AS from_date,
				CASE WHEN $2::timestamptz=date_trunc('day',$2::timestamptz)
					THEN ($2::timestamptz)::date ELSE ($2::timestamptz)::date+1 END AS to_date
		), official_covered AS (
			SELECT DISTINCT u.meter_id FROM daily_usages u JOIN eligible e ON e.id=u.meter_id
			CROSS JOIN bounds b
			WHERE u.has_upstream_data
			  AND u.usage_date>=b.from_date AND u.usage_date<b.to_date
		), estimated AS (
			SELECT DISTINCT d.meter_id FROM live_scan_daily_consumption d JOIN eligible e ON e.id=d.meter_id
			CROSS JOIN bounds b
			WHERE d.usage_date>=b.from_date AND d.usage_date<b.to_date
		), covered AS (
			SELECT meter_id FROM official_covered UNION SELECT meter_id FROM estimated
		), stale AS (
			SELECT DISTINCT ON (r.meter_id) r.meter_id, r.freshness
			FROM meter_readings r JOIN estimated e ON e.meter_id=r.meter_id
			ORDER BY r.meter_id, r.reading_time DESC
		)
		SELECT (SELECT count(*) FROM eligible), (SELECT count(*) FROM covered),
			(SELECT count(*) FROM stale WHERE freshness='stale'),
			(SELECT count(*) FROM anomaly_events a JOIN estimated e ON e.meter_id=a.meter_id
			 WHERE a.detected_at >= $1 AND a.detected_at < $2)
	`, from, to, building, floor, nonPlatformBillingBuilding).Scan(&q.Eligible, &q.Covered, &q.Stale, &q.Anomalies)
	if q.Eligible > 0 {
		q.CoverageRatio = math.Round(float64(q.Covered)/float64(q.Eligible)*10000) / 10000
	}
	return q, err
}

func availability(q Quality) string {
	switch {
	case q.Covered == 0:
		return "insufficient_history"
	case q.Covered < q.Eligible || q.Stale > 0 || q.Anomalies > 0:
		return "partial"
	default:
		return "ready"
	}
}

func GetCampusSummary(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, building, floor string) (CampusSummaryView, error) {
	result := CampusSummaryView{From: from, To: to, Scope: map[string]string{"campus": "示例校区"}}
	if building != "" {
		result.Scope["building"] = building
	}
	if floor != "" {
		result.Scope["floor"] = floor
	}
	if err := pool.QueryRow(ctx, `
		WITH daily_counts AS (
			/* 官方值与未发布日暂估值使用同一逐日非空房口径。 */
			SELECT r.usage_date, sum(r.occupied_rooms) AS rooms
			FROM effective_daily_campus_rollup r
			WHERE r.building<>$5
			  AND r.usage_date >= ($1::timestamptz)::date
			  AND r.usage_date < CASE WHEN $2::timestamptz=date_trunc('day',$2::timestamptz)
				THEN ($2::timestamptz)::date ELSE ($2::timestamptz)::date+1 END
			  AND ($3='' OR r.building=$3) AND ($4='' OR r.floor=$4)
			GROUP BY r.usage_date
		)
		SELECT (
			SELECT sum(r.usage_kwh)::text
			FROM effective_daily_campus_rollup r
			WHERE r.building<>$5
			  AND r.usage_date >= ($1::timestamptz)::date
			  AND r.usage_date < CASE WHEN $2::timestamptz=date_trunc('day',$2::timestamptz)
				THEN ($2::timestamptz)::date ELSE ($2::timestamptz)::date+1 END
			  AND ($3='' OR r.building=$3) AND ($4='' OR r.floor=$4)
		), COALESCE((SELECT round(avg(rooms))::integer FROM daily_counts),0)`,
		from, to, building, floor, nonPlatformBillingBuilding).Scan(&result.TotalKWH, &result.Meters); err != nil {
		return result, err
	}
	q, err := qualityForScope(ctx, pool, from, to, building, floor)
	if err != nil {
		return result, err
	}
	result.Quality = q
	result.Availability = availability(q)
	if result.TotalKWH != nil && result.Meters > 0 {
		if err := pool.QueryRow(ctx, `SELECT (CAST($1 AS numeric)/$2)::numeric(24,4)::text`, *result.TotalKWH, result.Meters).Scan(&result.PerRoomKWH); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validGranularity(value string) bool {
	return value == "day" || value == "week" || value == "month"
}

func GetSeries(ctx context.Context, pool *pgxpool.Pool, meter, metric, granularity string, from, to time.Time, building, floor string) (TimeSeriesView, error) {
	if !validGranularity(granularity) {
		return TimeSeriesView{}, fmt.Errorf("unsupported granularity %q", granularity)
	}
	result := TimeSeriesView{Metric: metric, Granularity: granularity, Points: []SeriesPoint{}}
	if metric == "balance" {
		result.Unit = "yuan"
	} else {
		result.Unit = "kWh"
	}
	var q Quality
	var err error
	if meter != "" {
		q = Quality{Eligible: 1}
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meters WHERE meter_no=$1)`, meter).Scan(&exists); err != nil {
			return result, err
		}
		if !exists {
			return result, pgx.ErrNoRows
		}
	} else {
		q, err = qualityForScope(ctx, pool, from, to, building, floor)
		if err != nil {
			return result, err
		}
	}
	// 每桶按各自然日 ydl 判空。户均除数取日均非空房数。
	// 禁止用昨日状态覆盖历史日。
	var rows pgx.Rows
	if metric == "balance" {
		rows, err = pool.Query(ctx, `
			WITH values_by_period AS (
				SELECT date_trunc($1, r.reading_time) period_start,
				       (ARRAY_AGG(r.total_yuan::text ORDER BY r.reading_time DESC))[1] AS value
				FROM meter_readings r
				JOIN meters m ON m.id=r.meter_id
				WHERE r.reading_time >= $2 AND r.reading_time < $3
				  AND ($4='' OR m.meter_no=$4) AND ($5='' OR m.building=$5) AND ($6='' OR m.floor=$6)
				  AND ($4<>'' OR m.building<>$7)
				  AND m.active AND NOT m.excluded
				GROUP BY 1
			), daily_counts AS (
				/* 非空房数走官方值与暂估值的统一日汇总。
				   单表时除数恒为 1。结果会被丢弃。
				   使用 $4='' 跳过全表聚合。 */
				SELECT date_trunc($1, r.usage_date::timestamptz) period_start,
				       r.usage_date,
				       sum(r.occupied_rooms) AS rooms
				FROM effective_daily_campus_rollup r
				WHERE $4=''
				  AND r.usage_date >= ($2::timestamptz)::date
				  AND r.usage_date < CASE
					WHEN $3::timestamptz=date_trunc('day',$3::timestamptz) THEN ($3::timestamptz)::date
					ELSE ($3::timestamptz)::date+1
				  END
				  AND ($5='' OR r.building=$5) AND ($6='' OR r.floor=$6)
				  AND r.building<>$7
				GROUP BY 1,2
			), occupancy_by_period AS (
				SELECT period_start, round(avg(rooms))::integer AS rooms
				FROM daily_counts GROUP BY period_start
			)
			SELECT v.period_start, v.period_start + ('1 ' || $1)::interval, v.value,
			       CASE WHEN $4<>'' THEN 1 ELSE COALESCE(o.rooms,0) END
			FROM values_by_period v LEFT JOIN occupancy_by_period o USING (period_start)
			ORDER BY v.period_start`,
			granularity, from, to, meter, building, floor, nonPlatformBillingBuilding)
	} else {
		rows, err = pool.Query(ctx, `
			WITH consumption AS (
				/* 全校范围走官方值与暂估值的统一汇总。
				   该汇总无 meter 维度。单表时（$4<>''）回落原始视图。 */
				SELECT r.usage_date, r.usage_kwh
				FROM effective_daily_campus_rollup r
				WHERE $4=''
				  AND r.usage_date >= ($2::timestamptz)::date
				  AND r.usage_date < CASE
					WHEN $3::timestamptz=date_trunc('day',$3::timestamptz) THEN ($3::timestamptz)::date
					ELSE ($3::timestamptz)::date+1
				  END
				  AND ($5='' OR r.building=$5) AND ($6='' OR r.floor=$6)
				  AND r.building<>$7
				UNION ALL
				SELECT d.usage_date, d.usage_kwh
				FROM effective_daily_consumption d JOIN meters m ON m.id=d.meter_id
				WHERE $4<>'' AND m.meter_no=$4
				  AND d.usage_date >= ($2::timestamptz)::date
				  AND d.usage_date < CASE
					WHEN $3::timestamptz=date_trunc('day',$3::timestamptz) THEN ($3::timestamptz)::date
					ELSE ($3::timestamptz)::date+1
				  END
				  AND ($5='' OR m.building=$5) AND ($6='' OR m.floor=$6)
				  AND m.active AND NOT m.excluded
			), values_by_period AS (
				SELECT date_trunc($1, usage_date::timestamptz) period_start,
				       sum(usage_kwh)::numeric(24,4)::text AS value
				FROM consumption
				GROUP BY 1
			), daily_counts AS (
				/* 非空房数走官方值与暂估值的统一日汇总。
				   单表时除数恒为 1。结果会被丢弃。
				   使用 $4='' 跳过全表聚合。 */
				SELECT date_trunc($1, r.usage_date::timestamptz) period_start,
				       r.usage_date,
				       sum(r.occupied_rooms) AS rooms
				FROM effective_daily_campus_rollup r
				WHERE $4=''
				  AND r.usage_date >= ($2::timestamptz)::date
				  AND r.usage_date < CASE
					WHEN $3::timestamptz=date_trunc('day',$3::timestamptz) THEN ($3::timestamptz)::date
					ELSE ($3::timestamptz)::date+1
				  END
				  AND ($5='' OR r.building=$5) AND ($6='' OR r.floor=$6)
				  AND r.building<>$7
				GROUP BY 1,2
			), occupancy_by_period AS (
				SELECT period_start, round(avg(rooms))::integer AS rooms
				FROM daily_counts GROUP BY period_start
			)
			SELECT v.period_start, v.period_start + ('1 ' || $1)::interval, v.value,
			       CASE WHEN $4<>'' THEN 1 ELSE COALESCE(o.rooms,0) END
			FROM values_by_period v LEFT JOIN occupancy_by_period o USING (period_start)
			ORDER BY v.period_start`, granularity, from, to, meter, building, floor, nonPlatformBillingBuilding)
	}
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var point SeriesPoint
		if err := rows.Scan(&point.PeriodStart, &point.PeriodEnd, &point.Value, &point.Meters); err != nil {
			return result, err
		}
		point.Availability = "ready"
		point.Quality = q
		result.Points = append(result.Points, point)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if meter != "" && len(result.Points) > 0 {
		q.Covered = 1
		q.CoverageRatio = 1
	}
	result.Quality = q
	for _, point := range result.Points {
		if point.Meters > result.ScopeMeters {
			result.ScopeMeters = point.Meters
		}
	}
	result.Availability = availability(q)
	return result, nil
}

/*
GetCampusBreakdown 按楼栋或楼层 × 时间桶聚合用电量。
	building 为空时按楼栋分组。否则按该栋楼层分组。
	库存仅含宿舍表。禁止按建筑类别拆分。
*/
func GetCampusBreakdown(
	ctx context.Context, pool *pgxpool.Pool,
	granularity string, from, to time.Time, building string,
) (CampusBreakdownView, error) {
	if !validGranularity(granularity) {
		return CampusBreakdownView{}, fmt.Errorf("unsupported granularity %q", granularity)
	}
	groupBy := "building"
	if building != "" {
		groupBy = "floor"
	}
	result := CampusBreakdownView{
		GroupBy: groupBy, Granularity: granularity,
		Buckets: []time.Time{}, Rows: []CampusBreakdownRow{},
		TotalKWH: "0", MaxCellKWH: "0",
	}
	q, err := qualityForScope(ctx, pool, from, to, building, "")
	if err != nil {
		return result, err
	}
	result.Quality = q
	result.Availability = availability(q)

	/* 空房按天变化。Meters 为整窗日均非空房数。
	   每桶除数在 meter_counts。禁止用昨日状态覆盖历史。
	   使用 GROUPING SETS 一次算两个粒度。避免双扫。
	   数据源为 effective_daily_campus_rollup。见 migration 000033。
	   bucket 是 usage_date 的函数。窗口平均等于整体平均。 */
	meterCounts := map[string]int{}
	bucketMeterCounts := map[string]map[time.Time]int{}
	countRows, err := pool.Query(ctx, `
		WITH daily_counts AS (
			SELECT CASE WHEN $4='' THEN r.building ELSE r.floor END AS row_key,
			       date_trunc($1, r.usage_date::timestamptz) AS bucket,
			       r.usage_date, sum(r.occupied_rooms) AS rooms
			FROM effective_daily_campus_rollup r
			WHERE r.usage_date >= ($2::timestamptz)::date
			  AND r.usage_date < CASE WHEN $3::timestamptz=date_trunc('day',$3::timestamptz)
				THEN ($3::timestamptz)::date ELSE ($3::timestamptz)::date+1 END
			  AND r.building<>$5
			  AND ($4='' OR r.building=$4)
			GROUP BY 1,2,3
		)
		SELECT row_key, bucket, round(avg(rooms))::integer, grouping(bucket)
		FROM daily_counts
		GROUP BY GROUPING SETS ((row_key, bucket), (row_key))`,
		granularity, from, to, building, nonPlatformBillingBuilding)
	if err != nil {
		return result, err
	}
	for countRows.Next() {
		var key string
		var bucket *time.Time
		var n, windowLevel int
		if err := countRows.Scan(&key, &bucket, &n, &windowLevel); err != nil {
			countRows.Close()
			return result, err
		}
		// grouping(bucket)=1 为整窗汇总。bucket 为 NULL。
		if windowLevel == 1 {
			meterCounts[key] = n
			continue
		}
		if bucket == nil {
			continue
		}
		if bucketMeterCounts[key] == nil {
			bucketMeterCounts[key] = map[time.Time]int{}
		}
		bucketMeterCounts[key][*bucket] = n
	}
	countRows.Close()
	if err := countRows.Err(); err != nil {
		return result, err
	}

	/* 统一汇总已合并官方值与未发布日扫描暂估值。 */
	rows, err := pool.Query(ctx, `
		SELECT CASE WHEN $4='' THEN r.building ELSE r.floor END AS row_key,
		       date_trunc($1, r.usage_date::timestamptz) AS bucket,
		       sum(r.usage_kwh)::numeric(24,4)::text
		FROM effective_daily_campus_rollup r
		WHERE r.usage_date >= ($2::timestamptz)::date
		  AND r.usage_date < CASE WHEN $3::timestamptz=date_trunc('day',$3::timestamptz)
			THEN ($3::timestamptz)::date ELSE ($3::timestamptz)::date+1 END
		  AND r.building<>$5
		  AND ($4='' OR r.building=$4)
		GROUP BY 1,2 ORDER BY 1,2`, granularity, from, to, building, nonPlatformBillingBuilding)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	type cell struct {
		bucket time.Time
		value  string
	}
	cells := map[string][]cell{}
	bucketSeen := map[time.Time]bool{}
	for rows.Next() {
		var key string
		var bucket time.Time
		var value string
		if err := rows.Scan(&key, &bucket, &value); err != nil {
			return result, err
		}
		cells[key] = append(cells[key], cell{bucket: bucket, value: value})
		bucketSeen[bucket] = true
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	buckets := make([]time.Time, 0, len(bucketSeen))
	for b := range bucketSeen {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Before(buckets[j]) })
	index := make(map[time.Time]int, len(buckets))
	for i, b := range buckets {
		index[b] = i
	}
	result.Buckets = buckets

	grandTotal := 0.0
	maxCell := 0.0
	for key, list := range cells {
		row := CampusBreakdownRow{
			Key: key, Meters: meterCounts[key],
			MeterCounts: make([]int, len(buckets)), Values: make([]*string, len(buckets)),
		}
		for i, bucket := range buckets {
			row.MeterCounts[i] = bucketMeterCounts[key][bucket]
		}
		total := 0.0
		for _, c := range list {
			value := c.value
			row.Values[index[c.bucket]] = &value
			if v, err := strconv.ParseFloat(c.value, 64); err == nil {
				total += v
				if v > maxCell {
					maxCell = v
				}
			}
		}
		row.TotalKWH = strconv.FormatFloat(total, 'f', 4, 64)
		grandTotal += total
		result.Rows = append(result.Rows, row)
	}
	// 用电量降序。构成图与热力图优先高用电。
	sort.Slice(result.Rows, func(i, j int) bool {
		a, _ := strconv.ParseFloat(result.Rows[i].TotalKWH, 64)
		b, _ := strconv.ParseFloat(result.Rows[j].TotalKWH, 64)
		if a == b {
			return result.Rows[i].Key < result.Rows[j].Key
		}
		return a > b
	})
	if grandTotal > 0 {
		for i := range result.Rows {
			v, _ := strconv.ParseFloat(result.Rows[i].TotalKWH, 64)
			result.Rows[i].Share = v / grandTotal
		}
	}
	result.TotalKWH = strconv.FormatFloat(grandTotal, 'f', 4, 64)
	result.MaxCellKWH = strconv.FormatFloat(maxCell, 'f', 4, 64)
	return result, nil
}

/*
rankingCTE 为榜单口径唯一来源。
$1=from $2=toExclusive $3=previousFrom $4=building $5=floor
$6=不支持楼栋 $7=模式 $8=本期天数 $9=上期天数。
	列表与本人名次必须使用同一 CTE。否则口径会漂移。
*/
const rankingCTE = `
	WITH raw_values AS (
		SELECT m.id, m.building, m.floor, m.room,
			COALESCE(sum(d.usage_kwh) FILTER (WHERE d.usage_date >= ($1::timestamptz)::date AND d.usage_date < ($2::timestamptz)::date),0) current_raw_kwh,
			COALESCE(sum(d.usage_kwh) FILTER (WHERE d.usage_date >= ($3::timestamptz)::date AND d.usage_date < ($1::timestamptz)::date),0) previous_raw_kwh,
			COALESCE(sum(d.usage_kwh) FILTER (WHERE d.usage_date >= ($1::timestamptz)::date AND d.usage_date < ($2::timestamptz)::date AND o.is_occupied),0) current_occupied_kwh,
			COALESCE(sum(d.usage_kwh) FILTER (WHERE d.usage_date >= ($3::timestamptz)::date AND d.usage_date < ($1::timestamptz)::date AND o.is_occupied),0) previous_occupied_kwh,
			count(DISTINCT d.usage_date) FILTER (WHERE d.usage_date >= ($1::timestamptz)::date AND d.usage_date < ($2::timestamptz)::date AND o.is_occupied) current_occupied_days,
			count(DISTINCT d.usage_date) FILTER (WHERE d.usage_date >= ($3::timestamptz)::date AND d.usage_date < ($1::timestamptz)::date AND o.is_occupied) previous_occupied_days
		FROM meters m
		LEFT JOIN effective_daily_consumption d ON d.meter_id=m.id
			AND d.usage_date >= ($3::timestamptz)::date AND d.usage_date < ($2::timestamptz)::date
		/* 日期边界必须在 o 上再写一遍。
		   规划器不从 o.usage_date=d.usage_date 推导范围。
		   缺少边界会全表物化 daily_room_occupancy。查询显著变慢。 */
		LEFT JOIN daily_room_occupancy o ON o.meter_id=d.meter_id AND o.usage_date=d.usage_date
			AND o.usage_date >= ($3::timestamptz)::date AND o.usage_date < ($2::timestamptz)::date
		WHERE m.active AND NOT m.excluded AND m.building<>$6
		  AND ($4='' OR m.building=$4) AND ($5='' OR m.floor=$5)
		GROUP BY m.id
	), values AS (
		SELECT id, building, floor, room, current_occupied_days,
			CASE WHEN $7='saving' AND current_occupied_days>0
				THEN current_occupied_kwh/current_occupied_days*$8 ELSE current_raw_kwh END AS current_kwh,
			CASE WHEN $7='saving' AND previous_occupied_days>0
				THEN previous_occupied_kwh/previous_occupied_days*$9 ELSE previous_raw_kwh END AS previous_kwh
		FROM raw_values
	), ranked AS (
		SELECT *, CASE WHEN previous_kwh=0 THEN NULL ELSE ((current_kwh-previous_kwh)/previous_kwh)::double precision END change_ratio
		FROM values
		WHERE current_kwh > 0
		  AND ($7<>'saving' OR current_occupied_days>0)
	), owners AS (
		/* 一块表最多 4 个绑定者。见 migrations/000025。
		   榜单每表仅一行。先按 meter_id 聚合。禁止 JOIN 后重复行。
		   任一绑定者关字段则隐藏（bool_and）。
		   任一绑定者要求打码则打码（bool_or）。
		   无偏好记录时使用 defaultLeaderboardPreference() 默认值。
		   两处默认值必须一致。 */
		SELECT b.meter_id,
			count(*)::int AS binder_count,
			array_agg(b.user_id::text) AS user_ids,
			array_agg(COALESCE(u.nickname,'') ORDER BY b.bound_at, b.id) AS nicknames,
			bool_and(COALESCE(p.show_building, true))  AS show_building,
			bool_and(COALESCE(p.show_floor,    true))  AS show_floor,
			bool_and(COALESCE(p.show_room,     true))  AS show_room,
			bool_and(COALESCE(p.show_nickname, false)) AS show_nickname,
			bool_or (COALESCE(p.mask_building, false)) AS mask_building,
			bool_or (COALESCE(p.mask_floor,    false)) AS mask_floor,
			bool_or (COALESCE(p.mask_room,     true))  AS mask_room
		FROM user_meter_bindings b
		JOIN user_accounts u ON u.id=b.user_id
		LEFT JOIN user_leaderboard_preferences p ON p.user_id=b.user_id
		WHERE b.unbound_at IS NULL
		GROUP BY b.meter_id
	), owned AS (
		SELECT r.*, COALESCE(o.binder_count,0) AS binder_count,
			COALESCE(o.user_ids, ARRAY[]::text[]) AS user_ids,
			COALESCE(o.nicknames, ARRAY[]::text[]) AS nicknames,
			o.show_building, o.show_floor, o.show_room, o.show_nickname,
			o.mask_building, o.mask_floor, o.mask_room
		FROM ranked r
		LEFT JOIN owners o ON o.meter_id=r.id
	)`

/*
GetRanking 渲染榜单。viewerUserID 为空表示匿名调用者。
平台支持的 active 电表强制参与。偏好仅影响文案，不控制是否上榜。
	reveal 为真时跳过脱敏。仅给 operator / admin。
	角色由 API 层现查。见 httpapi.campusRankings。
*/
func GetRanking(ctx context.Context, pool *pgxpool.Pool, period, mode, building, floor string, limit int, now time.Time, refreshTime, viewerUserID string, reveal bool) (RankingView, error) {
	window := rankingPeriods(period, now, refreshTime)
	q, err := qualityForScope(ctx, pool, window.From, window.To, building, floor)
	if err != nil {
		return RankingView{}, err
	}
	result := RankingView{
		Period: period, Mode: mode, CurrentPeriod: window.Current, PreviousPeriod: window.Previous,
		UpdatedAt: window.UpdatedAt, NextUpdateAt: window.NextUpdateAt,
		Availability: availability(q), Quality: q, Items: []RankingEntryView{},
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM meters WHERE active AND excluded`).Scan(&result.ExcludedCount); err != nil {
		return result, err
	}
	order := "current_kwh DESC"
	if mode == "saving" {
		order = "current_kwh ASC"
	} else if mode == "surge" {
		order = "change_ratio DESC NULLS LAST"
	} else if mode == "drop" {
		order = "change_ratio ASC NULLS LAST"
	}
	/* 每表带所有者展示偏好。active 电表强制参与，不看 opted_in。 */
	query := fmt.Sprintf(`
		%s
		/* 文本用电量输出列必须命名为 value_kwh。
		   若命名 current_kwh，ORDER BY 会绑定 text 列。排序变字典序。 */
		SELECT building, floor, room, current_kwh::numeric(24,4)::text AS value_kwh, change_ratio,
			binder_count, user_ids, nicknames,
			show_building, show_floor, show_room, show_nickname, mask_building, mask_floor, mask_room
		FROM owned ORDER BY %s, building, floor, room LIMIT $10`,
		rankingCTE, order)
	rows, err := pool.Query(ctx, query,
		window.From, window.To, window.PreviousFrom, building, floor, nonPlatformBillingBuilding,
		mode, window.CurrentDays, window.PreviousDays, limit,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RankingEntryView
		var building, floor, room string
		var binderCount int
		var userIDs, nicknames []string
		var showBuilding, showFloor, showRoom, showNickname, maskBuilding, maskFloor, maskRoom *bool
		if err := rows.Scan(
			&building, &floor, &room, &item.ValueKWH, &item.ChangeRatio,
			&binderCount, &userIDs, &nicknames,
			&showBuilding, &showFloor, &showRoom, &showNickname, &maskBuilding, &maskFloor, &maskRoom,
		); err != nil {
			return result, err
		}
		pref := preferenceFromColumns(showBuilding, showFloor, showRoom, showNickname, maskBuilding, maskFloor, maskRoom)
		item.Rank = len(result.Items) + 1
		// 合住表每个绑定者均标记为本人行。
		item.IsSelf = viewerUserID != "" && slices.Contains(userIDs, viewerUserID)
		// 本人行使用同一展示偏好。便于核对隐私设置是否生效。
		item.Name = rankingName(binderCount, pref.ShowNickname, nicknames, reveal)
		item.Label = rankingLocation(pref, building, floor, room, reveal)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	rows.Close()

	self, err := selfRanking(ctx, pool, order,
		window.From, window.To, window.PreviousFrom, building, floor, mode,
		window.CurrentDays, window.PreviousDays, viewerUserID, reveal,
	)
	if err != nil {
		return result, err
	}
	if self != nil {
		// 已在前 N 名时标记。前端可只高亮，不重复渲染卡片。
		for _, item := range result.Items {
			if item.IsSelf {
				self.InList = true
				break
			}
		}
	}
	result.Self = self
	return result, nil
}

type rankingPeriodWindow struct {
	From, To, PreviousFrom    time.Time
	CurrentDays, PreviousDays int
	Current, Previous         RankingPeriodView
	UpdatedAt, NextUpdateAt   time.Time
}

// rankingPeriods 将三档榜单固定在已结束的完整自然周期。
// 刷新点使用管理端配置的时区时刻。缺省 09:00。
// 日榜每天。周榜周一。月榜每月 1 日。
// 查询使用 [From, To) 半开区间。显示时 To-1 为闭区间末日。
func rankingPeriods(period string, now time.Time, refreshTime string) rankingPeriodWindow {
	local := now.In(now.Location())
	loc := local.Location()
	dayStart := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc) }
	today := dayStart(local)
	refreshHour, refreshMinute := 9, 0
	if _, err := fmt.Sscanf(refreshTime, "%d:%d", &refreshHour, &refreshMinute); err != nil ||
		refreshHour < 0 || refreshHour > 23 || refreshMinute < 0 || refreshMinute > 59 {
		refreshHour, refreshMinute = 9, 0
	}

	var updatedAt, nextUpdateAt, to, from, previousFrom time.Time
	currentDays, previousDays := 1, 1
	switch period {
	case "week":
		mondayOffset := (int(today.Weekday()) + 6) % 7
		monday := today.AddDate(0, 0, -mondayOffset)
		updatedAt = time.Date(monday.Year(), monday.Month(), monday.Day(), refreshHour, refreshMinute, 0, 0, loc)
		if local.Before(updatedAt) {
			updatedAt = updatedAt.AddDate(0, 0, -7)
		}
		nextUpdateAt = updatedAt.AddDate(0, 0, 7)
		to = dayStart(updatedAt)
		from = to.AddDate(0, 0, -7)
		previousFrom = from.AddDate(0, 0, -7)
		currentDays, previousDays = 7, 7
	case "month":
		updatedAt = time.Date(local.Year(), local.Month(), 1, refreshHour, refreshMinute, 0, 0, loc)
		if local.Before(updatedAt) {
			updatedAt = time.Date(local.Year(), local.Month()-1, 1, refreshHour, refreshMinute, 0, 0, loc)
		}
		nextUpdateAt = time.Date(updatedAt.Year(), updatedAt.Month()+1, 1, refreshHour, refreshMinute, 0, 0, loc)
		to = time.Date(updatedAt.Year(), updatedAt.Month(), 1, 0, 0, 0, 0, loc)
		from = to.AddDate(0, -1, 0)
		previousFrom = from.AddDate(0, -1, 0)
		currentDays = to.AddDate(0, 0, -1).Day()
		previousDays = from.AddDate(0, 0, -1).Day()
	default:
		updatedAt = time.Date(today.Year(), today.Month(), today.Day(), refreshHour, refreshMinute, 0, 0, loc)
		if local.Before(updatedAt) {
			updatedAt = updatedAt.AddDate(0, 0, -1)
		}
		nextUpdateAt = updatedAt.AddDate(0, 0, 1)
		to = dayStart(updatedAt)
		from = to.AddDate(0, 0, -1)
		previousFrom = from.AddDate(0, 0, -1)
	}

	day := func(t time.Time) string { return t.Format("2006-01-02") }
	return rankingPeriodWindow{
		From: from, To: to, PreviousFrom: previousFrom,
		CurrentDays: currentDays, PreviousDays: previousDays,
		Current:   RankingPeriodView{From: day(from), To: day(to.AddDate(0, 0, -1))},
		Previous:  RankingPeriodView{From: day(previousFrom), To: day(from.AddDate(0, 0, -1))},
		UpdatedAt: updatedAt, NextUpdateAt: nextUpdateAt,
	}
}

/*
selfRanking 计算调用者电表名次。与列表使用同一 CTE 和排序。
	名次全量计算。不受 limit 影响。
*/
func selfRanking(
	ctx context.Context, pool *pgxpool.Pool, order string,
	from, to, previousFrom time.Time, building, floor, mode string,
	currentDays, previousDays int, viewerUserID string,
	reveal bool,
) (*RankingSelfView, error) {
	if viewerUserID == "" {
		return nil, nil
	}
	/* 全量参与。同时取上下各一名作为参照。 */
	query := fmt.Sprintf(`
		%s, scored AS (
			SELECT *,
				row_number() OVER (ORDER BY %s, building, floor, room) AS position,
				count(*) OVER () AS population
			FROM owned
		), anchor AS (
			-- 合住表：绑定者含调用者即本人行。
			SELECT position, population FROM scored WHERE $10::text = ANY(user_ids)
		)
		SELECT s.position, a.population, s.building, s.floor, s.room,
			s.current_kwh::numeric(24,4)::text AS value_kwh, s.change_ratio,
			s.binder_count, s.user_ids, s.nicknames,
			s.show_building, s.show_floor, s.show_room, s.show_nickname,
			s.mask_building, s.mask_floor, s.mask_room
		FROM scored s JOIN anchor a ON s.position BETWEEN a.position - 1 AND a.position + 1
		ORDER BY s.position`, rankingCTE, order)
	rows, err := pool.Query(ctx, query, from, to, previousFrom, building, floor, nonPlatformBillingBuilding, mode, currentDays, previousDays, viewerUserID)
	if err != nil {
		return nil, fmt.Errorf("self ranking: %w", err)
	}
	defer rows.Close()

	var self *RankingSelfView
	neighbors := make([]RankingEntryView, 0, 3)
	for rows.Next() {
		var position, population int
		var b, f, room string
		var binderCount int
		var userIDs, nicknames []string
		var valueKWH string
		var changeRatio *float64
		var showBuilding, showFloor, showRoom, showNickname, maskBuilding, maskFloor, maskRoom *bool
		if err := rows.Scan(
			&position, &population, &b, &f, &room, &valueKWH, &changeRatio,
			&binderCount, &userIDs, &nicknames,
			&showBuilding, &showFloor, &showRoom, &showNickname, &maskBuilding, &maskFloor, &maskRoom,
		); err != nil {
			return nil, err
		}
		isSelf := slices.Contains(userIDs, viewerUserID)
		pref := preferenceFromColumns(
			showBuilding, showFloor, showRoom, showNickname, maskBuilding, maskFloor, maskRoom,
		)
		// 本人也按隐私设置渲染。与公开榜单视角一致。
		entry := RankingEntryView{
			Rank: position, IsSelf: isSelf, ValueKWH: valueKWH, ChangeRatio: changeRatio,
			Name:  rankingName(binderCount, pref.ShowNickname, nicknames, reveal),
			Label: rankingLocation(pref, b, f, room, reveal),
		}
		if isSelf {
			self = &RankingSelfView{
				Rank: position, Total: population, Name: entry.Name, Label: entry.Label,
				// Building / Floor / Room 保留绑定真值。对外展示用 Name / Label。
				Building: b, Floor: f, Room: room,
				ValueKWH: valueKWH, ChangeRatio: changeRatio,
			}
			if population > 0 {
				self.Percentile = float64(position) / float64(population)
			}
		}
		neighbors = append(neighbors, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if self == nil {
		// 无绑表，或该期无有效用量（current_kwh > 0 已过滤）。
		return nil, nil
	}
	self.Neighbors = neighbors
	return self, nil
}

// preferenceFromColumns 将可为 NULL 的偏好列还原为结构。
// 全 NULL 表示无账号。使用默认：完整栋/层，房间打码。
func preferenceFromColumns(showBuilding, showFloor, showRoom, showNickname, maskBuilding, maskFloor, maskRoom *bool) LeaderboardPreference {
	if showBuilding == nil {
		return defaultLeaderboardPreference()
	}
	pref := LeaderboardPreference{
		OptedIn: true, ShowBuilding: *showBuilding, ShowFloor: *showFloor,
		ShowRoom: *showRoom, ShowNickname: *showNickname,
		MaskBuilding: *maskBuilding, MaskFloor: *maskFloor, MaskRoom: true,
	}
	if maskRoom != nil {
		pref.MaskRoom = *maskRoom
	}
	return pref
}

func InventoryCounters(ctx context.Context, pool *pgxpool.Pool) (RunCounters, error) {
	var c RunCounters
	err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE active AND excluded),
			count(*) FILTER (WHERE active AND NOT excluded) FROM meters
	`).Scan(&c.InventoryTotal, &c.ExcludedTotal, &c.EligibleTotal)
	return c, err
}

func UnacknowledgedAnomalyCounts(ctx context.Context, pool *pgxpool.Pool) (total, critical int, err error) {
	err = pool.QueryRow(ctx, `
		SELECT count(*),count(*) FILTER (WHERE severity='critical')
		FROM anomaly_events WHERE acknowledged_at IS NULL
	`).Scan(&total, &critical)
	return total, critical, err
}

func MigrationVersion(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var version int
	err := pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&version)
	return version, err
}

func WorkerState(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration) (string, error) {
	heartbeat, err := WorkerHeartbeat(ctx, pool)
	if err != nil {
		return "unavailable", err
	}
	if heartbeat == nil {
		return "unavailable", nil
	}
	if time.Since(*heartbeat) > staleAfter {
		return "stale", nil
	}
	return "ready", nil
}

func WorkerHeartbeat(ctx context.Context, pool *pgxpool.Pool) (*time.Time, error) {
	var heartbeat *time.Time
	err := pool.QueryRow(ctx, `SELECT max(heartbeat_at) FROM worker_heartbeats`).Scan(&heartbeat)
	return heartbeat, err
}

func LatestRollupAt(ctx context.Context, pool *pgxpool.Pool) (*time.Time, error) {
	var updatedAt *time.Time
	err := pool.QueryRow(ctx, `SELECT max(updated_at) FROM consumption_rollups`).Scan(&updatedAt)
	return updatedAt, err
}
