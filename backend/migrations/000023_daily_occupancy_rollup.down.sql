DROP MATERIALIZED VIEW IF EXISTS daily_occupancy_rollup;

-- 回到 000020 视图形状并删除阈值函数。
CREATE OR REPLACE VIEW daily_room_occupancy AS
WITH policy AS (
    SELECT COALESCE(
               NULLIF(config #>> '{display,empty_room_threshold_kwh}', '')::numeric,
               0.3
           ) AS threshold_kwh
    FROM frontend_config
    WHERE singleton
)
SELECT u.meter_id,
       u.usage_date,
       u.usage_kwh AS ydl_kwh,
       p.threshold_kwh,
       u.usage_kwh < p.threshold_kwh AS is_empty,
       u.usage_kwh >= p.threshold_kwh AS is_occupied
FROM daily_usages u
CROSS JOIN policy p;

DROP FUNCTION IF EXISTS empty_room_threshold_kwh();
