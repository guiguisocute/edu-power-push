# EDU Power Push · Backend

校园电费数据后端（Go）。学校不写死在代码里：采集目标由运维面板下发，爬虫是 `internal/provider/` 下可整体替换的目录（见仓库根 [`docs/PROVIDERS.md`](../docs/PROVIDERS.md)）。

产品前端在仓库 `frontend/`，只依赖已冻结的 API v1（[`api/openapi.yaml`](api/openapi.yaml)）。

---

## 三个进程

| 二进制 | 入口 | 常驻？ | 职责 |
|---|---|---|---|
| `api` | `cmd/api` | 是 | HTTP API、认证、产品查询、管理端点、单表即时刷新 |
| `worker` | `cmd/worker` | 是 | 余额扫描、绑表优先扫描、月账单、官方日明细、推送引擎、聚合维护 |
| `admin` | `cmd/admin` | 否 | 迁移、导入清单/快照/账单、受控扫描、rollup 重建等 CLI |

Compose 中 `api` 与 `worker` 常驻；`admin` 在 profile `tools` 下按需 `run --rm`。

---

## 进程启动与依赖关系

### `cmd/api`

```
config.Load
  → storage.Open + storage.Migrate
  → secrets.New（SETTINGS_ENCRYPTION_KEY）
  → provider.NewDynamic + school.NewBinder（15s Watch）
  → mailer.NewDynamic
  → httpapi.New → http.Server
```

- 学校与邮件均动态解析：面板改配置后，下次请求用新值，无需重启。
- 未选学校时服务可启动；采集相关能力返回 `school_not_configured` 或跳过，不崩溃。

### `cmd/worker`

```
config.Load
  → storage.Open + Migrate
  → UpstreamRequestGate（全站共享 QPS/并发，状态在 PostgreSQL）
  → secrets + school.Binder（15s）
  → mailer.NewDynamic + push.New
  → cron：
       · 余额 / 绑表优先 / 账单 / 日明细 / 日明细重试（可热重载）
       · 每分钟 push.Engine
       · 每日 03:00 maintenance
  → 循环：心跳 30s · 待跑任务 5s · 恢复 1m · 设置 15s
```

采集 run 的取消信号写在库里；API 只标记取消，worker 心跳（约 15s）后停派发、等在途表结束，终态 `canceled`。

### `cmd/admin`

一次性工具：`migrate`、`import-inventory`、`import-snapshot`、`import-bills`、`scan`、`bill-scan`、`refresh-rollups` 等。不替代 worker 的定时任务。

---

## 包结构（`internal/`）

```
internal/
├── config/          # 环境变量 → 强类型配置；生产硬校验
├── provider/        # 上游契约（Querier / Provider / Dynamic 注册表）
│   └── bdfairy/     # 内置 H5 爬虫 + schools.json（55 校）
├── school/          # 面板/环境 → Binding → Dynamic 热切换
├── storage/         # PostgreSQL：任务、读数、账号、聚合、闸门、设置
├── httpapi/         # HTTP 路由与 handler（产品 + 管理）
├── scanner/         # 余额扫描 Runner
├── biller/          # 月账单 Runner
├── detailer/        # 官方日明细 Runner
├── statistics/      # 读数差值与异常分类
├── importer/        # 清单 / 快照 / 账单 / 日明细文件导入
├── auth/            # JWT、Argon2id、会话 family
├── oauth/           # Google / GitHub 授权码流程
├── captcha/         # 人机验证适配
├── secrets/         # AES-GCM 凭证箱（面板密钥）
├── mailer/          # Resend / SMTP / 腾讯云 SES + HTML 模板 embed
├── notification/    # 规范推送消息结构与用户模板
└── push/            # 多渠道投递引擎 + 各渠道适配
```

### 包之间的调用方向（简化）

```
cmd/api ──▶ httpapi ──▶ storage
                 │         ▲
                 ├─▶ auth / oauth / captcha
                 ├─▶ mailer / secrets
                 └─▶ provider.Dynamic  ◀── school.Binder
                            ▲
cmd/worker ──▶ scanner / biller / detailer ──┘
         │              │
         │              └─▶ statistics（差值）
         ├─▶ push ──▶ mailer + 各 Webhook/Bot 渠道
         └─▶ storage（任务锁、闸门、读数、推送日志）

cmd/admin ──▶ importer / storage / provider（探测）
```

