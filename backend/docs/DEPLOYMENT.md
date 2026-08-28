# 部署手册

## 边界

- API 与 Web 只映射到服务器回环地址。对外统一经 443 网关。
- PostgreSQL 与 Redis 只在 Compose 网络中开放，不映射宿主端口。
- `ADMIN_TOKEN` 是 SSH 边界之外的第二层保护。
- 凭据只存在于服务器 `.env` 或进程环境中。`.env` 权限应为 `0600`。

本文规格数字来自一台 4 vCPU / 约 4 GiB 内存 / 40 GiB 盘的 Debian 12 机器。
环境为 Docker 29 + Compose 5。
那是这套参数已实测过的配置，不是硬性要求。
API、Web 分别映射到 `127.0.0.1:8080/8081`。
PostgreSQL 不映射宿主端口。

## 0. 环境分隔：同一套代码 + 环境变量 + 各自独立的基础设施

代码没有任何环境分支，也**没有任何域名**。
差异全部由环境变量表达。
对外入口由反向代理决定。
换域名不需要改代码或重新构建前端。

| | development | test | production |
|---|---|---|---|
| `APP_ENV` | `development` | `test` | `production` |
| `COMPOSE_PROJECT_NAME` | `edu-power-push-dev` | `edu-power-push-test`（现网测试，勿改名） | `edu-power-push-prod` |
| 数据库 | 该项目专属卷 | 该项目专属卷 | 该项目专属卷 |
| `APP_IMAGE` | `edu-power-push:dev` | `edu-power-push:test` | `edu-power-push:prod` |
| `API_PORT`（宿主回环） | 8080 | 8080 | 8081（同机时必须错开） |
| `AUTH_COOKIE_SECURE` | 留空→false | 公网 HTTPS 显式 true | 强制 true，否则拒绝启动 |
| `ADMIN_TOKEN` | 随意 | 优先 32+ | 强制 32+，否则拒绝启动 |
| 扫描参数 | 0.5 QPS / 1 并发 | 实测 3 / 6 | 实测 3 / 6 |

要点：

- **`APP_ENV` 写错直接启动失败**。
  以前任何拼写都会被当成「非生产」。
  于是 refresh cookie 的 `Secure` 静默变成 false。
  线上看不出任何异常。
- **基础设施靠 `COMPOSE_PROJECT_NAME` 隔离**。
  compose 用它给容器与卷加前缀。
  `<project>_postgres-data` 就是该环境专属的库。
  测试环境的迁移、导入、扫描碰不到生产库。
  **改已有环境的项目名 = 换一个空库**。
  禁止对现网做这件事。
- **镜像 tag 也按环境分**（`APP_IMAGE`）。
  两个环境同机共用 `:local` 时，给测试环境 `docker load` 会连生产的镜像一起顶掉。
  下次 recreate 会悄悄换版本。
- 每个环境的 `POSTGRES_PASSWORD` / `ADMIN_TOKEN` / `AUTH_JWT_SECRET` 必须各自独立生成。
  复用口令时「隔离」只剩容器名不一样。
- 各环境一份 `.env`（`0600`），放在仓库/发布目录根。
  模板见根目录 `.env.example`。

### 域名入口

API 只监听宿主回环。
域名入口由**你自己的反向代理**提供。
**域名只出现在反代配置里**。
仓库不带任何一家网关的配置脚本。
Nginx、Caddy、Traefik 都可以。
要求只有三条：

1. 终止 TLS，把 `443` 反代到 `127.0.0.1:$API_PORT`
2. 前端与 API **同源**（见下）
3. 禁止在防火墙或云安全组里开放 `8080`

如果反代要给不同来源分配独立限流桶，可在 `.env` 里设 `AUTH_RATE_PROXY_SECRET`。
然后由反代在请求上带 `X-Rate-Proxy-Secret` 与 `X-Rate-Proxy-Key`。
后端仅在密钥匹配、且来源是回环/内网时才认这个 key。
不配或不匹配时整组头会被忽略。
客户端伪造不出无限限流桶。

