CREATE TABLE bill_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_run_id uuid REFERENCES bill_runs(id),
    trigger text NOT NULL CHECK (trigger IN ('manual', 'schedule', 'retry', 'recovery')),
    status text NOT NULL CHECK (
        status IN ('pending', 'running', 'completed', 'completed_with_errors', 'interrupted', 'failed')
    ),
    mode text NOT NULL CHECK (mode IN ('visible_months')),
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
    partial_total integer NOT NULL DEFAULT 0 CHECK (partial_total >= 0),
    no_data_total integer NOT NULL DEFAULT 0 CHECK (no_data_total >= 0),
    empty_total integer NOT NULL DEFAULT 0 CHECK (empty_total >= 0),
    error_total integer NOT NULL DEFAULT 0 CHECK (error_total >= 0),
    month_data_total integer NOT NULL DEFAULT 0 CHECK (month_data_total >= 0),
    month_no_data_total integer NOT NULL DEFAULT 0 CHECK (month_no_data_total >= 0),
    month_partial_total integer NOT NULL DEFAULT 0 CHECK (month_partial_total >= 0),
    month_error_total integer NOT NULL DEFAULT 0 CHECK (month_error_total >= 0),
    canonical_saved_total integer NOT NULL DEFAULT 0 CHECK (canonical_saved_total >= 0),
    changed_total integer NOT NULL DEFAULT 0 CHECK (changed_total >= 0),
    error_message text
);

CREATE INDEX bill_runs_started_at_idx ON bill_runs (started_at DESC);
CREATE UNIQUE INDEX bill_runs_one_active_school
    ON bill_runs ((1)) WHERE status IN ('pending', 'running');

CREATE TABLE bill_run_meters (
    run_id uuid NOT NULL REFERENCES bill_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    ordinal integer NOT NULL CHECK (ordinal > 0),
    PRIMARY KEY (run_id, meter_id),
    UNIQUE (run_id, ordinal)
);

CREATE INDEX bill_run_meters_meter_idx ON bill_run_meters (meter_id);

CREATE TABLE bill_results (
    run_id uuid NOT NULL REFERENCES bill_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    status text NOT NULL CHECK (status IN ('valid', 'partial', 'no_data', 'empty', 'error', 'canceled')),
    attempts integer NOT NULL CHECK (attempts >= 1),
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    queried_at timestamptz NOT NULL,
    months_requested integer NOT NULL CHECK (months_requested >= 0),
    months_with_data integer NOT NULL CHECK (months_with_data >= 0),
    months_no_data integer NOT NULL CHECK (months_no_data >= 0),
    months_partial integer NOT NULL CHECK (months_partial >= 0),
    months_error integer NOT NULL CHECK (months_error >= 0),
    canonical_saved integer NOT NULL CHECK (canonical_saved >= 0),
    changed_count integer NOT NULL CHECK (changed_count >= 0),
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, meter_id)
);

CREATE INDEX bill_results_run_status_idx ON bill_results (run_id, status);

CREATE TABLE monthly_bill_observations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES bill_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    month date NOT NULL,
    status text NOT NULL CHECK (status IN ('data', 'no_data', 'partial', 'error')),
    start_kwh numeric(20, 4),
    end_kwh numeric(20, 4),
    usage_kwh numeric(20, 4),
    cost_yuan numeric(18, 4),
    upstream_message text,
    upstream_period text,
    error_message text,
    observed_at timestamptz NOT NULL,
    raw_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, meter_id, month)
);

CREATE INDEX monthly_bill_observations_meter_month_idx
    ON monthly_bill_observations (meter_id, month DESC, observed_at DESC);

CREATE TABLE bill_revisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES bill_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    month date NOT NULL,
    previous_values jsonb NOT NULL,
    current_values jsonb NOT NULL,
    detected_at timestamptz NOT NULL DEFAULT now(),
    acknowledged_at timestamptz,
    acknowledged_note text,
    UNIQUE (run_id, meter_id, month)
);

CREATE INDEX bill_revisions_open_idx
    ON bill_revisions (detected_at DESC) WHERE acknowledged_at IS NULL;
