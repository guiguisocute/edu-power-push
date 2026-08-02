#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -lt 1 || $# -gt 3 ]]; then
  echo "usage: $0 <limit> [qps] [concurrency]" >&2
  exit 2
fi

limit=$1
qps=${2:-0.5}
concurrency=${3:-1}
if ! [[ "$limit" =~ ^[0-9]+$ ]] || (( limit < 1 )); then
  echo 'limit must be a positive integer' >&2
  exit 2
fi
if (( limit > 200 )) && [[ "${ALLOW_LARGE_PROBE:-}" != true ]]; then
  echo 'probe limit exceeds the default safety ceiling of 200' >&2
  exit 2
fi

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
bootstrap_dir=${BOOTSTRAP_DIR:-"$repo_dir/bootstrap"}
probe_id="edu-power-scan-probe-$(date +%s)"
test_password=$(python -c 'import secrets; print(secrets.token_hex(16))')

cleanup() {
  docker rm -f "$probe_id" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$probe_id" \
  -e POSTGRES_USER=edu_power \
  -e "POSTGRES_PASSWORD=$test_password" \
  -e POSTGRES_DB=edu_power \
  -p 127.0.0.1::5432 postgres:17-alpine >/dev/null

for _ in $(seq 1 30); do
  if docker exec "$probe_id" pg_isready -U edu_power -d edu_power >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
database_port=$(docker port "$probe_id" 5432/tcp | awk -F: '{print $NF}')
export DATABASE_URL="postgres://edu_power:$test_password@127.0.0.1:$database_port/edu_power?sslmode=disable"

go run ./cmd/admin migrate >/dev/null
go run ./cmd/admin import-inventory --file "$bootstrap_dir/room_meters.json" >/dev/null
go run ./cmd/admin import-snapshot --file "$bootstrap_dir/meter_balances.json" >/dev/null
go run ./cmd/admin scan --limit "$limit" --qps "$qps" --concurrency "$concurrency" >/dev/null

docker exec -e "PGPASSWORD=$test_password" "$probe_id" psql -U edu_power -d edu_power -Atc "
WITH latest AS (
  SELECT * FROM scan_runs WHERE trigger='manual' AND config_snapshot ? 'qps'
  ORDER BY started_at DESC LIMIT 1
), latency AS (
  SELECT
    round(percentile_cont(0.5) WITHIN GROUP (ORDER BY sr.duration_ms))::bigint p50_ms,
    round(percentile_cont(0.95) WITHIN GROUP (ORDER BY sr.duration_ms))::bigint p95_ms,
    max(sr.duration_ms) max_ms,
    round(avg(sr.attempts)::numeric,2) avg_attempts
  FROM scan_results sr JOIN latest l ON l.id=sr.run_id
), codes AS (
  SELECT COALESCE(jsonb_object_agg(error_code,total),'{}'::jsonb) value
  FROM (
    SELECT COALESCE(error_code,'none') error_code,count(*) total
    FROM scan_results sr JOIN latest l ON l.id=sr.run_id
    GROUP BY COALESCE(error_code,'none')
  ) grouped
)
SELECT json_build_object(
  'limit', (SELECT eligible_total FROM latest),
  'qps', (SELECT (config_snapshot->>'qps')::numeric FROM latest),
  'concurrency', (SELECT (config_snapshot->>'concurrency')::integer FROM latest),
  'status', (SELECT status FROM latest),
  'elapsed_seconds', (SELECT round(extract(epoch FROM (finished_at-started_at))::numeric,3) FROM latest),
  'processed', (SELECT processed_total FROM latest),
  'valid', (SELECT valid_total FROM latest),
  'stale', (SELECT stale_total FROM latest),
  'empty', (SELECT empty_total FROM latest),
  'error', (SELECT error_total FROM latest),
  'parse_error', (SELECT parse_error_total FROM latest),
  'duplicate_reading', (SELECT duplicate_reading_total FROM latest),
  'p50_ms', (SELECT p50_ms FROM latency),
  'p95_ms', (SELECT p95_ms FROM latency),
  'max_ms', (SELECT max_ms FROM latency),
  'avg_attempts', (SELECT avg_attempts FROM latency),
  'error_codes', (SELECT value FROM codes),
  'live_readings', (SELECT count(*) FROM meter_readings WHERE source='live_scan'),
  'deltas', (SELECT count(*) FROM consumption_deltas)
);"
