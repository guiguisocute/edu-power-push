-- 排行榜参与与脱敏，必须服务端生效。
CREATE TABLE user_leaderboard_preferences (
    user_id uuid PRIMARY KEY REFERENCES user_accounts(id) ON DELETE CASCADE,
    opted_in boolean NOT NULL DEFAULT true,
    -- 榜单身份字段开关
    show_building boolean NOT NULL DEFAULT true,
    show_floor boolean NOT NULL DEFAULT true,
    show_room boolean NOT NULL DEFAULT false,
    show_nickname boolean NOT NULL DEFAULT false,
    -- 已展示字段是否打码；房间与昵称仅展示或隐藏
    mask_building boolean NOT NULL DEFAULT false,
    mask_floor boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 无绑定账号电表无行，走代码默认：参与、显示栋层、隐藏房间。
COMMENT ON TABLE user_leaderboard_preferences IS
    'Per-user leaderboard participation and identity masking; absent row means the code defaults apply.';
