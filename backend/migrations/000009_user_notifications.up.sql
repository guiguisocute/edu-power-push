-- 用户推送偏好与渠道。
-- alert_latched：跌破阈值推一次，回升超阈值+缓冲后重置。

CREATE TABLE user_notification_settings (
    user_id uuid PRIMARY KEY REFERENCES user_accounts(id) ON DELETE CASCADE,
    low_balance_alert boolean NOT NULL DEFAULT true,
    threshold_yuan numeric(12,2) NOT NULL DEFAULT 20
        CHECK (threshold_yuan >= 0 AND threshold_yuan <= 1000),
    scheduled_digest boolean NOT NULL DEFAULT true,
    period text NOT NULL DEFAULT 'daily'
        CHECK (period IN ('daily', 'twice', 'every3', 'weekly')),
    push_time text NOT NULL DEFAULT '08:00'
        CHECK (push_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    timezone text NOT NULL DEFAULT 'Asia/Shanghai',
    -- 引擎状态，不暴露给前端 API
    alert_latched boolean NOT NULL DEFAULT false,
    last_digest_at timestamptz,
    last_alert_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_notification_channels (
    user_id uuid NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    channel text NOT NULL
        CHECK (channel IN ('mail', 'sms', 'dingtalk', 'wecom', 'feishu', 'pushplus', 'mp', 'telegram')),
    enabled boolean NOT NULL DEFAULT false,
    -- 凭证仅服务端持有；API 读回掩码。
    config jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(config) = 'object'),
    last_status text CHECK (last_status IS NULL OR last_status IN ('ok', 'failed')),
    last_message text,
    last_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel)
);

CREATE TABLE user_push_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    channel text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('low_balance', 'digest', 'test', 'system')),
    status text NOT NULL CHECK (status IN ('delivered', 'failed', 'skipped')),
    summary text NOT NULL,
    error text,
    sent_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_push_logs_user_sent_idx ON user_push_logs (user_id, sent_at DESC);
CREATE INDEX user_push_logs_sent_idx ON user_push_logs (sent_at);

-- 放开找回密码前端入口开关。
UPDATE frontend_config
SET config = jsonb_set(config, '{features,auth,email_code}', 'true'::jsonb, true),
    updated_at = now()
WHERE singleton;

INSERT INTO frontend_config_audit (version, config, changed_by)
SELECT version + 1, config, 'migration-000009'
FROM frontend_config WHERE singleton;

UPDATE frontend_config SET version = version + 1 WHERE singleton;

COMMENT ON TABLE user_notification_settings IS
    'Per-user push rules (low-balance alert + scheduled digest) and engine latch state.';
COMMENT ON TABLE user_notification_channels IS
    'Per-user notification channel credentials; secrets never leave the server unmasked.';
COMMENT ON TABLE user_push_logs IS
    'User-visible push history (recent deliveries on overview).';
