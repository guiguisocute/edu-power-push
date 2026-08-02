# EDU Power Push API v1

机器可读契约是 [api/openapi.yaml](../api/openapi.yaml)。
本文说明编写或消费 API 时易遗漏的语义。

## 访问边界

- API 默认监听 `127.0.0.1:8080`（Compose 仅映射回环）。
  公网访问经同源反向代理 + 443 上的 TLS。
  运维也可经 SSH 隧道访问回环端口。
- 健康检查、产品认证、`GET /api/v1/frontend-config` 在产品入口（及回环）内公开。
  匿名校园聚合同样公开。
- 内部运维与原始单表端点使用操作员/管理员会话，或 `Authorization: Bearer <ADMIN_TOKEN>`。
- 产品用户端点使用短期 access JWT。
  轮换的 refresh JWT 仅作为 HttpOnly、SameSite=Strict cookie 发送。
  作用域为 `/api/v1/auth`。
- 校园聚合（`/api/v1/campus/*`）匿名。
  无账号即可查看全校用电与排行榜是产品前提。
  登录后仅增加本人电表数据。
  返回的是楼栋与楼层聚合。
  不含账号身份。
- 排行榜匿名。
  身份脱敏在服务端执行。
  `RankingEntry.label` 按该表所有者偏好渲染。
  默认显示楼栋与楼层，隐藏房间。
  明文 `building/floor/room` 仅返回调用者本人所在行。
  所有者已退出榜单的电表永不出现。
  见 `GET/PUT /api/v1/me/leaderboard`。
  请求携带有效用户 access JWT 且账号已绑表时，响应还含 `self`。
  `self` 是相对全体人口的名次，与 Top-N 分页无关。
  响应还可含可选的 `neighbors`。
- 公网入口仍可在网络层拒绝 `/api/v1/admin/*`，以形成纵深防御。
  面板本身要求角色认证加入口令牌。
  `ADMIN_TOKEN` 是机器凭据，不是浏览器密钥。
- 本部署必须绑定回环。
  仅有 Token 不等于允许在无 TLS/网络评审下暴露进程。

## 产品用户认证

首个产品身份是邮箱 + 密码。
手机号/SMS 有意延后。
注册只创建账号。
`RegisterRequest` 中 `meter` 可选且已弃用。
前端在登录后绑表。
会话建立前绑表会让匿名访客用八位数表号反查地址。
电表必须存在于当前清单。
电表必须为 active 且未排除。
绑定与换绑从不发起上游查询。
每个账号恰好有一个活动绑表。
每块表只能成为一个账号的活动绑定。
PostgreSQL 部分唯一索引在并发请求下同时约束双方。

| 动作 | 端点 | 凭据 |
|---|---|---|
| 请求注册验证码 | `POST /api/v1/auth/register/code` | email |
| 注册 | `POST /api/v1/auth/register` | email、password、6 位验证码、nickname |
| 登录 | `POST /api/v1/auth/login` | email、password |
| 请求找回密码验证码 | `POST /api/v1/auth/password/forgot` | email（始终 204） |
| 重置密码 | `POST /api/v1/auth/password/reset` | email、code、新密码 |
| 开始第三方登录 | `GET /api/v1/auth/oauth/{provider}/start` | 无（302 到 Google/GitHub） |
| 结束第三方登录 | `GET /api/v1/auth/oauth/{provider}/callback` | authorization code + state cookie |
| 轮换 | `POST /api/v1/auth/refresh` | refresh cookie |
| 登出 | `POST /api/v1/auth/logout` | refresh cookie |
| 当前用户 | `GET /api/v1/me` | access JWT |
| 删除账号 | `DELETE /api/v1/me` | access JWT + 当前密码 |
| 预览电表 | `GET /api/v1/me/meter/preview` | access JWT |
| 绑定或更换电表 | `PUT /api/v1/me/meter` | access JWT |
| 刷新已绑电表 | `POST /api/v1/me/refresh` | access JWT |
| 渠道 / 规则 / 推送记录 | `/api/v1/me/channels*`、`/notification-settings`、`/push-logs` | access JWT |
| 榜单隐私 | `GET/PUT /api/v1/me/leaderboard` | access JWT |