前端与 API **必须同源**。
refresh cookie 是 `HttpOnly; SameSite=Strict; Path=/api/v1/auth`。
跨站点根本不会带上。
禁止把前端挂在另一个域名再跨域调 API。

上 HTTPS 之后（正式环境必须）：`APP_ENV=production` 会强制 `AUTH_COOKIE_SECURE=true`。
测试环境保持 `APP_ENV=test`。
一旦测试也走 HTTPS 入口，就应显式设 `AUTH_COOKIE_SECURE=true`。
反代终止 TLS 的部署都属于这种情况。

前端产物随 Web 镜像发布。
产物里没有域名。
同一份构建可以挂到任何域名下。

## 1. 服务器前置条件

```bash
docker --version
docker compose version
docker info
```

部署用户必须能直接访问 Docker。

## 2. 配置

在发布目录创建 `.env`，并限制权限：

```bash
umask 077
cp .env.example .env
chmod 600 .env
```

至少设置：

```dotenv
# 环境身份：这四个决定了这套服务用哪个库、哪个镜像、哪个端口（见 §0）
APP_ENV=test                      # development | test | production，写错直接启动失败
COMPOSE_PROJECT_NAME=edu-power-push-test
APP_IMAGE=edu-power-push:test
API_PORT=8080

POSTGRES_USER=edu_power
POSTGRES_DB=edu_power
POSTGRES_PASSWORD=<独立随机值，建议 openssl rand -hex 32>
ADMIN_TOKEN=<独立随机值，建议 openssl rand -hex 32>
AUTH_JWT_SECRET=<另一份独立随机值，建议 openssl rand -hex 32>
REDIS_PASSWORD=<另一份独立随机值，建议 openssl rand -hex 32>
REDIS_MAXMEMORY=256mb
REDIS_CONTAINER_MEMORY=320m
AUTH_RATE_PROXY_SECRET=<可选：与反向代理约定的随机值>
AUTH_ACCESS_TTL_SEC=900
AUTH_REFRESH_TTL_DAYS=30
# 留空 = 按 APP_ENV 取默认（production→true，其余→false）
AUTH_COOKIE_SECURE=true
# 三类扫描器可并行，但所有进程的实际 HTTP 请求合计受这道共享闸门约束。
# GLOBAL 是默认值，面板保存后以 system_settings 为准；MAX 是面板越不过的天花板。
UPSTREAM_GLOBAL_QPS=12
UPSTREAM_GLOBAL_CONCURRENCY=12
UPSTREAM_MAX_QPS=30
UPSTREAM_MAX_CONCURRENCY=16
# 闸门每个并发槽占住一条池连接，池子必须 >= UPSTREAM_MAX_CONCURRENCY + 8
DATABASE_MAX_CONNS=24
SCAN_CRON=15 */4 * * *
BOUND_SCAN_CRON=5 * * * *
SCAN_QPS=4
SCAN_CONCURRENCY=4
SCAN_MAX_QPS=10
SCAN_MAX_CONCURRENCY=16
BILL_CRON=0 19 1 * *
BILL_QPS=5
BILL_CONCURRENCY=4
BILL_MAX_QPS=30
BILL_MAX_CONCURRENCY=8
DETAIL_CRON=30 6 * * *
DETAIL_RETRY_CRON=15 10 * * *
DETAIL_QPS=8
DETAIL_CONCURRENCY=8
```

Redis 只承载 Campus 聚合与排行榜的共享 L2 缓存：禁用 RDB/AOF，使用 `volatile-lfu`
在 256 MiB 内淘汰带 TTL 的响应；代际键不带 TTL，不会因淘汰丢失失效版本。API 进程内
还有一层 L1，默认 Campus 新鲜 5 分钟、陈旧可回源 30 分钟并异步刷新；排行榜缓存到当前
榜期的 `next_update_at`。Worker 写入数据后提升共享代际，Redis 故障时两类读请求都回退
PostgreSQL。Redis 因而不进入数据库备份，也不能存会话、任务或任何不可重建数据。

