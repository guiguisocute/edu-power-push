ALTER TABLE daily_detail_runs DROP CONSTRAINT daily_detail_runs_trigger_check;
ALTER TABLE daily_detail_runs ADD CONSTRAINT daily_detail_runs_trigger_check
    CHECK (trigger IN ('manual', 'schedule', 'retry', 'recovery', 'import'));

