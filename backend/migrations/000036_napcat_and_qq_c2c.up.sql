-- QQ 官方机器人改用 C2C user_openid；新增 NapCat。

ALTER TABLE user_notification_channels
    DROP CONSTRAINT IF EXISTS user_notification_channels_channel_check;

ALTER TABLE user_notification_channels
    ADD CONSTRAINT user_notification_channels_channel_check
    CHECK (channel IN ('mail', 'sms', 'dingtalk', 'wecom', 'wecom_webhook', 'feishu', 'lark', 'discord', 'pushplus', 'mp', 'qq', 'napcat', 'telegram'));

-- 旧群 OpenID 不可用于 C2C：停用并清目标，保留 AppID 与密钥。
UPDATE user_notification_channels
SET enabled = false,
    config = config - 'group_openid',
    updated_at = now()
WHERE channel = 'qq' AND config ? 'group_openid';

UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            config,
            '{features,channels,napcat}',
            'true'::jsonb,
            true
        ),
        '{features,channel_coming_soon,napcat}',
        'false'::jsonb,
        true
    ),
    updated_at = now()
WHERE singleton;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["napcat"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["napcat"]'::jsonb;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000036'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
