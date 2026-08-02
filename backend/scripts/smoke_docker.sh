#!/usr/bin/env bash
set -Eeuo pipefail
export MSYS_NO_PATHCONV=1

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
project="edu-power-docker-smoke-$(date +%s)"
export POSTGRES_PASSWORD=$(python -c 'import secrets; print(secrets.token_hex(16))')
export ADMIN_TOKEN=$(python -c 'import secrets; print(secrets.token_hex(24))')
export AUTH_JWT_SECRET=$(python -c 'import secrets; print(secrets.token_hex(32))')
export AUTH_COOKIE_SECURE=false
export API_PORT=$(python -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
export BOOTSTRAP_DIR=${BOOTSTRAP_DIR:-./bootstrap}
export MAIL_PROVIDER=''
export SCAN_CRON=''
export BILL_CRON='0 19 1 * *'

cleanup() {
  if [[ "$project" == edu-power-docker-smoke-* ]]; then
    docker compose -p "$project" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

docker compose -p "$project" build
docker compose -p "$project" up -d db
docker compose -p "$project" --profile tools run --rm admin migrate >/dev/null
docker compose -p "$project" --profile tools run --rm admin import-inventory --file /bootstrap/room_meters.json >/dev/null
docker compose -p "$project" --profile tools run --rm admin import-snapshot --file /bootstrap/meter_balances.json >/dev/null
docker compose -p "$project" up -d api worker

base_url="http://127.0.0.1:$API_PORT"
ready=false
for _ in $(seq 1 60); do
  if response=$(curl -fsS "$base_url/health/ready" 2>/dev/null) && \
     python -c 'import json,sys; raise SystemExit(0 if json.loads(sys.argv[1])["status"]=="ready" else 1)' "$response"; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  docker compose -p "$project" ps >&2
  docker compose -p "$project" logs --tail=100 api worker >&2
  exit 1
fi

overview=$(curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$base_url/api/v1/admin/overview")
frontend_config=$(curl -fsS "$base_url/api/v1/frontend-config")
admin_page=$(curl -fsS "$base_url/admin/")
if [[ "$admin_page" != *'id="frontendConfigEditor"'* || "$admin_page" != *'id="billRuns"'* ]]; then
  echo 'admin page is missing the frontend config editor or bill-run panel' >&2
  exit 1
fi

# 验证 Linux/Docker 真实 SIGTERM 路径。
# Worker 有进展后停止它。
# 必须写入 interrupted 检查点。
# 替换 Worker 仅恢复未结束的冻结电表。
recovery_run_id=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atq <<'SQL'
WITH chosen AS (
  SELECT id,row_number() OVER (ORDER BY meter_no)::integer ordinal
  FROM meters WHERE active AND NOT excluded ORDER BY meter_no LIMIT 5
), created AS (
  INSERT INTO scan_runs (
    trigger,status,scope,config_snapshot,inventory_total,eligible_total
  )
  SELECT 'recovery','pending',jsonb_build_object('limit',5),
    jsonb_build_object('qps',1,'concurrency',1),
    (SELECT count(*) FROM meters),5
  RETURNING id
), frozen AS (
  INSERT INTO scan_run_meters (run_id,meter_id,ordinal)
  SELECT created.id,chosen.id,chosen.ordinal FROM created CROSS JOIN chosen
  RETURNING run_id
)
SELECT run_id::text FROM frozen LIMIT 1;
SQL
)
if [[ -z "$recovery_run_id" ]]; then
  echo 'failed to create Docker recovery fixture' >&2
  exit 1
fi
pre_stop_status=pending
pre_stop_processed=0
for _ in $(seq 1 120); do
  row=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atc \
    "select status||'|'||processed_total from scan_runs where id='$recovery_run_id'::uuid")
  IFS='|' read -r pre_stop_status pre_stop_processed <<<"$row"
  if [[ "$pre_stop_status" == running && "$pre_stop_processed" -ge 1 ]]; then
    break
  fi
  sleep 0.25
done
if [[ "$pre_stop_status" != running || "$pre_stop_processed" -lt 1 ]]; then
  echo "Docker recovery fixture did not begin: status=$pre_stop_status processed=$pre_stop_processed" >&2
  exit 1
fi
docker compose -p "$project" stop -t 15 worker >/dev/null
interrupted_status=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atc \
  "select status from scan_runs where id='$recovery_run_id'::uuid")
if [[ "$interrupted_status" != interrupted ]]; then
  docker compose -p "$project" logs --tail=120 worker >&2
  echo "SIGTERM did not checkpoint the scan as interrupted: $interrupted_status" >&2
  exit 1
