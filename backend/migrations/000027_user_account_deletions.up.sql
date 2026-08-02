-- 硬删除账号留下灾备标记行。
-- 无外键：账号同事务删除，本行须保留。
CREATE TABLE user_account_deletions (
    user_id uuid PRIMARY KEY,
    deleted_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_account_deletions_deleted_at_idx
    ON user_account_deletions (deleted_at);