`DELETE /api/v1/me` 要求请求体中的当前密码。
不能仅靠有效 access token。
解锁的手机不足以注销账号。
删除是真实行删除。
账号拥有的数据经 `ON DELETE CASCADE` 一并删除。
范围包括：绑表、refresh 会话、榜单偏好。
范围包括：通知设置与渠道（含已存密钥）、推送记录。
删除绑表后，该表可供下一入住人绑定。
电表读数、用电差值与月账单挂在 `meters` 上，不挂在账号上。
它们因此保持不变。
它们是全校统计的账本，不是个人财产。
最后一个 `admin` 账号不能删除自身（HTTP 409 `last_admin`）。
否则无人能再次进入面板。

`GET /api/v1/me/meter/preview` 在用户确认绑表前回答「这是我的宿舍吗？」。
它返回 campus/building/floor/room 与 `claimable`。
从不返回余额或任何读数。
电表尚未属于调用者。
返回余额会让任意账号扫遍全校宿舍余额。
限流按用户（3 s 间隔，突发 20），不按 IP。
会话使账号成为限流主体。

密码存为加盐 Argon2id PHC 字符串。
从不做可逆加密。
Access JWT 默认 15 分钟，并携带 refresh 会话族 ID。
受保护请求要求该族仍有活动 refresh 会话。
因此登出、改密、会话撤销与账号禁用会立即作废 access JWT。
Refresh JWT 默认 30 天。
它们在 PostgreSQL 中哈希保存。
它们每次使用轮换，并归属一个会话族。
轮换结束时，已在途的同浏览器请求获得短并发宽限。
该宽限服务共享 cookie 的标签页。
轮换后启动的重放会撤销该族。
Access 与 refresh JWT 有独立 audience 与 `token_use` 声明。
它们不能互相替代。

API 在 JSON 中返回 access JWT，供前端保存在内存。
API 从不在 JSON 或 localStorage 中返回 refresh JWT。
登录错误有意不透露邮箱是否存在。
认证端点对每个客户端有保守的进程内限流。
分布式限流延后到多 API 副本时再引入。

### 第三方登录（Google、GitHub）

两个供应商均使用 authorization-code 流程。
Google 使用 PKCE。
GitHub OAuth Apps 仍不接受 `code_challenge`。
这两个端点是浏览器以*导航*而非 `fetch` 到达的仅有端点。
这一特性决定了它们的全部行为。
它们始终以 302 响应。
失败以站点 URL 上的 `?oauth_error=<reason>` 返回。
它们不返回用户会直视的 JSON 正文。
reason 为 `denied`、`state`、`exchange`、`email_missing`、`email_unverified`。
reason 还有 `registration_disabled`、`conflict`、`account_disabled`、`disabled_entry`、`unavailable`。
底层错误留在日志中。
供应商消息携带 client ID 与 redirect URI。
这些消息不得出现在地址栏。

身份键是供应商自身的稳定 subject。
Google 的 `sub`、GitHub 的数字 `id`。
从不是邮箱。
两个供应商都允许用户更改邮箱。
`user_oauth_identities` 在 `(provider, subject)` 与 `(user_id, provider)` 上唯一。

仅当供应商报告邮箱为**已验证**时，新身份才挂到已有账号。
此时「我用邮箱注册，再点 Sign in with Google」落到同一账号。
未验证邮箱不链接、不创建（`email_unverified`）。
在 GitHub 资料上填他人地址无需成本。
GitHub 的 `/user` 公开邮箱被忽略。
改用 `/user/emails` 中 `primary` + `verified` 条目。
无匹配账号时创建无密码账号，并设置 `email_verified_at`。
此处 `registration_disabled` 与邮箱注册规则相同。

此类账号的 `has_password` 为 `false`。
在存在密码之前，下列接口返回 HTTP 409 `password_not_set`：
`PUT /api/v1/me/password`、`PUT /api/v1/me/email` 与 `DELETE /api/v1/me`。
要求不放宽。
解锁的手机仍不足以删除账号。
密码找回设置首个密码。
已验证邮箱已支持该路径。

