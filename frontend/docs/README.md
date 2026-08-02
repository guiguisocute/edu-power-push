# 前端 → 后端 契约索引

本目录记录前端消费的后端能力、历史缺口提案与当前落地状态。  
阅读顺序建议：本索引 → 具体专题文档 → [`../README.md`](../README.md)（前端结构）→ 仓库根 [`../../README.md`](../../README.md)（整仓调用关系）。

---

## 约定

1. 前端先实现消费端与**端点缺失时的真实降级**，不伪造数据、不假装成功。
2. 后端按 OpenAPI 上线后，前端零改码或极小改动即可接通。
3. 机器契约以 [`../../backend/api/openapi.yaml`](../../backend/api/openapi.yaml) 为准；语义补充见 [`../../backend/docs/API.md`](../../backend/docs/API.md)。

---

## 前端如何打到后端（关系摘要）

```
视图 / admin 页
    → api/live.ts 或 admin/api.ts 或 store 动作
    → api/client.ts（同源 /api/v1，401 单飞 refresh）
    → backend internal/httpapi
    → storage / provider / mailer …
```

| 前端模块 | 后端入口 | 说明 |
|---|---|---|
| `api/session.ts` | `POST /auth/refresh` | 启动静默恢复 |
| `api/client.ts` 登录注册 | `POST /auth/*` | 限流 + 可选 captcha |
| `api/live.ts` | `/me/*`、`/campus/*` | 产品视图数据 |
| `api/notifications.ts` | `/me/channels*`、`notification-settings`、`push-logs` | 推送 |
| `api/leaderboard.ts` | `GET/PUT /me/leaderboard` | 榜单隐私 |
| `config/features.ts` | `GET /frontend-config` | 特性与展示默认 |
| `admin/api.ts` | `/api/v1/admin/*` | 运维面板 |

鉴权分档（与 `internal/httpapi/server.go` 一致）：

| 档位 | 端点 | 凭证 |
|---|---|---|
| 公开 | `health/*`、`frontend-config`、`auth/*`、**`campus/*`** | 无 |
| 用户 | `/me/*` | access JWT |
| 运维 | `meters/*`、`inventory/tree`、`admin/*` | operator/admin 会话或 `ADMIN_TOKEN` |

产品前端**不持有 `ADMIN_TOKEN`**。校园聚合匿名开放是产品前提。  
Gate 只挡概览、用电分析、推送配置（以电表为前提的视图）。

`campus/rankings` 匿名可读，因此身份脱敏在**服务端**执行：默认楼栋+楼层；明文位置仅本人；退榜电表不出现。已登录绑表时附带 `self` / `neighbors`。

---

## 文档与状态

| 文档 | 覆盖 | 状态 |
|---|---|---|
| [FRONTEND-CONFIG.md](FRONTEND-CONFIG.md) | `GET /api/v1/frontend-config` | 已上线并接通 |
| [AUTH-GAPS.md](AUTH-GAPS.md) | 鉴权模型、注册解耦绑表、电表预览 | 已上线（`campus/*` 为匿名） |
| [AUTH-GAPS.md](AUTH-GAPS.md) §2 | 找回密码 | 已上线；可用 `features.auth.email_code` 关入口 |
| [CAMPUS-GAPS.md](CAMPUS-GAPS.md) §1 | `GET /campus/scopes` | 已上线 |
| [CAMPUS-GAPS.md](CAMPUS-GAPS.md) §2 | 同楼平均 | 已用 summary/series/bills 接通，无独立 `metric=per_room` |
| [METER-REFRESH.md](METER-REFRESH.md) | `POST /me/refresh` | 已接通 |
| [USER-PREFERENCES.md](USER-PREFERENCES.md) §1–4 | 渠道 / 测试 / 规则 / 推送记录 | 已接通 |
| [USER-PREFERENCES.md](USER-PREFERENCES.md) §5 | 榜单隐私 | 已接通 |
| [USER-PREFERENCES.md](USER-PREFERENCES.md) §6 | 我的排名 | 经 `rankings.self` 接通 |
| [ASSETS.md](ASSETS.md) | 品牌与图标资源 | 工程约定 |

---

## 剩余可选项（不阻塞上线）

1. `metric=per_room` 查询参数：减少前端往返，语义已可用现有字段覆盖。
2. 独立 `GET /me/ranking`：`rankings.self` 已够用。
3. 短信登录 / 短信推送：UI 与代码预留，默认关或 Coming Soon。

---

## 双方默认值（易踩坑）

| 项 | 约定 |
|---|---|
| `features.auth.email_code` | 迁移 `000009` 与前端默认均为 `true`；需配置 `MAIL_PROVIDER` |
| `display.electricity_rate` | 前端默认 `"0.62"` 作展示；确认前非账单依据 |
| `display.ranking_refresh_time` | 默认 `"09:00"`（迁移 `000035`） |
| `features.charts.day_range` / `hourly_usage` | 默认 `false`（上游无可靠小时数据） |
| `features.channel_coming_soon.sms` | 前端本地默认 `true` |
