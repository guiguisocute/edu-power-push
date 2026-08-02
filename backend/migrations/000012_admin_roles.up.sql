-- Web 管理角色：user / operator / admin。
-- 鉴权走用户会话；ADMIN_TOKEN 保留给机器。

ALTER TABLE user_accounts
    ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'user';

ALTER TABLE user_accounts DROP CONSTRAINT IF EXISTS user_accounts_role_check;
ALTER TABLE user_accounts
    ADD CONSTRAINT user_accounts_role_check CHECK (role IN ('user', 'operator', 'admin'));

CREATE INDEX IF NOT EXISTS user_accounts_role_idx ON user_accounts (role) WHERE role <> 'user';

-- 面板写操作留痕；凭证只记字段名，禁止记值。
CREATE TABLE admin_audit_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id uuid REFERENCES user_accounts(id) ON DELETE SET NULL,
    actor_label text NOT NULL,
    action text NOT NULL,
    target text NOT NULL DEFAULT '',
    detail jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object'),
    request_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX admin_audit_log_created_idx ON admin_audit_log (created_at DESC);
CREATE INDEX admin_audit_log_actor_idx ON admin_audit_log (actor_id, created_at DESC);

COMMENT ON COLUMN user_accounts.role IS
    'Authorization tier for the web admin panel: user < operator < admin.';
COMMENT ON TABLE admin_audit_log IS
    'Every write performed through the admin panel. Never stores credential values.';
COMMENT ON COLUMN admin_audit_log.actor_label IS
    'Human-readable actor: an email for session auth, or "admin-token" for machine auth.';
