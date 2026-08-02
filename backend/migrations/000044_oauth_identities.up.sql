-- 第三方登录：身份主键为 subject，禁止用可改邮箱。
-- (provider, subject) 与 (user_id, provider) 均唯一。

CREATE TABLE user_oauth_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('google', 'github')),
    subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    -- 登录时回传邮箱仅作审计；登录邮箱以 user_accounts 为准
    email text CHECK (email IS NULL OR length(email) <= 254),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

CREATE UNIQUE INDEX user_oauth_identities_provider_subject_idx
    ON user_oauth_identities (provider, subject);
CREATE UNIQUE INDEX user_oauth_identities_user_provider_idx
    ON user_oauth_identities (user_id, provider);

-- 第三方注册无密码：密码列可空；长度 CHECK 对 NULL 仍通过。
ALTER TABLE user_accounts ALTER COLUMN password_hash DROP NOT NULL;

COMMENT ON COLUMN user_accounts.password_hash IS
    'NULL means the account has no password yet (created through OAuth); it can set one through password recovery.';

-- 登录页 OAuth 入口默认开；未配凭证时按钮本就不显示。
UPDATE frontend_config
SET config = jsonb_set(
        jsonb_set(config, '{features,auth,google_oauth}', 'true'::jsonb, true),
        '{features,auth,github_oauth}', 'true'::jsonb, true
    ),
    updated_at = now()
WHERE singleton
  AND config #> '{features,auth,google_oauth}' IS NULL;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000044'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;
