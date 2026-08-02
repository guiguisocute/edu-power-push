-- 榜单默认：完整栋号与楼层，房间仍打码。
-- 覆盖 000010 将 mask_building/mask_floor 默认改为 true。

ALTER TABLE user_leaderboard_preferences
    ALTER COLUMN mask_building SET DEFAULT false,
    ALTER COLUMN mask_floor SET DEFAULT false,
    ALTER COLUMN mask_room SET DEFAULT true;

-- 仍三字段全打码的旧默认行切到新默认。
-- 用户曾单独改展示或打码的不批量改动。
UPDATE user_leaderboard_preferences
SET mask_building = false,
    mask_floor = false,
    updated_at = now()
WHERE mask_building = true
  AND mask_floor = true
  AND mask_room = true;
