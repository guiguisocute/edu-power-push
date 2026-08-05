#!/usr/bin/env bash
set -euo pipefail

# Turnstile 密钥失效或上游故障时的 break-glass 通道。
# 仅在服务器本机执行，复用只供机器使用的 ADMIN_TOKEN；不会把令牌交给浏览器。
APP_ROOT="${1:-$HOME/apps/edu-power-push/current}"
ENV_FILE="$APP_ROOT/.env"
[ -f "$ENV_FILE" ] || { echo "找不到 $ENV_FILE" >&2; exit 1; }
command -v jq >/dev/null || { echo "需要 jq" >&2; exit 1; }

API_PORT_VALUE="$(sed -n 's/^API_PORT=//p' "$ENV_FILE" | tail -1)"
API_PORT_VALUE="${API_PORT_VALUE:-8080}"
ADMIN_API_TOKEN="$(sed -n 's/^ADMIN_TOKEN=//p' "$ENV_FILE" | tail -1)"
[ "${#ADMIN_API_TOKEN}" -ge 32 ] || { echo "ADMIN_TOKEN 缺失或过短" >&2; exit 1; }
API_BASE="http://127.0.0.1:$API_PORT_VALUE"

current="$(curl -fsS --max-time 10 -H "Authorization: Bearer $ADMIN_API_TOKEN" \
  "$API_BASE/api/v1/admin/settings/captcha")"
payload="$(jq -c '{settings:(.settings + {provider:"disabled"}),secrets:{}}' <<<"$current")"
curl -fsS --max-time 10 -X PUT \
  -H "Authorization: Bearer $ADMIN_API_TOKEN" -H 'Content-Type: application/json' \
  --data "$payload" "$API_BASE/api/v1/admin/settings/captcha" >/dev/null
public="$(curl -fsS --max-time 10 "$API_BASE/api/v1/captcha/config")"
grep -q '"provider":"disabled"' <<<"$public"

unset ADMIN_API_TOKEN current payload public
echo "CAPTCHA 已关闭；站点密钥、加密 secret 与受保护动作均已保留。现在可登录管理面板修复并重新测试。"