**规则：**

- 核心包只依赖 `provider` 接口，不依赖具体爬虫实现名。
- 实现包在 `init` 中 `provider.Register`；入口用 `_ "…/bdfairy"` 空白导入。
- 共享上游限速在 `storage.UpstreamRequestGate`，api 单表刷新与 worker 批量采集共用。

---

## 采集类型与互斥

| 类型 | Runner | 任务表 | QPS 含义 | 与其他类型 |
|---|---|---|---|---|
| 余额扫描 | `scanner` | `scan_runs` | 每秒启动多少块表 | 与账单、日明细可重叠 |
| 绑表优先 | 同 scanner | 同上 | 同上 | 与全量余额互斥（同类型） |
| 月账单 | `biller` | `bill_runs` | 每秒 HTTP 请求 | 可与余额/明细重叠 |
| 官方日明细 | `detailer` | `daily_detail_runs` | 每秒 HTTP 请求 | 可与余额/账单重叠 |

同类型仅允许一轮活动任务（PostgreSQL 约束）。所有上游 HTTP 合计受 `UPSTREAM_GLOBAL_QPS` / `UPSTREAM_GLOBAL_CONCURRENCY` 约束；面板可调，但不可越过 `*_MAX_*`。

面板 `SCHEDULE → MANUAL`：停掉**未来**定时触发，并禁止自动恢复该采集器中断任务。  
「终止」按钮：停**当前这一次** run，终态 `canceled`（与可自动恢复的 `interrupted` 不同）。

---

## HTTP API 分层

路由注册见 `internal/httpapi/server.go`：

| 层 | 路径前缀 | 鉴权 |
|---|---|---|
| 健康 / 契约 | `/health/*`、`/openapi.yaml` | 无 |
| 公开配置 | `/api/v1/frontend-config`、`/api/v1/captcha/config` | 无 |
| 认证 | `/api/v1/auth/*` | 限流；OAuth 为浏览器导航 302 |
| 用户 | `/api/v1/me/*` | access JWT |
| 校园聚合 | `/api/v1/campus/*` | **匿名** |
| 运维 | `/api/v1/admin/*`、`/meters/*`、`/inventory/tree` | operator/admin 会话或 `ADMIN_TOKEN` |

语义说明（可用性、扫描状态、OAuth、注销级联等）见 [`docs/API.md`](docs/API.md)。机器可读契约见 [`api/openapi.yaml`](api/openapi.yaml)。

---

## 能力清单

- 上游客户端：会话与解析、超时、重试、错误分类；无 OAuth 查表链路（内置 bdfairy）。
- PostgreSQL：迁移、清单双阶段导入、历史快照、扫描结果、读数去重与差值、异常、汇总、保留期。
- 扫描器：跨类型并行、共享闸门、同类型防重复、退避、冻结清单、中断续跑、失败子集重跑、取消。
- 月账单：全可见月初始化与核准、逐月观测、规范账单、修订与运维确认、中断续跑。
- 官方日明细：增量与 bootstrap 回填、占位与回落语义。
- 产品认证：邮箱 + Argon2id、注册/找回验证码、access/refresh 轮换与复用检测、绑表（最多 4 账号/表）、注销。
- OAuth：Google（含 PKCE）/ GitHub；凭证面板可配；已验证邮箱关联已有账号。
- 推送：多渠道凭证 AES-GCM；低额预警 + 定时摘要；推送记录；邮件 HTML `go:embed`。
- 管理：角色分层、审计日志、扫描控制热重载、前端配置下发。
- Docker：非 root、只读文件系统、回环端口、PostgreSQL 17。

---

## 要求

- Go 1.25 或更新版本
- PostgreSQL 17（开发可用 Docker）
- Docker Engine + Compose v1.29+ 或 v2
- Windows 开发优先 Git Bash（仓库脚本为 Bash）

---

## 验证

```bash
go test ./...
go vet ./...
npx -y @redocly/cli@latest lint api/openapi.yaml
bash scripts/smoke_api.sh
bash scripts/smoke_docker.sh
```

| 脚本 | 作用 |
|---|---|
| `smoke_api.sh` | 一次性 PG + Mailpit；注册/JWT/绑表/换绑/账单 1 表 19 月/前端配置等 |
| `smoke_docker.sh` | Compose 生命周期；扫描与账单 SIGTERM 中断恢复 |
| `scan_probe.sh` | 受控扫描分档探测（默认拒绝 >200 表） |
| `audit_bill_run.sh` | 月账单 run 只读审计 |