回调**仅**设置 refresh cookie 并重定向。
随后 SPA 调用 `/api/v1/auth/refresh` 获取 access token。
令牌从不放入 URL。
URL 会进入历史、`Referer` 与代理日志。
流程的 `state` 与 PKCE verifier 存在短生命周期 cookie 中。
该 cookie 为 `SameSite=Lax` 的 `edu_oauth`。
使用 Lax 而非 Strict，正是为了跨站返回时能存活。
refresh cookie 保持 Strict。
它仅在 SPA 后续同源请求中需要。
`?next=` 限制为同站路径。
因此该流程不能变成挂着本域名的开放重定向。

凭据是运行时配置。
可在 `GET`/`PUT /api/v1/admin/settings/oauth`（仅 admin）编辑。
凭据像邮件供应商一样存入 `system_settings`。
client ID 与站点 base URL 明文存储。
client secret 用 AES-GCM 加密。
环境变量 `OAUTH_BASE_URL`（回退到 `PUBLIC_BASE_URL`）仍作为**按字段回退**。
`GOOGLE_OAUTH_CLIENT_ID` / `_SECRET` 与 `GITHUB_OAUTH_CLIENT_ID` / `_SECRET` 同样按字段回退。
仅当面板某字段为空时使用环境值。
因此既有部署可原样继续工作。
仅在面板配置 GitHub 时，Google 仍用环境值。
保存会使 5 秒缓存失效。
无需重启即可生效。

redirect URI 始终由该 base URL 构建。
它从不取自请求的 `Host`。
代理或客户端可以伪造 `Host`。
面板响应返回须粘贴到供应商控制台的精确 `redirect_uris`。
误填该行是配置失败的最常见原因。
半对凭据对在保存时拒绝（HTTP 400 `oauth_not_configured`）。
来自环境时会导致启动失败。
密钥遵循与邮件设置相同的三态写约定。
字段缺省则保留已存值。
显式 `null` 清除并回退到环境。
原样回传的掩码值被忽略。

`GET /api/v1/frontend-config` 在 `oauth.google` / `oauth.github` 中只读报告结果能力。
管理面板开关 `features.auth.google_oauth` / `github_oauth` 只控制是否展示入口。

注册可在运行时经前端配置关闭。
该状态下注册验证码与注册端点返回 HTTP 403 与 `registration_disabled`。
既有用户仍可登录。
注册与密码找回使用六位邮箱验证码。
手机号/SMS 有意不支持。

`POST /api/v1/me/refresh` 对已绑电表做一次真实上游读取。
结果经与定时扫描相同的去重与差值管道落库。
服务强制每表 30 秒冷却。
另有短等待 refresh 槽位上限。
它不等待批任务结束。
每次 HTTP 请求仍共享分布式闸门。
该闸门是 `UPSTREAM_GLOBAL_QPS` / `UPSTREAM_GLOBAL_CONCURRENCY`。
余额、账单、日明细采集也共享同一闸门。
`status=upstream_unavailable` 表示上游未返回可用数据。
它也表示 refresh 槽位在 2 秒内保持饱和，或请求已取消。
此时返回缓存的最新读数。

Refresh 运行像其他运行一样记入 `scan_runs`。
它们排除在 `GET /api/v1/admin/scan-runs` 之外（该接口只列批任务）。
它们与 `scan_results` 在 `SCAN_RESULT_RETENTION_DAYS` 到期后一并清理。

产品前端对任何个人数据必须使用 `/api/v1/me/*` 与用户 access JWT。
校园聚合（`/api/v1/campus/summary|series|rankings|scopes|breakdown|bills|hourly-heatmap`）无需凭据。
原始 `/meters/*`、`/inventory/tree` 与全部 `/admin/*` 端点仅限操作员。
认证方式为操作员/管理员会话或 `ADMIN_TOKEN`。
禁止在公开前端构建中嵌入 `ADMIN_TOKEN`。
`GET /api/v1/campus/scopes` 使筛选下拉无需操作员库存树。
它只列出含 eligible 电表的校区、楼栋与楼层。
用户可选的每一项都返回数据。

