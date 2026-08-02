-- 一块表最多绑 4 个账号；用槽位唯一索引防并发超限。
-- 一个账号仍只能绑一块表。

DROP INDEX IF EXISTS user_meter_bindings_one_account_per_meter_idx;

ALTER TABLE user_meter_bindings ADD COLUMN slot smallint;

-- 历史生效绑定全部落入 1 号槽。
UPDATE user_meter_bindings SET slot = 1 WHERE unbound_at IS NULL;

-- 已解绑历史行 slot 为 NULL，不占槽。
ALTER TABLE user_meter_bindings
    ADD CONSTRAINT user_meter_bindings_active_slot_check
    CHECK (unbound_at IS NOT NULL OR (slot IS NOT NULL AND slot BETWEEN 1 AND 4));

CREATE UNIQUE INDEX user_meter_bindings_meter_slot_idx
    ON user_meter_bindings (meter_id, slot)
    WHERE unbound_at IS NULL;
