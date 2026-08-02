-- 图表开关：日视图与分时用电。默认关闭。
UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            config,
            '{features,charts}',
            COALESCE(config #> '{features,charts}', '{}'::jsonb),
            true
        ),
        '{features,charts}',
        COALESCE(config #> '{features,charts}', '{}'::jsonb)
            || '{"day_range": false, "hourly_usage": false}'::jsonb,
        true
    ),
    updated_at = now(),
    updated_by = 'migration_000015'
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version, config, 'migration_000015'
FROM frontend_config
WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
