# EDU Power Push

校园电费数据平台：定时采集全校电表读数与官方账单，向绑定宿舍的用户推送余额预警与用电摘要，并提供全校用电看板与排行榜。

**学校不写死在代码里。** 采集目标在运维面板选择；爬虫是可整体替换的目录。内置上游（`bdfairy`）实测覆盖 55 所学校；清单外学校自行实现 `provider.Querier` 后接入即可。

本项目面向个人宿舍查询与研究。部署前必须确认已获目标系统授权，并把采集频率控制在对方可承受范围内。仓库内的共享 QPS / 并发闸门就是为此存在的。

---

## 仓库组成

| 部分 | 路径 | 职责 |
|---|---|---|
| 编排 | `compose.yaml`、`.env.example` | db + api + worker + web + admin 工具 + mailpit |
| 后端 | `backend/` | 三个 Go 二进制：`api`、`worker`、`admin` |
| 前端 | `frontend/` | React + TypeScript；用户界面与管理界面同一应用 |
| 跨端文档 | `docs/` | 爬虫接入、管理面板、推送渠道调研 |

---

## 运行时拓扑

```
                    ┌─────────────────────────────────────┐
  浏览器 (同源)      │  reverse proxy :443 (自备)           │
  ── / ────────────▶│  → web  :127.0.0.1:8081             │
  ── /api/* ───────▶│  → api  :127.0.0.1:8080             │
  ── /health/* ────▶│  → api                              │
                    └─────────────────────────────────────┘
                                      │
           ┌──────────────────────────┼──────────────────────────┐
           ▼                          ▼                          ▼
     ┌──────────┐              ┌────────────┐              ┌──────────┐
     │  web     │              │    api     │              │  worker  │
     │  Caddy   │              │  HTTP API  │              │  cron    │
     │  静态产物 │              │  会话/查询  │              │  采集/推送 │
     └──────────┘              └─────┬──────┘              └────┬─────┘
                                     │                          │
                                     └──────────┬───────────────┘
                                                ▼
                                         ┌────────────┐
                                         │ PostgreSQL │
                                         │ 状态真源   │
                                         └────────────┘
                                                │
                    worker / api 经 provider.Dynamic ──▶ 上游 H5 / 电费系统
```

| Compose 服务 | 二进制 / 镜像 | 默认端口 | 说明 |
|---|---|---|---|
| `db` | PostgreSQL 17 | 仅容器网 | 不映射宿主端口 |
| `api` | `cmd/api` | `127.0.0.1:8080` | 产品 API + 管理 API |
| `worker` | `cmd/worker` | 无 HTTP | 扫描、账单、日明细、推送、维护 |
| `web` | 前端静态 + Caddy | `127.0.0.1:8081` | SPA |
| `admin` | `cmd/admin` | 无常驻 | profile `tools`：迁移、导入、探测 |
| `mailpit` | Mailpit | 开发用 | 拦截 SMTP，不发真实邮件 |

API 与 Web **只绑回环**。对外入口、TLS、域名全部由你自己的反向代理决定；仓库代码与前端产物里**不写死任何域名**。

---

## 调用关系总览

### 1. 请求怎么进系统

| 调用方 | 入口 | 后端包 | 落库 / 副作用 |
|---|---|---|---|
| 产品前端 (live) | `/api/v1/*` | `internal/httpapi` | `storage` → PostgreSQL |
| 产品前端 (mock) | 无网络 | `frontend/src/lib/mock.ts` | 仅本地演示 |
| 管理前端 | `/api/v1/admin/*` | `httpapi` + `admin_auth` | 角色现查；审计写 `admin_audit_log` |
| 机器脚本 / CI | `Authorization: Bearer ADMIN_TOKEN` | 视同 `admin` | 迁移、探测、健康检查 |
| Worker 内部 | 不走 HTTP | `scanner` / `biller` / `detailer` / `push` | 任务表 + 读数表 + 推送日志 |

### 2. 三条主数据路径

**A. 采集路径（worker 主导）**

```
cron / 面板创建 →  worker 建 run（scan_runs / bill_runs / daily_detail_runs）
                 →  scanner|biller|detailer.Runner
                 →  provider.Dynamic → Querier（bdfairy 或自有实现）
                 →  共享 UpstreamRequestGate（全站 QPS/并发）
                 →  storage 去重、差值、观测、规范账单
                 →  聚合物化视图 / rollup 刷新
                 →  push.Engine（低额预警、定时摘要）
```

**B. 产品读路径**

