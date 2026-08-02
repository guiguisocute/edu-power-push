-- 接通企微智能机器人、公众号测试号与 QQ 机器人。
-- 企微改为 Bot ID + Secret 长连接。

ALTER TABLE user_notification_channels
    DROP CONSTRAINT IF EXISTS user_notification_channels_channel_check;

ALTER TABLE user_notification_channels
    ADD CONSTRAINT user_notification_channels_channel_check
    CHECK (channel IN ('mail', 'sms', 'dingtalk', 'wecom', 'feishu', 'lark', 'pushplus', 'mp', 'qq', 'telegram'));

UPDATE user_notification_channels
SET enabled = false,
    config = config - 'webhook' - 'mention',
    secret_config = NULL,
    secret_key_id = NULL,
    updated_at = now()
WHERE channel = 'wecom';

UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    jsonb_set(
                        jsonb_set(config, '{features,channels,wecom}', 'true'::jsonb, true),
                        '{features,channels,mp}', 'true'::jsonb, true
                    ),
                    '{features,channels,qq}', 'true'::jsonb, true
                ),
                '{features,channel_coming_soon,wecom}', 'false'::jsonb, true
            ),
            '{features,channel_coming_soon,mp}', 'false'::jsonb, true
        ),
        '{features,channel_coming_soon,qq}', 'false'::jsonb, true
    ),
    updated_at = now()
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000029'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
