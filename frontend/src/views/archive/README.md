# 归档视图片段

存放**当前上游数据能力用不上**、但代码完整、将来可重新启用的界面。  
逻辑与样式仍保持可挂载状态，默认不进入主路径。

与主视图的关系：

```
UsageView
  └─ features.charts.hourly_usage === true
        → 动态/条件挂载 archive/HourlyUsageSection
        → 请求 GET /api/v1/campus/hourly-heatmap
        → 本校上游固定返回 availability=unavailable
             reason_code=upstream_hourly_data_unavailable
```

---

## HourlyUsageSection.tsx · 分时用电

| 项 | 说明 |
|---|---|
| 内容 | 分时热力图、时段画像、尖峰小时 |
| 数据依赖 | 小时级用电；本校上游每日约两个抄表窗口，无可靠小时拆分 |
| 后端 | `GET /api/v1/campus/hourly-heatmap` 固定不可用说明 |
| 开关 | `features.charts.hourly_usage`（`GET /frontend-config`，运维「前端展示」） |
| 默认 | `false`。打开后 `UsageView` 重新挂载本组件 |

---

## 相关但不在本目录的「日」视图

数据看板「日」周期由 `features.charts.day_range` 控制，逻辑仍在 `CampusView.tsx`（24h 负荷等），默认关闭。原因同样是上游无可靠小时数据。

开关表与默认值见 [`../../../docs/FRONTEND-CONFIG.md`](../../../docs/FRONTEND-CONFIG.md)。