生产环境应为 `REDIS_PASSWORD` 生成独立随机值，并在切换 API/Worker 前等待 Compose
健康检查返回 Redis `PONG`。
健康检查返回 `cache=ready|unavailable|disabled`；缓存故障会暴露在状态中，但不会让数据库
仍可服务的 API 被负载均衡器摘除。

共享闸门、三类扫描器的 QPS 与并发都能在管理面板上改。
保存后 worker 与 API 在下一次取设置时换挡，不必重启。
`.env` 里的 `UPSTREAM_GLOBAL_*` 只是没保存过时的默认值。
面板越不过 `*_MAX_*` 天花板。
改天花板仍然要重新发布。

调高并发之前先看池子。
闸门的每个并发槽在整个上游请求期间占住一条数据库连接。
因此 `DATABASE_MAX_CONNS` 才是「最多几个请求在飞」的真上限。
2026-07-30 实测把闸门开到 20 而池子还是 12 时，在飞的仍然只有 ~12。
吞吐反而比开 4 更差。
扫描器抢不到连接去写自己的结果。

余额全量扫描由 `SCAN_CRON` 控制。
活跃账号当前绑定的电表由 `BOUND_SCAN_CRON` 每小时优先刷新。
两者属于同一“余额”类型。
因此仍互相防重复。
余额、月账单、日明细可以重叠执行。
每次 HTTP 请求都共享 `UPSTREAM_GLOBAL_QPS` / `UPSTREAM_GLOBAL_CONCURRENCY`。
余额扫描的 `SCAN_QPS` 是每秒启动的电表数。
安全默认值是 0.5 QPS / 并发 1。
月账单和日明细的 QPS 是每秒 HTTP 请求数。
仅当目标服务器重复通过 1、10、50、200 表关卡后，才改余额配置。
改为已经验证的 3 / 6。
月账单初始默认是 5 QPS / 4 个电表并发。
它必须独立跑过分档实验。
禁止套用余额扫描参数。

邮件三选一。
未配置时邮件测试接口返回 503。
扫描本身不受影响。
配置错了（缺 key / 缺发件人）**进程直接启动失败**。
不会静默降级成发不出信。

Resend（**当前测试阶段用这个**）：

```dotenv
MAIL_PROVIDER=resend
RESEND_API_KEY=<Resend 控制台的 API key>
# 发件人必须在 Resend 里已验证的域名下，可带显示名
MAIL_FROM=POWER·PUSH <noreply@你的邮件域名>
MAIL_ADMIN_TO=admin@example.com
MAIL_USER_LIMIT_PER_MINUTE=20
MAIL_USER_LIMIT_PER_DAY=500
# 邮件正文里的站点链接前缀（页脚 / 通知设置）
PUBLIC_BASE_URL=https://你的站点域名
```

两项 `MAIL_USER_LIMIT_*` 是所有 API 实例共享的数据库硬配额。
它们只覆盖用户触发的注册、找回密码、换邮箱。
也覆盖通知收件人验证码。
它们不影响系统告警邮件。
达到额度后不会调用邮件供应商。
也不会留下一个实际未发送却进入冷却期的验证码。

**邮件域名与站点域名可以不同**。
禁止写反。
腾讯云 SES 的代码原样保留。
把 `MAIL_PROVIDER` 改回 `tencent_ses` 即可切回。

产品邮件模板（余额提醒 / 用电情况 / 注册验证码 / 找回密码）在 `backend/internal/mailer/templates/`。
它们用 `go:embed` 编进二进制。
运行时不读外部文件。
那一份就是唯一的源文件。
改样式直接改它。
运维面板「发送测试邮件」旁的下拉可以用示例数据逐套预览。

SMTP：