```
浏览器 → api.client.ts → /api/v1/me/* | /api/v1/campus/*
       → httpapi handlers → storage 查询
       → availability / quality 语义原样返回（缺数不伪造）
```

**C. 配置热路径**

```
运维保存 system_settings / frontend_config
  → api：下次读设置即生效（mailer.Resolver、oauth、captcha…）
  → worker：15s 轮询 reloadScannerSettings；school.Binder 15s 刷新
  → 前端：启动时 GET /frontend-config，用户刷新后生效
```

正在跑的采集 run **冻结** `config_snapshot` 与查询器实例；换学校/改 QPS 只影响下一轮。

### 3. 认证与权限分界

| 档位 | 凭证 | 典型端点 |
|---|---|---|
| 公开 | 无 | `health/*`、`frontend-config`、`auth/*`、**`campus/*`** |
| 用户 | access JWT（内存）+ refresh cookie | `/api/v1/me/*` |
| 运维 | 用户会话且角色 `operator`/`admin`，或 `ADMIN_TOKEN` | `/api/v1/admin/*`、`meters/*`、`inventory/tree` |

- access JWT 默认 15 分钟，**只存浏览器内存**。
- refresh JWT 默认 30 天，HttpOnly + SameSite=Strict，路径 `/api/v1/auth`，**前端必须与 API 同源**。
- 管理界面走**账号角色**，不是 `ADMIN_TOKEN`。`ADMIN_TOKEN` 禁止打进浏览器产物。
- `campus/*` 匿名可读是产品前提；排行榜脱敏在**服务端**按用户偏好完成。

### 4. 上游与学校切换

```
system_settings['school']  ──Load──▶  school.Binder  ──Resolve──▶  provider.Dynamic
        ▲                                                                  │
        └── 面板保存 / .env 兜底                              扫描、账单、日明细、
                                                              用户手动刷新都取 Querier
```

核心代码只依赖 `internal/provider` 接口，**从不 import `bdfairy` 包名**。`cmd/*/main.go` 用空白导入注册实现。

---

## 目录地图

```
EDU-POWER-PUSH/
├── compose.yaml              # 整套编排（必须在仓库根）
├── .env.example              # 环境变量模板（每环境一份 .env，不进库）
├── README.md                 # 本文件
├── docs/
│   ├── PROVIDERS.md          # 接入自有爬虫
│   ├── ADMIN-PANEL.md        # 管理界面权限与凭证
│   └── DEPLOYMENT.md         # 部署与环境隔离
├── backend/
│   ├── cmd/
│   │   ├── api/              # HTTP API 进程
│   │   ├── worker/           # 采集 + 推送 + 维护
│   │   └── admin/            # 一次性 CLI（migrate / import / scan…）
│   ├── internal/             # 业务包（见 backend/README.md）
│   ├── api/openapi.yaml      # OpenAPI 3.1 契约
│   ├── migrations/           # 仅前进迁移
│   ├── bootstrap/            # 清单与快照样例
│   ├── scripts/              # 冒烟、探测、审计
│   └── docs/                 # API 语义、部署、扫描实验
└── frontend/
    ├── src/                  # 应用源码（见 frontend/README.md）
    ├── docs/                 # 前后端契约与缺口索引
    └── public/               # 静态资源
```

---

## 本机运行

### mock

```bash
cd frontend && pnpm install && pnpm dev
```

确定性 mock 数据驱动，界面全部可点，用于演示与复刻验收。

### 完整栈

```bash
cp .env.example .env
# 至少填写：POSTGRES_PASSWORD、ADMIN_TOKEN、AUTH_JWT_SECRET
docker compose up -d db api worker web
```

1. 打开 Web（默认 `http://127.0.0.1:8081`）。
2. 前端接真后端：URL 加 `?api=live`，或在 `frontend/.env.local` 写 `VITE_API_MODE=live`。
3. 用 `ADMIN_TOKEN` 提升首个管理员，或直接用已有 `admin` 账号登录。
4. 进入「系统管理 → 扫描器」，顶部选择**学校**并保存。
5. 约 15 秒内 worker 加载新配置，无需重启进程。

**未选学校时服务仍运行**，全部采集器跳过。多校部署的容器禁止替部署者默认选定学校。

---

## 部署

四个容器：`db` + `api` + `worker` + `web`。没有别的依赖，不需要 Kubernetes，也不需要在宿主机装 Go 或 Node。

### 1. 准备 `.env`

```bash
cp .env.example .env
```

必填三项，缺一个 compose 会直接拒绝启动（`:?` 断言）：

