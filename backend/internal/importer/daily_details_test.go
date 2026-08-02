package importer

import (
	"strings"
	"testing"
	"time"
)

func TestValidateDailyDetailsCleansAndDeduplicates(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	source := strings.Join([]string{
		`{"meter":"190610004855","month":"2026-07","ok":false,"queried_at":"2026-07-28T15:00:00","error":"timeout"}`,
		`{"meter":"190610004855","month":"2026-07","ok":true,"status_code":200,"queried_at":"2026-07-28T15:01:00","row_count":81,"raw_row_count":81,"days_with_usage":2,"days":[{"sj":"2026-07-27","ydl":"9.7","ydje":"6.01"},{"sj":"2026-07-26","ydl":"13.15","ydje":"8.15"}],"building":"11栋","sum_kwh":22.85}`,
		`{"meter":"190610004855","month":"2026-07","ok":true,"status_code":200,"queried_at":"2026-07-28T15:02:00","row_count":81,"raw_row_count":81,"days_with_usage":2,"days":[{"sj":"2026-07-26","ydl":"13.1500","ydje":"8.150"},{"sj":"2026-07-27","ydl":"9.70","ydje":"6.0100"}]}`,
		`{"meter":"190610000001","month":"2026-06","ok":false,"queried_at":"2026-07-28T15:00:00","error":"session/error page"}`,
	}, "\n") + "\n"

	plan, err := ValidateDailyDetails(strings.NewReader(source), "test.jsonl", loc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalLines != 4 || plan.UniqueSuccessCells != 1 || plan.UniqueFailedCells != 1 || plan.DuplicateSuccessLines != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	var records []DailyDetailRecord
	if err := StreamDailyDetailPlan(strings.NewReader(source), &plan, loc, func(record DailyDetailRecord) error {
		records = append(records, record)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !records[0].OK || records[0].Days[0].UsageKWh != "13.1500" {
		t.Fatalf("records=%+v", records)
	}
	if plan.DayRows != 2 || plan.CoveredDays != 27 {
		t.Fatalf("day_rows=%d covered_days=%d", plan.DayRows, plan.CoveredDays)
	}
}

func TestValidateDailyDetailsRejectsConflictingSuccesses(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	source := strings.Join([]string{
		`{"meter":"m","month":"2026-07","ok":true,"queried_at":"2026-07-28T15:00:00","raw_row_count":81,"days_with_usage":1,"days":[{"sj":"2026-07-27","ydl":"1","ydje":"0.64"}]}`,
		`{"meter":"m","month":"2026-07","ok":true,"queried_at":"2026-07-28T15:01:00","raw_row_count":81,"days_with_usage":1,"days":[{"sj":"2026-07-27","ydl":"2","ydje":"1.28"}]}`,
	}, "\n") + "\n"
	plan, err := ValidateDailyDetails(strings.NewReader(source), "conflict.jsonl", loc)
	if err == nil || len(plan.Errors) == 0 {
		t.Fatalf("expected conflict, plan=%+v err=%v", plan, err)
	}
}

func TestValidateDailyDetailsRejectsInvalidDayValue(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	source := `{"meter":"m","month":"2026-07","ok":true,"queried_at":"2026-07-28T15:00:00","raw_row_count":81,"days_with_usage":1,"days":[{"sj":"2026-07-27","ydl":"-1","ydje":"0"}]}` + "\n"
	if _, err := ValidateDailyDetails(strings.NewReader(source), "bad.jsonl", loc); err == nil {
		t.Fatal("expected negative usage to be rejected")
	}
}
