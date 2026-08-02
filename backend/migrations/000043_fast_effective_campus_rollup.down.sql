-- 恢复 000033 可变窗口实现。
CREATE OR REPLACE VIEW effective_daily_campus_rollup AS
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
