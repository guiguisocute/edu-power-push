DROP VIEW IF EXISTS daily_room_occupancy;

CREATE VIEW current_room_occupancy AS
WITH policy AS (
    SELECT COALESCE(
               NULLIF(config #>> '{display,empty_room_threshold_kwh}', '')::numeric,
               0.3
           ) AS threshold_kwh
    FROM frontend_config
    WHERE singleton
)
SELECT m.id AS meter_id,
       ((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 1) AS usage_date,
       u.usage_kwh AS yesterday_kwh,
       p.threshold_kwh,
       u.meter_id IS NOT NULL AND u.usage_kwh < p.threshold_kwh AS is_empty,
       u.meter_id IS NOT NULL AND u.usage_kwh >= p.threshold_kwh AS is_occupied
FROM meters m
CROSS JOIN policy p
LEFT JOIN daily_usages u
  ON u.meter_id=m.id
 AND u.usage_date=((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 1);
