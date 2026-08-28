-- 恢复 000033 的两级实时估算视图。
CREATE OR REPLACE VIEW live_scan_daily_campus_rollup AS
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
