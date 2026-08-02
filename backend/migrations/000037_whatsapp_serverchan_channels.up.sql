-- 新增 WhatsApp CallMeBot 与 Server酱双版本渠道。

ALTER TABLE user_notification_channels
    DROP CONSTRAINT IF EXISTS user_notification_channels_channel_check;

ALTER TABLE user_notification_channels
    ADD CONSTRAINT user_notification_channels_channel_check
    CHECK (channel IN (
        'mail', 'sms', 'dingtalk', 'wecom', 'wecom_webhook', 'feishu', 'lark',
        'discord', 'pushplus', 'mp', 'qq', 'napcat', 'telegram',
        'whatsapp', 'serverchan_turbo', 'serverchan3'
    ));

UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(
            config,
            '{features,channels}',
            COALESCE(config #> '{features,channels}', '{}'::jsonb) ||
                '{"whatsapp":true,"serverchan_turbo":true,"serverchan3":true}'::jsonb,
            true
        ),
        '{features,channel_coming_soon}',
        COALESCE(config #> '{features,channel_coming_soon}', '{}'::jsonb) ||
            '{"whatsapp":false,"serverchan_turbo":false,"serverchan3":false}'::jsonb,
        true
    ),
    updated_at = now()
WHERE singleton;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["whatsapp"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["whatsapp"]'::jsonb;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) ||
            '["serverchan_turbo","serverchan3"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["serverchan_turbo"]'::jsonb
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["serverchan3"]'::jsonb;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["serverchan_turbo"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["serverchan_turbo"]'::jsonb;

UPDATE frontend_config
SET config = jsonb_set(
        config,
        '{features,channel_order}',
        COALESCE(config #> '{features,channel_order}', '[]'::jsonb) || '["serverchan3"]'::jsonb,
        true
    )
WHERE singleton
  AND NOT COALESCE(config #> '{features,channel_order}', '[]'::jsonb) @> '["serverchan3"]'::jsonb;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000037'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
