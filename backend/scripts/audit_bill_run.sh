#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat >&2 <<'EOF'
usage: audit_bill_run.sh <run-id> [--expected-meters N] [--require-clean] [--allow-running]

Audits one monthly bill run without changing data. It uses DATABASE_URL with a
local psql when available; otherwise it connects through the Compose `db`
service in the current deployment directory.
EOF
  exit 2
}

[[ $# -ge 1 ]] || usage
run_id=$1
shift

if ! [[ "$run_id" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]; then
  echo 'run-id must be a UUID' >&2
  exit 2
fi

expected_meters=''
require_clean=false
allow_running=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --expected-meters)
      [[ $# -ge 2 ]] || usage
      expected_meters=$2
      shift 2
      ;;
    --require-clean)
      require_clean=true
      shift
      ;;
    --allow-running)
      allow_running=true
      shift
      ;;
    *) usage ;;
  esac
done

if [[ -n "$expected_meters" ]] && { ! [[ "$expected_meters" =~ ^[0-9]+$ ]] || (( expected_meters < 1 )); }; then
  echo 'expected meter count must be a positive integer' >&2
  exit 2
fi
run_psql() {
  if [[ -n "${DATABASE_URL:-}" ]] && command -v psql >/dev/null 2>&1; then
    psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 "$@"
    return
  fi

  local -a compose
  if docker compose version >/dev/null 2>&1; then
    compose=(docker compose)
  elif command -v docker-compose >/dev/null 2>&1; then
    compose=(docker-compose)
  else
    echo 'DATABASE_URL + psql or Docker Compose is required' >&2
    return 2
  fi
  "${compose[@]}" exec -T db sh -c \
    'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' sh "$@"
}

audit_json=$(run_psql -At \
  -v "run_id=$run_id" \
  -v "expected_meters=$expected_meters" \
  -v "require_clean=$require_clean" \
  -v "allow_running=$allow_running" <<'SQL'
WITH settings AS (
  SELECT
    NULLIF(:'expected_meters','')::integer AS expected_meters,
    :'require_clean'::boolean AS require_clean,
    :'allow_running'::boolean AS allow_running
), target AS (
  SELECT * FROM bill_runs WHERE id=:'run_id'::uuid
), result_actual AS (
  SELECT
    count(*)::integer AS results,
    count(*) FILTER (WHERE status='valid')::integer AS valid,
    count(*) FILTER (WHERE status='partial')::integer AS partial,
    count(*) FILTER (WHERE status='no_data')::integer AS no_data,
    count(*) FILTER (WHERE status='empty')::integer AS empty,
    count(*) FILTER (WHERE status IN ('error','canceled'))::integer AS errors,
    COALESCE(sum(months_with_data),0)::integer AS month_data,
    COALESCE(sum(months_no_data),0)::integer AS month_no_data,
    COALESCE(sum(months_partial),0)::integer AS month_partial,
    COALESCE(sum(months_error),0)::integer AS month_error,
    COALESCE(sum(months_with_data+months_no_data+months_partial+months_error),0)::integer AS month_slots,
    COALESCE(sum(canonical_saved),0)::integer AS canonical_saved,
    COALESCE(sum(changed_count),0)::integer AS changed
  FROM bill_results WHERE run_id=:'run_id'::uuid
), observation_actual AS (
  SELECT
    count(*)::integer AS observations,
    count(*) FILTER (WHERE status='data')::integer AS observation_data,
    count(*) FILTER (WHERE status='no_data')::integer AS observation_no_data,
    count(*) FILTER (WHERE status='partial')::integer AS observation_partial,
    count(*) FILTER (WHERE status='error')::integer AS observation_errors
  FROM monthly_bill_observations WHERE run_id=:'run_id'::uuid
), structural AS (
  SELECT
    (SELECT count(*)::integer FROM bill_run_meters WHERE run_id=:'run_id'::uuid) AS frozen_meters,
    (SELECT count(*)::integer
       FROM bill_results r CROSS JOIN target t
      WHERE r.run_id=t.id AND (
        r.months_requested<>cardinality(t.months) OR
        r.months_with_data+r.months_no_data+r.months_partial+r.months_error<>r.months_requested
      )) AS bad_result_month_counts,
    (SELECT count(*)::integer
       FROM bill_results r
      WHERE r.run_id=:'run_id'::uuid AND
        (SELECT count(*) FROM monthly_bill_observations o
          WHERE o.run_id=r.run_id AND o.meter_id=r.meter_id)<>r.months_requested) AS bad_observations_per_meter,
    (SELECT count(*)::integer
       FROM monthly_bill_observations o CROSS JOIN target t
      WHERE o.run_id=t.id AND NOT (o.month=ANY(t.months))) AS observations_outside_frozen_months,
    (SELECT count(*)::integer
       FROM monthly_bill_observations o
      WHERE o.run_id=:'run_id'::uuid AND o.status='data' AND NOT EXISTS (
        SELECT 1 FROM monthly_bills b WHERE b.meter_id=o.meter_id AND b.month=o.month
      )) AS data_without_canonical
), violations AS (
  SELECT array_remove(ARRAY[
    CASE WHEN (SELECT count(*) FROM target)<>1
      THEN 'bill run was not found exactly once' END,
    CASE WHEN (SELECT count(*) FROM target)=1 AND NOT cfg.allow_running
              AND (SELECT status FROM target) NOT IN ('completed','completed_with_errors','interrupted','failed')
      THEN format('run status is not terminal: %s',(SELECT status FROM target)) END,
    CASE WHEN (SELECT count(*) FROM target)=1 AND cfg.expected_meters IS NOT NULL
              AND (SELECT eligible_total FROM target)<>cfg.expected_meters
      THEN format('eligible_total is %s, expected %s',(SELECT eligible_total FROM target),cfg.expected_meters) END,
    CASE WHEN (SELECT count(*) FROM target)=1 AND (SELECT eligible_total FROM target)<>s.frozen_meters
      THEN format('eligible_total/frozen_meters mismatch: %s != %s',(SELECT eligible_total FROM target),s.frozen_meters) END,
    CASE WHEN ra.month_slots<>oa.observations
      THEN format('month_slots/observations mismatch: %s != %s',ra.month_slots,oa.observations) END,
    CASE WHEN ra.month_data<>oa.observation_data
      THEN format('month_data/observation_data mismatch: %s != %s',ra.month_data,oa.observation_data) END,
    CASE WHEN ra.month_no_data<>oa.observation_no_data
      THEN format('month_no_data/observation_no_data mismatch: %s != %s',ra.month_no_data,oa.observation_no_data) END,
    CASE WHEN ra.month_partial<>oa.observation_partial
      THEN format('month_partial/observation_partial mismatch: %s != %s',ra.month_partial,oa.observation_partial) END,
    CASE WHEN ra.month_error<>oa.observation_errors
      THEN format('month_error/observation_errors mismatch: %s != %s',ra.month_error,oa.observation_errors) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT processed_total FROM target)<>ra.results
      THEN format('processed_total/results mismatch: %s != %s',(SELECT processed_total FROM target),ra.results) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT valid_total FROM target)<>ra.valid
      THEN format('valid_total/valid mismatch: %s != %s',(SELECT valid_total FROM target),ra.valid) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT partial_total FROM target)<>ra.partial
      THEN format('partial_total/partial mismatch: %s != %s',(SELECT partial_total FROM target),ra.partial) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT no_data_total FROM target)<>ra.no_data
      THEN format('no_data_total/no_data mismatch: %s != %s',(SELECT no_data_total FROM target),ra.no_data) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT empty_total FROM target)<>ra.empty
      THEN format('empty_total/empty mismatch: %s != %s',(SELECT empty_total FROM target),ra.empty) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT error_total FROM target)<>ra.errors
      THEN format('error_total/errors mismatch: %s != %s',(SELECT error_total FROM target),ra.errors) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT month_data_total FROM target)<>ra.month_data
      THEN format('month_data_total/month_data mismatch: %s != %s',(SELECT month_data_total FROM target),ra.month_data) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT month_no_data_total FROM target)<>ra.month_no_data
      THEN format('month_no_data_total/month_no_data mismatch: %s != %s',(SELECT month_no_data_total FROM target),ra.month_no_data) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT month_partial_total FROM target)<>ra.month_partial
      THEN format('month_partial_total/month_partial mismatch: %s != %s',(SELECT month_partial_total FROM target),ra.month_partial) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT month_error_total FROM target)<>ra.month_error
      THEN format('month_error_total/month_error mismatch: %s != %s',(SELECT month_error_total FROM target),ra.month_error) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT canonical_saved_total FROM target)<>ra.canonical_saved
      THEN format('canonical_saved_total/canonical_saved mismatch: %s != %s',(SELECT canonical_saved_total FROM target),ra.canonical_saved) END,
    CASE WHEN (SELECT count(*) FROM target)=1
              AND NOT (cfg.allow_running AND (SELECT status FROM target)='running')
              AND (SELECT changed_total FROM target)<>ra.changed
      THEN format('changed_total/changed mismatch: %s != %s',(SELECT changed_total FROM target),ra.changed) END,
    CASE WHEN (SELECT status FROM target) IN ('completed','completed_with_errors')
              AND ra.results<>(SELECT eligible_total FROM target)
      THEN 'completed run does not have one result per frozen meter' END,
    CASE WHEN s.bad_result_month_counts<>0
      THEN format('bad_result_month_counts is %s',s.bad_result_month_counts) END,
    CASE WHEN s.bad_observations_per_meter<>0
      THEN format('bad_observations_per_meter is %s',s.bad_observations_per_meter) END,
    CASE WHEN s.observations_outside_frozen_months<>0
      THEN format('observations_outside_frozen_months is %s',s.observations_outside_frozen_months) END,
    CASE WHEN s.data_without_canonical<>0
      THEN format('data_without_canonical is %s',s.data_without_canonical) END,
    CASE WHEN cfg.require_clean AND ra.partial<>0 THEN format('clean run has partial=%s',ra.partial) END,
    CASE WHEN cfg.require_clean AND ra.empty<>0 THEN format('clean run has empty=%s',ra.empty) END,
    CASE WHEN cfg.require_clean AND ra.errors<>0 THEN format('clean run has errors=%s',ra.errors) END,
    CASE WHEN cfg.require_clean AND ra.month_partial<>0 THEN format('clean run has month_partial=%s',ra.month_partial) END,
    CASE WHEN cfg.require_clean AND ra.month_error<>0 THEN format('clean run has month_error=%s',ra.month_error) END
  ],NULL)::text[] AS errors
  FROM settings cfg, result_actual ra, observation_actual oa, structural s
)
SELECT jsonb_build_object(
  'found', (SELECT count(*) FROM target),
  'run', COALESCE((SELECT jsonb_build_object(
    'id',id,'parent_run_id',parent_run_id,'trigger',trigger,'status',status,
    'month_count',cardinality(months),'eligible_total',eligible_total,'processed_total',processed_total,
    'valid_total',valid_total,'partial_total',partial_total,'no_data_total',no_data_total,
    'empty_total',empty_total,'error_total',error_total,'month_data_total',month_data_total,
    'month_no_data_total',month_no_data_total,'month_partial_total',month_partial_total,
    'month_error_total',month_error_total,'canonical_saved_total',canonical_saved_total,
    'changed_total',changed_total
  ) FROM target),'{}'::jsonb),
  'actual', to_jsonb(result_actual) || to_jsonb(observation_actual) || to_jsonb(structural),
  'audit', jsonb_build_object('ok',cardinality(violations.errors)=0,'errors',violations.errors)
)::text
FROM result_actual, observation_actual, structural, violations;
SQL
)

printf '%s\n' "$audit_json"
if ! grep -Eq '"ok": true' <<<"$audit_json"; then
  exit 1
fi
