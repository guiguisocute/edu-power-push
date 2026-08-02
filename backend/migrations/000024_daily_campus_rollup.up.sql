-- 物化慢变官方用量半边；快变扫描估算现算。
-- daily_campus_rollup 同时供给 occupied_rooms 与 usage_kwh。
-- 估算视图经 NOT EXISTS 与官方覆盖月份互斥。
CREATE VIEW live_scan_daily_consumption AS
SELECT d.meter_id,
       d.to_time::date AS usage_date,
       sum(d.delta_kwh)::numeric(20,4) AS usage_kwh
FROM consumption_deltas d
WHERE d.status = ANY (ARRAY['valid'::text, 'unchanged'::text])
  AND NOT EXISTS (
      SELECT 1 FROM daily_detail_months c
      WHERE c.meter_id = d.meter_id
        AND c.month = date_trunc('month'::text, d.to_time)::date
        AND c.covered_through >= d.to_time::date
  )
GROUP BY d.meter_id, d.to_time::date;

DROP MATERIALIZED VIEW IF EXISTS daily_occupancy_rollup;

-- public. 限定同 000023：物化视图收紧 search_path 时展开函数体。
CREATE MATERIALIZED VIEW daily_campus_rollup AS
SELECT u.usage_date,
       m.building,
       m.floor,
       count(*) FILTER (WHERE u.usage_kwh >= public.empty_room_threshold_kwh())::integer AS occupied_rooms,
       count(*)::integer AS total_rooms,
       sum(u.usage_kwh)::numeric(24,4) AS usage_kwh
FROM public.daily_usages u
JOIN public.meters m ON m.id = u.meter_id
WHERE m.active AND NOT m.excluded
GROUP BY 1, 2, 3;

CREATE UNIQUE INDEX daily_campus_rollup_key
    ON daily_campus_rollup (usage_date, building, floor);

CREATE INDEX daily_campus_rollup_date_idx
    ON daily_campus_rollup (usage_date);
