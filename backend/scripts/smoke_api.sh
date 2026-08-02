#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
bootstrap_dir=${BOOTSTRAP_DIR:-"$repo_dir/bootstrap"}

smoke_id="edu-power-api-smoke-$(date +%s)"
mailpit_id="$smoke_id-mailpit"
smoke_dir=$(mktemp -d)
api_binary="$smoke_dir/api.exe"
worker_binary="$smoke_dir/worker.exe"
api_pid=""
worker_pid=""
test_password=$(python -c 'import secrets; print(secrets.token_hex(16))')
test_token=$(python -c 'import secrets; print(secrets.token_hex(24))')
test_jwt_secret=$(python -c 'import secrets; print(secrets.token_hex(32))')

cleanup() {
  if [[ -n "$api_pid" ]] && kill -0 "$api_pid" 2>/dev/null; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  if [[ -n "$worker_pid" ]] && kill -0 "$worker_pid" 2>/dev/null; then
    kill "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
  fi
  docker rm -f "$smoke_id" >/dev/null 2>&1 || true
  docker rm -f "$mailpit_id" >/dev/null 2>&1 || true
  rm -rf -- "$smoke_dir"
}
trap cleanup EXIT

docker run -d --name "$smoke_id" \
  -e POSTGRES_USER=edu_power \
  -e "POSTGRES_PASSWORD=$test_password" \
  -e POSTGRES_DB=edu_power \
  -p 127.0.0.1::5432 postgres:17-alpine >/dev/null
docker run -d --name "$mailpit_id" \
  -p 127.0.0.1::1025 -p 127.0.0.1::8025 axllent/mailpit:v1.30.0 >/dev/null

database_ready=false
for _ in $(seq 1 30); do
  if docker exec "$smoke_id" pg_isready -U edu_power -d edu_power >/dev/null 2>&1; then
    database_ready=true
    break
  fi
  sleep 0.5
done
if [[ "$database_ready" != true ]]; then
  echo 'PostgreSQL did not become ready' >&2
  exit 1
fi

database_port=$(docker port "$smoke_id" 5432/tcp | awk -F: '{print $NF}')
mailpit_smtp_port=$(docker port "$mailpit_id" 1025/tcp | awk -F: '{print $NF}')
mailpit_http_port=$(docker port "$mailpit_id" 8025/tcp | awk -F: '{print $NF}')
for _ in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:$mailpit_http_port/api/v1/messages" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
database_url="postgres://edu_power:$test_password@127.0.0.1:$database_port/edu_power?sslmode=disable"
export DATABASE_URL="$database_url"

go run ./cmd/admin migrate >/dev/null
go run ./cmd/admin import-inventory --file "$bootstrap_dir/room_meters.json" >/dev/null
go run ./cmd/admin import-snapshot --file "$bootstrap_dir/meter_balances.json" >/dev/null
go build -o "$api_binary" ./cmd/api
go build -o "$worker_binary" ./cmd/worker

api_port=$(python -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
DATABASE_URL="$database_url" ADMIN_TOKEN="$test_token" AUTH_JWT_SECRET="$test_jwt_secret" \
  AUTH_COOKIE_SECURE=false HTTP_ADDR="127.0.0.1:$api_port" MAIL_PROVIDER=smtp \
  MAIL_FROM='Power Push Test <no-reply@example.test>' SMTP_HOST=127.0.0.1 SMTP_PORT="$mailpit_smtp_port" \
  SMTP_TLS_MODE=none \
  "$api_binary" >"$smoke_dir/api.stdout.log" 2>"$smoke_dir/api.stderr.log" &
api_pid=$!
base_url="http://127.0.0.1:$api_port"

api_ready=false
for _ in $(seq 1 40); do
  if curl -fsS "$base_url/health/live" >"$smoke_dir/live.json"; then
    api_ready=true
    break
  fi
  sleep 0.25
done
if [[ "$api_ready" != true ]]; then
  sed -n '1,80p' "$smoke_dir/api.stderr.log" >&2
  exit 1
fi

auth_header="Authorization: Bearer $test_token"
unauthorized_status=$(curl -sS -o /dev/null -w '%{http_code}' "$base_url/api/v1/admin/overview")

# 在 Worker 启动前关闭三类定时任务。
# 使用管理接口写入 disabled 设置。
# 空 Cron 会回落为部署默认值。
# 禁止依赖空 Cron 隔离测试。
# 保存结果也验证 Worker 启动读取。
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/settings/scanner" >"$smoke_dir/scanner-settings.json"
python - "$smoke_dir/scanner-settings.json" >"$smoke_dir/scanner-disabled.json" <<'PY'
import json, sys
settings = json.load(open(sys.argv[1], encoding="utf-8"))["settings"]
for key in ("balance", "bills", "daily_details"):
    settings[key]["enabled"] = False
print(json.dumps(settings, separators=(",", ":")))
PY
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' -X PUT \
  --data-binary "@$smoke_dir/scanner-disabled.json" \
  "$base_url/api/v1/admin/settings/scanner" >"$smoke_dir/scanner-settings-disabled.json"
DATABASE_URL="$database_url" MAIL_PROVIDER='' \
  "$worker_binary" >"$smoke_dir/worker.stdout.log" 2>"$smoke_dir/worker.stderr.log" &
worker_pid=$!

for _ in $(seq 1 20); do
  curl -fsS "$base_url/health/ready" >"$smoke_dir/ready.json"
  readiness_status=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["status"])' "$smoke_dir/ready.json")
  if [[ "$readiness_status" == ready ]]; then
    break
  fi
  sleep 0.25
