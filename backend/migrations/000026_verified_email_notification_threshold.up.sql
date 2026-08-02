-- 回填注册时漏写的邮箱验证时间。
UPDATE user_accounts
SET email_verified_at = created_at,
    updated_at = now()
WHERE email_verified_at IS NULL;

-- 低额阈值范围 1–50 元，默认 10；越界旧值夹到边界。
ALTER TABLE user_notification_settings
    DROP CONSTRAINT IF EXISTS user_notification_settings_threshold_yuan_check;

UPDATE user_notification_settings
SET threshold_yuan = LEAST(50, GREATEST(1, threshold_yuan)),
    updated_at = now()
WHERE threshold_yuan < 1 OR threshold_yuan > 50;

ALTER TABLE user_notification_settings
    ALTER COLUMN threshold_yuan SET DEFAULT 10,
    ADD CONSTRAINT user_notification_settings_threshold_yuan_check
        CHECK (threshold_yuan >= 1 AND threshold_yuan <= 50);
