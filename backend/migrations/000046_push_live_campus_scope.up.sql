-- 看板按楼栋/楼层查询时，先缩小电表集合再读取余额差值。
-- 旧视图先聚合全校 live_scan_daily_consumption，外层位置条件无法下推，
-- 导致每个冷请求扫描全校 consumption_deltas 并逐行探测官方日明细。
-- 保持“仅 has_upstream_data=true 替换扫描估算”的既有数据语义。
CREATE OR REPLACE VIEW live_scan_daily_campus_rollup AS
WITH per_meter_day AS (
    SELECT d.meter_id,
           d.to_time::date AS usage_date,
           m.building,
           m.floor,
           sum(d.delta_kwh)::numeric(20,4) AS usage_kwh
    FROM consumption_deltas d
    JOIN meters m ON m.id = d.meter_id
    WHERE d.status IN ('valid','unchanged')
      AND m.active
      AND NOT m.excluded
      AND NOT EXISTS (
          SELECT 1
          FROM daily_usages u
          WHERE u.meter_id = d.meter_id
            AND u.usage_date = d.to_time::date
            AND u.has_upstream_data
      )
    GROUP BY d.meter_id, d.to_time::date, m.building, m.floor
)
SELECT e.usage_date,
       e.building,
       e.floor,
       count(*) FILTER (WHERE e.usage_kwh >= public.empty_room_threshold_kwh())::integer AS occupied_rooms,
       count(*)::integer AS total_rooms,
       sum(e.usage_kwh)::numeric(24,4) AS usage_kwh
FROM per_meter_day e
GROUP BY 1, 2, 3;