| 变量 | 生成方式 |
|---|---|
| `POSTGRES_PASSWORD` | `openssl rand -hex 32` |
| `ADMIN_TOKEN` | `openssl rand -hex 32` |
| `AUTH_JWT_SECRET` | `openssl rand -hex 32`（与上一条**不要**复用） |

另外几个按环境改：

| 变量 | 说明 |
|---|---|
| `APP_ENV` | `development` / `test` / `production`。`production` 会强制 HTTPS cookie |
| `COMPOSE_PROJECT_NAME` | 容器与卷的前缀。**改它等于换一个空数据库**，已有环境不要改 |
| `APP_IMAGE` / `WEB_IMAGE` | 见下一节。每个环境钉自己的 tag |
| `API_PORT` / `WEB_PORT` | 宿主回环端口，默认 8080 / 8081。同机多环境必须错开 |
| `SETTINGS_ENCRYPTION_KEY` | `openssl rand -base64 32`。留空则面板只能改非敏感设置，不会静默存明文 |
| `PUBLIC_BASE_URL` | 邮件里指向站点的链接。代码里没有任何域名，只有这一处 |

### 2. 取镜像

**A. 直接拉预构建镜像**（不需要克隆源码之外的任何东西）：

```bash
# .env
APP_IMAGE=guiguisocute/edu-power-push:api
WEB_IMAGE=guiguisocute/edu-power-push:web
```

```bash
docker compose pull
```

两个组件共用**一个**公开仓库，用 tag 前缀区分：`api-*` 是三个 Go 二进制（api / worker / admin），`web-*` 是前端产物 + Caddy。带日期与 commit 的版本 tag 形如 `api-20260802-ac1465f`，`:api` / `:web` 是随最后一次推送移动的指针 —— 生产环境请钉版本 tag，别钉指针。

**B. 自己构建**（改过代码，或不想用别人的镜像）：

```bash
# .env
APP_IMAGE=edu-power-push:local
WEB_IMAGE=edu-power-push-web:local
```

```bash
docker compose build
```

推到自己的仓库：

```bash
rev="$(date -u +%Y%m%d)-$(git rev-parse --short HEAD)"
docker tag edu-power-push:local     <你的账号>/edu-power-push:api-$rev
docker tag edu-power-push-web:local <你的账号>/edu-power-push:web-$rev
docker push <你的账号>/edu-power-push:api-$rev
docker push <你的账号>/edu-power-push:web-$rev
```

推之前自己查一遍镜像里没有夹带凭据 —— 公开仓库的层任何人都能拉下来翻：

```bash
docker run --rm --entrypoint sh <镜像> -c \
  'find / -xdev \( -path /proc -o -path /sys -o -path /etc/ssl \) -prune -o \
     \( -name ".env" -o -name ".env.*" \) -print 2>/dev/null'
```

### 3. 建库、起服务

```bash
docker compose --profile tools run --rm admin migrate
docker compose up -d db api worker web
```

迁移也会在 api / worker 启动时自动跑（PostgreSQL advisory lock 下串行），上面这条只是让你在起服务前先看到迁移结果。

### 4. 确认起来了

```bash
curl -fsS http://127.0.0.1:8080/health/ready
# {"database":"ready","migrations":"ready","status":"ready","version":"version_1","worker":"ready"}

curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8081/
# 200
```

`web` 容器同时把 `/api` 与 `/health` 反代给 `api`，所以前端与 API **同源**，refresh 的 HttpOnly cookie 不跨域。这是硬要求：把前端和 API 放到两个域名下，登录态会直接失效。

### 5. 第一个管理员

