-- 面板可改且即时生效的系统设置。
-- 非敏感进 value；凭证 AES-GCM 进 secrets；缺省回落环境变量。
-- 密钥仅来自 SETTINGS_ENCRYPTION_KEY，禁止入库。

CREATE TABLE system_settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(value) = 'object'),
    -- AES-256-GCM 密文，明文为 JSON 对象
    secrets bytea,
    -- 密钥指纹，用于识别换钥后无法解密
    secrets_key_id text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text NOT NULL DEFAULT ''
);

COMMENT ON TABLE system_settings IS
    'Runtime configuration editable from the admin panel. Secrets are AES-GCM encrypted.';
COMMENT ON COLUMN system_settings.secrets IS
    'AES-256-GCM sealed JSON object of credential fields. Key comes from SETTINGS_ENCRYPTION_KEY.';
COMMENT ON COLUMN system_settings.version IS
    'Bumped on every save; the mail provider cache watches it to pick up changes without a restart.';