## 表示规则

- 全部时间戳为 RFC 3339。服务以 UTC 存储应用时间戳。
- 上游 `reading_time` 值先按 `Asia/Shanghai` 解析，再转换为 UTC。
- 金额与电量值为 JSON 字符串，例如 `"43.87"`。客户端禁止假定 IEEE-754 浮点精度。
- 列表端点使用游标分页。游标不透明。
- 错误有稳定的机器可读 `code` 与请求 trace ID。

```json
{
  "error": {
    "code": "scan_already_running",
    "message": "A campus scan is already running.",
    "request_id": "01J..."
  }
}
```

## 数据可用性

依赖历史覆盖的每项统计都报告可用性状态：

| 状态 | 含义 |
|-------|---------|
| `ready` | 请求范围满足覆盖规则。 |
| `partial` | 存在部分有效数据，但覆盖不完整。 |
| `insufficient_history` | 请求区间内可用读数少于两条。 |
| `unavailable` | 上游源无法提供该能力。 |

伴随的 `quality` 对象始终区分 eligible、covered、stale 与 anomalous 电表。

```json
{
  "availability": "partial",
  "quality": {
    "eligible": 7519,
    "covered": 7310,
    "coverage_ratio": 0.9722,
    "stale": 72,
    "anomalies": 3
  }
}
```

仅当两条有效连续读数证明零差值时，才返回零用电。
缺失历史从不表示为零。

## 扫描语义

- 扫描运行在创建时冻结其 eligible 电表集合与配置。
- `scan_results` 对每次运行的每块 eligible 电表含一条终态结果，即使上游读数未变。
- 新电表读数按电表加上游读数时间去重。
- `valid`、`stale`、`empty`、`error` 与 `parse_error` 是不同的结果状态。
- 重试创建子运行。重试从不修改源运行。
- 学校级任务锁持有期间，第二次校园扫描返回 HTTP 409。
- 优雅停止将未终态运行记为 `interrupted`。
  新 worker 仅续跑尚无终态 `scan_result` 的电表。
  过期的 `running` 心跳先转为 `interrupted`，再恢复。
- `POST /api/v1/admin/{scan-runs,bill-runs,daily-detail-runs}/{run_id}/cancel` 请求取消并立即返回 `202`。
  API 进程无法中断 worker 内循环。
  因此只记录请求。
  worker 在下次心跳（15s）看到请求。
  然后停止派发，等待在飞电表结束。
  再将运行终态定为 `canceled`。
  取消已结束运行返回 `409`。
- `canceled` 是终态，从不自动续跑。
  这与 `interrupted` 不同。
  该区别是此状态的要点。
  恢复有意只查找 `interrupted` 与过期 `running`。
- 面板中采集器禁用时完全跳过恢复。
  否则关闭扫描器无法真正停下。
  重启会把运行标为 `interrupted`。
  下次启动又会续跑。
- 余额扫描、官方日明细运行与账单运行可以重叠。
  PostgreSQL 仅拒绝同类型扫描器的第二个活动运行。
  全部类型共享分布式按请求的 QPS/并发闸门。

## 官方日明细采集

- 运维使用 `GET/POST /api/v1/admin/daily-detail-runs` 与运行详情、结果端点。
  也使用 `POST /api/v1/admin/daily-detail-runs/{run_id}/retry`。
- 普通手工运行采集增量月份集合。
  `initialization=true` 从配置的 bootstrap 月回填。
- 每次运行冻结电表与月份集合。
  重试子运行保留父运行月份集合。
  子运行仅含选定的 `partial`、`empty`、`error` 或 `canceled` 电表。
- 管理扫描器设置文档控制全部三个采集器。
  Worker cron 与未来运行默认值无需重启即可重载。
  活动运行保留其冻结的 `config_snapshot`。

## 月账单采集与核准

- 账单运行在创建时冻结当前 `active && !excluded` 电表集合与可见月份集合。
- 可见月份严格遵循上游 H5 选择器。
  范围是从一月到当前月，再加前一年全部十二个月。
  2026 年 7 月为 19 个月（`2026-07` 至 `2025-01`）。
