-- 校验 migration 000033。
-- 仅明确官方行可覆盖暂估值。
-- 校园汇总与 effective_daily_consumption 必须逐行相等。
-- 两半重叠会导致重复计数。
-- 本脚本证伪重叠重复计数。
-- 用法：psql -v ON_ERROR_STOP=1 -f verify_campus_rollup.sql
BEGIN;

\i /tmp/000033_unpublished_daily_detail_fallback.up.sql

\echo '=== 0) 两半是否真的不相交（重叠 = 重复计数，必须为 0）==='
SELECT count(*) AS overlapping_meter_days
FROM daily_usages u
JOIN live_scan_daily_consumption e
  ON e.meter_id = u.meter_id AND e.usage_date = u.usage_date
WHERE u.has_upstream_data;

\echo '=== 1) breakdown 楼栋 × 日 用电量（近一年）==='
WITH old AS (
    SELECT m.building AS row_key, date_trunc('day', d.usage_date::timestamptz) AS bucket,
           sum(d.usage_kwh)::numeric(24,4) AS kwh
    FROM effective_daily_consumption d JOIN meters m ON m.id=d.meter_id
    WHERE d.usage_date >= (now() - interval '1 year')::date AND d.usage_date < now()::date + 1
      AND m.active AND NOT m.excluded AND m.building<>'平台计费'
    GROUP BY 1,2
), new AS (
    SELECT row_key, date_trunc('day', usage_date::timestamptz) AS bucket,
           sum(usage_kwh)::numeric(24,4) AS kwh
    FROM (
        SELECT r.building AS row_key, r.usage_date, r.usage_kwh
        FROM daily_campus_rollup r
        WHERE r.usage_date >= (now() - interval '1 year')::date AND r.usage_date < now()::date + 1
          AND r.building<>'平台计费'
        UNION ALL
        SELECT m.building, e.usage_date, e.usage_kwh
        FROM live_scan_daily_consumption e JOIN meters m ON m.id=e.meter_id
        WHERE e.usage_date >= (now() - interval '1 year')::date AND e.usage_date < now()::date + 1
          AND m.active AND NOT m.excluded AND m.building<>'平台计费'
    ) parts
    GROUP BY 1,2
)
SELECT COALESCE(o.row_key,n.row_key) AS row_key, COALESCE(o.bucket,n.bucket) AS bucket,
       o.kwh AS old_kwh, n.kwh AS new_kwh, '*** MISMATCH ***' AS verdict
FROM old o FULL JOIN new n USING (row_key, bucket)
WHERE o.kwh IS DISTINCT FROM n.kwh
UNION ALL
SELECT 'cells compared: all equal', NULL, count(*)::numeric, count(*)::numeric, 'ok'
FROM old o JOIN new n USING (row_key, bucket) WHERE o.kwh = n.kwh;

\echo '=== 2) series 全校按月合计（近一年）==='
WITH old AS (
    SELECT date_trunc('month', d.usage_date::timestamptz) AS p,
           sum(d.usage_kwh)::numeric(24,4) AS kwh
    FROM effective_daily_consumption d JOIN meters m ON m.id=d.meter_id
    WHERE d.usage_date >= (now() - interval '1 year')::date AND d.usage_date < now()::date + 1
      AND m.active AND NOT m.excluded AND m.building<>'平台计费'
    GROUP BY 1
), new AS (
    SELECT date_trunc('month', usage_date::timestamptz) AS p,
           sum(usage_kwh)::numeric(24,4) AS kwh
    FROM (
        SELECT r.usage_date, r.usage_kwh FROM daily_campus_rollup r
        WHERE r.usage_date >= (now() - interval '1 year')::date AND r.usage_date < now()::date + 1
          AND r.building<>'平台计费'
        UNION ALL
        SELECT e.usage_date, e.usage_kwh
        FROM live_scan_daily_consumption e JOIN meters m ON m.id=e.meter_id
        WHERE e.usage_date >= (now() - interval '1 year')::date AND e.usage_date < now()::date + 1
          AND m.active AND NOT m.excluded AND m.building<>'平台计费'
    ) parts
    GROUP BY 1
)
SELECT COALESCE(o.p,n.p) AS period, o.kwh AS old_kwh, n.kwh AS new_kwh,
       CASE WHEN o.kwh IS DISTINCT FROM n.kwh THEN '*** MISMATCH ***' ELSE 'ok' END AS verdict
