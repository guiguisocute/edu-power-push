-- 未发布日仅有空占位；成功拉月页不代表昨日已结算。
-- 仅 has_upstream_data=true 可替换扫描估算。

CREATE INDEX IF NOT EXISTS daily_usages_published_meter_date_idx
    ON daily_usages (meter_id, usage_date)
    WHERE has_upstream_data;

-- 修复旧采集器乐观水位：保留至最近已发布日。
-- 无发布日则无 coverage 行；日级 has_upstream_data 为规范。
WITH published AS (
    SELECT meter_id,
           date_trunc('month', usage_date)::date AS month,
           max(usage_date) AS published_through
    FROM daily_usages
    WHERE has_upstream_data
    GROUP BY 1, 2
)
UPDATE daily_detail_months c
SET covered_through = p.published_through,
    updated_at = now()
FROM published p
WHERE p.meter_id = c.meter_id
  AND p.month = c.month
  AND c.covered_through IS DISTINCT FROM p.published_through;

DELETE FROM daily_detail_months c
WHERE NOT EXISTS (
    SELECT 1
    FROM daily_usages u
    WHERE u.meter_id = c.meter_id
      AND date_trunc('month', u.usage_date)::date = c.month
      AND u.has_upstream_data
);

CREATE OR REPLACE VIEW effective_daily_consumption AS
SELECT u.meter_id,
       u.usage_date,
       u.usage_kwh,
       u.cost_yuan,
       'official_detail'::text AS source
FROM daily_usages u
WHERE u.has_upstream_data
UNION ALL
SELECT d.meter_id,
       d.to_time::date AS usage_date,
       sum(d.delta_kwh)::numeric(20,4) AS usage_kwh,
       NULL::numeric(18,4) AS cost_yuan,
       'live_scan_estimate'::text AS source
FROM consumption_deltas d
WHERE d.status IN ('valid','unchanged')
  AND NOT EXISTS (
      SELECT 1
      FROM daily_usages u
      WHERE u.meter_id = d.meter_id
        AND u.usage_date = d.to_time::date
        AND u.has_upstream_data
  )
GROUP BY d.meter_id, d.to_time::date;

CREATE OR REPLACE VIEW live_scan_daily_consumption AS
SELECT d.meter_id,
       d.to_time::date AS usage_date,
       sum(d.delta_kwh)::numeric(20,4) AS usage_kwh
FROM consumption_deltas d
WHERE d.status IN ('valid','unchanged')
  AND NOT EXISTS (
      SELECT 1
      FROM daily_usages u
      WHERE u.meter_id = d.meter_id
        AND u.usage_date = d.to_time::date
        AND u.has_upstream_data
  )
GROUP BY d.meter_id, d.to_time::date;

-- 节约榜单按表视图须与 series/summary 同一有效值口径。
CREATE OR REPLACE VIEW daily_room_occupancy AS
SELECT d.meter_id,
       d.usage_date,
       d.usage_kwh AS ydl_kwh,
       empty_room_threshold_kwh() AS threshold_kwh,
       d.usage_kwh <  empty_room_threshold_kwh() AS is_empty,
       d.usage_kwh >= empty_room_threshold_kwh() AS is_occupied
FROM effective_daily_consumption d;

-- 重建慢变官方半边，排除未发布占位行。
DROP VIEW IF EXISTS effective_daily_campus_rollup;
DROP VIEW IF EXISTS live_scan_daily_campus_rollup;
DROP MATERIALIZED VIEW IF EXISTS daily_campus_rollup;

CREATE MATERIALIZED VIEW daily_campus_rollup AS
SELECT u.usage_date,
       m.building,
       m.floor,
       count(*) FILTER (WHERE u.usage_kwh >= public.empty_room_threshold_kwh())::integer AS occupied_rooms,
       count(*)::integer AS total_rooms,
       sum(u.usage_kwh)::numeric(24,4) AS usage_kwh
FROM public.daily_usages u
JOIN public.meters m ON m.id = u.meter_id
WHERE u.has_upstream_data
  AND m.active
  AND NOT m.excluded
GROUP BY 1, 2, 3;

CREATE UNIQUE INDEX daily_campus_rollup_key
    ON daily_campus_rollup (usage_date, building, floor);

CREATE INDEX daily_campus_rollup_date_idx
    ON daily_campus_rollup (usage_date);

-- 快变估算半边在查询时聚合。
CREATE VIEW live_scan_daily_campus_rollup AS
SELECT e.usage_date,
       m.building,
       m.floor,
       count(*) FILTER (WHERE e.usage_kwh >= public.empty_room_threshold_kwh())::integer AS occupied_rooms,
       count(*)::integer AS total_rooms,
       sum(e.usage_kwh)::numeric(24,4) AS usage_kwh
FROM live_scan_daily_consumption e
JOIN meters m ON m.id = e.meter_id
WHERE m.active
  AND NOT m.excluded
GROUP BY 1, 2, 3;

-- 可变月份现算；更早历史用物化视图。
-- 避免中途切换口径留下空洞，且不扫全表历史。
CREATE VIEW effective_daily_campus_rollup AS
WITH mutable_window AS (
    SELECT CASE WHEN extract(day FROM current_date) <= 3
                THEN (date_trunc('month', current_date) - interval '1 month')::date
                ELSE date_trunc('month', current_date)::date
           END AS from_date
), official AS (
    SELECT r.usage_date, r.building, r.floor,
           r.occupied_rooms, r.total_rooms, r.usage_kwh
    FROM daily_campus_rollup r
    CROSS JOIN mutable_window w
    WHERE r.usage_date < w.from_date
    UNION ALL
    SELECT u.usage_date,
           m.building,
           m.floor,
           count(*) FILTER (WHERE u.usage_kwh >= public.empty_room_threshold_kwh())::integer,
           count(*)::integer,
           sum(u.usage_kwh)::numeric(24,4)
    FROM daily_usages u
    JOIN meters m ON m.id = u.meter_id
    CROSS JOIN mutable_window w
    WHERE u.has_upstream_data
      AND u.usage_date >= w.from_date
      AND m.active
      AND NOT m.excluded
    GROUP BY 1, 2, 3
), parts AS (
    SELECT usage_date, building, floor, occupied_rooms, total_rooms, usage_kwh
    FROM official
    UNION ALL
    SELECT usage_date, building, floor, occupied_rooms, total_rooms, usage_kwh
    FROM live_scan_daily_campus_rollup
)
SELECT usage_date,
       building,
       floor,
       sum(occupied_rooms)::integer AS occupied_rooms,
       sum(total_rooms)::integer AS total_rooms,
       sum(usage_kwh)::numeric(24,4) AS usage_kwh
FROM parts
GROUP BY 1, 2, 3;
