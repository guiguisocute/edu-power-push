CREATE TABLE user_accounts (
    id uuid PRIMARY KEY,
    email text NOT NULL,
    password_hash text NOT NULL,
    nickname text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    email_verified_at timestamptz,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (email = lower(email)),
    CHECK (length(email) BETWEEN 3 AND 254),
    CHECK (length(password_hash) BETWEEN 32 AND 512),
    CHECK (length(nickname) <= 80)
);

CREATE UNIQUE INDEX user_accounts_email_unique_idx ON user_accounts (email);

CREATE TABLE user_meter_bindings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    bound_at timestamptz NOT NULL DEFAULT now(),
    unbound_at timestamptz,
    CHECK (unbound_at IS NULL OR unbound_at >= bound_at)
);

CREATE UNIQUE INDEX user_meter_bindings_one_active_idx
    ON user_meter_bindings (user_id)
    WHERE unbound_at IS NULL;
CREATE INDEX user_meter_bindings_meter_idx
    ON user_meter_bindings (meter_id)
    WHERE unbound_at IS NULL;

CREATE TABLE auth_refresh_sessions (
    id uuid PRIMARY KEY,
    family_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    jti uuid NOT NULL UNIQUE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at timestamptz,
    revoked_reason text,
    replaced_by_id uuid REFERENCES auth_refresh_sessions(id),
    user_agent_hash bytea CHECK (user_agent_hash IS NULL OR octet_length(user_agent_hash) = 32),
    CHECK (expires_at > created_at),
    CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL))
);

CREATE INDEX auth_refresh_sessions_user_idx
    ON auth_refresh_sessions (user_id, created_at DESC);
CREATE INDEX auth_refresh_sessions_family_idx
    ON auth_refresh_sessions (family_id);
CREATE INDEX auth_refresh_sessions_expiry_idx
    ON auth_refresh_sessions (expires_at)
    WHERE revoked_at IS NULL;
