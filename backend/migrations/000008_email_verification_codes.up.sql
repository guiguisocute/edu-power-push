-- 邮箱验证码表，注册与找回密码共用。
-- 只存哈希；一次性消费、失败上限与发送间隔限制爆破。
CREATE TABLE auth_email_codes (
    email text NOT NULL,
    purpose text NOT NULL CHECK (purpose IN ('register', 'password_reset')),
    code_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (email, purpose),
    CHECK (email = lower(email)),
    CHECK (length(email) BETWEEN 3 AND 254)
);

CREATE INDEX auth_email_codes_expiry_idx ON auth_email_codes (expires_at);
