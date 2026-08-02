-- 用户验证/找回邮件的跨进程配额。
-- 固定窗口；维护任务删除过期行。
CREATE TABLE mail_send_quota_windows (
    scope text NOT NULL,
    window_start timestamptz NOT NULL,
    sent_count integer NOT NULL CHECK (sent_count >= 0),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, window_start)
);

CREATE INDEX mail_send_quota_windows_expiry_idx
    ON mail_send_quota_windows (expires_at);
