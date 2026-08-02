# 校园聚合数据缺口（提案）· 供后端使用

> 补两处前端已有 UI、但早期 `campus/*` 契约喂不了的数据。
> 分别是 **筛选项来源** 与 **同楼平均**。
> 两项均已接通，不阻塞主流程。
>
> 鉴权前提见 `AUTH-GAPS.md` §0：`campus/*` 全部匿名开放（`security: []`）。

---

## 1. 筛选项 `GET /api/v1/campus/scopes` · ✅ 已上线（2026-07-27）

**实际落地**与提案的差异：

- `security` 最终是 `[]`（匿名开放），与其余 `campus/*` 同档。
- 口径是 **eligible（`active AND NOT excluded`）电表所在的楼栋楼层**。
  该口径与 `campus/series`、`campus/rankings` 的分母完全一致。
  提案里写的「只含有可用读数的」会让下拉随扫描覆盖率抖动。
  反而不可预期。
- `room_count` 已上线（每楼栋 eligible 电表数）。
- **真实楼层字符串很脏**。
  实测形如 `1楼(不是NF，SF)`、`SF,NF架空层`、`3楼N`、`2楼`。
  前端因此**原样显示、原样回传**。
  任何拼接（`+F`、补零）都会既难看又对不上查询参数。
  `lib/scopes.ts` 与 `BoardView` 的地址渲染已按此改。

下面是原始提案：

排行榜与数据看板都有「楼栋 / 楼层」两级下拉。
live 模式下若仍用 mock 的 `DORMS` 常量填选项，一旦真实楼栋名对不上就会空榜。

唯一能给出层级的既有端点是 `GET /api/v1/inventory/tree`。
但它：管理端保护、返回全量房间、带 `excluded` 运维信息。
对一个下拉框来说过重且拿不到。

```
GET /api/v1/campus/scopes
→ 200
{
  "updated_at": "2026-07-27T03:00:00+08:00",
  "campuses": [
    {
      "name": "示例校区",
      "buildings": [
        { "name": "河东 12 栋", "floors": ["1", "2", "3", "4", "5", "6"], "room_count": 168 },
        { "name": "河西 3 栋",  "floors": ["1", "2", "3", "4"],           "room_count": 96 }
      ]
    }
  ]
}
```

- 只含**有可用读数**的楼栋楼层（excluded / 无库存的不出现）。
  这样筛选结果一定非空。
- `room_count` 供「共 N 间」文案，可选。
- 楼层用字符串。
  与 `RankingEntry.floor`、`campus/series` 的 `floor` 参数取值**逐字一致**。
  前端直接把下拉值透传回查询参数。
  两边格式不一致就会静默查空。
- 变动频率极低。
  响应可加 `Cache-Control: max-age=3600`。
  前端启动拉一次。

---

## 2. 同楼平均 · ✅ 已用现有聚合接通（未新增 `metric=per_room`）

**实际落地**：没有给 `campus/series` 增加 `metric=per_room`，而是复用已有字段：

| 位置 | 用途 | live 数据来源 |
|---|---|---|
| 概览 用电量图 | 红色虚线「同楼平均」 | 日/周：`campus/series?building=` 总量 ÷ 有数表数；年视图：`campus/bills?building=` 的 `per_room_kwh` |
| 用电分析 | 「同楼 / 同层 / 全校」户均对比 | `campus/summary?building|floor`（及全校）的 `per_room_kwh`（按窗口日数折日均） |

`campus/summary.per_room_kwh` 的除数是查询窗口内逐日判定后的日均非空房数。
阈值键为 `empty_room_threshold_kwh`（默认 0.3 kWh/日）。
不是「eligible 全量硬除」。

### 原始方案：给 `campus/series` 增加 `metric` 参数（可选优化，未上线）

```
GET /api/v1/campus/series?from=&to=&granularity=day&building=河东%2012%20栋&metric=per_room
```

- `metric`: `total`（默认，保持现有行为不变）/ `per_room`
- 这会少一次前端除法往返。
  当前写法已够用，不阻塞产品。

### 隐私边界

同楼平均是聚合值。
**楼层粒度 + 房间数很少时可反推个人**。
后端以日均非空房为除数。
覆盖过低时前端不画假低线。

---

## OpenAPI 片段

```yaml
  /api/v1/campus/scopes:
    get:
      tags: [Campus]
      operationId: getCampusScopes
      summary: Lightweight campus/building/floor list for frontend filters
      security: []
      responses:
        "200":
          description: Scope tree limited to buildings and floors that have usable readings
          content: { application/json: { schema: { $ref: "#/components/schemas/CampusScopes" } } }
```

在既有 `/api/v1/campus/series` 的 `parameters:` 列表末尾追加一项：

```yaml
- name: metric
  in: query
  schema:
    type: string
    enum: [total, per_room]
    default: total
  description: >
    total = range aggregate (current behaviour).
    per_room = mean per eligible room in the range; null when coverage is too low.
```

`components.schemas` 追加：

```yaml
    CampusScopes:
      type: object
      required: [updated_at, campuses]
      properties:
        updated_at: { type: string, format: date-time }
        campuses:
          type: array
          items:
            type: object
            required: [name, buildings]
            properties:
              name: { type: string }
              buildings:
                type: array
                items:
                  type: object
                  required: [name, floors]
                  properties:
                    name: { type: string }
                    floors:
                      type: array
                      items: { type: string }
                    room_count: { type: [integer, "null"] }
```
