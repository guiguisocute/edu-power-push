-- 用户端推送渠道顺序进入前端配置。

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        '["mail","dingtalk","lark","wecom","feishu","pushplus","mp","qq","telegram","sms"]'::jsonb,
        true
    ),
    updated_at = now()
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000030'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