done
frontend_config_payload='{"features":{"auth":{"email_login":true,"sms_login":false,"email_code":false,"sms_code":false,"registration":true},"channels":{"mail":true,"sms":true,"dingtalk":true,"wecom":true,"wecom_webhook":true,"feishu":true,"lark":true,"discord":true,"webhook":true,"bark":true,"gotify":true,"whatsapp":true,"pushplus":true,"serverchan_turbo":true,"serverchan3":true,"mp":true,"qq":true,"napcat":true,"telegram":true},"channel_coming_soon":{"mail":false,"sms":true,"dingtalk":false,"wecom":false,"wecom_webhook":false,"feishu":false,"lark":false,"discord":false,"webhook":false,"bark":false,"gotify":false,"whatsapp":false,"pushplus":false,"serverchan_turbo":false,"serverchan3":false,"mp":false,"qq":false,"napcat":false,"telegram":false},"channel_order":["qq","napcat","webhook","bark","gotify","whatsapp","serverchan_turbo","serverchan3","mail","dingtalk","lark","wecom","wecom_webhook","feishu","discord","pushplus","mp","telegram","sms"]},"display":{"campus_name":"示例校区","area_name":"示例大学","ranking_refresh_time":"09:00"}}'
curl -fsS "$base_url/api/v1/frontend-config" >"$smoke_dir/frontend-config.json"
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' -X PUT \
  -d "$frontend_config_payload" "$base_url/api/v1/admin/frontend-config" >"$smoke_dir/frontend-config-updated.json"
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/overview" >"$smoke_dir/overview.json"
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/scan-runs?page_size=2" >"$smoke_dir/runs.json"
curl -fsS -H "$auth_header" "$base_url/api/v1/inventory/tree" >"$smoke_dir/tree.json"
curl -fsS -F "file=@$bootstrap_dir/room_meters.json" -H "$auth_header" \
  "$base_url/api/v1/admin/inventory/imports/validate" >"$smoke_dir/validation.json"

mapfile -t auth_meters < <(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
  'select meter_no from meters where active and not excluded order by building,floor,room,meter_no limit 3')
if [[ "${#auth_meters[@]}" -ne 3 ]]; then
  echo 'authentication smoke requires three eligible meters' >&2
  exit 1
fi
meter=${auth_meters[0]}
second_meter=${auth_meters[1]}
third_meter=${auth_meters[2]}
mailpit_base="http://127.0.0.1:$mailpit_http_port"
cookie_jar="$smoke_dir/cookies.txt"
old_cookie_jar="$smoke_dir/old-cookies.txt"

# 从 Mailpit 查询最近邮件的 6 位验证码。
# 注册、找回密码与换绑邮箱共用此函数。
fetch_mail_code() {
  local want_subj_hint="${1:-}"
  python - "$mailpit_base" "$smoke_dir" "$want_subj_hint" <<'PY'
import json, re, sys, urllib.request, time
base, out, hint = sys.argv[1], sys.argv[2], sys.argv[3]
body = ""
for _ in range(30):
    data = json.load(urllib.request.urlopen(base + "/api/v1/messages"))
    msgs = data.get("messages") or []
    if msgs:
        # 邮件列表最新在前。
        # 如果提供主题 hint，则选取匹配邮件。
        chosen = msgs[0]
        if hint:
            for m in msgs:
                subj = (m.get("Subject") or "")
                if hint in subj:
                    chosen = m
                    break
        mid = chosen["ID"]
        msg = json.load(urllib.request.urlopen(base + f"/api/v1/message/{mid}"))
        body = (msg.get("Text") or "") + "\n" + (msg.get("HTML") or "")
        open(out + "/last-mail.html", "w", encoding="utf-8").write(body)
        m = re.search(r"(?:CODE|验证码)[^\d]{0,120}(\d{6})", body, re.I) or re.search(r"\b(\d{6})\b", body)
        if m:
            print(m.group(1))
            raise SystemExit(0)
    time.sleep(0.25)
raise SystemExit("no 6-digit code in mailpit")
PY
}

# 认证端点有 IP 限流：10 burst / 2s。
# 如果返回 429，则按 Retry-After 退避重试。
auth_curl() {
  # 用法：auth_curl [curl 参数...]
  # 自动重试 429，最多 8 次。
  local attempt=0
  local code bodyfile
  bodyfile=$(mktemp)
  while true; do
    attempt=$((attempt + 1))
    code=$(curl -sS -o "$bodyfile" -w '%{http_code}' "$@") || true
    if [[ "$code" != 429 ]]; then
      if [[ "$code" -ge 400 ]]; then
        echo "auth_curl HTTP $code for: $*" >&2
        sed -n '1,20p' "$bodyfile" >&2 || true
        rm -f "$bodyfile"
        return 22
      fi
      cat "$bodyfile"
      rm -f "$bodyfile"
      return 0
    fi
    if (( attempt >= 8 )); then
      echo "auth_curl still 429 after $attempt tries: $*" >&2
      sed -n '1,20p' "$bodyfile" >&2 || true
      rm -f "$bodyfile"
      return 22
    fi
    sleep 2
  done
}

register_with_code() {
  local email="$1" password="$2" nickname="$3" meter_no="${4:-}"
  auth_curl -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\"}" "$base_url/api/v1/auth/register/code" >/dev/null
  local code
  code=$(fetch_mail_code "验证码")
  local payload
  payload=$(python - "$email" "$password" "$nickname" "$code" "$meter_no" <<'PY'
import json, sys
email, password, nickname, code, meter = sys.argv[1:6]
body = {"email": email, "password": password, "nickname": nickname, "code": code}
if meter:
    body["meter"] = meter
print(json.dumps(body))
PY
)
  auth_curl -c "$cookie_jar" -H 'Content-Type: application/json' -d "$payload" \
    "$base_url/api/v1/auth/register"
}

register_with_code "student@example.test" "correct horse battery staple" "测试同学" "$meter" \
  >"$smoke_dir/register.json"

# 已注册邮箱必须在发码步骤返回冲突。
# 禁止等到最终注册提交才提示。
existing_code_body="$smoke_dir/existing-register-code.json"
existing_code_status=""
for attempt in 1 2 3 4 5 6 7 8; do
  existing_code_status=$(curl -sS -o "$existing_code_body" -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -d '{"email":"student@example.test"}' \
    "$base_url/api/v1/auth/register/code")
  [[ "$existing_code_status" != 429 ]] && break
  sleep 2
done
if [[ "$existing_code_status" != 409 ]] || ! grep -q 'email_exists' "$existing_code_body"; then
  echo "expected existing email registration-code request to return 409 email_exists, got $existing_code_status" >&2
  sed -n '1,20p' "$existing_code_body" >&2 || true
  exit 1
fi

# 一块宿舍电表最多绑定 4 个账号。
# 测试库直接预置 2 至 4 号槽。
# 禁止辅助注册消耗认证限流额度。
# 第 5 个账号走真实接口验证容量冲突。
for roommate_slot in 2 3 4; do
  docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -v ON_ERROR_STOP=1 -q -c "
    WITH created AS (
      INSERT INTO user_accounts (id,email,password_hash,nickname,status,email_verified_at)
      SELECT gen_random_uuid(),'roommate-${roommate_slot}@example.test',password_hash,'roommate-${roommate_slot}','active',now()
      FROM user_accounts WHERE email='student@example.test'
      RETURNING id
    )
    INSERT INTO user_meter_bindings (user_id,meter_id,slot)
    SELECT created.id,meters.id,${roommate_slot}
    FROM created CROSS JOIN meters WHERE meters.meter_no='${meter}'
  " >/dev/null
