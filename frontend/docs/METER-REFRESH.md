# 单表即时刷新契约

> 状态：version_1 前后端均已上线。
> 产品端使用 `/api/v1/me/refresh`。
> 管理端可指定电表。
> 前端仍保留 404/405/501 的兼容降级，以便连接旧环境时不伪造刷新成功。

## 端点（两个变体，产品前端用前者）

```
POST /api/v1/me/refresh                 # 产品用户，userAccessToken，作用于已绑定电表  ← 主用
POST /api/v1/meters/{meter}/refresh     # 内部/管理，operator/admin 或 ADMIN_TOKEN，指定表号
```

- 无请求体，响应 schema 相同。
- **安全模型**：`/api/v1/meters/*` 是运维边界。
  浏览器中的产品前端禁止持有管理令牌。
  因此产品路径只能是 `/me/refresh`
  （与 `backend/docs/API.md` 的 Product-prototype mapping 一致）。
  前端 live 路径调用 `/api/v1/me/refresh`
  （见 `api/client.ts` / 概览刷新按钮）。
- 语义：**只对这一块表**向上游发起一次即时抓取，落库后返回最新读数。
  不是 `POST /api/v1/admin/scan-runs`
  （那是管理端全量/范围扫描，需管理权限，campus 级互斥会 409）。
  用户触发的刷新必须是单表、限流、无管理权限。

## 为什么不能只靠前端重取

`GET .../overview` 返回的是**库里已有**的最近一次读数。
如果采集 worker 的周期是 N 分钟，用户点刷新只重取一次 GET，拿到的仍是同一条记录。
那才是「假刷新」。
本端点让用户能主动触发一次上游抓取。
`status` 字段如实告知这次到底有没有拿到新数据。

## 响应 200

```json
{
  "meter": "31240718",
  "status": "refreshed",
  "latest": {
    "reading_time": "2026-07-27T09:40:00+08:00",
    "observed_at": "2026-07-27T09:41:02+08:00",
    "prepaid_yuan": "43.87",
    "subsidy_yuan": "0.00",
    "total_yuan": "43.87",
    "total_kwh": "3356.48",
    "meter_status": "normal",
    "freshness": "fresh"
  },
  "refreshed_at": "2026-07-27T09:41:02+08:00",
  "next_allowed_at": "2026-07-27T09:41:32+08:00",
  "availability": "ready",
  "quality": { "eligible": 1, "covered": 1, "coverage_ratio": 1, "stale": 0, "anomalies": 0 }
}
```

| 字段 | 语义 |
|---|---|
| `status` | `refreshed` = 上游有新读数并已落库。`cached` = 与库中最后一条相同。`upstream_unavailable` = 上游超时或报错，`latest` 退回库中最后一条。 |
| `latest` | 与 `MeterOverview.latest` 同 schema（`LatestReading`），无任何读数时为 `null` |
| `refreshed_at` | 本次抓取结束时间（RFC 3339） |
| `next_allowed_at` | 该表下次允许刷新的时间；前端据此对齐冷却 |
| `availability` / `quality` | 沿用全局语义，前端不伪造数值 |

`status: upstream_unavailable` 仍返回 **200**
（这是一次成功的「已尝试」应答，前端据 `status` 展示降级文案）。
只有真正的服务端故障才用 5xx。

## 限流（必须服务端实施）

- **每表 30 秒冷却**，与前端按钮 CD 一致。
  前端 CD 只是体验层，服务端限流才是真实防线。
- 冷却内再次请求 → **429**，`Retry-After: <秒>`。
  错误体用既有 `ErrorResponse`（`code: rate_limited`）。
- 另有短时刷新槽位上限。
  所有上游 HTTP 请求仍共享 `UPSTREAM_GLOBAL_QPS` / `UPSTREAM_GLOBAL_CONCURRENCY` 闸门。
- 每个账号只能绑定一块电表，因此按表冷却同时覆盖按账号限流。

## 前端降级矩阵

| 后端应答 | 前端行为 |
|---|---|
| 200 | 清缓存重取 overview/series，刷新时间戳记为本次 |
| 404 / 405 / 501 | 认定端点未上线（旧环境），**静默降级**为清缓存重取（不报错） |
| 429 | 冷却对齐 `Retry-After`，不视为错误 |
| 其他 4xx/5xx / 网络失败 | 按钮旁以 `var(--red)` 显示错误信息，仍执行重取 |
