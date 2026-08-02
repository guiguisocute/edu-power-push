package storage

/* 全校月度用量。数据源为月账单。
   账单为权威值。来自上游官方接口。
   日序列优先官方 electricdetail。未结算日回退扫描差值。
   月账单为独立月度对账真值。
   保留独立路径：月度对账、修订审计、日明细未齐时的长期趋势。
   禁止与日序列混画。粒度不同（月 vs 日）。
   biller 按 BILL_CRON 重抓。上游改数写入 bill_revisions。 */

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CampusBillMonth 为全校或某栋楼某月账单合计。
type CampusBillMonth struct {
	Month    string `json:"month"` // YYYY-MM
	TotalKWH string `json:"total_kwh"`
	CostYuan string `json:"cost_yuan"`
	// Meters 为该月逐日判定后的日均非空房数。月度总量仍含空房用量。
	Meters     int    `json:"meters"`
	PerRoomKWH string `json:"per_room_kwh"`
}

// CampusBillBuilding 为某月按楼栋拆分。
type CampusBillBuilding struct {
	Building string  `json:"building"`
	TotalKWH string  `json:"total_kwh"`
	Meters   int     `json:"meters"`
	Share    float64 `json:"share"`
}

// CampusBillsView 为 GET /api/v1/campus/bills 的响应。
type CampusBillsView struct {
	FromMonth string               `json:"from_month"`
	ToMonth   string               `json:"to_month"`
	Building  string               `json:"building,omitempty"`
	Months    []CampusBillMonth    `json:"months"`
	Buildings []CampusBillBuilding `json:"buildings,omitempty"`
	Source    string               `json:"source"` // 恒为 monthly_bill。不是读数差值口径。
}

// GetCampusBills 汇总全校月度账单。
// building 非空时仅统计该楼栋。额外返回最后一个月的楼栋拆分。
func GetCampusBills(
	ctx context.Context, pool *pgxpool.Pool,
	fromMonth, toMonth, building string,
) (CampusBillsView, error) {
	from, err := normalizeBillMonth(fromMonth)
	if err != nil {
		return CampusBillsView{}, err
	}
	to, err := normalizeBillMonth(toMonth)
	if err != nil {
		return CampusBillsView{}, err
	}
	if from != "" && to != "" && from > to {
		return CampusBillsView{}, fmt.Errorf("from_month must not be after to_month")
	}

	view := CampusBillsView{FromMonth: fromMonth, ToMonth: toMonth, Building: building, Source: "monthly_bill"}

	rows, err := pool.Query(ctx, `
		WITH bill_values AS (
			SELECT b.month, sum(b.usage_kwh) AS total_kwh, sum(b.cost_yuan) AS cost_yuan
			FROM monthly_bills b JOIN meters m ON m.id=b.meter_id
			WHERE ($1='' OR b.month >= ($1 || '-01')::date)
			  AND ($2='' OR b.month <= ($2 || '-01')::date)
			  AND ($3='' OR m.building=$3) AND m.building<>$4
			  AND m.active AND NOT m.excluded
			GROUP BY b.month
		), daily_counts AS (
			/* 无月份参数时覆盖全部历史。汇总表是解法。
			   读取 effective_daily_campus_rollup。见 migration 000033。 */
			SELECT date_trunc('month',r.usage_date)::date AS month, r.usage_date,
			       sum(r.occupied_rooms) AS rooms
			FROM effective_daily_campus_rollup r
			WHERE ($1='' OR r.usage_date >= ($1 || '-01')::date)
			  AND ($2='' OR r.usage_date < (($2 || '-01')::date + interval '1 month'))
			  AND ($3='' OR r.building=$3) AND r.building<>$4
			GROUP BY 1,2
		), occupancy AS (
			SELECT month, round(avg(rooms))::integer AS rooms FROM daily_counts GROUP BY month
		)
		SELECT to_char(b.month,'YYYY-MM'), b.total_kwh::text, b.cost_yuan::text,
		       COALESCE(o.rooms,0),
		       CASE WHEN COALESCE(o.rooms,0) > 0
		            THEN round(b.total_kwh / o.rooms, 4)::text
		            ELSE '0' END
		FROM bill_values b LEFT JOIN occupancy o USING (month)
		ORDER BY b.month`, from, to, building, nonPlatformBillingBuilding)
	if err != nil {
		return CampusBillsView{}, err
	}
	defer rows.Close()
	view.Months = make([]CampusBillMonth, 0)
	for rows.Next() {
		var m CampusBillMonth
		if err := rows.Scan(&m.Month, &m.TotalKWH, &m.CostYuan, &m.Meters, &m.PerRoomKWH); err != nil {
			return CampusBillsView{}, err
		}
		view.Months = append(view.Months, m)
	}
	if err := rows.Err(); err != nil {
		return CampusBillsView{}, err
	}
	if len(view.Months) == 0 {
		return view, nil
	}

	// 楼栋拆分仅给区间最后一个月。供热力图与排行使用。
	last := view.Months[len(view.Months)-1].Month
	brows, err := pool.Query(ctx, `
		WITH bill_values AS (
			SELECT m.building, sum(b.usage_kwh) AS total_kwh
			FROM monthly_bills b JOIN meters m ON m.id=b.meter_id
			WHERE b.month = ($1 || '-01')::date
			  AND ($2='' OR m.building=$2) AND m.building<>$3
			  AND m.active AND NOT m.excluded
			  AND m.building IS NOT NULL AND m.building <> ''
			GROUP BY m.building
		), daily_counts AS (
			SELECT r.building, r.usage_date, sum(r.occupied_rooms) AS rooms
			FROM effective_daily_campus_rollup r
			WHERE r.usage_date >= ($1 || '-01')::date
			  AND r.usage_date < (($1 || '-01')::date + interval '1 month')
			  AND ($2='' OR r.building=$2) AND r.building<>$3
			GROUP BY r.building,r.usage_date
		), occupancy AS (
			SELECT building, round(avg(rooms))::integer AS rooms FROM daily_counts GROUP BY building
		)
		SELECT b.building, b.total_kwh::text, COALESCE(o.rooms,0)
		FROM bill_values b LEFT JOIN occupancy o USING (building)
		ORDER BY b.total_kwh DESC`, last, building, nonPlatformBillingBuilding)
	if err != nil {
		return CampusBillsView{}, err
	}
	defer brows.Close()
	buildings, err := pgx.CollectRows(brows, func(row pgx.CollectableRow) (CampusBillBuilding, error) {
		var b CampusBillBuilding
		err := row.Scan(&b.Building, &b.TotalKWH, &b.Meters)
		return b, err
	})
	if err != nil {
		return CampusBillsView{}, err
	}
	var total float64
	for _, b := range buildings {
		total += parseFloatOrZero(b.TotalKWH)
	}
	if total > 0 {
		for i := range buildings {
			buildings[i].Share = parseFloatOrZero(buildings[i].TotalKWH) / total
		}
	}
	view.Buildings = buildings
	return view, nil
}