FROM old o FULL JOIN new n USING (p) ORDER BY 1;

\echo '=== 3) summary 全校 / 单栋 / 单层 窗口合计（近 30 天）==='
WITH old AS (
    SELECT
      (SELECT sum(d.usage_kwh) FROM effective_daily_consumption d JOIN meters m ON m.id=d.meter_id
       WHERE m.active AND NOT m.excluded AND m.building<>'平台计费'
         AND d.usage_date >= current_date - 30 AND d.usage_date < current_date + 1) AS all_kwh,
      (SELECT sum(d.usage_kwh) FROM effective_daily_consumption d JOIN meters m ON m.id=d.meter_id
       WHERE m.active AND NOT m.excluded AND m.building='13栋'
         AND d.usage_date >= current_date - 30 AND d.usage_date < current_date + 1) AS bldg_kwh
), new AS (
    SELECT
      (SELECT sum(usage_kwh) FROM (
          SELECT r.usage_kwh FROM daily_campus_rollup r
          WHERE r.building<>'平台计费' AND r.usage_date >= current_date - 30 AND r.usage_date < current_date + 1
          UNION ALL
          SELECT e.usage_kwh FROM live_scan_daily_consumption e JOIN meters m ON m.id=e.meter_id
          WHERE m.active AND NOT m.excluded AND m.building<>'平台计费'
            AND e.usage_date >= current_date - 30 AND e.usage_date < current_date + 1) x) AS all_kwh,
      (SELECT sum(usage_kwh) FROM (
          SELECT r.usage_kwh FROM daily_campus_rollup r
          WHERE r.building='13栋' AND r.usage_date >= current_date - 30 AND r.usage_date < current_date + 1
          UNION ALL
          SELECT e.usage_kwh FROM live_scan_daily_consumption e JOIN meters m ON m.id=e.meter_id
          WHERE m.active AND NOT m.excluded AND m.building='13栋'
            AND e.usage_date >= current_date - 30 AND e.usage_date < current_date + 1) x) AS bldg_kwh
)
SELECT old.all_kwh AS old_all, new.all_kwh AS new_all,
       old.bldg_kwh AS old_bldg, new.bldg_kwh AS new_bldg,
       CASE WHEN old.all_kwh IS NOT DISTINCT FROM new.all_kwh
             AND old.bldg_kwh IS NOT DISTINCT FROM new.bldg_kwh
            THEN 'ok' ELSE '*** MISMATCH ***' END AS verdict
FROM old, new;

\echo '=== 4) 空房除数没被 000024 改坏（对照 000023 口径）==='
WITH old AS (
    SELECT date_trunc('month',o.usage_date)::date AS month, o.usage_date,
           count(*) FILTER (WHERE o.is_occupied) AS rooms
    FROM daily_room_occupancy o JOIN meters m ON m.id=o.meter_id
    WHERE m.building<>'平台计费' AND m.active AND NOT m.excluded
    GROUP BY 1,2
), new AS (
    SELECT date_trunc('month',r.usage_date)::date AS month, r.usage_date,
           sum(r.occupied_rooms) AS rooms
    FROM effective_daily_campus_rollup r WHERE r.building<>'平台计费'
    GROUP BY 1,2
)
SELECT count(*) FILTER (WHERE o.rooms IS DISTINCT FROM n.rooms) AS mismatches,
       count(*) AS compared
FROM old o FULL JOIN new n USING (month, usage_date);

ROLLBACK;
