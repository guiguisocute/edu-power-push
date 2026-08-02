DROP MATERIALIZED VIEW IF EXISTS daily_campus_rollup;
DROP VIEW IF EXISTS live_scan_daily_consumption;

-- 回到 000023 形状。
CREATE MATERIALIZED VIEW daily_occupancy_rollup AS
SELECT u.usage_date,
       m.building,
       m.floor,
       count(*) FILTER (WHERE u.usage_kwh >= public.empty_room_threshold_kwh())::integer AS occupied_rooms,
       count(*)::integer AS total_rooms
FROM public.daily_usages u
JOIN public.meters m ON m.id = u.meter_id
WHERE m.active AND NOT m.excluded
GROUP BY 1, 2, 3;

CREATE UNIQUE INDEX daily_occupancy_rollup_key
    ON daily_occupancy_rollup (usage_date, building, floor);

CREATE INDEX daily_occupancy_rollup_date_idx
    ON daily_occupancy_rollup (usage_date);
