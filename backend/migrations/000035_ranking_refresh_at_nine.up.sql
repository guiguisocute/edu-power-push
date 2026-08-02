-- 日榜默认由 08:00 改为 09:00，仅动上条迁移写入的默认值。
WITH changed AS (
    UPDATE frontend_config
    SET config = jsonb_set(config, '{display,ranking_refresh_time}', '"09:00"'::jsonb, true),
        version = version + 1,
        updated_at = now(),
        updated_by = 'migration_000035'
    WHERE singleton
      AND config #>> '{display,ranking_refresh_time}' = '08:00'
      AND updated_by = 'migration_000034'
    RETURNING version, config
)
INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version, config, 'migration_000035'
FROM changed;
