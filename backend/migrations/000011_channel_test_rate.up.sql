-- 测试发送限流独立为 last_test_at，不再复用 last_at。

ALTER TABLE user_notification_channels
    ADD COLUMN IF NOT EXISTS last_test_at timestamptz;

COMMENT ON COLUMN user_notification_channels.last_at IS
    'Last delivery attempt on this channel, from either the push engine or a manual test.';
COMMENT ON COLUMN user_notification_channels.last_test_at IS
    'Last manual test send; drives the test rate limit only.';
