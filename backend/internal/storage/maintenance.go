package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const rollupLockID int64 = 836005284

type sqlExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type rollupScope struct {
	typeName string
	expr     func(string) string
}

var rollupScopes = []rollupScope{
	{typeName: "campus", expr: func(alias string) string { return alias + ".campus" }},
	{typeName: "building", expr: func(alias string) string {
		return alias + ".campus || E'\\x1f' || " + alias + ".building"
	}},
	{typeName: "floor", expr: func(alias string) string {
		return alias + ".campus || E'\\x1f' || " + alias + ".building || E'\\x1f' || " + alias + ".floor"
	}},
	{typeName: "meter", expr: func(alias string) string { return alias + ".meter_no" }},
}

func RefreshRollupsForRun(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	var from *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT min(reading_time) FROM meter_readings
		WHERE source='live_scan' AND source_ref=$1
	`, runID).Scan(&from); err != nil {
		return err
	}
	if from == nil {
		return nil
	}
	return RefreshRollups(ctx, pool, *from)
}

// RefreshRollupsForDailyDetailRun 使官方日明细替换进入通用汇总。
// 余额扫描与日明细并发时，后完成者按 rollupLockID 重建。
// 窗口取 source_run_id 实际写入或修正的行。见 daily_detail_run.go。
// 禁止按 has_upstream_data 过滤。撤回官方值也必须纳入窗口。
func RefreshRollupsForDailyDetailRun(ctx context.Context, pool *pgxpool.Pool, runID string) error {
	var from *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT min(usage_date)::timestamptz
		FROM daily_usages
		WHERE source_run_id=$1::uuid
	`, runID).Scan(&from); err != nil {
		return err
	}
	if from == nil {
		return nil
	}
	return RefreshRollups(ctx, pool, *from)
}