面板走账号角色，不是 `ADMIN_TOKEN`。所以先在站点上注册一个账号，再用 token 把它提成管理员：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/admin/users/promote \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","role":"admin"}'
```

之后所有运维操作都在站点侧栏的「系统管理」里做。`ADMIN_TOKEN` 只留给机器（CI、迁移、健康探测），**禁止**打进浏览器产物。

### 6. 选学校

到「系统管理 → 扫描器」，顶部选一所学校并保存，约 15 秒内 worker 热加载，不必重启。没选之前服务照常跑，只是采集器全部跳过 —— 面板与登录都可用。

也可以在 `.env` 里填 `AREA_ID` / `AREA_NAME` 兜底；面板保存过之后以面板为准。

### 7. 对外入口

`api` 与 `web` **只绑宿主回环**，不要在防火墙或安全组里放开 8080 / 8081。对外由你自己的反向代理终止 TLS：

```
443 → /      → 127.0.0.1:8081   (web)
443 → /api/* → 127.0.0.1:8080   (api)
443 → /health/* → 127.0.0.1:8080
```

Nginx / Caddy / Traefik 都可以，仓库里不带任何一家的配置。要点只有三条：终止 TLS、前端与 API 同源、别把 8080 暴露出去。生产环境记得 `APP_ENV=production`（强制 `Secure` cookie）。

更细的导入、分档扫描、环境隔离、备份与回滚见 [`backend/docs/DEPLOYMENT.md`](backend/docs/DEPLOYMENT.md)。

### 冒烟自检

```bash
bash backend/scripts/smoke_docker.sh
```

从零起一套一次性环境：迁移 → 导入库存与快照 → 起 api/worker → 校验管理端点鉴权 → 用真实 SIGTERM 验证采集任务的检查点与续跑，跑完自动拆掉。上游指向不可路由地址，**不会**向任何真实学校系统发请求。

账单生命周期那一段需要一个能返回月账单的上游，默认跳过；要连真实上游跑：

```bash
SMOKE_BILL_LIFECYCLE=1 AREA_ID=<你的> ELECTRICITY_BASE_URL=<你的> \
  bash backend/scripts/smoke_docker.sh
```

---

## 接入自有学校爬虫

内置 `bdfairy` 覆盖一类微信内嵌 H5 缴费协议。学校用其他系统时：

1. 复制 `backend/internal/provider/bdfairy/` 为新目录。
2. 实现 `QueryMeter` / `QueryMonthlyBills` / `QueryDailyDetails`（不支持的返回空结果 + Errors，禁止 panic）。
3. 每次真实 HTTP 必须调用 `BeforeRequest` 与 `AcquireRequest`（共享闸门）。
4. 在三个 `cmd/*/main.go` 空白导入注册；`.env` 的 `PROVIDER` 或面板选学校。

完整步骤与契约陷阱见 [`docs/PROVIDERS.md`](docs/PROVIDERS.md)。

---

## 管理界面

管理界面**不是独立站点**，而是主站侧栏「系统管理」一组（`frontend/src/admin/`）：

| 页 | 作用 |
|---|---|
| 控制台 | 合格表数、worker 心跳、异常、邮件是否可用 |
| 扫描器 | 学校选择、余额/账单/日明细任务、终止、QPS/cron |
| 用户 | 角色、启停、踢下线、解绑、删号 |
| 推送渠道 | 平台邮件供应商 + 用户端渠道展示状态 |
| 人机验证 | Turnstile 等 |
| 前端展示 | 登录开关、图表、电价、校历、站点名 |

角色：`user` / `operator` / `admin`。权限每次请求现查库。详见 [`docs/ADMIN-PANEL.md`](docs/ADMIN-PANEL.md)。

---

## 文档索引

| 文档 | 内容 |
|---|---|
| [`backend/README.md`](backend/README.md) | 后端包结构、进程职责、本地开发 |
| [`frontend/README.md`](frontend/README.md) | 前端结构、mock/live、视图数据源 |
| [`backend/docs/API.md`](backend/docs/API.md) | API 语义与前端交接 |
| [`backend/api/openapi.yaml`](backend/api/openapi.yaml) | OpenAPI 3.1 机器契约 |
| [`backend/docs/DEPLOYMENT.md`](backend/docs/DEPLOYMENT.md) | 部署、环境隔离、运维命令 |
| [`backend/docs/SCAN-EXPERIMENTS.md`](backend/docs/SCAN-EXPERIMENTS.md) | 扫描分档实测记录 |
| [`docs/PROVIDERS.md`](docs/PROVIDERS.md) | 接入自有爬虫 |
| [`docs/ADMIN-PANEL.md`](docs/ADMIN-PANEL.md) | 管理面板安全模型 |
| [`frontend/docs/README.md`](frontend/docs/README.md) | 前后端契约落地索引 |

---

## 约定

- **禁止提交凭据**：口令、Token、Cookie、真实收件地址、云厂商密钥、部署密钥。`.env` 与 `backups/` 已在 `.gitignore`。
- **`ADMIN_TOKEN` 禁止进入浏览器产物**。它供 CI、迁移脚本、健康探测使用。管理界面使用登录账号角色。
- API 上金额与电量为十进制**字符串**；时间为 RFC 3339。禁止用浮点数搬运金额与电量。
- 数据可用性语义：`ready` / `partial` / `insufficient_history` / `unavailable`。缺数如实显示，禁止用估算值填充。
- **代码禁止写死任何学校**。校名、校区名、站点名为运行时配置；测试夹具使用「示例大学 / 示例校区」占位。
