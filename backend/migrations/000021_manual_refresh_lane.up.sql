-- 单表手动刷新与批量扫描分通道。
-- 独占仅限批量 run；scope->>'meter' 非空为单表刷新。

DROP INDEX IF EXISTS scan_runs_one_active_school;

CREATE UNIQUE INDEX scan_runs_one_active_school
    ON scan_runs ((1))
    WHERE status IN ('pending', 'running') AND scope->>'meter' IS NULL;

-- 批量任务是否在跑的查询索引。
CREATE INDEX scan_runs_active_batch_idx
    ON scan_runs (started_at)
    WHERE status IN ('pending', 'running') AND scope->>'meter' IS NULL;

-- 手动刷新无 worker 接管；重启后捞 running 孤儿标失败。
CREATE INDEX scan_runs_active_meter_refresh_idx
    ON scan_runs (heartbeat_at)
    WHERE status IN ('pending', 'running') AND scope->>'meter' IS NOT NULL;
