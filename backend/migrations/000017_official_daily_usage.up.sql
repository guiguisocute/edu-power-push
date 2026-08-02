CREATE TABLE daily_detail_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_run_id uuid REFERENCES daily_detail_runs(id),
    trigger text NOT NULL CHECK (trigger IN ('manual', 'schedule', 'retry', 'recovery')),
    status text NOT NULL CHECK (
        status IN ('pending', 'running', 'completed', 'completed_with_errors', 'interrupted', 'failed')
    ),
    months date[] NOT NULL CHECK (cardinality(months) > 0),
    scope jsonb NOT NULL DEFAULT '{}'::jsonb,
    config_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    heartbeat_at timestamptz,
    inventory_total integer NOT NULL DEFAULT 0 CHECK (inventory_total >= 0),
    excluded_total integer NOT NULL DEFAULT 0 CHECK (excluded_total >= 0),
    eligible_total integer NOT NULL DEFAULT 0 CHECK (eligible_total >= 0),
    processed_total integer NOT NULL DEFAULT 0 CHECK (processed_total >= 0),
    valid_total integer NOT NULL DEFAULT 0 CHECK (valid_total >= 0),
    no_data_total integer NOT NULL DEFAULT 0 CHECK (no_data_total >= 0),
    partial_total integer NOT NULL DEFAULT 0 CHECK (partial_total >= 0),
    empty_total integer NOT NULL DEFAULT 0 CHECK (empty_total >= 0),
    error_total integer NOT NULL DEFAULT 0 CHECK (error_total >= 0),
    days_saved_total integer NOT NULL DEFAULT 0 CHECK (days_saved_total >= 0),
    changed_total integer NOT NULL DEFAULT 0 CHECK (changed_total >= 0),
    error_message text
);

CREATE UNIQUE INDEX daily_detail_runs_one_active
    ON daily_detail_runs ((1)) WHERE status IN ('pending', 'running');
CREATE INDEX daily_detail_runs_started_idx ON daily_detail_runs (started_at DESC);

CREATE TABLE daily_detail_run_meters (
    run_id uuid NOT NULL REFERENCES daily_detail_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    ordinal integer NOT NULL CHECK (ordinal > 0),
    PRIMARY KEY (run_id, meter_id),
    UNIQUE (run_id, ordinal)
);

CREATE TABLE daily_detail_results (
    run_id uuid NOT NULL REFERENCES daily_detail_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    status text NOT NULL CHECK (status IN ('valid', 'no_data', 'partial', 'empty', 'error', 'canceled')),
    attempts integer NOT NULL CHECK (attempts >= 1),
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    queried_at timestamptz NOT NULL,
    months_requested integer NOT NULL CHECK (months_requested >= 0),
    months_valid integer NOT NULL CHECK (months_valid >= 0),
    months_no_data integer NOT NULL CHECK (months_no_data >= 0),
    months_error integer NOT NULL CHECK (months_error >= 0),
    days_saved integer NOT NULL CHECK (days_saved >= 0),
    changed_count integer NOT NULL CHECK (changed_count >= 0),
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, meter_id)
);

CREATE INDEX daily_detail_results_run_status_idx
    ON daily_detail_results (run_id, status);

-- 成功拉取覆盖至 covered_through；范围内缺行视为权威零用电。
CREATE TABLE daily_detail_months (
    meter_id uuid NOT NULL REFERENCES meters(id),
    month date NOT NULL,
    covered_through date NOT NULL,
    status text NOT NULL CHECK (status IN ('valid', 'no_data')),
    raw_row_count integer NOT NULL DEFAULT 0 CHECK (raw_row_count >= 0),
    days_with_usage integer NOT NULL DEFAULT 0 CHECK (days_with_usage >= 0),
    observed_at timestamptz NOT NULL,
    source_run_id uuid NOT NULL REFERENCES daily_detail_runs(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (meter_id, month),
    CHECK (covered_through >= month AND covered_through < month + interval '1 month')
);

CREATE INDEX daily_detail_months_month_idx
    ON daily_detail_months (month, covered_through);

CREATE TABLE daily_usages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meter_id uuid NOT NULL REFERENCES meters(id),
    usage_date date NOT NULL,
    usage_kwh numeric(20, 4) NOT NULL CHECK (usage_kwh >= 0),
    cost_yuan numeric(18, 4) NOT NULL CHECK (cost_yuan >= 0),
    has_upstream_data boolean NOT NULL,
    observed_at timestamptz NOT NULL,
    source_run_id uuid NOT NULL REFERENCES daily_detail_runs(id),
    raw_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (meter_id, usage_date)
);

CREATE INDEX daily_usages_meter_date_idx
    ON daily_usages (meter_id, usage_date DESC);
CREATE INDEX daily_usages_date_idx ON daily_usages (usage_date);

CREATE TABLE daily_usage_revisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES daily_detail_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    usage_date date NOT NULL,
    previous_values jsonb NOT NULL,
    current_values jsonb NOT NULL,
    detected_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, meter_id, usage_date)
);

CREATE INDEX daily_usage_revisions_date_idx
    ON daily_usage_revisions (detected_at DESC);

-- 已结算日官方明细优先；未结算日保留扫描估算。
CREATE VIEW effective_daily_consumption AS
SELECT u.meter_id, u.usage_date, u.usage_kwh, u.cost_yuan, 'official_detail'::text AS source
FROM daily_usages u
UNION ALL
SELECT d.meter_id,
       d.to_time::date AS usage_date,
       sum(d.delta_kwh)::numeric(20,4) AS usage_kwh,
       NULL::numeric(18,4) AS cost_yuan,
       'live_scan_estimate'::text AS source
FROM consumption_deltas d
WHERE d.status IN ('valid','unchanged')
  AND NOT EXISTS (
      SELECT 1 FROM daily_detail_months c
      WHERE c.meter_id=d.meter_id
        AND c.month=date_trunc('month',d.to_time)::date
        AND c.covered_through>=d.to_time::date
  )
GROUP BY d.meter_id, d.to_time::date;
