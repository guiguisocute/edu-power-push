DROP INDEX IF EXISTS scan_runs_active_meter_refresh_idx;
DROP INDEX IF EXISTS scan_runs_active_batch_idx;
DROP INDEX IF EXISTS scan_runs_one_active_school;

-- 回滚前须无 running 单表刷新，否则唯一索引建失败。
CREATE UNIQUE INDEX scan_runs_one_active_school
    ON scan_runs ((1))
    WHERE status IN ('pending', 'running');
