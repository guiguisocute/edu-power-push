package bdfairy

import "testing"

func TestParseMeterMain(t *testing.T) {
	html := "<script>" +
		"const yffye = ref('43.87');" +
		"const bzdye = ref(\"1.20\");" +
		"const zye = ref('45.07');" +
		"const zydl = ref('3356.48');" +
		"const dbzt = ref('第一路(空调):合闸');" +
		"const cbsj = ref('2026-07-25 17:41:45');" +
		"const roomaddr = ref('undefined');" +
		"const chargeType = ref('1');" +
		"</script>"
	got, err := ParseMeterMain(html)
	if err != nil {
		t.Fatalf("ParseMeterMain() error = %v", err)
	}
	if got.PrepaidYuan != "43.87" || got.SubsidyYuan != "1.20" || got.TotalKWh != "3356.48" {
		t.Fatalf("unexpected balance: %+v", got)
	}
	if got.ReadingTime != "2026-07-25 17:41:45" {
		t.Fatalf("ReadingTime = %q", got.ReadingTime)
	}
}

func TestParseMeterMainRequiresReading(t *testing.T) {
	if _, err := ParseMeterMain("<html>error</html>"); err == nil {
		t.Fatal("ParseMeterMain() expected an error")
	}
}

func TestParseElectricBill(t *testing.T) {
	html := "<script>" +
		"const month = ref(\"2026-06\");" +
		"const billData = ref({" +
		"qcdl: \"2943.99\"," +
		"qmdl: \"3128.88\"," +
		"ydl: \"184.89\"," +
		"ydje: \"114.63\"" +
		"});" +
		"</script>"
	got, err := ParseElectricBill(html)
	if err != nil {
		t.Fatalf("ParseElectricBill() error = %v", err)
	}
	if got.Month != "2026-06" || got.Data.UsageKWh != "184.89" || got.Data.CostYuan != "114.63" {
		t.Fatalf("unexpected bill: %+v", got)
	}
}

func TestParseElectricDetailFiltersPlaceholders(t *testing.T) {
	html := `<script>const comments = ref(JSON.parse('[{"ydl":"9.70","sj":"2026-07-27","ydje":"6.01"},{"ydl":"","sj":"2026-07-27","ydje":""},{"ydl":"13.15","sj":"2026-07-26","ydje":"8.15"}]'));</script>`
	days, rawCount, err := ParseElectricDetail(html)
	if err != nil {
		t.Fatal(err)
	}
	if rawCount != 3 || len(days) != 2 {
		t.Fatalf("raw=%d days=%+v", rawCount, days)
	}
	if days[0].Date != "2026-07-27" || days[0].UsageKWh != "9.70" || days[0].CostYuan != "6.01" {
		t.Fatalf("first day=%+v", days[0])
	}
}

func TestParseElectricDetailRejectsConflictingDate(t *testing.T) {
	html := `<script>comments=ref(JSON.parse('[{"ydl":"1","sj":"2026-07-27","ydje":"0.62"},{"ydl":"2","sj":"2026-07-27","ydje":"1.24"}]'))</script>`
	if _, _, err := ParseElectricDetail(html); err == nil {
		t.Fatal("expected conflicting rows to fail")
	}
}