```dotenv
MAIL_PROVIDER=smtp
MAIL_FROM=EDU Power Push <sender@example.com>
MAIL_ADMIN_TO=admin@example.com
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=sender@example.com
SMTP_PASS=<secret>
SMTP_TLS_MODE=starttls
```

远端 SMTP 必须使用 `tls` 或 `starttls`。
`none` 会被后端拒绝。
只有 `localhost` 或回环 IP 上的 Mailpit/测试服务器例外。
这样可防止误配置时把 SMTP 凭据或邮件内容发送到明文远端连接。

腾讯云 SES：

```dotenv
MAIL_PROVIDER=tencent_ses
MAIL_ADMIN_TO=admin@example.com
TENCENTCLOUD_SECRET_ID=<secret>
TENCENTCLOUD_SECRET_KEY=<secret>
TENCENTCLOUD_REGION=ap-guangzhou
TENCENT_SES_FROM_EMAIL=sender@example.com
TENCENT_SES_TEMPLATE_ID=<approved-template-id>
```

SES 模板应接受 `title` 和 `message` 两个字符串变量。

`AUTH_COOKIE_SECURE=true` 要求产品前端通过 HTTPS 使用 refresh cookie。
测试环境一旦走 HTTPS 入口（反代终止 TLS），就要显式设 `AUTH_COOKIE_SECURE=true`。
仅在服务端和本机都绑定回环、纯 HTTP 的 SSH 本地转发、且无公网代理时，才应设为 `false`。
一旦有 HTTPS 终止层就必须为 `true`。
access JWT 在响应 JSON 中返回。
refresh JWT 只通过 HttpOnly、SameSite=Strict cookie 传递。
并以 SHA-256 摘要落库。

本地邮件集成验证使用测试 profile 中固定版本的 Mailpit。
它不进入生产启动集合：

```bash
docker compose --profile mailpit up -d mailpit
```

Mailpit Web UI 只绑定本机回环端口。
生产服务器不启动该 profile。

## 3. 首次启动和导入

先准备独立 bootstrap 副本。
该目录被 `.gitignore` 忽略。
数据不会打进镜像。

以下命令都在仓库根目录执行（`compose.yaml` 在那里）：

```bash
bash backend/scripts/prepare_bootstrap.sh /path/to/reference/campus-data
docker compose build
docker compose up -d db
docker compose --profile tools run --rm admin migrate
docker compose --profile tools run --rm admin import-inventory --file /bootstrap/room_meters.json
docker compose --profile tools run --rm admin import-snapshot --file /bootstrap/meter_balances.json
docker compose up -d redis api worker
```

两个导入命令均按源文件哈希幂等。
历史读数标记为 `legacy_import`。
它们不会冒充实时扫描。

### 补往期月账单（可选）

如果手上有从旧库导出的 `monthly_bills` CSV，可以把历史月度用量一并补进来：

```bash
docker compose --profile tools run --rm admin import-bills --file /bootstrap/monthly_bills.csv --dry-run
docker compose --profile tools run --rm admin import-bills --file /bootstrap/monthly_bills.csv
```

先跑 `--dry-run`。
它会真的建临时表、真的做关联和 upsert，只是最后回滚。
所以报出来的匹配数就是正式执行会写进去的数。
它不是另写一套「假装执行」去猜。
重点看 `unknown_meters`：不为 0 说明有表号在当前库存里找不到。
那些行会被丢掉。

关于这条路径有三点必须知道：

- **CSV 里的 `meter_id` 一律忽略**。
  关联只走 `meter_no`。
  那列是旧库的主键。
  库存重新导入之后同一块表的 UUID 已经变了。
  照抄会写出指向**别的**电表的外键。
- **`observed_at_cst` 是 CST 墙上时间**，按部署时区解析。
  当成 UTC 会整体偏 8 小时。
- **冲突时只覆盖更旧的记录**（`WHERE monthly_bills.observed_at < EXCLUDED.observed_at`）。
  所以这条命令可以反复跑。
  它也不会把线上后来查到的更准的数据按回历史值。
  导入是补历史，不是回滚现状。