// parseFloatOrZero 仅用于算占比。数值以字符串传递。避免浮点误差。
// 占比为展示近似值。不影响落库或对账。
func parseFloatOrZero(raw string) float64 {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return v
}

/*
GetCampusBillBreakdown 用月账单铺楼栋或楼层 × 月矩阵。
	年视图热力图必须使用已持久化官方历史。禁止用当日扫描估算。
	返回形状与 GetCampusBreakdown 一致。
	buckets 列出 from_month 到 to_month 全部月份。缺数据为 null。
*/
func GetCampusBillBreakdown(
	ctx context.Context, pool *pgxpool.Pool,
	fromMonth, toMonth, building string,
) (CampusBreakdownView, error) {
	from, err := normalizeBillMonth(fromMonth)
	if err != nil {
		return CampusBreakdownView{}, err
	}
	to, err := normalizeBillMonth(toMonth)
	if err != nil {
		return CampusBreakdownView{}, err
	}
	if from == "" || to == "" {
		return CampusBreakdownView{}, fmt.Errorf("from_month and to_month are required")
	}
	if from > to {
		return CampusBreakdownView{}, fmt.Errorf("from_month must not be after to_month")
	}

	groupBy := "building"
	if building != "" {
		groupBy = "floor"
	}
	result := CampusBreakdownView{
		GroupBy: groupBy, Granularity: "month",
		Buckets: []time.Time{}, Rows: []CampusBreakdownRow{},
		TotalKWH: "0", MaxCellKWH: "0",
		Availability: "ready",
	}

	// 完整月序列。横轴固定。不因缺账单塌缩。
	fromT, _ := time.Parse("2006-01", from)
	toT, _ := time.Parse("2006-01", to)
	buckets := make([]time.Time, 0, 16)
	for t := fromT; !t.After(toT); t = t.AddDate(0, 1, 0) {
		buckets = append(buckets, t)
	}
	result.Buckets = buckets
	index := make(map[string]int, len(buckets))
	for i, b := range buckets {
		index[b.Format("2006-01")] = i
	}

	/* 空房按日变化。Meters 为整窗日均非空房数。
	   meter_counts 为逐月日均值。
	   使用 GROUPING SETS 一次算两个粒度。
	   数据源为 effective_daily_campus_rollup。见 migration 000033。
	   窗口平均等于整体平均。 */
	meterCounts := map[string]int{}
	bucketMeterCounts := map[string]map[string]int{}
	countRows, err := pool.Query(ctx, `
		WITH daily_counts AS (
			SELECT CASE WHEN $3='' THEN r.building ELSE r.floor END AS row_key,
			       to_char(r.usage_date,'YYYY-MM') AS ym, r.usage_date,
			       sum(r.occupied_rooms) AS rooms
			FROM effective_daily_campus_rollup r
			WHERE r.usage_date >= ($1 || '-01')::date
			  AND r.usage_date < (($2 || '-01')::date + interval '1 month')
			  AND r.building<>$4
			  AND ($3='' OR r.building=$3)
			GROUP BY 1,2,3
		)
		SELECT row_key, ym, round(avg(rooms))::integer, grouping(ym)
		FROM daily_counts
		GROUP BY GROUPING SETS ((row_key, ym), (row_key))`,
		from, to, building, nonPlatformBillingBuilding)
	if err != nil {
		return result, err
	}
	for countRows.Next() {
		var key string
		var ym *string
		var n, windowLevel int
		if err := countRows.Scan(&key, &ym, &n, &windowLevel); err != nil {
			countRows.Close()
			return result, err
		}
		// grouping(ym)=1 为整窗汇总。ym 为 NULL。
		if windowLevel == 1 {
			meterCounts[key] = n
			continue
		}
		if ym == nil {
			continue
		}
		if bucketMeterCounts[key] == nil {
			bucketMeterCounts[key] = map[string]int{}
		}
		bucketMeterCounts[key][*ym] = n
	}
	countRows.Close()
	if err := countRows.Err(); err != nil {
		return result, err
	}

	rows, err := pool.Query(ctx, `
		SELECT CASE WHEN $3='' THEN m.building ELSE m.floor END AS row_key,
		       to_char(b.month,'YYYY-MM') AS ym,
		       COALESCE(sum(b.usage_kwh),0)::numeric(24,4)::text
		FROM monthly_bills b JOIN meters m ON m.id=b.meter_id
		WHERE b.month >= ($1 || '-01')::date
		  AND b.month <= ($2 || '-01')::date
		  AND m.active AND NOT m.excluded AND m.building<>$4
		  AND ($3='' OR m.building=$3)
		  AND CASE WHEN $3='' THEN m.building ELSE m.floor END IS NOT NULL
		  AND CASE WHEN $3='' THEN m.building ELSE m.floor END <> ''
		GROUP BY 1, 2
		ORDER BY 1, 2`, from, to, building, nonPlatformBillingBuilding)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	type cell struct {
		ym    string
		value string
	}
	cells := map[string][]cell{}
	for rows.Next() {
		var key, ym, value string
		if err := rows.Scan(&key, &ym, &value); err != nil {
			return result, err
		}
		cells[key] = append(cells[key], cell{ym: ym, value: value})
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	grandTotal := 0.0
	maxCell := 0.0
	for key, list := range cells {
		row := CampusBreakdownRow{
			Key: key, Meters: meterCounts[key],
			MeterCounts: make([]int, len(buckets)), Values: make([]*string, len(buckets)),
		}
		for i, bucket := range buckets {
			row.MeterCounts[i] = bucketMeterCounts[key][bucket.Format("2006-01")]
		}
		total := 0.0
		for _, c := range list {
			i, ok := index[c.ym]
			if !ok {
				continue
			}
			value := c.value
			row.Values[i] = &value
			if v, err := strconv.ParseFloat(c.value, 64); err == nil {
				total += v
				if v > maxCell {
					maxCell = v
				}
			}
		}
		row.TotalKWH = strconv.FormatFloat(total, 'f', 4, 64)
		grandTotal += total
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool {
		a, _ := strconv.ParseFloat(result.Rows[i].TotalKWH, 64)
		b, _ := strconv.ParseFloat(result.Rows[j].TotalKWH, 64)
		if a == b {
			return result.Rows[i].Key < result.Rows[j].Key
		}
		return a > b
	})
	if grandTotal > 0 {
		for i := range result.Rows {
			v, _ := strconv.ParseFloat(result.Rows[i].TotalKWH, 64)
			result.Rows[i].Share = v / grandTotal
		}
	} else if len(result.Rows) == 0 {
		result.Availability = "insufficient_history"
	}
	result.TotalKWH = strconv.FormatFloat(grandTotal, 'f', 4, 64)
	result.MaxCellKWH = strconv.FormatFloat(maxCell, 'f', 4, 64)
	// 账单路径无覆盖/陈旧读数语义。质量字段给完整覆盖占位。
	nMeters := 0
	for _, n := range meterCounts {
		nMeters += n
	}
	result.Quality = Quality{Eligible: nMeters, Covered: nMeters, CoverageRatio: 1, Stale: 0, Anomalies: 0}
	return result, nil
}

// normalizeBillMonth 接受 "2025-01" 与 "2025-01-01"。空串表示不限。
func normalizeBillMonth(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	for _, layout := range []string{"2006-01", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01"), nil
		}
	}
	return "", fmt.Errorf("month must look like YYYY-MM, got %q", raw)
}