done
auth_curl -X POST -H 'Content-Type: application/json' \
  -d '{"email":"overflow-roommate@example.test"}' "$base_url/api/v1/auth/register/code" >/dev/null
overflow_code=$(fetch_mail_code "验证码")
overflow_register_payload=$(python - "$meter" "$overflow_code" <<'PY'
import json, sys
print(json.dumps({
  "email": "overflow-roommate@example.test",
  "password": "another correct password",
  "nickname": "overflow-roommate",
  "code": sys.argv[2],
  "meter": sys.argv[1],
}))
PY
)
exclusive_meter_status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  -d "$overflow_register_payload" "$base_url/api/v1/auth/register")
# 409 表示业务冲突。
# 如果返回 429，则重试一次。
if [[ "$exclusive_meter_status" == 429 ]]; then
  sleep 2
  exclusive_meter_status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d "$overflow_register_payload" "$base_url/api/v1/auth/register")
fi
if [[ "$exclusive_meter_status" != 409 ]]; then
  echo "expected a fifth account binding the same meter to return 409, got $exclusive_meter_status" >&2
  exit 1
fi
access_token=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["access_token"])' "$smoke_dir/register.json")
curl -fsS -H "Authorization: Bearer $access_token" "$base_url/api/v1/me" >"$smoke_dir/me.json"
curl -fsS -H "Authorization: Bearer $access_token" "$base_url/api/v1/me/overview" >"$smoke_dir/me-overview.json"
cp "$cookie_jar" "$old_cookie_jar"
auth_curl -b "$cookie_jar" -c "$cookie_jar" -X POST \
  "$base_url/api/v1/auth/refresh" >"$smoke_dir/refresh.json"
refreshed_access=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["access_token"])' "$smoke_dir/refresh.json")
curl -fsS -H "Authorization: Bearer $refreshed_access" "$base_url/api/v1/me" >"$smoke_dir/refreshed-me.json"
refresh_replay_status=$(curl -sS -o /dev/null -w '%{http_code}' -b "$old_cookie_jar" -X POST \
  "$base_url/api/v1/auth/refresh")
if [[ "$refresh_replay_status" != 401 ]]; then
  echo "expected refresh replay to return 401, got $refresh_replay_status" >&2
  exit 1
fi
login_payload='{"email":"student@example.test","password":"correct horse battery staple"}'
auth_curl -c "$cookie_jar" -H 'Content-Type: application/json' -d "$login_payload" \
  "$base_url/api/v1/auth/login" >"$smoke_dir/login.json"
login_access=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["access_token"])' "$smoke_dir/login.json")

# 找回密码：发码、改密、旧密码失败、新密码成功。
auth_curl -X POST -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test"}' "$base_url/api/v1/auth/password/forgot" >/dev/null
# 已注册邮箱的重复请求必须返回 204。
# 禁止用 429 暴露账号存在。
repeat_forgot=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test"}' "$base_url/api/v1/auth/password/forgot")
if [[ "$repeat_forgot" == 429 ]]; then sleep 2; repeat_forgot=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test"}' "$base_url/api/v1/auth/password/forgot"); fi
if [[ "$repeat_forgot" != 204 ]]; then
  echo "expected repeated password forgot for existing email to return 204, got $repeat_forgot" >&2
  exit 1
fi
# 未注册邮箱也必须返回 204。
# 禁止暴露账号是否存在。
unknown_forgot=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -d '{"email":"missing@example.test"}' "$base_url/api/v1/auth/password/forgot")
if [[ "$unknown_forgot" == 429 ]]; then sleep 2; unknown_forgot=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -d '{"email":"missing@example.test"}' "$base_url/api/v1/auth/password/forgot"); fi
if [[ "$unknown_forgot" != 204 ]]; then
  echo "expected password forgot for unknown email to return 204, got $unknown_forgot" >&2
  exit 1
fi
reset_code=$(fetch_mail_code "找回密码")
# 找回邮件仅提供验证码。
# 禁止展示站点重置链接。
python - "$smoke_dir/last-mail.html" <<'PY'
import sys
html = open(sys.argv[1], encoding="utf-8").read()
if "RESET LINK" in html or "reset=1" in html or "reset=true" in html:
    raise SystemExit("password-reset mail still contains the removed reset link")
PY
auth_curl -X POST -H 'Content-Type: application/json' \
  -d "{\"email\":\"student@example.test\",\"code\":\"$reset_code\",\"password\":\"brand new password 99\"}" \
  "$base_url/api/v1/auth/password/reset" >/dev/null
old_access_after_reset=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $login_access" \
  "$base_url/api/v1/me")
if [[ "$old_access_after_reset" != 401 ]]; then
  echo "expected access token revoked by password reset to return 401, got $old_access_after_reset" >&2
  exit 1
fi
old_login_status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test","password":"correct horse battery staple"}' \
  "$base_url/api/v1/auth/login")
if [[ "$old_login_status" == 429 ]]; then sleep 2; old_login_status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test","password":"correct horse battery staple"}' \
  "$base_url/api/v1/auth/login"); fi
if [[ "$old_login_status" != 401 ]]; then
  echo "expected old password to fail after reset, got $old_login_status" >&2
  exit 1
fi
auth_curl -c "$cookie_jar" -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test","password":"brand new password 99"}' \
  "$base_url/api/v1/auth/login" >"$smoke_dir/login-after-reset.json"
login_access=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["access_token"])' "$smoke_dir/login-after-reset.json")

# 账号设置：改昵称、改密码、榜单 mask_room。
# 改密码必须吊销会话。
# 昵称使用 ASCII。
# Windows 控制台编码会损坏 curl -d 中文。
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d '{"nickname":"new-nick"}' "$base_url/api/v1/me/profile" >"$smoke_dir/profile.json"
python -c 'import json,sys; u=json.load(open(sys.argv[1],encoding="utf-8")); assert u["nickname"]=="new-nick", u' "$smoke_dir/profile.json"
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d '{"opted_in":true,"show_building":true,"show_floor":true,"show_room":true,"show_nickname":false,"mask_building":true,"mask_floor":true,"mask_room":true}' \
  "$base_url/api/v1/me/leaderboard" >"$smoke_dir/leaderboard-pref.json"
