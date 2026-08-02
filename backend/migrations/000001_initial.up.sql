CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE inventory_imports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_name text NOT NULL,
    source_hash char(64) NOT NULL,
    source_updated_at timestamptz,
    imported_at timestamptz NOT NULL DEFAULT now(),
    valid_until timestamptz,
    status text NOT NULL CHECK (status IN ('validated', 'applied', 'rejected')),
    counts jsonb NOT NULL DEFAULT '{}'::jsonb,
    errors jsonb NOT NULL DEFAULT '[]'::jsonb,
    payload jsonb,
    applied_at timestamptz
);

CREATE UNIQUE INDEX inventory_imports_one_applied_hash
    ON inventory_imports (source_hash)
    WHERE status = 'applied';

CREATE INDEX inventory_imports_imported_at_idx
    ON inventory_imports (imported_at DESC);

CREATE TABLE meters (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    area_id text NOT NULL,
    campus text NOT NULL DEFAULT '主校区',
    building text NOT NULL,
    floor text NOT NULL,
    room text NOT NULL,
    meter_no text NOT NULL UNIQUE,
    active boolean NOT NULL DEFAULT true,
    excluded boolean NOT NULL DEFAULT false,
    exclude_reason text,
    source_import_id uuid REFERENCES inventory_imports(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (NOT excluded OR exclude_reason IS NOT NULL)
);

CREATE INDEX meters_location_idx
    ON meters (campus, building, floor, room);
CREATE INDEX meters_eligible_idx
    ON meters (active, excluded)
    WHERE active AND NOT excluded;

CREATE TABLE scan_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_run_id uuid REFERENCES scan_runs(id),
    trigger text NOT NULL CHECK (trigger IN ('manual', 'schedule', 'retry', 'recovery')),
    status text NOT NULL CHECK (
        status IN ('pending', 'running', 'completed', 'completed_with_errors', 'interrupted', 'failed')
    ),
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
    stale_total integer NOT NULL DEFAULT 0 CHECK (stale_total >= 0),
    empty_total integer NOT NULL DEFAULT 0 CHECK (empty_total >= 0),
    error_total integer NOT NULL DEFAULT 0 CHECK (error_total >= 0),
    parse_error_total integer NOT NULL DEFAULT 0 CHECK (parse_error_total >= 0),
    duplicate_reading_total integer NOT NULL DEFAULT 0 CHECK (duplicate_reading_total >= 0),
    error_message text
);

CREATE INDEX scan_runs_started_at_idx
    ON scan_runs (started_at DESC);
CREATE UNIQUE INDEX scan_runs_one_active_school
    ON scan_runs ((1))
    WHERE status IN ('pending', 'running');

CREATE TABLE scan_run_meters (
    run_id uuid NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    ordinal integer NOT NULL CHECK (ordinal > 0),
    PRIMARY KEY (run_id, meter_id),
    UNIQUE (run_id, ordinal)
);

CREATE INDEX scan_run_meters_meter_idx
    ON scan_run_meters (meter_id);

CREATE TABLE meter_readings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meter_id uuid NOT NULL REFERENCES meters(id),
    reading_time timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    prepaid_yuan numeric(18, 4),
    subsidy_yuan numeric(18, 4),
    total_yuan numeric(18, 4),
    total_kwh numeric(20, 4) NOT NULL,
    meter_status text,
    charge_type text,
    freshness text NOT NULL CHECK (freshness IN ('fresh', 'stale')),
    source text NOT NULL CHECK (source IN ('legacy_import', 'live_scan')),
    source_ref text,
    reading_hash char(64) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (meter_id, reading_time)
);

CREATE INDEX meter_readings_meter_time_idx
    ON meter_readings (meter_id, reading_time DESC);
CREATE INDEX meter_readings_time_idx
    ON meter_readings (reading_time DESC);

CREATE TABLE scan_results (
    run_id uuid NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    meter_id uuid NOT NULL REFERENCES meters(id),
    status text NOT NULL CHECK (
        status IN ('valid', 'stale', 'empty', 'error', 'parse_error', 'canceled')
    ),
    attempts integer NOT NULL CHECK (attempts >= 1),
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    queried_at timestamptz NOT NULL,
    reading_id uuid REFERENCES meter_readings(id),
    duplicate_reading boolean NOT NULL DEFAULT false,
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, meter_id)
);

