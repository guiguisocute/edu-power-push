-- 采集任务取消：API 写 cancel_requested，worker 写 canceled。
-- canceled 为终态，恢复逻辑不捞取，禁止自动续跑。

ALTER TABLE scan_runs         ADD COLUMN IF NOT EXISTS cancel_requested boolean NOT NULL DEFAULT false;
ALTER TABLE bill_runs         ADD COLUMN IF NOT EXISTS cancel_requested boolean NOT NULL DEFAULT false;
ALTER TABLE daily_detail_runs ADD COLUMN IF NOT EXISTS cancel_requested boolean NOT NULL DEFAULT false;

ALTER TABLE scan_runs DROP CONSTRAINT IF EXISTS scan_runs_status_check;
ALTER TABLE scan_runs ADD CONSTRAINT scan_runs_status_check
    CHECK (status IN ('pending','running','completed','completed_with_errors','interrupted','canceled','failed'));

ALTER TABLE bill_runs DROP CONSTRAINT IF EXISTS bill_runs_status_check;
ALTER TABLE bill_runs ADD CONSTRAINT bill_runs_status_check
    CHECK (status IN ('pending','running','completed','completed_with_errors','interrupted','canceled','failed'));

ALTER TABLE daily_detail_runs DROP CONSTRAINT IF EXISTS daily_detail_runs_status_check;
ALTER TABLE daily_detail_runs ADD CONSTRAINT daily_detail_runs_status_check
    CHECK (status IN ('pending','running','completed','completed_with_errors','interrupted','canceled','failed'));