fi
docker compose -p "$project" start worker >/dev/null
recovery_status=interrupted
recovery_processed=0
recovery_results=0
for _ in $(seq 1 120); do
  row=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atc \
    "select status||'|'||processed_total||'|'||(select count(*) from scan_results where run_id='$recovery_run_id'::uuid) from scan_runs where id='$recovery_run_id'::uuid")
  IFS='|' read -r recovery_status recovery_processed recovery_results <<<"$row"
  if [[ "$recovery_status" != interrupted && "$recovery_status" != running ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$recovery_status" != completed && "$recovery_status" != completed_with_errors ]]; then
  docker compose -p "$project" logs --tail=120 worker >&2
  echo "replacement worker did not finish the interrupted scan: $recovery_status" >&2
  exit 1
fi
if [[ "$recovery_processed" != 5 || "$recovery_results" != 5 ]]; then
  echo "Docker recovery counters are inconsistent: processed=$recovery_processed results=$recovery_results" >&2
  exit 1
fi

# 对 3 表 2 月账单夹具重复 SIGTERM 验证。
# 等待 15 秒心跳。
# 实时计数必须跟踪已提交 bill_results。
# 生产运行使用全部可见月份。
# 本夹具缩短月份以限制生命周期测试。
bill_recovery_run_id=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atq <<'SQL'
WITH chosen AS (
  SELECT id,row_number() OVER (ORDER BY meter_no)::integer ordinal
  FROM meters WHERE active AND NOT excluded ORDER BY meter_no LIMIT 3
), created AS (
  INSERT INTO bill_runs (
    trigger,status,mode,months,scope,config_snapshot,inventory_total,eligible_total
  ) SELECT 'recovery','pending','visible_months',
    ARRAY[date_trunc('month',now())::date,(date_trunc('month',now())-interval '1 month')::date],
    jsonb_build_object('limit',3),jsonb_build_object('qps',1,'concurrency',1,'retry_max',0,'month_retry_max',0),
    (SELECT count(*) FROM meters),3
  RETURNING id
), frozen AS (
  INSERT INTO bill_run_meters (run_id,meter_id,ordinal)
  SELECT created.id,chosen.id,chosen.ordinal FROM created CROSS JOIN chosen
  RETURNING run_id
)
SELECT run_id::text FROM frozen LIMIT 1;
SQL
)
bill_pre_stop_status=pending
bill_pre_stop_processed=0
bill_pre_stop_results=0
for _ in $(seq 1 120); do
  row=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atc \
    "select status||'|'||processed_total||'|'||(select count(*) from bill_results where run_id='$bill_recovery_run_id'::uuid) from bill_runs where id='$bill_recovery_run_id'::uuid")
  IFS='|' read -r bill_pre_stop_status bill_pre_stop_processed bill_pre_stop_results <<<"$row"
  if [[ "$bill_pre_stop_status" == running && "$bill_pre_stop_processed" -ge 1 && "$bill_pre_stop_processed" == "$bill_pre_stop_results" ]]; then break; fi
  sleep 0.5
done
if [[ "$bill_pre_stop_status" != running || "$bill_pre_stop_processed" -lt 1 || "$bill_pre_stop_processed" != "$bill_pre_stop_results" ]]; then
  echo "Docker bill live counters did not catch up: status=$bill_pre_stop_status processed=$bill_pre_stop_processed results=$bill_pre_stop_results" >&2
  exit 1
fi
docker compose -p "$project" stop -t 15 worker >/dev/null
bill_interrupted_status=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atc \
  "select status from bill_runs where id='$bill_recovery_run_id'::uuid")
if [[ "$bill_interrupted_status" != interrupted ]]; then
  docker compose -p "$project" logs --tail=120 worker >&2
  echo "SIGTERM did not checkpoint the bill run as interrupted: $bill_interrupted_status" >&2
  exit 1
fi
docker compose -p "$project" start worker >/dev/null
bill_recovery_status=interrupted
bill_recovery_processed=0
bill_recovery_observations=0
for _ in $(seq 1 120); do
  row=$(docker compose -p "$project" exec -T db psql -U edu_power -d edu_power -Atc \
    "select status||'|'||processed_total||'|'||(select count(*) from monthly_bill_observations where run_id='$bill_recovery_run_id'::uuid) from bill_runs where id='$bill_recovery_run_id'::uuid")
  IFS='|' read -r bill_recovery_status bill_recovery_processed bill_recovery_observations <<<"$row"
  if [[ "$bill_recovery_status" != interrupted && "$bill_recovery_status" != running ]]; then break; fi
  sleep 0.5
done
if [[ "$bill_recovery_status" != completed && "$bill_recovery_status" != completed_with_errors ]]; then
  docker compose -p "$project" logs --tail=120 worker >&2
  echo "replacement worker did not finish the interrupted bill run: $bill_recovery_status" >&2
  exit 1
fi
if [[ "$bill_recovery_processed" != 3 || "$bill_recovery_observations" != 6 ]]; then
  echo "Docker bill recovery counters are inconsistent: processed=$bill_recovery_processed observations=$bill_recovery_observations" >&2
  exit 1
fi
COMPOSE_PROJECT_NAME="$project" bash scripts/audit_bill_run.sh \
  "$bill_recovery_run_id" --expected-meters 3 --require-clean >/dev/null

python - "$overview" "$frontend_config" "$interrupted_status" "$recovery_status" "$recovery_processed" "$bill_interrupted_status" "$bill_recovery_status" "$bill_recovery_observations" "$bill_pre_stop_processed" <<'PY'
import json
import sys
value = json.loads(sys.argv[1])
frontend = json.loads(sys.argv[2])
interrupted_status, recovery_status, recovery_processed, bill_interrupted_status, bill_recovery_status, bill_recovery_observations, bill_live_progress = sys.argv[3:]
assert value["version"] == "version_1"
assert value["migrations"] == "ready"
assert frontend["version"] == 1
assert frontend["features"]["auth"]["registration"] is True
print(json.dumps({
    "readiness": "ready",
    "inventory_total": value["inventory"]["inventory_total"],
    "eligible_total": value["inventory"]["eligible_total"],
    "worker": value["worker"],
    "version": value["version"],
    "migrations": value["migrations"],
    "frontend_config": frontend["version"],
    "sigterm_checkpoint": interrupted_status,
    "recovery_status": recovery_status,
    "recovery_processed": int(recovery_processed),
    "bill_sigterm_checkpoint": bill_interrupted_status,
    "bill_live_progress": int(bill_live_progress),
    "bill_recovery_status": bill_recovery_status,
    "bill_recovery_observations": int(bill_recovery_observations),
}, ensure_ascii=False, separators=(",", ":")))
PY
