ALTER TABLE user_notification_settings
    DROP CONSTRAINT IF EXISTS user_notification_settings_threshold_yuan_check;

ALTER TABLE user_notification_settings
    ALTER COLUMN threshold_yuan SET DEFAULT 20,
    ADD CONSTRAINT user_notification_settings_threshold_yuan_check
        CHECK (threshold_yuan >= 0 AND threshold_yuan <= 1000);