- 首次生产 full 运行初始化全部可见月，并满足该公历月的计划。
  默认 cron（`0 19 1 * *`）在每月 1 日 19:00 运行。
  此时上游通常已发布上月账单。
  该公历月已记录计划或人工 full 运行时，阻止重复运行。
- 限速适用于每次 HTTP 请求。
  正常电表需要四次会话/准备调用。
  再加每个可见月一次 POST（重试前）。
- 失败月份响应在当前会话使用有界退避。
  若某月仍失败，下一次表级尝试开启新上游会话。
  且只请求失败月份。
  该运行内已成功月份不再抓取。
- 每个请求月份得到一条不可变 `monthly_bill_observation`。
  状态为 `data`、`no_data`、`partial` 或 `error`。
  空当前月是正常情况。
  从不转为零值账单。
- 仅当观测具备全部四个有效数值字段时，才更新产品 API 返回的规范 `monthly_bills` 行。
- 若核准变更已有规范月份，旧值与新值写入 `bill_revisions`。
  管理总览与面板在运维确认前显示未确认修订。
- 优雅停止将运行标为 `interrupted`。
  恢复只查询尚无终态 `bill_result` 的电表。
  每表观测与规范写入在同一 PostgreSQL 事务中提交。
  运行中，心跳最多每 15 秒同步已提交计数器。
  这样可避免管理面板显示过期的零进度。
- `POST /api/v1/admin/bill-runs/{run_id}/retry` 创建可审计子运行。
  子运行仅含从 `partial`、`empty`、`error` 或 `canceled` 中选定的父终态结果。
  它复用父运行的冻结月份。
  它从不删除或覆盖父观测。
  `no_data` 有意不作为重试失败。

运维使用 `GET/POST /api/v1/admin/bill-runs`。
也使用账单运行详情/结果/重试端点，以及 `GET/PATCH /api/v1/admin/bill-revisions`。
创建无界运行需要显式 `full=true`。
省略 `limit` 与 `full` 均会被拒绝。
`dry_run=true` 返回冻结月份列表与最低 HTTP 请求估算。

API 同时暴露原始库存规模与 eligible 分母。
当前参考基线为：

| 计数器 | 基线 |
|---------|----------|
| Inventory | 7655 |
| Excluded categories | 136 |
| Eligible | 7519 |
| Legacy scanner `ok` rows | 7447 |
| Usable imported readings | 7446 |
| Snapshot parse errors | 1 |
| Existing empty rows | 72 |

这些是导入基线，不是硬编码运行时总数。

## 用电语义

- 周期用电是相邻累计 `total_kwh` 读数之差。
- 上游 `reading_time` 定义区间，不是本地查询时间。
- 正差值进入聚合。
- 零差值是有效的未变观测。
- 负差值是 `negative_reset` 异常，从不进入正常聚合。
- 余额变化不作为用电量。充值与补贴会影响余额。
- 日、周、月边界使用 `Asia/Shanghai`。

上游通常每天约两个窗口刷新。
因此 API 不把区间差值摊到虚构小时。
`/api/v1/campus/hourly-heatmap` 有意返回：

```json
{
  "availability": "unavailable",
  "reason_code": "upstream_hourly_data_unavailable",
  "message": "Reliable hourly electricity data is not available from the upstream source.",
  "quality": {
    "eligible": 0,
    "covered": 0,
    "coverage_ratio": 0,
    "stale": 0,
    "anomalies": 0
  }
}
```

## 产品原型映射

