-- 看板改读物化 campus rollup，避免每次重聚合 daily_usages。
-- 官方明细后 CONCURRENTLY 刷新，读者见完整快照。
-- 未发布日仍走 live_scan 估算，有规范行后排除。
CREATE OR REPLACE VIEW effective_daily_campus_rollup AS
WITH parts AS (
    SELECT usage_date, building, floor, occupied_rooms, total_rooms, usage_kwh
    FROM daily_campus_rollup
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
