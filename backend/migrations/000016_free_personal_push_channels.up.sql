-- Telegram Bot 与 PushPlus 个人免费推送。
-- 凭据拆入 AES-256-GCM 密文列；主密钥仅 SETTINGS_ENCRYPTION_KEY。

ALTER TABLE user_notification_channels
    ADD COLUMN IF NOT EXISTS secret_config bytea,
    ADD COLUMN IF NOT EXISTS secret_key_id text;

COMMENT ON COLUMN user_notification_channels.config IS
    'Non-secret per-user channel settings only. Credentials belong in secret_config.';
COMMENT ON COLUMN user_notification_channels.secret_config IS
    'AES-256-GCM encrypted per-user channel credentials.';
COMMENT ON COLUMN user_notification_channels.secret_key_id IS
    'Fingerprint of SETTINGS_ENCRYPTION_KEY used for secret_config.';

-- PushPlus 200 仅异步受理，禁止日志冒充已送达。
ALTER TABLE user_push_logs DROP CONSTRAINT IF EXISTS user_push_logs_status_check;
ALTER TABLE user_push_logs ADD CONSTRAINT user_push_logs_status_check
    CHECK (status IN ('accepted', 'delivered', 'failed', 'skipped'));

-- 打开 Telegram 与 PushPlus 功能开关。
UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(config, '{features,channels,pushplus}', 'true'::jsonb, true),
        '{features,channels,telegram}', 'true'::jsonb, true
    ),
    updated_at = now()
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000016'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