| 原型数据需求 | API |
|---------------------|-----|
| 个人余额/状态 | `GET /api/v1/me/overview` |
| 按需个人刷新 | `POST /api/v1/me/refresh` |
| 余额与用电曲线 | `GET /api/v1/me/series` |
| 月账单 | `GET /api/v1/me/bills` |
| 白天与夜间拆分 | `GET /api/v1/me/day-night`，由扫描区间推导；读数时间无法对齐时自禁用 |
| 校区/楼栋总览 | `GET /api/v1/campus/summary` |
| 日/周/月图表 | `GET /api/v1/campus/series` |
| 用电/节约/变化排行 + 本人名次 | `GET /api/v1/campus/rankings`（调用者已登录且已绑表时含 `self` / `neighbors`） |
| 校区层级/筛选 | `GET /api/v1/campus/scopes`（产品）；`GET /api/v1/inventory/tree` 仍仅限操作员 |
| 楼栋/楼层矩阵 | `GET /api/v1/campus/breakdown` |
| 官方月度校园账单 | `GET /api/v1/campus/bills` |
| 小时热力图槽位 | `GET /api/v1/campus/hourly-heatmap`，当前不可用 |
| 通知渠道 / 测试 / 规则 / 记录 | `/api/v1/me/channels*`、`/api/v1/me/notification-settings`、`/api/v1/me/push-logs` |
| 榜单隐私 | `GET/PUT /api/v1/me/leaderboard` |
| 运维仪表盘 | `GET /api/v1/admin/overview`，以及扫描、账单运行、日明细、修订与异常端点 |
| 注册/登录/密码重置/会话 | `/api/v1/auth/*` 与 `GET /api/v1/me` |
| 更换已绑电表 | `PUT /api/v1/me/meter` |
| 个人余额/用电序列 | `GET /api/v1/me/series` |
| 已存个人月账单 | `GET /api/v1/me/bills` |

未确认异常流按严重程度排序（`critical`、`warning`、`info`），再按最新优先。
因此 `negative_reset` 会排在大量 stale-reading 警告之前。
客户端应保持服务端顺序。

## 前端运行配置

`GET /api/v1/frontend-config` 返回带版本、可缓存的配置文档。
它有意仅在回环/SSH 边界内公开。
浏览器可在登录前决定渲染哪些已具备的认证与通知选项。

```json
{
  "version": 1,
  "features": {
    "auth": {
      "email_login": true,
      "sms_login": false,
      "email_code": true,
      "sms_code": false,
      "registration": true
    },
    "channels": {
      "mail": true,
      "sms": true,
      "dingtalk": true,
      "wecom": true,
      "wecom_webhook": true,
      "feishu": true,
      "lark": true,
      "discord": true,
      "pushplus": true,
      "mp": true,
      "qq": true,
      "telegram": true
    },
    "channel_coming_soon": {
      "mail": false,
      "sms": true,
      "dingtalk": false,
      "wecom": false,
      "wecom_webhook": false,
      "feishu": false,
      "lark": false,
      "discord": false,
      "pushplus": false,
      "mp": false,
      "qq": false,
      "telegram": false
    },
    "channel_order": ["mail", "dingtalk", "lark", "wecom", "wecom_webhook", "feishu", "discord", "webhook", "bark", "gotify", "whatsapp", "pushplus", "serverchan_turbo", "serverchan3", "mp", "qq", "napcat", "telegram", "sms"],
    "channel_categories": [
      { "id": "essentials", "name": "开箱即用", "en": "READY TO USE", "desc": "装个 App 或注册免费服务，填一个凭证就能收到推送。", "channels": ["mail", "sms", "pushplus", "serverchan_turbo", "serverchan3", "bark", "whatsapp"] },
      { "id": "group_bots", "name": "群聊机器人", "en": "GROUP CHAT BOTS", "desc": "在钉钉、飞书、企业微信群里加自定义机器人，复制 Webhook 即可。", "channels": ["dingtalk", "feishu", "lark", "wecom", "wecom_webhook", "discord", "telegram"] },
      { "id": "developer", "name": "进阶开发者渠道", "en": "SELF-HOSTED & DEVELOPER", "desc": "需要自建服务或申请开发者应用。", "channels": ["webhook", "gotify", "napcat", "qq", "mp"] }
    ],
    "charts": {
      "day_range": false,
      "hourly_usage": false
    }
  },
  "display": {
    "electricity_rate": "0.62",
    "empty_room_threshold_kwh": "0.3",
    "campus_name": "示例校区",
    "area_name": "示例大学",
    "ranking_refresh_time": "09:00",
    "semesters": []
  },
  "updated_at": "2026-07-27T00:00:00Z"
}
```

