-- 日榜默认切到 08:00，给结算与采集留窗口。
-- 仅迁移旧默认；管理员自定义时刻保持。
WITH changed AS (
    UPDATE frontend_config
    SET config = jsonb_set(config, '{display,ranking_refresh_time}', '"08:00"'::jsonb, true),
        version = version + 1,
        updated_at = now(),
        updated_by = 'migration_000034'
    WHERE singleton
      AND COALESCE(NULLIF(config #>> '{display,ranking_refresh_time}', ''), '05:00') = '05:00'
    RETURNING version, config
)
INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version, config, 'migration_000034'
FROM changed;