python -c 'import json,sys; p=json.load(open(sys.argv[1],encoding="utf-8")); assert p.get("mask_room") is True and p.get("show_room") is True, p' \
  "$smoke_dir/leaderboard-pref.json"
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d '{"low_balance_alert":true,"threshold_yuan":"10","scheduled_digest":true,"period":"daily","push_time":"08:00"}' \
  "$base_url/api/v1/me/notification-settings" >"$smoke_dir/notif-settings.json"
# 未验证的第三方地址禁止写入推送渠道。
unverified_mail_status=$(curl -sS -o "$smoke_dir/unverified-mail.json" -w '%{http_code}' \
  -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d '{"enabled":false,"config":{"to":["student@example.test","recipient@example.test"]}}' \
  "$base_url/api/v1/me/channels/mail")
if [[ "$unverified_mail_status" != 409 ]] || ! grep -q 'email_not_verified' "$smoke_dir/unverified-mail.json"; then
  echo "expected unverified mail recipient to be rejected, got $unverified_mail_status" >&2
  exit 1
fi
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X POST \
  -d '{"email":"recipient@example.test"}' \
  "$base_url/api/v1/me/channels/mail/recipient/code" >/dev/null
recipient_code=$(fetch_mail_code "验证推送收件邮箱")
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X POST \
  -d "{\"email\":\"recipient@example.test\",\"code\":\"$recipient_code\"}" \
  "$base_url/api/v1/me/channels/mail/recipient/verify" >"$smoke_dir/verified-mail.json"
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d '{"enabled":false,"config":{"to":["student@example.test","recipient@example.test"]}}' \
  "$base_url/api/v1/me/channels/mail" >"$smoke_dir/channel-mail.json"
# 测试发送与启用开关解耦。
# 关闭的渠道仍可验证已保存凭据。
curl -fsS -H "Authorization: Bearer $login_access" -X POST \
  "$base_url/api/v1/me/channels/mail/test" >"$smoke_dir/channel-mail-test-disabled.json"
python - "$mailpit_base" "$meter" <<'PY'
import json, sys, time, urllib.request

base, meter = sys.argv[1:]
for _ in range(30):
    messages = json.load(urllib.request.urlopen(base + "/api/v1/messages")).get("messages") or []
    selected = next((item for item in messages if "POWER·PUSH 测试" in (item.get("Subject") or "")), None)
    if selected:
        message = json.load(urllib.request.urlopen(base + "/api/v1/message/" + selected["ID"]))
        body = (message.get("Text") or "") + "\n" + (message.get("HTML") or "")
        for required in ("剩余电费：", "数据更新时间：", "宿舍位置：", "电表号：" + meter):
            assert required in body, (required, body)
        break
    time.sleep(0.25)
else:
    raise SystemExit("structured push-test mail was not captured")
PY
# 改密吊销全部 refresh family。
# 现有 access JWT 必须立即失效。
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d '{"current_password":"brand new password 99","new_password":"final password value 1"}' \
  "$base_url/api/v1/me/password" >/dev/null
access_after_pw=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $login_access" \
  "$base_url/api/v1/me")
if [[ "$access_after_pw" != 401 ]]; then
  echo "expected access token revoked after password change to return 401, got $access_after_pw" >&2
  exit 1
fi
sleep 2  # 等待限流窗口。禁止将 429 判为未吊销。
refresh_after_pw=$(curl -sS -o /dev/null -w '%{http_code}' -b "$cookie_jar" -X POST \
  "$base_url/api/v1/auth/refresh")
if [[ "$refresh_after_pw" == 429 ]]; then
  sleep 2
  refresh_after_pw=$(curl -sS -o /dev/null -w '%{http_code}' -b "$cookie_jar" -X POST \
    "$base_url/api/v1/auth/refresh")
fi
if [[ "$refresh_after_pw" != 401 ]]; then
  echo "expected refresh revoked after password change, got $refresh_after_pw" >&2
  exit 1
fi
auth_curl -c "$cookie_jar" -H 'Content-Type: application/json' \
  -d '{"email":"student@example.test","password":"final password value 1"}' \
  "$base_url/api/v1/auth/login" >"$smoke_dir/login-final.json"
login_access=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["access_token"])' "$smoke_dir/login-final.json")

# 预置满另一块表的 4 个槽位。
# 验证换绑满员电表返回 409。
# 测试夹具禁止走注册接口。
# 禁止覆盖主账号 cookie。
# 禁止消耗后续断言的限流额度。
for holder_slot in 1 2 3 4; do
  docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -v ON_ERROR_STOP=1 -q -c "
    WITH created AS (
      INSERT INTO user_accounts (id,email,password_hash,nickname,status,email_verified_at)
      SELECT gen_random_uuid(),'holder-${holder_slot}@example.test',password_hash,'holder-${holder_slot}','active',now()
      FROM user_accounts WHERE email='student@example.test'
      RETURNING id
    )
    INSERT INTO user_meter_bindings (user_id,meter_id,slot)
    SELECT created.id,meters.id,${holder_slot}
    FROM created CROSS JOIN meters WHERE meters.meter_no='${second_meter}'
  " >/dev/null
done
rebind_conflict_status=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $login_access" \
  -H 'Content-Type: application/json' -X PUT -d "{\"meter\":\"$second_meter\"}" \
  "$base_url/api/v1/me/meter")
if [[ "$rebind_conflict_status" != 409 ]]; then
  echo "expected rebind to another account's meter to return 409, got $rebind_conflict_status" >&2
  exit 1
fi
curl -fsS -H "Authorization: Bearer $login_access" -H 'Content-Type: application/json' -X PUT \
  -d "{\"meter\":\"$third_meter\"}" "$base_url/api/v1/me/meter" >"$smoke_dir/rebind.json"
curl -fsS -H "Authorization: Bearer $login_access" "$base_url/api/v1/me" >"$smoke_dir/rebind-me.json"
# 换绑后 me 必须含 building/floor/room。
# 前端使用结构字段拼展示。
# 禁止再反解析 place。
python -c 'import json,sys; u=json.load(open(sys.argv[1],encoding="utf-8")); m=u.get("meter") or {}; assert m.get("building") and m.get("room") is not None, m' \
  "$smoke_dir/rebind-me.json"
active_user_bindings=$(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
  "select count(*) from user_meter_bindings b join user_accounts u on u.id=b.user_id where u.email='student@example.test' and b.unbound_at is null")
