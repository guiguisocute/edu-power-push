package importer

import (
	"strings"
	"testing"
	"time"
)

const billHeader = "meter_no,campus,building,floor,room,area_id,excluded,month,start_kwh,end_kwh,usage_kwh,cost_yuan,observed_at_cst,source,meter_id,bill_id\n"

func shanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func TestValidateBillsParsesExportedRows(t *testing.T) {
	// floor 含半角逗号，覆盖真实导出字段错位场景。
	csv := billHeader +
		`190610000827,示例校区,10栋,1楼,E101,83,f,2025-01-01,459.9600,628.6500,168.6900,107.9600,2026-07-27 02:09:25.019419,live_query,f43ec73c-b1b7-4b4f-819a-eb50a7e19542,71098429-c581-4958-a152-78411042271f` + "\n" +
		`190610000828,示例校区,10栋,"1楼(不是NF,SF)",E102,83,f,2025-02-01,,,,,2026-07-27 02:09:25,live_query,,` + "\n"

	loc := shanghai(t)
	plan, err := ValidateBills(strings.NewReader(csv), "bills.csv", loc)
	if err != nil {
		t.Fatalf("ValidateBills: %v", err)
	}
	if plan.Total != 2 || len(plan.Records) != 2 || plan.Skipped != 0 {
		t.Fatalf("total=%d records=%d skipped=%d, want 2/2/0", plan.Total, len(plan.Records), plan.Skipped)
	}
	if plan.Meters != 2 {
		t.Fatalf("meters=%d, want 2", plan.Meters)
	}
	if got := strings.Join(plan.Months, ","); got != "2025-01,2025-02" {
		t.Fatalf("months=%q", got)
	}

	first := plan.Records[0]
	if first.MeterNo != "190610000827" {
		t.Fatalf("meter_no=%q", first.MeterNo)
	}
	if first.UsageKWh == nil || *first.UsageKWh != 168.69 {
		t.Fatalf("usage=%v, want 168.69", first.UsageKWh)
	}
	if first.Month.Format("2006-01-02") != "2025-01-01" {
		t.Fatalf("month=%s", first.Month)
	}
	// CST 02:09 必须换算为 UTC 前一日 18:09。
	if got := first.ObservedAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-07-26T18:09:25Z" {
		t.Fatalf("observed_at=%s, want 2026-07-26T18:09:25Z（CST 应换算成 UTC）", got)
	}

	// 含逗号行必须完整解析，禁止字段错位。
	second := plan.Records[1]
	if second.MeterNo != "190610000828" || second.Month.Format("2006-01") != "2025-02" {
		t.Fatalf("second row misaligned: meter=%q month=%s", second.MeterNo, second.Month)
	}
	// 空数值列必须为 NULL，禁止当成 0。
	if second.UsageKWh != nil || second.CostYuan != nil {
		t.Fatalf("empty numerics should stay NULL, got usage=%v cost=%v", second.UsageKWh, second.CostYuan)
	}
}

func TestValidateBillsRejectsBadRows(t *testing.T) {
	loc := shanghai(t)
	good := `190610000827,示例校区,10栋,1楼,E101,83,f,2025-01-01,1,2,1,1,2026-07-27 02:09:25,live_query,,` + "\n"
	cases := []struct {
		name string
		row  string
	}{
		{"空表号", `,示例校区,10栋,1楼,E101,83,f,2025-01-01,1,2,1,1,2026-07-27 02:09:25,live_query,,`},
		{"坏月份", `1,示例校区,10栋,1楼,E101,83,f,not-a-month,1,2,1,1,2026-07-27 02:09:25,live_query,,`},
		{"坏时间", `1,示例校区,10栋,1楼,E101,83,f,2025-01-01,1,2,1,1,yesterday,live_query,,`},
		// 非法 source 必须拒绝，禁止默认改写。
		{"未知来源", `1,示例校区,10栋,1楼,E101,83,f,2025-01-01,1,2,1,1,2026-07-27 02:09:25,guessed,,`},
		{"坏数值", `1,示例校区,10栋,1楼,E101,83,f,2025-01-01,abc,2,1,1,2026-07-27 02:09:25,live_query,,`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ValidateBills(strings.NewReader(billHeader+good+tc.row+"\n"), "b.csv", loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// 坏行跳过计数，好行仍入库。
			if plan.Skipped != 1 || len(plan.Records) != 1 {
				t.Fatalf("skipped=%d records=%d, want 1/1", plan.Skipped, len(plan.Records))
			}
			if len(plan.Errors) == 0 {
				t.Fatal("bad row should be reported in plan.Errors")
			}
		})
	}
}

func TestValidateBillsRejectsDuplicateMeterMonth(t *testing.T) {
	// 同批重复 meter/month 在校验期拒绝。
	loc := shanghai(t)
	row := `190610000827,示例校区,10栋,1楼,E101,83,f,2025-01-01,1,2,1,1,2026-07-27 02:09:25,live_query,,` + "\n"
	plan, err := ValidateBills(strings.NewReader(billHeader+row+row), "b.csv", loc)
	if err != nil {
		t.Fatalf("ValidateBills: %v", err)
	}
	if len(plan.Records) != 1 || plan.Skipped != 1 {
		t.Fatalf("records=%d skipped=%d, want 1/1", len(plan.Records), plan.Skipped)
	}
}

func TestValidateBillsRequiresColumns(t *testing.T) {
	loc := shanghai(t)
	if _, err := ValidateBills(strings.NewReader("meter_no,month\n1,2025-01-01\n"), "b.csv", loc); err == nil {
		t.Fatal("missing observed_at_cst/source should be rejected up front")
	}
}

func TestValidateBillsStripsBOM(t *testing.T) {
	// Excel CSV 含 BOM，必须剥离后才能匹配表头。
	loc := shanghai(t)
	csv := "\ufeff" + billHeader +
		`190610000827,示例校区,10栋,1楼,E101,83,f,2025-01-01,1,2,1,1,2026-07-27 02:09:25,live_query,,` + "\n"
	plan, err := ValidateBills(strings.NewReader(csv), "b.csv", loc)
	if err != nil {
		t.Fatalf("ValidateBills with BOM: %v", err)
	}
	if len(plan.Records) != 1 || plan.Records[0].MeterNo != "190610000827" {
		t.Fatalf("BOM not stripped: %+v", plan.Records)
	}
}
