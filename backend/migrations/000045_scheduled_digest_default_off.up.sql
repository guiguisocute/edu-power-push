-- 新号默认关闭定时用电摘要。已有用户的 scheduled_digest 值不动。
ALTER TABLE user_notification_settings
    ALTER COLUMN scheduled_digest SET DEFAULT false;
