package importer

import (
	"strings"
	"testing"
	"time"
)

func TestValidateInventory(t *testing.T) {
	input := "{\"updated_at\":\"2026-07-26T07:17:54\",\"count\":3,\"records\":[" +
		"{\"building\":\"10栋\",\"floor\":\"1楼\",\"room\":\"E101\",\"meter\":\"1001\"}," +
		"{\"building\":\"通信运营商\",\"floor\":\"1楼\",\"room\":\"机房\",\"meter\":\"1002\"}," +
		"{\"building\":\"10栋\",\"floor\":\"1楼\",\"room\":\"E102\",\"meter\":\"1001\"}" +
		"]}"
	plan, err := ValidateInventory(
		strings.NewReader(input),
		"room_meters.json",
		"83",
		"示例校区",
		time.FixedZone("CST", 8*60*60),
		DefaultExcludedBuildings(),
	)
	if err != nil {
		t.Fatalf("ValidateInventory() error = %v", err)
	}
	if plan.Total != 3 || len(plan.Records) != 2 || plan.Excluded != 1 || plan.Duplicate != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestValidateSnapshotClassifiesFreshness(t *testing.T) {
	input := "{\"updated_at\":\"2026-07-26T12:19:33\",\"count\":3,\"records\":[" +
		"{\"building\":\"10栋\",\"floor\":\"1楼\",\"room\":\"E101\",\"meter\":\"1001\",\"ok\":true," +
		"\"prepaid_yuan\":\"3.73\",\"subsidy_yuan\":\"0.00\",\"total_yuan\":\"3.73\",\"kwh\":\"2339.05\"," +
		"\"reading_time\":\"2026-07-26 06:49:03\",\"queried_at\":\"2026-07-26T12:08:52\",\"status\":\"valid\"}," +
		"{\"building\":\"10栋\",\"floor\":\"1楼\",\"room\":\"E102\",\"meter\":\"1002\",\"ok\":true," +
		"\"prepaid_yuan\":\"3.06\",\"total_yuan\":\"3.06\",\"kwh\":\"1970.59\"," +
		"\"reading_time\":\"2023-07-23 05:01:59\",\"queried_at\":\"2026-07-26T12:08:52\",\"status\":\"valid\"}," +
		"{\"building\":\"10栋\",\"floor\":\"3楼\",\"room\":\"E301\",\"meter\":\"1003\",\"ok\":false," +
		"\"queried_at\":\"2026-07-26T12:09:05\",\"status\":\"empty\",\"error\":\"no data\"}" +
		"]}"
	plan, err := ValidateSnapshot(
		strings.NewReader(input),
		"meter_balances.json",
		time.FixedZone("CST", 8*60*60),
		36*time.Hour,
	)
	if err != nil {
		t.Fatalf("ValidateSnapshot() error = %v", err)
	}
	if plan.Valid != 1 || plan.Stale != 1 || plan.Empty != 1 || plan.ParseError != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Records[0].ReadingHash == "" {
		t.Fatal("expected a reading hash")
	}
	if got := *plan.Records[0].PrepaidYuan; got != "3.7300" {
		t.Fatalf("prepaid amount was not canonicalized: %q", got)
	}
}