冒烟使用随机凭据与临时容器，结束后删除精确容器与卷；不发真实邮件；上游查询限制在少量电表。

---

## 本地启动（不用 Compose）

应用**只读进程环境**，不会自动加载仓库 `.env`。

```bash
export DATABASE_URL='postgres://...'
export ADMIN_TOKEN="$(openssl rand -hex 32)"
export AUTH_JWT_SECRET="$(openssl rand -hex 32)"
go run ./cmd/admin migrate
go run ./cmd/admin import-inventory --file ./bootstrap/room_meters.json
go run ./cmd/admin import-snapshot --file ./bootstrap/meter_balances.json
go run ./cmd/api
```

另开终端：

```bash
export DATABASE_URL='postgres://...'
export SCAN_CRON='10 * * * *'
export BOUND_SCAN_CRON='5 * * * *'
export BILL_CRON='0 19 1 * *'
go run ./cmd/worker
```

默认 API：`127.0.0.1:8080`。

公开端点：健康检查、`/openapi.yaml`、`GET /api/v1/frontend-config`、产品认证、**匿名** `GET /api/v1/campus/*`。  
原始电表与管理端点需要操作员会话或：

```http
Authorization: Bearer <ADMIN_TOKEN>
```

管理面板在产品前端侧栏（见仓库根 [`docs/ADMIN-PANEL.md`](../docs/ADMIN-PANEL.md)）。浏览器禁止打包 `ADMIN_TOKEN`。

---

## 管理命令示例

```bash
# 只验证范围，不创建任务
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"limit":10,"dry_run":true}' \
  http://127.0.0.1:8080/api/v1/admin/scan-runs

# CLI 受控扫描；全量必须显式 --full
go run ./cmd/admin scan --limit 10 --qps 0.5 --concurrency 1
go run ./cmd/admin scan --full --qps 3 --concurrency 6

# 月账单分档（1 → 10 → 50 → 200 → full）
go run ./cmd/admin bill-scan --limit 1 --qps 5 --concurrency 1
go run ./cmd/admin bill-scan --full --qps 10 --concurrency 6

# 手工重建聚合（扫描结束时也会自动刷新）
go run ./cmd/admin refresh-rollups
```

实测与参数选择记录见 [`docs/SCAN-EXPERIMENTS.md`](docs/SCAN-EXPERIMENTS.md)。

抬高并发前必须检查 `DATABASE_MAX_CONNS`：闸门每个并发槽在整次上游请求期间占用一条池连接；池小于闸门时吞吐反而下降。

---

## Docker 与部署

完整步骤见 [部署手册](docs/DEPLOYMENT.md)。  
**命令在仓库根目录执行**（`compose.yaml` 与 `.env` 都在根）：

```bash
cp .env.example .env
bash backend/scripts/prepare_bootstrap.sh
docker compose build
docker compose up -d db
docker compose --profile tools run --rm admin import-inventory --file /bootstrap/room_meters.json
docker compose --profile tools run --rm admin import-snapshot --file /bootstrap/meter_balances.json
# 可选：往期月账单 CSV
docker compose --profile tools run --rm admin import-bills --file /bootstrap/monthly_bills.csv
docker compose up -d api worker web
```

- `import-bills` 按 `meter_no` 关联（CSV 里的 `meter_id` 是旧库主键，忽略）。
- 当前单实例不引入 Redis：任务锁、会话、配置与持久状态均以 PostgreSQL 为真源。
- 认证限流在 API 进程内完成；多 API 副本再评估分布式限流/缓存。

---

## 契约与配置

| 文档 | 内容 |
|---|---|
| [OpenAPI 3.1](api/openapi.yaml) | 机器契约 |
| [API 语义](docs/API.md) | 前端交接、可用性、扫描语义 |
| [部署手册](docs/DEPLOYMENT.md) | 环境隔离、邮件、扫描参数 |
| [扫描实验](docs/SCAN-EXPERIMENTS.md) | 分档实测 |
| [环境变量样例](../.env.example) | 仓库根 |
| [邮件模板](internal/mailer/templates/README.md) | HTML 占位符 |


