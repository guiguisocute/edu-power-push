CREATE UNIQUE INDEX user_meter_bindings_one_account_per_meter_idx
    ON user_meter_bindings (meter_id)
    WHERE unbound_at IS NULL;
