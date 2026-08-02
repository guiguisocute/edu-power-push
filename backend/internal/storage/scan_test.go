package storage

import "testing"

// SingleMeter 是手动刷新与批量扫描的唯一分界。
// 判定必须与 scan_runs 上 scope->>'meter' 部分索引一致。
func TestScanScopeSingleMeter(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope ScanScope
		want  bool
	}{
		{"全量扫描", ScanScope{}, false},
		{"楼栋扫描", ScanScope{Building: "16", Limit: 100}, false},
		{"楼层扫描", ScanScope{Building: "16", Floor: "3"}, false},
		{"单表刷新", ScanScope{Meter: "190610005876", Limit: 1}, true},
	} {
		if got := tc.scope.SingleMeter(); got != tc.want {
			t.Errorf("%s: SingleMeter() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestScanScopeBoundOnlyIsBatch(t *testing.T) {
	scope := ScanScope{BoundOnly: true}
	if scope.SingleMeter() {
		t.Fatal("bound-only priority scan must keep batch locking semantics")
	}
}