CREATE INDEX scan_results_run_status_idx
    ON scan_results (run_id, status);
CREATE INDEX scan_results_created_at_idx
    ON scan_results (created_at);

CREATE TABLE consumption_deltas (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meter_id uuid NOT NULL REFERENCES meters(id),
    previous_reading_id uuid NOT NULL REFERENCES meter_readings(id),
    current_reading_id uuid NOT NULL UNIQUE REFERENCES meter_readings(id),
    from_time timestamptz NOT NULL,
    to_time timestamptz NOT NULL,
    delta_kwh numeric(20, 4),
    status text NOT NULL CHECK (
        status IN ('valid', 'unchanged', 'negative_reset', 'time_regression', 'invalid')
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (to_time >= from_time)
);

CREATE INDEX consumption_deltas_meter_time_idx
    ON consumption_deltas (meter_id, to_time DESC);
CREATE INDEX consumption_deltas_time_valid_idx
    ON consumption_deltas (to_time)
    WHERE status IN ('valid', 'unchanged');

CREATE TABLE consumption_rollups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_type text NOT NULL CHECK (scope_type IN ('campus', 'building', 'floor', 'meter')),
    scope_key text NOT NULL,
    period text NOT NULL CHECK (period IN ('day', 'week', 'month')),
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL,
    kwh numeric(24, 4),
    eligible_count integer NOT NULL CHECK (eligible_count >= 0),
    covered_count integer NOT NULL CHECK (covered_count >= 0),
    stale_count integer NOT NULL CHECK (stale_count >= 0),
    anomaly_count integer NOT NULL CHECK (anomaly_count >= 0),
    availability text NOT NULL CHECK (
        availability IN ('ready', 'partial', 'insufficient_history', 'unavailable')
    ),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (scope_type, scope_key, period, period_start)
);

CREATE INDEX consumption_rollups_lookup_idx
    ON consumption_rollups (scope_type, scope_key, period, period_start DESC);

CREATE TABLE anomaly_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type text NOT NULL CHECK (
        type IN (
            'negative_reset', 'same_timestamp_conflict', 'stale_reading',
            'parse_drift', 'implausible_delta', 'time_regression'
        )
    ),
    severity text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    meter_id uuid REFERENCES meters(id),
    reading_id uuid REFERENCES meter_readings(id),
    scan_run_id uuid REFERENCES scan_runs(id),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    detected_at timestamptz NOT NULL DEFAULT now(),
    acknowledged_at timestamptz,
    acknowledged_note text
);

CREATE INDEX anomaly_events_open_idx
    ON anomaly_events (detected_at DESC)
    WHERE acknowledged_at IS NULL;
CREATE INDEX anomaly_events_meter_idx
    ON anomaly_events (meter_id, detected_at DESC);
CREATE UNIQUE INDEX anomaly_events_reading_type_unique
    ON anomaly_events (type, reading_id)
    WHERE reading_id IS NOT NULL;

CREATE TABLE monthly_bills (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    meter_id uuid NOT NULL REFERENCES meters(id),
    month date NOT NULL,
    start_kwh numeric(20, 4),
    end_kwh numeric(20, 4),
    usage_kwh numeric(20, 4),
    cost_yuan numeric(18, 4),
    observed_at timestamptz NOT NULL,
    source text NOT NULL CHECK (source IN ('legacy_import', 'live_query')),
    UNIQUE (meter_id, month)
);

CREATE INDEX monthly_bills_meter_month_idx
    ON monthly_bills (meter_id, month DESC);

CREATE TABLE mail_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL CHECK (provider IN ('smtp', 'tencent_ses')),
    message_type text NOT NULL,
    recipient_masked text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    provider_message_id text,
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);

CREATE INDEX mail_deliveries_created_at_idx
    ON mail_deliveries (created_at DESC);

CREATE TABLE worker_heartbeats (
    worker_name text PRIMARY KEY,
    instance_id text NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);
