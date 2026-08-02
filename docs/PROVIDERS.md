# 接入自有学校爬虫

现在的边界只有一条：

```
backend/internal/provider/            契约。核心代码只 import 它，禁止改核心以适配某爬虫
    types.go        Result / Balance / Bill / DailyDetails / QueryOptions …
    provider.go     Querier + Provider 接口、注册表、可热切换的 Dynamic

backend/internal/provider/bdfairy/    一套具体爬虫，自成一体，可整体删除
    bdfairy.go      注册自己 + 学校清单
    client.go       会话、选校区、选表
    parser.go       HTML → 结构化读数
    daily_details.go
    schools.json    实测通过的 55 所学校
```
扫描器只识别 `provider.Querier`。

---

## 必须提供的三个方法

```go
type Querier interface {
    QueryMeter(ctx, meter string, opts QueryOptions) Result
    QueryMonthlyBills(ctx, meter string, months []string, opts MonthlyQueryOptions) MonthlyBillsResult
    QueryDailyDetails(ctx, meter string, months []string, opts DailyDetailQueryOptions) DailyDetailsResult
}
```

三个方法互相独立。
**仅提供第一个也能运行**：在运维面板关闭月账单与日明细即可。
不支持的方法返回带 `Errors` 的空结果。
禁止 panic。

还须提供描述「身份与支持学校」的 `Provider`：

```go
type Provider interface {
    Name() string            // .env 里 PROVIDER 的取值，稳定的小写标识符
    DisplayName() string     // 面板上给人看的名字
    Schools() []School       // 支持的学校；返回空切片 = 没有清单，面板退化为手填 area id
    DefaultBaseURL() string  // 留空表示必须由部署者填 ELECTRICITY_BASE_URL
    Open(SchoolConfig) (Querier, error)
}
```

---

## 五步

1. **复制目录**

   ```bash
   cp -r backend/internal/provider/bdfairy backend/internal/provider/myschool
   ```

2. **改写请求与解析。** `client.go` / `parser.go` / `daily_details.go` 全部是 bdfairy 协议细节，与你的学校无关，可整体重写。必须使用 `QueryOptions` 中的两个回调：

   - `BeforeRequest` —— 每次 HTTP 请求前调用，用于检查「扫描器是否被叫停」
   - `AcquireRequest` —— 取一个全局并发槽，返回释放函数

   **每一次真实 HTTP 请求前后必须调用这两个回调。**
   它们是全站共享的 QPS 与并发闸门。
   绕过即绕过全部限速保护。

3. **改注册信息。** 在 `myschool.go` 中改 `Name()` / `DisplayName()`。重写 `schools.json`，或让 `Schools()` 直接返回 `nil`。

4. **在入口注册。** 三个 `cmd/*/main.go` 各有一行空导入，换成你的包：

   ```go
   _ "github.com/edu-power-push/edu-power-push/backend/internal/provider/myschool"
   ```

   两套可并存：都导入，然后在 `.env` 用 `PROVIDER` 选择，或在运维面板「扫描器 · 学校」中切换。

5. **选中它。** `.env` 写 `PROVIDER=myschool`，或直接在面板上选。

---

## 选学校如何生效

```
system_settings['school']  ──Load──▶  Binding  ──Resolve──▶  provider.Dynamic
        ▲                                                          │
        └── 面板保存 / .env 兜底                          扫描器、月账单、日明细、
                                                          用户手动刷新都从这里取查询器
```

`internal/school` 每 15 秒重新走一遍该路径。
**换学校 = 在面板保存一次。**
禁止依赖「改 .env 再重启三个进程」。

正在运行的那一轮扫描不受影响。
它开始时取到哪个查询器，就用到本轮结束。
换挡发生在两轮之间。
否则一半数据属于 A，一半属于 B。

未选学校是合法状态。
服务照常启动。
面板照常可登录。
用户照常可看历史数据。
仅采集器全部跳过。
日志每十分钟提醒一次。
这优于替用户随意选定一所学校。

---

## 数据契约中的易错点

- **`BillData` 与 `DailyUsageDay` 的 JSON 字段名是 `qcdl` / `ydl` / `ydje` / `sj`。**
  这是 bdfairy 的原始字段名，已作为 JSONB 写入数据库。
  改名即一次数据迁移。
  新代码按这些键填值即可。
  Go 侧字段名是 `StartKWh` / `UsageKWh` / `CostYuan` / `Period`。

- **金额与度数一律用字符串。**
  电费是钱。
  二进制浮点在此无收益。
  解析结果原样填入。
  四舍五入交给展示层。

- **`Result.AreaID` 留空时，`Dynamic` 会自动补上当前学校。**
  若你有更准确的一手信息，自行填写。
  填写后不会被覆盖。

- **`Status` 必须分清 `empty` 与 `error`。**
  `empty` 表示「该表无数据」（不重试、不告警）。
  `error` 表示「本次查询失败」（会重试）。
  混淆后果：要么漏采，要么把空房当故障刷屏。