func RefreshRollups(ctx context.Context, pool *pgxpool.Pool, from time.Time) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire rollup connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", rollupLockID); err != nil {
		return fmt.Errorf("acquire rollup lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", rollupLockID) }()

	periods := []struct {
		name     string
		interval string
	}{{"day", "1 day"}, {"week", "1 week"}, {"month", "1 month"}}
	// 删除与重建在同一事务。任一 scope 失败不留半截空档。
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rollup transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, period := range periods {
		for _, scope := range rollupScopes {
			if err := refreshRollup(ctx, tx, from, period.name, period.interval, scope); err != nil {
				return fmt.Errorf("refresh %s %s rollups: %w", scope.typeName, period.name, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rollups: %w", err)
	}
	return nil
}

func refreshRollup(ctx context.Context, db sqlExecer, from time.Time, period, interval string, scope rollupScope) error {
	/* 重建窗口内汇总。DELETE 必须是独立语句。
	   与 INSERT 同 CTE 时共享快照。唯一索引仍见已删行。会触发 23505。 */
	if _, err := db.Exec(ctx, fmt.Sprintf(`
		DELETE FROM consumption_rollups
		WHERE scope_type=%[1]s AND period=%[2]s AND period_start>=date_trunc(%[2]s,$1::timestamptz)
	`, quoteLiteral(scope.typeName), quoteLiteral(period)), from); err != nil {
		return err
	}
	query := fmt.Sprintf(`
		WITH aggregated AS (
				SELECT %[3]s scope_key, date_trunc(%[2]s,d.usage_date::timestamptz) period_start,
					sum(d.usage_kwh)::numeric(24,4) kwh, count(DISTINCT m.id)::integer covered_count
				FROM effective_daily_consumption d JOIN meters m ON m.id=d.meter_id
				WHERE d.usage_date>=date_trunc(%[2]s,$1::timestamptz)::date
				  AND m.active AND NOT m.excluded
			GROUP BY 1,2
		), eligible AS (
			SELECT %[4]s scope_key,count(*)::integer eligible_count
			FROM meters e WHERE e.active AND NOT e.excluded GROUP BY 1
		), stale AS (
			SELECT %[5]s scope_key,date_trunc(%[2]s,r.observed_at) period_start,
				count(DISTINCT r.meter_id)::integer stale_count
			FROM meter_readings r JOIN meters sm ON sm.id=r.meter_id
			WHERE r.observed_at>=date_trunc(%[2]s,$1::timestamptz) AND r.freshness='stale'
			  AND sm.active AND NOT sm.excluded GROUP BY 1,2
		), anomalies AS (
			SELECT %[6]s scope_key,date_trunc(%[2]s,a.detected_at) period_start,
				count(*)::integer anomaly_count
			FROM anomaly_events a JOIN meters xm ON xm.id=a.meter_id
			WHERE a.detected_at>=date_trunc(%[2]s,$1::timestamptz)
			  AND xm.active AND NOT xm.excluded GROUP BY 1,2
		)
		INSERT INTO consumption_rollups (
			scope_type,scope_key,period,period_start,period_end,kwh,
			eligible_count,covered_count,stale_count,anomaly_count,availability
		)
		SELECT %[1]s,a.scope_key,%[2]s,a.period_start,a.period_start+%[7]s::interval,a.kwh,
			e.eligible_count,a.covered_count,COALESCE(s.stale_count,0),COALESCE(n.anomaly_count,0),
			CASE
				WHEN a.covered_count=0 THEN 'insufficient_history'
				WHEN a.covered_count<e.eligible_count OR COALESCE(s.stale_count,0)>0 OR COALESCE(n.anomaly_count,0)>0 THEN 'partial'
				ELSE 'ready'
			END
		FROM aggregated a JOIN eligible e USING (scope_key)
		LEFT JOIN stale s USING (scope_key,period_start)
		LEFT JOIN anomalies n USING (scope_key,period_start)
	`, quoteLiteral(scope.typeName), quoteLiteral(period), scope.expr("m"), scope.expr("e"), scope.expr("sm"), scope.expr("xm"), quoteLiteral(interval))
	_, err := db.Exec(ctx, query, from)
	return err
}

func PruneOperationalData(ctx context.Context, pool *pgxpool.Pool, resultRetention, mailRetention time.Duration) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM scan_results WHERE created_at < now() - $1::interval
		`, durationInterval(resultRetention)); err != nil {
			return fmt.Errorf("prune scan results: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM scan_run_meters rm USING scan_runs r
			WHERE rm.run_id=r.id AND r.finished_at < now() - $1::interval
		`, durationInterval(resultRetention)); err != nil {
			return fmt.Errorf("prune frozen scan inventories: %w", err)
		}
		/* 单表手动刷新每次点击写一行 scan_runs。须随 scan_results 过期。
		   anomaly_events.scan_run_id 无级联。先摘引用再删。事件本身保留。 */
		if _, err := tx.Exec(ctx, `
			UPDATE anomaly_events SET scan_run_id = NULL
			WHERE scan_run_id IN (
				SELECT id FROM scan_runs
				WHERE scope->>'meter' IS NOT NULL
				  AND finished_at IS NOT NULL
				  AND finished_at < now() - $1::interval
			)
		`, durationInterval(resultRetention)); err != nil {
			return fmt.Errorf("detach anomalies from expired meter refreshes: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM scan_runs
			WHERE scope->>'meter' IS NOT NULL
			  AND finished_at IS NOT NULL
			  AND finished_at < now() - $1::interval
		`, durationInterval(resultRetention)); err != nil {
			return fmt.Errorf("prune expired meter refreshes: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM mail_deliveries WHERE created_at < now() - $1::interval
		`, durationInterval(mailRetention)); err != nil {
			return fmt.Errorf("prune mail deliveries: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM mail_send_quota_windows WHERE expires_at < now()
		`); err != nil {
			return fmt.Errorf("prune mail quota windows: %w", err)
		}
		/* 已轮换 refresh token 写多读少。过重放检测窗口后无价值。
		   先清自引用。吊销行保留 7 天。过期活跃行保留 1 天。 */
		if _, err := tx.Exec(ctx, `
			UPDATE auth_refresh_sessions SET replaced_by_id=NULL
			WHERE replaced_by_id IN (
				SELECT id FROM auth_refresh_sessions
				WHERE expires_at < now()-interval '1 day'
				   OR revoked_at < now()-interval '7 days'
			)
		`); err != nil {
			return fmt.Errorf("detach expired refresh sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM auth_refresh_sessions
			WHERE expires_at < now()-interval '1 day'
			   OR revoked_at < now()-interval '7 days'
		`); err != nil {
			return fmt.Errorf("prune refresh sessions: %w", err)
		}
		return nil
	})
}

func durationInterval(value time.Duration) string {
	return fmt.Sprintf("%f seconds", value.Seconds())
}

func quoteLiteral(value string) string {
	// 调用方仅传内部枚举常量。此处加引号使查询显式。防止误当标识符。
	return "'" + value + "'"
}
