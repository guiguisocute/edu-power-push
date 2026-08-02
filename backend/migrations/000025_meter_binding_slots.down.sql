-- 回到一块表一账号；多余绑定须先解绑。
-- 每块表仅保留最早生效绑定。
UPDATE user_meter_bindings b SET unbound_at = now()
WHERE b.unbound_at IS NULL
  AND b.id <> (
      SELECT k.id FROM user_meter_bindings k
      WHERE k.meter_id = b.meter_id AND k.unbound_at IS NULL
      ORDER BY k.bound_at, k.id
      LIMIT 1
  );

DROP INDEX IF EXISTS user_meter_bindings_meter_slot_idx;
ALTER TABLE user_meter_bindings DROP CONSTRAINT IF EXISTS user_meter_bindings_active_slot_check;
ALTER TABLE user_meter_bindings DROP COLUMN IF EXISTS slot;

CREATE UNIQUE INDEX user_meter_bindings_one_account_per_meter_idx
    ON user_meter_bindings (meter_id)
    WHERE unbound_at IS NULL;
