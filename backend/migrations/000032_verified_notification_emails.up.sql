-- 推送收件邮箱必须验证归属。
CREATE TABLE user_verified_notification_emails (
    user_id uuid NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    email text NOT NULL,
    verified_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, email),
    CHECK (email = lower(email)),
    CHECK (length(email) BETWEEN 3 AND 254)
);

COMMENT ON TABLE user_verified_notification_emails IS
    'Additional notification recipients whose mailbox ownership was verified by this user.';

-- 验证码沿用一次性、短时效与失败次数限制。
ALTER TABLE auth_email_codes DROP CONSTRAINT IF EXISTS auth_email_codes_purpose_check;
ALTER TABLE auth_email_codes
    ADD CONSTRAINT auth_email_codes_purpose_check
    CHECK (purpose IN ('register', 'password_reset', 'email_change', 'notification_recipient'));

-- 发布时仅保留已验证登录邮箱，停止向历史第三方 to[] 投递。
UPDATE user_notification_channels c
SET config = jsonb_set(c.config, '{to}', to_jsonb(ARRAY[u.email]), true),
    updated_at = now()
FROM user_accounts u
WHERE c.user_id = u.id AND c.channel = 'mail';
