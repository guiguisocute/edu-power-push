-- 房间号可打码；无账号电表默认栋层房均打码。
-- purpose 扩展 email_change。

ALTER TABLE user_leaderboard_preferences
    ADD COLUMN IF NOT EXISTS mask_room boolean NOT NULL DEFAULT true;

-- 新行默认展示栋层房并打码；昵称默认隐藏。
ALTER TABLE user_leaderboard_preferences
    ALTER COLUMN show_room SET DEFAULT true,
    ALTER COLUMN mask_building SET DEFAULT true,
    ALTER COLUMN mask_floor SET DEFAULT true,
    ALTER COLUMN mask_room SET DEFAULT true;

COMMENT ON COLUMN user_leaderboard_preferences.mask_room IS
    'When show_room is true, mask room digits on public rankings.';

-- purpose 增加 email_change。
ALTER TABLE auth_email_codes DROP CONSTRAINT IF EXISTS auth_email_codes_purpose_check;
ALTER TABLE auth_email_codes
    ADD CONSTRAINT auth_email_codes_purpose_check
    CHECK (purpose IN ('register', 'password_reset', 'email_change'));
