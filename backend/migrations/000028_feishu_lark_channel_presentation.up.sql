-- 排行刷新时刻与渠道展示进入前端配置。
-- 新增 Lark；飞书独立，凭证与域名不混用。

ALTER TABLE user_notification_channels
    DROP CONSTRAINT IF EXISTS user_notification_channels_channel_check;

ALTER TABLE user_notification_channels
    ADD CONSTRAINT user_notification_channels_channel_check
    CHECK (channel IN ('mail', 'sms', 'dingtalk', 'wecom', 'feishu', 'lark', 'pushplus', 'mp', 'telegram'));

UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            jsonb_set(
                config,
                '{display,ranking_refresh_time}',
                '"05:00"'::jsonb,
                true
            ),
            '{features,channels}',
            '{"mail":true,"sms":true,"dingtalk":true,"wecom":true,"feishu":true,"lark":true,"pushplus":true,"mp":true,"telegram":true}'::jsonb,
            true
        ),
        '{features,channel_coming_soon}',
        '{"mail":false,"sms":true,"dingtalk":false,"wecom":true,"feishu":false,"lark":false,"pushplus":false,"mp":true,"telegram":false}'::jsonb,
        true
    ),
    updated_at = now()
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000028'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