若版本目录权限为 `0700`，容器内 UID 10001 不能直接读取宿主 bootstrap。
宿主文件权限同为 `0700/0600` 时同样读不到。
首次导入时可在上层版本目录仍为 `0700` 的前提下临时放行挂载点。
导入后立即收紧：

```bash
chmod 705 bootstrap
chmod 604 bootstrap/room_meters.json bootstrap/meter_balances.json
# 执行上面的两个 import 命令
chmod 700 bootstrap
chmod 600 bootstrap/room_meters.json bootstrap/meter_balances.json
```

因为上层版本目录保持 `0700`，其他宿主机用户无法沿路径访问这些文件。
bootstrap 在容器中始终以只读方式挂载。

## 4. 验收

在服务器执行：

```bash
curl -fsS http://127.0.0.1:8080/health/live
curl -fsS http://127.0.0.1:8080/health/ready
docker compose ps
docker compose logs --tail=100 api worker
```

在本机建立隧道（`<host>` 换成你的 SSH 别名）：

```bash
ssh -N -L 18080:127.0.0.1:8080 <host>
```

管理面板在产品前端。
用运维/管理员账号登录后，侧栏会多出「系统管理」一组（见仓库根 `docs/ADMIN-PANEL.md`）。
旧后端路径 `/admin/` 会 301 到 `/admin`。
`/admin` 则落回主站并直接打开控制台页。
API 探测仍可用：

```bash
curl -H 'Authorization: Bearer <ADMIN_TOKEN>' \
  http://127.0.0.1:18080/api/v1/admin/overview
```

禁止在防火墙或云安全组中开放 8080。
公网入口只走你的 443 反代。
无论 DNS 是否经 CDN 代理，下列三层都要保留。
三层是：源站端口收敛、反代限流、应用内硬上限。
把裸源站地址放进公开 DNS 记录，即把前两层直接绕过去。

## 5. 目标服务器分级实扫

每一级必须查看 `completed(_with_errors)`、耗时、P95、attempts 和错误码后再放量：

```bash
docker compose --profile tools run --rm admin scan --limit 1 --qps 0.5 --concurrency 1
docker compose --profile tools run --rm admin scan --limit 10 --qps 0.5 --concurrency 1
docker compose --profile tools run --rm admin scan --limit 50 --qps 1 --concurrency 2
docker compose --profile tools run --rm admin scan --limit 200 --qps 3 --concurrency 6
docker compose --profile tools run --rm admin scan --full --qps 3 --concurrency 6
```

若出现限流、错误率上升、P95 明显抬升或异常读数，停止放量并保留该轮结果。
全量验收目标是 45 分钟内、最终成功率不低于 99%。

扫描结束会自动刷新日/周/月聚合。
若历史版本的 CLI 扫描已经生成差值但没有聚合，可执行幂等修复：

```bash
docker compose --profile tools run --rm admin refresh-rollups
# 或只从明确时间点重建
docker compose --profile tools run --rm admin refresh-rollups --from 2026-07-01T00:00:00+08:00
```

Worker 退出时把未终态任务标记为 `interrupted`。
新 Worker 启动后只续跑尚无终态结果的冻结电表。
心跳超过 3 分钟的 `running` 任务也会先转为 `interrupted` 再恢复。
恢复前禁止删除 `scan_runs`、`scan_run_meters` 或 `scan_results`。

### 5.1 首次月账单初始化与月度核准

月账单和余额扫描允许并行。
开始前只需确认没有同类型 `pending/running` 任务，并确认共享流量闸门仍处于已验证范围。
每档都抓取当时全部可见月份：

```bash
docker compose --profile tools run --rm admin bill-scan --limit 1 --qps 5 --concurrency 1
docker compose --profile tools run --rm admin bill-scan --limit 10 --qps 5 --concurrency 4
docker compose --profile tools run --rm admin bill-scan --limit 50 --qps 10 --concurrency 6
docker compose --profile tools run --rm admin bill-scan --limit 200 --qps 15 --concurrency 8
# 只有前四档的错误率、P95 和上游状态均可接受时：
docker compose --profile tools run --rm admin bill-scan --full --qps 10 --concurrency 6
```

