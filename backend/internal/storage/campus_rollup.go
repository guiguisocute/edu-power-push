package storage

/* daily_campus_rollup 的维护。
   物化已发布官方日明细：某天 × 某楼层的非空房数与合计用电。
   见 migrations/000023、000024。
   未发布日扫描估算在查询中现算。
   effective_daily_campus_rollup 合并两半为同一总量与户均口径。
   变旧时机：日明细导入、盘点改 meters、改 empty_room_threshold_kwh。
   三入口均调用 RefreshCampusRollup。maintenance 每日兜底。 */

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const campusRollupLockID int64 = 836005285

/*
RefreshCampusRollup 重建 daily_campus_rollup。
	CONCURRENTLY：重建期间读者不阻塞。必须有唯一索引。
	必须等待前一次刷新结束再跑。否则新提交会缺席。
*/
func RefreshCampusRollup(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire campus rollup connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", campusRollupLockID); err != nil {
		return fmt.Errorf("acquire campus rollup lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", campusRollupLockID)
	}()

	// 单条语句。禁止包在显式事务中。CONCURRENTLY 限制。
	if _, err := conn.Exec(ctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY daily_campus_rollup"); err != nil {
		return fmt.Errorf("refresh daily_campus_rollup: %w", err)
	}
	return nil
}
