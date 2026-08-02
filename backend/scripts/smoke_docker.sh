#!/usr/bin/env bash
set -Eeuo pipefail
export MSYS_NO_PATHCONV=1

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_dir"
project="edu-power-docker-smoke-$(date +%s)"
# 本脚本必须能在一份干净克隆上直接跑。仓库里没有 .env，
# 而 compose 把 APP_ENV / APP_IMAGE 标成必填。不在这里导出，
# 就只能靠调用者本地恰好有一份 .env，等于拿别的环境的配置跑冒烟。
export APP_ENV=${APP_ENV:-development}
export APP_IMAGE=${APP_IMAGE:-edu-power-push:smoke}
export WEB_IMAGE=${WEB_IMAGE:-edu-power-push-web:smoke}
export POSTGRES_PASSWORD=$(python -c 'import secrets; print(secrets.token_hex(16))')
export ADMIN_TOKEN=$(python -c 'import secrets; print(secrets.token_hex(24))')
export AUTH_JWT_SECRET=$(python -c 'import secrets; print(secrets.token_hex(32))')
export AUTH_COOKIE_SECURE=false
export API_PORT=$(python -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
# 相对路径由 compose 按**项目目录**（仓库根）解析，不是按本脚本所在目录。
# 写成 ./bootstrap 时 compose 会在仓库根建一个空目录挂进去，导入随即找不到文件。
export BOOTSTRAP_DIR=${BOOTSTRAP_DIR:-./backend/bootstrap}
export MAIL_PROVIDER=''
export SCAN_CRON=''
export BILL_CRON='0 19 1 * *'
# 采集器在未选学校时会跳过全部 run（见 cmd/worker 的 runPending）。
# 下面的检查点/恢复夹具靠 worker 真的领取任务才成立，所以必须给它一所学校。
export AREA_ID=${AREA_ID:-1}
export AREA_NAME=${AREA_NAME:-示例大学}
# 指向 TEST-NET-1（RFC 5737 保留段，不可路由）：请求挂到超时再失败。
# 冒烟验的是「中断能不能续跑」，不是上游返回什么，
# 更不该让任何人跑一次冒烟就往真实学校系统上打请求。
export ELECTRICITY_BASE_URL=${ELECTRICITY_BASE_URL:-http://192.0.2.1}
export SCAN_TIMEOUT_SEC=${SCAN_TIMEOUT_SEC:-2}
export SCAN_RETRY_MAX=${SCAN_RETRY_MAX:-0}

cleanup() {
  if [[ "$project" == edu-power-docker-smoke-* ]]; then
    docker compose -p "$project" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  [[ -n "${body_sink:-}" ]] && rm -f "$body_sink"
  return 0
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
# 运维面板已并进前端 SPA，后端不再内嵌那张 HTML（/admin/ 现在只是一个 301）。
# 因此改查它背后的两个管理端点：账单任务列表（读）与前端配置编辑（写）。
# 注意：本脚本设了 MSYS_NO_PATHCONV=1，curl 的 -o /dev/null 在 Git Bash 下会报 error 23，
# 一律丢进 $body_sink 这个真实临时文件。
body_sink=$(mktemp)
http_code() {
  local method=$1 url=$2
  shift 2
  curl -s -X "$method" -o "$body_sink" -w '%{http_code}' "$@" "$url"
}
admin_bill_code=$(http_code GET "$base_url/api/v1/admin/bill-runs" -H "Authorization: Bearer $ADMIN_TOKEN")
if [[ "$admin_bill_code" != 200 ]]; then
  echo "admin API GET /api/v1/admin/bill-runs answered HTTP $admin_bill_code with ADMIN_TOKEN" >&2
  exit 1
fi
# 前端配置只写不读（读走公开的 GET /api/v1/frontend-config，上面已取）。
# 这里不真改配置，只确认路由还在：404/405 表示面板的保存按钮没有后端。
config_put_code=$(http_code PUT "$base_url/api/v1/admin/frontend-config"   -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{}')
if [[ "$config_put_code" == 404 || "$config_put_code" == 405 ]]; then
  echo "admin API PUT /api/v1/admin/frontend-config is not wired (HTTP $config_put_code)" >&2
  exit 1
fi
# 两条都必须拒绝匿名调用：运维接口能匿名打，等于把面板敞开。
for probe in "GET /api/v1/admin/bill-runs" "PUT /api/v1/admin/frontend-config"; do
  anon_code=$(http_code "${probe% *}" "$base_url${probe#* }")
  if [[ "$anon_code" != 401 && "$anon_code" != 403 ]]; then
    echo "admin API $probe answered anonymous caller with HTTP $anon_code" >&2
    exit 1
  fi
done

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

# 账单生命周期需要一个**能返回月账单**的上游：下面断言的是真的抄到 6 条月观测，
# 而上面那个不可路由的默认地址只会产出错误结果，观测数恒为 0。
# 所以这一段默认跳过；扫描那一段不依赖返回内容，始终会跑。
# 要连真实上游跑它：
#   SMOKE_BILL_LIFECYCLE=1 AREA_ID=<你的> ELECTRICITY_BASE_URL=<你的> bash backend/scripts/smoke_docker.sh
bill_interrupted_status=skipped
bill_recovery_status=skipped
bill_recovery_observations=0
bill_pre_stop_processed=0
if [[ "${SMOKE_BILL_LIFECYCLE:-0}" == 1 ]]; then
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

fi

python - "$overview" "$frontend_config" "$interrupted_status" "$recovery_status" "$recovery_processed" "$bill_interrupted_status" "$bill_recovery_status" "$bill_recovery_observations" "$bill_pre_stop_processed" <<'PY'
import json
import sys
value = json.loads(sys.argv[1])
frontend = json.loads(sys.argv[2])
interrupted_status, recovery_status, recovery_processed, bill_interrupted_status, bill_recovery_status, bill_recovery_observations, bill_live_progress = sys.argv[3:]
assert value["version"] == "version_1"
assert value["migrations"] == "ready"
# frontend_config.version 每次有迁移改动配置就自增，不是固定 1。
# 这里要断言的是「配置已播种且带版本号」，钉死具体数字只会在下次加迁移时假报警。
assert isinstance(frontend["version"], int) and frontend["version"] >= 1
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