当前 19 个可见月、7,519 个 eligible 电表的最低请求量约为 172,937。
5 QPS 理论下限约 9.6 小时。
因此 200 表关卡后可以在 `BILL_MAX_QPS` 范围内逐步实验更高 QPS。
任何上游错误率或延迟明显抬升都应回退。
目标机的 15/8 虽通过 200 表短档。
但持续全量在 332 表内出现 8 个 partial。
因此生产已降到 10/6。
短档成功不能覆盖持续运行证据。
初始化 full 会满足当月核准。
此后 Worker 从每月 2 日起每天 03:00 尝试。
本月一旦创建过计划任务或人工 full，数据库即阻止重复。
这样与延迟扫描冲突时会在次日补跑。
不会漏掉整月。

运维面板必须检查任务进度与 `no_data/partial/error` 月份。
还要检查规范账单写入数和历史修订数。
`no_data` 不等于零账单。
历史修订必须保留旧值/新值并由管理员确认。
账单任务中断时禁止删除下列表。
表含 `bill_runs`、`bill_run_meters`、`bill_results`、`monthly_bill_observations`。
Worker 会按尚无终态电表续跑。

全量运行中可执行只读结构审计。
运行中的汇总计数允许落后一个 15 秒心跳窗口：

```bash
bash scripts/audit_bill_run.sh <run-id> --expected-meters 7519 --allow-running
```

父任务结束后先做结构审计。
对 `partial/error` 创建 retry 子任务后，再要求子任务零错误。
脚本会校验冻结电表、逐表结果、每表冻结月份。
还会校验观测总数、运行汇总计数、月份边界和规范账单引用。
任何断言失败均返回非零：

```bash
bash scripts/audit_bill_run.sh <parent-run-id> --expected-meters 7519
bash scripts/audit_bill_run.sh <retry-run-id> --require-clean
```

## 6. 备份与恢复

备份输出必须写到受限目录，不能进入仓库：

```bash
umask 077
mkdir -p ~/backups/edu-power-push
docker compose exec -T db pg_dump -U edu_power -d edu_power -Fc > ~/backups/edu-power-push/db-$(date +%F-%H%M).dump
pg_restore --list ~/backups/edu-power-push/db-$(date +%F-%H%M).dump >/dev/null
```

恢复前先停止 API/Worker，并明确选择目标数据库：

```bash
docker compose stop api worker
docker compose exec -T db pg_restore -U edu_power -d edu_power --clean --if-exists < /path/to/selected.dump
docker compose start api worker
```

恢复会破坏目标库。
必须先验证备份文件和目标项目名。

恢复演练不是可选项。
**只有真的恢复过一次，备份才算存在。**
把备份恢复到一个隔离的临时 PostgreSQL 容器里，核对表行数与迁移序号，再删掉它。

## 7. 更新与回滚

每次发布使用独立版本目录。
更新前备份数据库。
在新目录执行 `docker compose build`，再切换服务。
数据库迁移由 API/Worker 启动时在 PostgreSQL advisory lock 下串行执行。

仅当新版本健康检查、授权检查、分级扫描和备份校验均通过后，才原子切换 `current` 符号链接。
失败候选不得成为 `current`。

回滚应用镜像前先确认迁移是否向后兼容。
内部迁移序列依次加入用户认证、全局独占电表绑定、前端运行配置。
序列还含月账单任务、观测与修订表。
迁移序号只用于数据库升级，不是产品版本。
对外版本统一为 `version_1`。
现有迁移均采用前滚策略，没有自动 down migration。
禁止通过删除卷回滚数据库。
若旧镜像不理解新增字段/表，应保留数据库结构。
只回滚到明确兼容的应用版本。