if [[ "$active_user_bindings" != 1 ]]; then
  echo "expected exactly one active binding after rebind, got $active_user_bindings" >&2
  exit 1
fi
auth_curl -b "$cookie_jar" -X POST "$base_url/api/v1/auth/logout" >/dev/null || true
access_after_logout=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $login_access" \
  "$base_url/api/v1/me")
if [[ "$access_after_logout" != 401 ]]; then
  echo "expected access token revoked by logout to return 401, got $access_after_logout" >&2
  exit 1
fi
curl -fsS -H "$auth_header" "$base_url/api/v1/meters/$meter/overview" >"$smoke_dir/meter-overview.json"
curl -fsS -G -H "$auth_header" \
  --data-urlencode 'from=2020-01-01T00:00:00+08:00' \
  --data-urlencode "to=$(python -c 'from datetime import datetime,timezone,timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat())')" \
  --data-urlencode 'granularity=month' --data-urlencode 'metric=balance' \
  "$base_url/api/v1/meters/$meter/series" >"$smoke_dir/meter-series.json"
curl -fsS -G -H "$auth_header" \
  --data-urlencode 'from=2020-01-01T00:00:00+08:00' \
  --data-urlencode "to=$(python -c 'from datetime import datetime,timezone,timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat())')" \
  "$base_url/api/v1/campus/summary" >"$smoke_dir/campus-summary.json"
curl -fsS -G -H "$auth_header" \
  --data-urlencode 'from=2020-01-01T00:00:00+08:00' \
  --data-urlencode "to=$(python -c 'from datetime import datetime,timezone,timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat())')" \
  --data-urlencode 'granularity=month' \
  "$base_url/api/v1/campus/series" >"$smoke_dir/campus-series.json"
curl -fsS -H "$auth_header" \
  -D "$smoke_dir/ranking-first.headers" \
  "$base_url/api/v1/campus/rankings?period=month&mode=usage&limit=5" >"$smoke_dir/ranking.json"
curl -fsS -H "$auth_header" \
  -D "$smoke_dir/ranking-second.headers" \
  "$base_url/api/v1/campus/rankings?period=month&mode=usage&limit=5" >/dev/null
tr -d '\r' <"$smoke_dir/ranking-first.headers" | grep -qi '^X-Ranking-Cache: MISS$'
tr -d '\r' <"$smoke_dir/ranking-second.headers" | grep -qi '^X-Ranking-Cache: HIT$'
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"limit":1,"dry_run":true}' "$base_url/api/v1/admin/scan-runs" >"$smoke_dir/dry-run.json"
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"limit":1}' "$base_url/api/v1/admin/scan-runs" >"$smoke_dir/new-run.json"

run_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["id"])' "$smoke_dir/new-run.json")
scan_status="pending"
for _ in $(seq 1 60); do
  curl -fsS -H "$auth_header" "$base_url/api/v1/admin/scan-runs/$run_id" >"$smoke_dir/completed.json"
  scan_status=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["status"])' "$smoke_dir/completed.json")
  if [[ "$scan_status" != pending && "$scan_status" != running ]]; then
    break
  fi
  sleep 0.5
done
rollup_count=0
for _ in $(seq 1 30); do
  rollup_count=$(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
    'select count(*) from consumption_rollups')
  if (( rollup_count > 0 )); then
    break
  fi
  sleep 0.25
done
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/scan-runs/$run_id/results?page_size=5" >"$smoke_dir/results.json"
legacy_run_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["items"][0]["id"])' "$smoke_dir/runs.json")
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' -d '{"statuses":["parse_error"]}' \
  "$base_url/api/v1/admin/scan-runs/$legacy_run_id/retry" >"$smoke_dir/retry-run.json"
retry_run_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["id"])' "$smoke_dir/retry-run.json")
for _ in $(seq 1 60); do
  curl -fsS -H "$auth_header" "$base_url/api/v1/admin/scan-runs/$retry_run_id" >"$smoke_dir/retry-completed.json"
  retry_status=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["status"])' "$smoke_dir/retry-completed.json")
  if [[ "$retry_status" != pending && "$retry_status" != running ]]; then
    break
  fi
  sleep 0.5
done
curl -fsS -G -H "$auth_header" \
  --data-urlencode 'from=2020-01-01T00:00:00+08:00' \
  --data-urlencode "to=$(python -c 'from datetime import datetime,timezone,timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat())')" \
  --data-urlencode 'granularity=month' \
  "$base_url/api/v1/campus/series" >"$smoke_dir/campus-series-after-scan.json"
curl -fsS -H "$auth_header" \
  "$base_url/api/v1/campus/rankings?period=month&mode=usage&limit=5" >"$smoke_dir/ranking-after-scan.json"
docker exec -i -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
WITH chosen AS (SELECT id FROM meters WHERE active AND NOT excluded ORDER BY meter_no LIMIT 1)
INSERT INTO anomaly_events (type,severity,meter_id,payload,detected_at)
SELECT 'negative_reset','critical',id,'{}'::jsonb,now()-interval '1 day' FROM chosen
UNION ALL
SELECT 'stale_reading','warning',id,'{}'::jsonb,now() FROM chosen;
SQL
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/anomalies?acknowledged=false&page_size=100" >"$smoke_dir/anomalies.json"
python - "$smoke_dir/anomalies.json" <<'PY'
import json
import sys

items = json.load(open(sys.argv[1], encoding="utf-8"))["items"]
priority = {"critical": 0, "warning": 1, "info": 2}
ranks = [priority[item["severity"]] for item in items]
assert ranks == sorted(ranks), "anomalies are not ordered by severity"
assert any(item["type"] == "negative_reset" and item["severity"] == "critical" for item in items)
PY
curl -fsS -H "$auth_header" "$base_url/api/v1/campus/hourly-heatmap" >"$smoke_dir/heatmap.json"
curl -fsS "$base_url/openapi.yaml" >"$smoke_dir/openapi.yaml"
admin_status=$(curl -sS -o /dev/null -w '%{http_code}' "$base_url/admin/")
mail_status=$(curl -sS -o /dev/null -w '%{http_code}' -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"recipient":"admin@example.com"}' "$base_url/api/v1/admin/mail/test")
if [[ "$mail_status" != 202 ]]; then
  echo "expected Mailpit-backed mail endpoint to return 202, got $mail_status" >&2
  exit 1