运维用 `PUT /api/v1/admin/frontend-config` 更新完整文档。
每次成功写入会递增 `version` 并追加审计行。
`features.channels` 控制可见性。
`features.channel_coming_soon` 把可见渠道变为禁用的 Coming Soon 卡片。
`features.channel_order` 控制用户侧顺序。
隐藏、Coming Soon 或未接入的渠道对既有用户也禁用。
`features.channel_categories` 对用户侧渠道列表分组。
类别按数组顺序渲染。
每个渠道最多属于一个类别。
组内顺序仍来自 `channel_order`。
未归入任何类别的渠道落入末尾 “other” 组。
空数组表示完全不分组。
后端拒绝未知通知渠道。
也拒绝重复或未知顺序项。
拒绝畸形或重复类别 id、无名类别。
还拒绝被两个类别占用的渠道。
拒绝不支持的认证能力、空显示名、无效电价。
空房阈值必须落在 `(0, 10]` kWh 内，否则拒绝。
`display.empty_room_threshold_kwh` 默认为 `0.3`。
`display.ranking_refresh_time` 是本地 `HH:MM` 时间（默认 `09:00`）。
日榜每日切换。
周榜每周一切换。
月榜每月 1 日在该时间切换。
入住率是按房间与日期的事实。
每个自然日对照当日官方 `ydl` 判定。
周/月分母使用该桶的日均入住房间数。
从不用今日状态。
缺失日数据视为未知。
不用作分母。

## 受控扫描请求

开发必须以 limit 与保守服务端默认值起步。

```http
POST /api/v1/admin/scan-runs
Authorization: Bearer <token>
Content-Type: application/json

{
  "limit": 10,
  "qps": 0.5,
  "concurrency": 1,
  "dry_run": false
}
```

服务必须拒绝超过配置安全上限的值，除非运维显式更改部署配置。

无界扫描额外要求 `"full": true`。
同时省略 `limit` 与 `full` 会被拒绝。
dry run 返回范围计数器，不创建扫描运行。

## 清单导入

清单同步仅限管理员，且分两阶段：

1. 将 JSON 上传到 `/api/v1/admin/inventory/imports/validate`。
2. 检查 added、updated、deactivated、duplicate、invalid 与 excluded 计数。
3. 经 `/api/v1/admin/inventory/imports` 应用返回的 `validation_id`。

应用错误或过期的校验必须原子失败。
缺失条目变为 inactive。
其历史读数不删除。

## 邮件

支持的供应商为 `resend`、通用 `smtp` 与 `tencent_ses`。
当前测试部署优先使用 Resend（已验证域名 + API key）。
通用 SMTP 按 host/port/TLS 配置覆盖标准兼容的第三方邮箱。
远端 SMTP 必须使用直接 TLS 或 STARTTLS。
未加密模式仅接受回环 Mailpit/测试服务器。
供应商选择是运行时配置。
来源是环境变量和/或管理面板加密的 `system_settings`。
热重载解析器在面板保存新凭据时重建 sender。
测试端点返回投递记录。
它从不返回供应商凭据或未掩码收件人。

本地集成测试在一次性容器中运行 Mailpit。
以无 TLS 方式将 SMTP 路由到它。
再调用真实邮件端点。
经 Mailpit HTTP API 断言捕获的消息。
Resend 与腾讯云 SES 适配器对注入的 sender 做单元测试。
测试从不接触真实收件人或云账号。

真实 Resend/腾讯云投递仍是环境验收测试。
需要已验证发件域名与运行时凭据。
腾讯云还需已批准模板。
缺少这些部署值时邮件未配置。
但不阻止扫描或 API readiness。
凭据配置错误会导致进程启动失败。
不会静默降级为不发送的 mailer。

月账单从不在小时余额扫描期间抓取。
专用账单 runner 执行初始化与月度核准。
产品只读 API 仅返回完整规范账单。
运维端点保留 empty/partial/error 观测。

## 兼容策略

- v1 内可引入加法字段。
- 删除、重命名或改变字段含义/类型需要新 API 版本。
- OpenAPI 文档是真源。本指南中的示例必须保留在契约测试中。
