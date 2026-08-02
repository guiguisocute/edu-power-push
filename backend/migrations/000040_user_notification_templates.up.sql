-- 用户非邮件渠道文本模板；邮件仍用内嵌 HTML 模板。

ALTER TABLE user_notification_settings
    ADD COLUMN IF NOT EXISTS templates jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(templates) = 'object');

COMMENT ON COLUMN user_notification_settings.templates IS
    'Per-user low-balance and digest title/body templates for non-email channels.';
