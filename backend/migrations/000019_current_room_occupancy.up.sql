-- 管理员可调空房阈值；首次升级写入默认 0.3 kWh。
UPDATE frontend_config
SET config = jsonb_set(config, '{display,empty_room_threshold_kwh}', '"0.3"'::jsonb, true),
    version = version + 1,
    updated_at = now(),
    updated_by = 'migration_000019'
WHERE singleton
  AND config #> '{display,empty_room_threshold_kwh}' IS NULL;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version, config, 'migration_000019'
FROM frontend_config
WHERE singleton;

-- 昨日官方电量低于阈值视为空房；无明细为 unknown。
-- 仅建业务视图，不改 meters.excluded。
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