fi
mailpit_messages=0
for _ in $(seq 1 20); do
  curl -fsS "http://127.0.0.1:$mailpit_http_port/api/v1/messages" >"$smoke_dir/mailpit.json"
  mailpit_messages=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8")).get("total",0))' "$smoke_dir/mailpit.json")
  if (( mailpit_messages >= 1 )); then
    break
  fi
  sleep 0.25
done
if (( mailpit_messages < 1 )); then
  echo 'Mailpit did not capture the test message' >&2
  exit 1
fi

frontend_config_disabled='{"features":{"auth":{"email_login":true,"sms_login":false,"email_code":false,"sms_code":false,"registration":false},"channels":{"mail":true,"sms":true,"dingtalk":true,"wecom":true,"wecom_webhook":true,"feishu":true,"lark":true,"discord":true,"webhook":true,"bark":true,"gotify":true,"whatsapp":true,"pushplus":true,"serverchan_turbo":true,"serverchan3":true,"mp":true,"qq":true,"napcat":true,"telegram":true},"channel_coming_soon":{"mail":false,"sms":true,"dingtalk":false,"wecom":false,"wecom_webhook":false,"feishu":false,"lark":false,"discord":false,"webhook":false,"bark":false,"gotify":false,"whatsapp":false,"pushplus":false,"serverchan_turbo":false,"serverchan3":false,"mp":false,"qq":false,"napcat":false,"telegram":false},"channel_order":["qq","napcat","webhook","bark","gotify","whatsapp","serverchan_turbo","serverchan3","mail","dingtalk","lark","wecom","wecom_webhook","feishu","discord","pushplus","mp","telegram","sms"]},"display":{"campus_name":"示例校区","area_name":"示例大学","ranking_refresh_time":"09:00"}}'
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' -X PUT \
  -d "$frontend_config_disabled" "$base_url/api/v1/admin/frontend-config" >/dev/null
# 注册关闭时，发码接口必须返回 403。
# 禁止再凑验证码请求。
registration_disabled_status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"email":"blocked@example.test"}' "$base_url/api/v1/auth/register/code")
if [[ "$registration_disabled_status" == 429 ]]; then
  sleep 2
  registration_disabled_status=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d '{"email":"blocked@example.test"}' "$base_url/api/v1/auth/register/code")
fi
if [[ "$registration_disabled_status" != 403 ]]; then
  echo "expected disabled registration code endpoint to return 403, got $registration_disabled_status" >&2
  exit 1
fi
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' -X PUT \
  -d "$frontend_config_payload" "$base_url/api/v1/admin/frontend-config" >/dev/null

# 验证新 Worker 恢复已中断的扫描。
# 禁止中断扫描长期滞留。
# 夹具仅冻结一块电表。
# 使用真实 PostgreSQL claim 与上游路径。
if [[ -n "$worker_pid" ]] && kill -0 "$worker_pid" 2>/dev/null; then
  kill "$worker_pid" 2>/dev/null || true
  wait "$worker_pid" 2>/dev/null || true
fi
worker_pid=""
active_scan_count=$(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
  "select count(*) from scan_runs where status in ('pending','running')")
if [[ "$active_scan_count" != 0 ]]; then
  echo "cannot prepare recovery fixture while $active_scan_count scan is active" >&2
  exit 1
fi
recovery_run_id=$(docker exec -i -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atq <<'SQL'
WITH chosen AS (
  SELECT id FROM meters WHERE active AND NOT excluded ORDER BY meter_no LIMIT 1
), created AS (
  INSERT INTO scan_runs (
    trigger,status,scope,config_snapshot,inventory_total,eligible_total,error_message
  )
  SELECT 'recovery','interrupted',jsonb_build_object('limit',1),
    jsonb_build_object('qps',10,'concurrency',1),
    (SELECT count(*) FROM meters),1,'smoke recovery fixture'
  RETURNING id
)
INSERT INTO scan_run_meters (run_id,meter_id,ordinal)
SELECT created.id,chosen.id,1 FROM created CROSS JOIN chosen
RETURNING run_id::text;
SQL
)
if [[ -z "$recovery_run_id" ]]; then
  echo 'failed to create interrupted recovery fixture' >&2
  exit 1
fi
DATABASE_URL="$database_url" SCAN_CRON='' MAIL_PROVIDER='' \
  "$worker_binary" >>"$smoke_dir/worker.stdout.log" 2>>"$smoke_dir/worker.stderr.log" &
worker_pid=$!
recovery_status="interrupted"
recovery_processed=0
recovery_results=0
for _ in $(seq 1 60); do
  recovery_row=$(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
    "select status||'|'||processed_total||'|'||(select count(*) from scan_results where run_id='$recovery_run_id'::uuid) from scan_runs where id='$recovery_run_id'::uuid")
  IFS='|' read -r recovery_status recovery_processed recovery_results <<<"$recovery_row"
  if [[ "$recovery_status" != interrupted && "$recovery_status" != running ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$recovery_status" != completed && "$recovery_status" != completed_with_errors ]]; then
  sed -n '1,120p' "$smoke_dir/worker.stderr.log" >&2
  echo "interrupted scan was not resumed to a terminal state: $recovery_status" >&2
  exit 1
fi
if [[ "$recovery_processed" != 1 || "$recovery_results" != 1 ]]; then
  echo "recovered scan counters are inconsistent: processed=$recovery_processed results=$recovery_results" >&2
  exit 1
fi

# 使用一块合格电表验证生产账单路径。
# 验证请求限流与全部可见月份。
# 验证规范写入与按月观测。
# 禁止把冒烟变成批量爬取。
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"limit":1,"dry_run":true,"qps":30,"concurrency":1}' \
  "$base_url/api/v1/admin/bill-runs" >"$smoke_dir/bill-dry-run.json"
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"limit":1,"qps":30,"concurrency":1}' \
  "$base_url/api/v1/admin/bill-runs" >"$smoke_dir/bill-created.json"
