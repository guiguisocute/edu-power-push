package importer

/* 解析并校验往期月账单 CSV，用于灾难恢复。
   关联必须用 meter_no，禁止使用旧库 meter_id。
   observed_at_cst 为 CST 墙上时间，必须按部署时区解析。 */

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// billSources 对齐 migrations/000001 的 CHECK 约束。
var billSources = map[string]bool{"legacy_import": true, "live_query": true}

// billTimeLayouts 覆盖 pg 导出的小数秒与缺秒写法。
var billTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
}

// BillRecord 为已校验的 monthly_bills 行。
type BillRecord struct {
	MeterNo    string
	Month      time.Time // 当月 1 号 UTC 零点
	StartKWh   *float64
	EndKWh     *float64
	UsageKWh   *float64
	CostYuan   *float64
	ObservedAt time.Time
	Source     string
}

// BillPlan 供 --dry-run 在写库前核对计数与错误样本。
type BillPlan struct {
	SourceName string
	Records    []BillRecord
	Total      int
	Skipped    int
	Months     []string
	Meters     int
	Errors     []string
}

const maxBillPlanErrors = 20

// ValidateBills 用 encoding/csv 解析导出，避免 floor 内半角逗号导致字段错位。
func ValidateBills(r io.Reader, sourceName string, loc *time.Location) (BillPlan, error) {
	if loc == nil {
		return BillPlan{}, errors.New("bills: timezone is required")
	}
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // 列数由表头决定
	reader.ReuseRecord = true

	head, err := reader.Read()
	if err != nil {
		return BillPlan{}, fmt.Errorf("bills: read header: %w", err)
	}
	idx := map[string]int{}
	for i, name := range head {
		// 去掉 Excel BOM，否则表头匹配不到 meter_no。
		idx[strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))] = i
	}
	for _, want := range []string{"meter_no", "month", "observed_at_cst", "source"} {
		if _, ok := idx[want]; !ok {
			return BillPlan{}, fmt.Errorf("bills: missing required column %q", want)
		}
	}

	plan := BillPlan{SourceName: sourceName}
	months := map[string]bool{}
	meters := map[string]bool{}
	// 同批重复 meter/month 在此拒绝，避免 upsert 中途失败。
	seen := map[string]bool{}

	get := func(row []string, key string) string {
		i, ok := idx[key]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	for line := 2; ; line++ {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return BillPlan{}, fmt.Errorf("bills: line %d: %w", line, err)
		}
		plan.Total++

		rec, problem := parseBillRow(row, get, loc)
		if problem != "" {
			plan.Skipped++
			if len(plan.Errors) < maxBillPlanErrors {
				plan.Errors = append(plan.Errors, fmt.Sprintf("line %d: %s", line, problem))
			}
			continue
		}
		key := rec.MeterNo + "\x00" + rec.Month.Format("2006-01")
		if seen[key] {
			plan.Skipped++
			if len(plan.Errors) < maxBillPlanErrors {
				plan.Errors = append(plan.Errors,
					fmt.Sprintf("line %d: duplicate meter/month %s %s", line, rec.MeterNo, rec.Month.Format("2006-01")))
			}
			continue
		}
		seen[key] = true
		months[rec.Month.Format("2006-01")] = true
		meters[rec.MeterNo] = true
		plan.Records = append(plan.Records, rec)
	}

	if len(plan.Records) == 0 {
		return plan, errors.New("bills: no usable rows")
	}
	plan.Meters = len(meters)
	for m := range months {
		plan.Months = append(plan.Months, m)
	}
	sortStrings(plan.Months)
	return plan, nil
}

func parseBillRow(row []string, get func([]string, string) string, loc *time.Location) (BillRecord, string) {
	meterNo := get(row, "meter_no")
	if meterNo == "" {
		return BillRecord{}, "empty meter_no"
	}
	rawMonth := get(row, "month")
	month, err := parseBillMonth(rawMonth)
	if err != nil {
		return BillRecord{}, fmt.Sprintf("bad month %q", rawMonth)
	}
	rawObserved := get(row, "observed_at_cst")
	observed, err := parseBillTime(rawObserved, loc)
	if err != nil {
		return BillRecord{}, fmt.Sprintf("bad observed_at %q", rawObserved)
	}
	source := get(row, "source")
	if !billSources[source] {
		// 禁止默认改写 source，避免记错来源。
		return BillRecord{}, fmt.Sprintf("unknown source %q", source)
	}

	rec := BillRecord{MeterNo: meterNo, Month: month, ObservedAt: observed, Source: source}
	for _, f := range []struct {
		col string
		dst **float64
	}{
		{"start_kwh", &rec.StartKWh},
		{"end_kwh", &rec.EndKWh},
		{"usage_kwh", &rec.UsageKWh},
		{"cost_yuan", &rec.CostYuan},
	} {
		v, err := parseNullableFloat(get(row, f.col))
		if err != nil {
			return BillRecord{}, fmt.Sprintf("bad %s %q", f.col, get(row, f.col))
		}
		*f.dst = v
	}
	return rec, ""
}

// parseBillMonth 将 "2025-01-01" 与 "2025-01" 归一为当月 1 号。
func parseBillMonth(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "2006-01"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparsable month %q", raw)
}

func parseBillTime(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	// 带时区写法优先原样解析，禁止本地时区覆盖。
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range billTimeLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparsable timestamp %q", raw)
}

// parseNullableFloat 将空串映射为 NULL，区别于数值 0。
func parseNullableFloat(raw string) (*float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "null") {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
