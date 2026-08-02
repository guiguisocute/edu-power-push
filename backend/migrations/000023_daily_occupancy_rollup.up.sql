-- 物化空房按 (usage_date, building, floor) 聚合，降低看板全表扫描。
-- 阈值沉为函数并内联 InitPlan；frontend_config 必须写 public.。
-- 禁止 SET search_path，否则函数无法内联。
CREATE OR REPLACE FUNCTION empty_room_threshold_kwh() RETURNS numeric
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT COALESCE(
               NULLIF(config #>> '{display,empty_room_threshold_kwh}', '')::numeric,
               0.3
           )
    FROM public.frontend_config
    WHERE singleton
$$;

CREATE OR REPLACE VIEW daily_room_occupancy AS
SELECT u.meter_id,
       u.usage_date,
       u.usage_kwh AS ydl_kwh,
       empty_room_threshold_kwh() AS threshold_kwh,
       u.usage_kwh <  empty_room_threshold_kwh() AS is_empty,
       u.usage_kwh >= empty_room_threshold_kwh() AS is_occupied
FROM daily_usages u;

-- active / excluded 物化时固化，随盘点重刷。
-- 平台计费楼栋由查询侧按 building 过滤。
CREATE MATERIALIZED VIEW daily_occupancy_rollup AS
SELECT u.usage_date,
       m.building,
       m.floor,
       count(*) FILTER (WHERE u.usage_kwh >= empty_room_threshold_kwh())::integer AS occupied_rooms,
       count(*)::integer AS total_rooms
FROM daily_usages u
JOIN meters m ON m.id = u.meter_id
WHERE m.active AND NOT m.excluded
GROUP BY 1, 2, 3;

-- 唯一索引供 REFRESH CONCURRENTLY；GROUP BY 1,2,3 保证唯一。
CREATE UNIQUE INDEX daily_occupancy_rollup_key
    ON daily_occupancy_rollup (usage_date, building, floor);

CREATE INDEX daily_occupancy_rollup_date_idx
    ON daily_occupancy_rollup (usage_date);