bill_run_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["id"])' "$smoke_dir/bill-created.json")
bill_status="pending"
for _ in $(seq 1 120); do
  curl -fsS -H "$auth_header" "$base_url/api/v1/admin/bill-runs/$bill_run_id" >"$smoke_dir/bill-completed.json"
  bill_status=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["status"])' "$smoke_dir/bill-completed.json")
  if [[ "$bill_status" != pending && "$bill_status" != running ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$bill_status" != completed && "$bill_status" != completed_with_errors ]]; then
  sed -n '1,160p' "$smoke_dir/api.stderr.log" >&2
  echo "bill run did not reach a terminal state: $bill_status" >&2
  exit 1
fi
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/bill-runs/$bill_run_id/results?page_size=10" >"$smoke_dir/bill-results.json"
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/bill-runs?page_size=2" >"$smoke_dir/bill-runs.json"
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/overview" >"$smoke_dir/overview.json"
bill_observations=$(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
  "select count(*) from monthly_bill_observations where run_id='$bill_run_id'::uuid")
bill_canonical=$(docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -Atc \
  "select count(*) from monthly_bills b join bill_run_meters rm on rm.meter_id=b.meter_id where rm.run_id='$bill_run_id'::uuid")

# 破坏一条可丢弃的规范账单行。
# 然后对同一冻结电表再对账。
# 上游值必须恢复该行。
# 必须写出可审计修订记录。
docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -v ON_ERROR_STOP=1 -q -c "
  UPDATE monthly_bills SET cost_yuan=cost_yuan+10
  WHERE id=(SELECT b.id FROM monthly_bills b JOIN bill_run_meters rm ON rm.meter_id=b.meter_id
    WHERE rm.run_id='$bill_run_id'::uuid ORDER BY b.month LIMIT 1)" >/dev/null
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"limit":1,"qps":30,"concurrency":1}' \
  "$base_url/api/v1/admin/bill-runs" >"$smoke_dir/bill-reconcile-created.json"
bill_reconcile_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["id"])' "$smoke_dir/bill-reconcile-created.json")
bill_reconcile_status="pending"
for _ in $(seq 1 120); do
  curl -fsS -H "$auth_header" "$base_url/api/v1/admin/bill-runs/$bill_reconcile_id" >"$smoke_dir/bill-reconcile-completed.json"
  bill_reconcile_status=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["status"])' "$smoke_dir/bill-reconcile-completed.json")
  if [[ "$bill_reconcile_status" != pending && "$bill_reconcile_status" != running ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$bill_reconcile_status" != completed && "$bill_reconcile_status" != completed_with_errors ]]; then
  echo "bill reconciliation did not reach a terminal state: $bill_reconcile_status" >&2
  exit 1
fi
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/bill-revisions?acknowledged=false&page_size=10" >"$smoke_dir/bill-revisions.json"
revision_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["items"][0]["id"])' "$smoke_dir/bill-revisions.json")
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' -X PATCH \
  -d '{"acknowledged":true,"note":"smoke verified"}' \
  "$base_url/api/v1/admin/bill-revisions/$revision_id" >"$smoke_dir/bill-revision-ack.json"

# 将可丢弃父结果标为 partial。
# 验证仅管理端重试路由。
# 必须创建可审计子运行。
# 子运行仅含该失败电表。
docker exec -e "PGPASSWORD=$test_password" "$smoke_id" psql -U edu_power -d edu_power -v ON_ERROR_STOP=1 -q -c \
  "UPDATE bill_results SET status='partial',error_code='smoke_fixture',error_message='retry fixture' WHERE run_id='$bill_reconcile_id'::uuid" >/dev/null
curl -fsS -H "$auth_header" -H 'Content-Type: application/json' \
  -d '{"statuses":["partial"],"qps":30,"concurrency":1}' \
  "$base_url/api/v1/admin/bill-runs/$bill_reconcile_id/retry" >"$smoke_dir/bill-retry-created.json"
bill_retry_id=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["id"])' "$smoke_dir/bill-retry-created.json")
bill_retry_status="pending"
for _ in $(seq 1 120); do
  curl -fsS -H "$auth_header" "$base_url/api/v1/admin/bill-runs/$bill_retry_id" >"$smoke_dir/bill-retry-completed.json"
  bill_retry_status=$(python -c 'import json,sys; print(json.load(open(sys.argv[1],encoding="utf-8"))["status"])' "$smoke_dir/bill-retry-completed.json")
  if [[ "$bill_retry_status" != pending && "$bill_retry_status" != running ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$bill_retry_status" != completed && "$bill_retry_status" != completed_with_errors ]]; then
  echo "bill retry child did not reach a terminal state: $bill_retry_status" >&2
  exit 1
fi
curl -fsS -H "$auth_header" "$base_url/api/v1/admin/overview" >"$smoke_dir/overview.json"

python - "$smoke_dir" "$unauthorized_status" "$admin_status" "$mail_status" "$rollup_count" "$refresh_replay_status" "$mailpit_messages" "$exclusive_meter_status" "$registration_disabled_status" "$recovery_status" "$recovery_processed" "$rebind_conflict_status" "$third_meter" "$bill_observations" "$bill_canonical" <<'PY'
import json
import os
import sys
import calendar
from datetime import date

root, unauthorized, admin_status, mail_status, rollup_count, refresh_replay_status, mailpit_messages, exclusive_meter_status, registration_disabled_status, recovery_status, recovery_processed, rebind_conflict_status, third_meter, bill_observations, bill_canonical = sys.argv[1:]
def load(name):
    with open(os.path.join(root, name), encoding="utf-8") as handle:
        return json.load(handle)

live = load("live.json")
ready = load("ready.json")
overview = load("overview.json")
runs = load("runs.json")
tree = load("tree.json")
validation = load("validation.json")
meter_overview = load("meter-overview.json")
meter_series = load("meter-series.json")
campus_summary = load("campus-summary.json")
campus_series = load("campus-series.json")
ranking = load("ranking.json")
campus_series_after_scan = load("campus-series-after-scan.json")
ranking_after_scan = load("ranking-after-scan.json")
dry_run = load("dry-run.json")
completed = load("completed.json")
retry_completed = load("retry-completed.json")
results = load("results.json")
heatmap = load("heatmap.json")
registered = load("register.json")
me = load("me.json")
me_overview = load("me-overview.json")
frontend_config = load("frontend-config.json")
frontend_config_updated = load("frontend-config-updated.json")
rebind_me = load("rebind-me.json")
bill_dry_run = load("bill-dry-run.json")
bill_completed = load("bill-completed.json")
bill_results = load("bill-results.json")
bill_runs = load("bill-runs.json")
bill_reconcile = load("bill-reconcile-completed.json")
bill_retry = load("bill-retry-completed.json")
bill_revisions = load("bill-revisions.json")
bill_revision_ack = load("bill-revision-ack.json")

assert overview["version"] == "version_1"
assert overview["migrations"] == "ready"
assert overview["worker"] == "ready"
assert overview["mail_provider"] == "smtp"
assert overview["mail_configured"] is True
assert overview["unacknowledged_critical_anomalies"] >= 1
assert overview["scan_schedule"]["qps"] > 0
assert overview["scan_schedule"]["concurrency"] > 0
assert overview["bill_schedule"]["mode"] == "visible_months"
assert overview["latest_bill_run"]["id"] == bill_retry["id"]
# 仅校验 version 单调递增。
# 仅校验 sms_login、channels、display。
assert int(frontend_config["version"]) >= 1
assert frontend_config["features"]["auth"]["sms_login"] is False
assert frontend_config["features"]["channels"]["mail"] is True
assert frontend_config["features"]["channel_coming_soon"]["sms"] is True
assert frontend_config_updated["features"]["channel_order"][:5] == ["qq", "napcat", "webhook", "bark", "gotify"]
assert frontend_config["display"]["campus_name"] == "示例校区"
assert frontend_config["display"]["ranking_refresh_time"] == "09:00"
assert frontend_config["updated_at"]
assert int(frontend_config_updated["version"]) == int(frontend_config["version"]) + 1
# 榜单必须下发两个完整自然月。
# 榜单必须下发 09:00 刷新元数据。
for payload in (ranking, ranking_after_scan):
    current = payload["current_period"]
    previous = payload["previous_period"]
    current_from, current_to = date.fromisoformat(current["from"]), date.fromisoformat(current["to"])
    previous_from, previous_to = date.fromisoformat(previous["from"]), date.fromisoformat(previous["to"])
    assert current_from.day == previous_from.day == 1
    assert current_to.day == calendar.monthrange(current_to.year, current_to.month)[1]
    assert previous_to.day == calendar.monthrange(previous_to.year, previous_to.month)[1]
    assert date.fromisoformat(previous["to"]).toordinal() + 1 == date.fromisoformat(current["from"]).toordinal()
    assert payload["updated_at"].endswith("09:00:00+08:00")
    assert payload["next_update_at"].endswith("09:00:00+08:00")
assert rebind_me["meter"]["meter"] == third_meter
assert rebind_me["meter"].get("building")
assert "floor" in rebind_me["meter"] and "room" in rebind_me["meter"]
# 产品契约：校验找回密码与账号设置结果。
profile = load("profile.json")
assert profile["nickname"] == "new-nick"
lb = load("leaderboard-pref.json")
assert lb.get("mask_room") is True
assert lb.get("show_room") is True
notif = load("notif-settings.json")
assert notif["period"] == "daily"
channel_mail = load("channel-mail.json")
channel_mail_test_disabled = load("channel-mail-test-disabled.json")
assert channel_mail["enabled"] is False
assert channel_mail["config"]["to"] == ["student@example.test", "recipient@example.test"]
assert channel_mail_test_disabled["status"] == "ok"
login_after_reset = load("login-after-reset.json")
assert login_after_reset.get("access_token")
assert len(bill_dry_run["months"]) >= 13
assert bill_dry_run["month_requests"] == len(bill_dry_run["months"])
assert bill_dry_run["request_floor"] == len(bill_dry_run["months"]) + 4
assert bill_completed["counters"]["processed_total"] == 1
assert bill_completed["counters"]["month_data_total"] + bill_completed["counters"]["month_no_data_total"] + bill_completed["counters"]["month_partial_total"] + bill_completed["counters"]["month_error_total"] == len(bill_completed["months"])
assert len(bill_results["items"]) == 1
assert len(bill_runs["items"]) >= 1
assert int(bill_observations) == len(bill_completed["months"])
assert int(bill_canonical) >= 1
assert bill_reconcile["counters"]["changed_total"] >= 1
assert bill_retry["trigger"] == "retry"
assert bill_retry["parent_run_id"] == bill_reconcile["id"]
assert bill_retry["counters"]["processed_total"] == 1
assert len(bill_revisions["items"]) >= 1
assert bill_revision_ack["acknowledged"] is True
assert overview["unacknowledged_bill_revisions"] == 0

print(json.dumps({
    "live": live["status"],
    "ready": ready["status"],
    "unauthorized_status": int(unauthorized),
    "inventory_total": overview["inventory"]["inventory_total"],
    "version": overview["version"],
    "migrations": overview["migrations"],
    "worker_heartbeat": overview["worker_heartbeat_at"] is not None,
    "mail_provider": overview["mail_provider"],
    "frontend_config_version": frontend_config_updated["version"],
    "registration_disabled_status": int(registration_disabled_status),
    "recovery_status": recovery_status,
    "recovery_processed": int(recovery_processed),
    "bill_status": bill_completed["status"],
    "bill_months": len(bill_completed["months"]),
    "bill_observations": int(bill_observations),
    "bill_canonical": int(bill_canonical),
    "bill_revisions": bill_reconcile["counters"]["changed_total"],
    "bill_retry_status": bill_retry["status"],
    "tree_campuses": len(tree["campuses"]),
    "run_list_count": len(runs["items"]),
    "inventory_validation_total": validation["diff"]["total"],
    "meter_latest": meter_overview["latest"] is not None,
    "meter_series_points": len(meter_series["points"]),
    "campus_availability": campus_summary["availability"],
    "campus_series_points": len(campus_series["points"]),
    "ranking_items": len(ranking["items"]),
    "dry_run_eligible": dry_run["counters"]["eligible_total"],
    "scan_status": completed["status"],
    "scan_processed": completed["counters"]["processed_total"],
    "scan_result_count": len(results["items"]),
    "retry_trigger": retry_completed["trigger"],
    "retry_processed": retry_completed["counters"]["processed_total"],
    "rollup_count": int(rollup_count),
    "campus_series_after_scan": len(campus_series_after_scan["points"]),
    "ranking_after_scan": len(ranking_after_scan["items"]),
    "heatmap": heatmap["availability"],
    "admin_status": int(admin_status),
    "auth_registered": registered["user"]["email"],
    "auth_me_meter": me["meter"] is not None,
    "auth_me_overview": me_overview["latest"] is not None,
    "exclusive_meter_status": int(exclusive_meter_status),
    "rebind_conflict_status": int(rebind_conflict_status),
    "rebind_meter_updated": True,
    "refresh_replay_status": int(refresh_replay_status),
    "mailpit_status": int(mail_status),
    "mailpit_messages": int(mailpit_messages),
    "openapi_bytes": os.path.getsize(os.path.join(root, "openapi.yaml")),
}, ensure_ascii=False, separators=(",", ":")))
PY
