-- 校验 migrations/000023 改写。
-- 旧口径现算 daily_room_occupancy。
-- 新口径读取 daily_occupancy_rollup。
-- 旧口径与新口径必须逐行相等。
-- 用法：psql -v ON_ERROR_STOP=1 -f verify_occupancy_rollup.sql
-- 整段包在事务内，最后 ROLLBACK。
-- 仅校验，禁止落库。
BEGIN;

\i /tmp/000023_daily_occupancy_rollup.up.sql

\echo '=== 1) campus/bills 月度日均非空房数 ==='
WITH old AS (
    SELECT month, round(avg(rooms))::integer AS rooms FROM (
        SELECT date_trunc('month',o.usage_date)::date AS month, o.usage_date,
               count(*) FILTER (WHERE o.is_occupied) AS rooms
        FROM daily_room_occupancy o JOIN meters m ON m.id=o.meter_id
        WHERE m.building<>'平台计费' AND m.active AND NOT m.excluded
        GROUP BY 1,2
    ) d GROUP BY month
), new AS (
    SELECT month, round(avg(rooms))::integer AS rooms FROM (
        SELECT date_trunc('month',r.usage_date)::date AS month, r.usage_date,
               sum(r.occupied_rooms) AS rooms
        FROM daily_occupancy_rollup r
        WHERE r.building<>'平台计费'
        GROUP BY 1,2
    ) d GROUP BY month
)
SELECT COALESCE(o.month,n.month) AS month, o.rooms AS old_rooms, n.rooms AS new_rooms,
       CASE WHEN o.rooms IS DISTINCT FROM n.rooms THEN '*** MISMATCH ***' ELSE 'ok' END AS verdict
FROM old o FULL JOIN new n USING (month)
WHERE o.rooms IS DISTINCT FROM n.rooms
UNION ALL
SELECT NULL, count(*)::int, count(*)::int, 'rows compared: all equal'
FROM old o JOIN new n USING (month) WHERE o.rooms = n.rooms;

\echo '=== 2) campus/breakdown 楼栋 × 月 日均非空房数 ==='
WITH old AS (
    SELECT row_key, bucket, round(avg(rooms))::integer AS rooms FROM (
        SELECT m.building AS row_key, date_trunc('month', o.usage_date::timestamptz) AS bucket,
               o.usage_date, count(*) FILTER (WHERE o.is_occupied) AS rooms
        FROM daily_room_occupancy o JOIN meters m ON m.id=o.meter_id
        WHERE m.active AND NOT m.excluded AND m.building<>'平台计费'
        GROUP BY 1,2,3
    ) d GROUP BY row_key, bucket
), new AS (
    SELECT row_key, bucket, round(avg(rooms))::integer AS rooms FROM (
        SELECT r.building AS row_key, date_trunc('month', r.usage_date::timestamptz) AS bucket,
               r.usage_date, sum(r.occupied_rooms) AS rooms
        FROM daily_occupancy_rollup r
        WHERE r.building<>'平台计费'
        GROUP BY 1,2,3
    ) d GROUP BY row_key, bucket
)
SELECT COALESCE(o.row_key,n.row_key) AS row_key, COALESCE(o.bucket,n.bucket) AS bucket,
       o.rooms AS old_rooms, n.rooms AS new_rooms, '*** MISMATCH ***' AS verdict
FROM old o FULL JOIN new n USING (row_key, bucket)
WHERE o.rooms IS DISTINCT FROM n.rooms
UNION ALL
SELECT 'rows compared: all equal', NULL, count(*)::int, count(*)::int, 'ok'
FROM old o JOIN new n USING (row_key, bucket) WHERE o.rooms = n.rooms;

\echo '=== 3) 楼层维度（选中楼栋时的 breakdown 行键） ==='
WITH old AS (
    SELECT m.floor AS row_key, o.usage_date, count(*) FILTER (WHERE o.is_occupied) AS rooms
    FROM daily_room_occupancy o JOIN meters m ON m.id=o.meter_id
    WHERE m.active AND NOT m.excluded AND m.building='13栋'
    GROUP BY 1,2
), new AS (
    SELECT r.floor AS row_key, r.usage_date, sum(r.occupied_rooms) AS rooms
    FROM daily_occupancy_rollup r WHERE r.building='13栋'
    GROUP BY 1,2
)
SELECT COALESCE(o.row_key,n.row_key) AS row_key, COALESCE(o.usage_date,n.usage_date) AS usage_date,
       o.rooms AS old_rooms, n.rooms AS new_rooms, '*** MISMATCH ***' AS verdict
FROM old o FULL JOIN new n USING (row_key, usage_date)
WHERE o.rooms IS DISTINCT FROM n.rooms
UNION ALL
SELECT 'rows compared: all equal', NULL, count(*)::int, count(*)::int, 'ok'
FROM old o JOIN new n USING (row_key, usage_date) WHERE o.rooms = n.rooms;

\echo '=== 4) campus/summary 窗口日均（近 30 天，全校 / 单栋 / 单层） ==='
WITH old AS (
    SELECT COALESCE((SELECT round(avg(rooms))::integer FROM (
        SELECT o.usage_date, count(*) FILTER (WHERE o.is_occupied) AS rooms
        FROM daily_room_occupancy o JOIN meters m ON m.id=o.meter_id
        WHERE m.active AND NOT m.excluded AND m.building<>'平台计费'
          AND o.usage_date >= current_date - 30
        GROUP BY o.usage_date) d),0) AS all_rooms,
    COALESCE((SELECT round(avg(rooms))::integer FROM (
        SELECT o.usage_date, count(*) FILTER (WHERE o.is_occupied) AS rooms
        FROM daily_room_occupancy o JOIN meters m ON m.id=o.meter_id
        WHERE m.active AND NOT m.excluded AND m.building='13栋'
          AND o.usage_date >= current_date - 30
        GROUP BY o.usage_date) d),0) AS bldg_rooms
), new AS (
    SELECT COALESCE((SELECT round(avg(rooms))::integer FROM (
        SELECT r.usage_date, sum(r.occupied_rooms) AS rooms
        FROM daily_occupancy_rollup r
        WHERE r.building<>'平台计费' AND r.usage_date >= current_date - 30
        GROUP BY r.usage_date) d),0) AS all_rooms,
    COALESCE((SELECT round(avg(rooms))::integer FROM (
        SELECT r.usage_date, sum(r.occupied_rooms) AS rooms
        FROM daily_occupancy_rollup r
        WHERE r.building='13栋' AND r.usage_date >= current_date - 30
        GROUP BY r.usage_date) d),0) AS bldg_rooms
)
SELECT old.all_rooms AS old_all, new.all_rooms AS new_all,
       old.bldg_rooms AS old_bldg, new.bldg_rooms AS new_bldg,
       CASE WHEN old.all_rooms=new.all_rooms AND old.bldg_rooms=new.bldg_rooms
            THEN 'ok' ELSE '*** MISMATCH ***' END AS verdict
FROM old, new;

ROLLBACK;
