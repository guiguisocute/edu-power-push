-- 通用 Webhook、Bark 与 Gotify 渠道。

ALTER TABLE user_notification_channels
    DROP CONSTRAINT IF EXISTS user_notification_channels_channel_check;

ALTER TABLE user_notification_channels
    ADD CONSTRAINT user_notification_channels_channel_check
    CHECK (channel IN (
        'mail', 'sms', 'dingtalk', 'wecom', 'wecom_webhook', 'feishu', 'lark',
        'discord', 'pushplus', 'mp', 'qq', 'napcat', 'telegram',
        'whatsapp', 'serverchan_turbo', 'serverchan3', 'webhook', 'bark', 'gotify'
    ));

UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            config,
            '{features,channels}',
            COALESCE(config #> '{features,channels}', '{}'::jsonb) ||
                '{"webhook":true,"bark":true,"gotify":true}'::jsonb,
            true
        ),
        '{features,channel_coming_soon}',
        COALESCE(config #> '{features,channel_coming_soon}', '{}'::jsonb) ||
            '{"webhook":false,"bark":false,"gotify":false}'::jsonb,
        true
    ),
    updated_at = now()
WHERE singleton;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["webhook"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["webhook"]'::jsonb;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["bark"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["bark"]'::jsonb;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["gotify"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["gotify"]'::jsonb;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000038'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
