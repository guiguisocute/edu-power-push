-- 新增企微消息 Webhook 与 Discord Incoming Webhook。

ALTER TABLE user_notification_channels
    DROP CONSTRAINT IF EXISTS user_notification_channels_channel_check;

ALTER TABLE user_notification_channels
    ADD CONSTRAINT user_notification_channels_channel_check
    CHECK (channel IN ('mail', 'sms', 'dingtalk', 'wecom', 'wecom_webhook', 'feishu', 'lark', 'discord', 'pushplus', 'mp', 'qq', 'telegram'));

UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(config, '{features,channels,wecom_webhook}', 'true'::jsonb, true),
                '{features,channels,discord}', 'true'::jsonb, true
            ),
            '{features,channel_coming_soon,wecom_webhook}', 'false'::jsonb, true
        ),
        '{features,channel_coming_soon,discord}', 'false'::jsonb, true
    ),
    updated_at = now()
WHERE singleton;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["wecom_webhook","discord"]'::jsonb,
        true
    )
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000031'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
