package provider

import (
	"context"
	"strings"
	"testing"
)

type stubQuerier struct{ area string }

func (s stubQuerier) QueryMeter(_ context.Context, meter string, _ QueryOptions) Result {
	return Result{Meter: meter, OK: true, Status: StatusValid, AreaName: s.area}
}

func (stubQuerier) QueryMonthlyBills(_ context.Context, meter string, months []string, _ MonthlyQueryOptions) MonthlyBillsResult {
	return MonthlyBillsResult{Meter: meter, OK: true, Status: MonthlyStatusValid, MonthsRequested: len(months)}
}

func (stubQuerier) QueryDailyDetails(_ context.Context, meter string, months []string, _ DailyDetailQueryOptions) DailyDetailsResult {
	return DailyDetailsResult{Meter: meter, OK: true, Status: DailyDetailStatusValid, MonthsRequested: len(months)}
}

// 未选学校时三方法返回可解释失败，禁止空指针崩溃。
func TestDynamicWithoutSchoolFailsCleanly(t *testing.T) {
	d := NewDynamic()
	if d.Ready() {
		t.Fatal("a fresh Dynamic must not report ready")
	}

	result := d.QueryMeter(context.Background(), "31240718", QueryOptions{})
	if result.OK || result.Status != StatusError {
		t.Fatalf("QueryMeter = %+v, want a failed result", result)
	}
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0], ErrSchoolNotConfigured.Error()) {
		t.Fatalf("QueryMeter errors = %v, want school_not_configured", result.Errors)
	}

	bills := d.QueryMonthlyBills(context.Background(), "31240718", []string{"2026-07"}, MonthlyQueryOptions{})
	if bills.OK || bills.Status != MonthlyStatusError || bills.MonthsRequested != 1 {
		t.Fatalf("QueryMonthlyBills = %+v, want a failed result", bills)
	}

	details := d.QueryDailyDetails(context.Background(), "31240718", []string{"2026-07"}, DailyDetailQueryOptions{})
	if details.OK || details.Status != DailyDetailStatusError {
		t.Fatalf("QueryDailyDetails = %+v, want a failed result", details)
	}
}

// Set 换学校后读数必须带新学校身份，避免多校数据混用。
func TestDynamicSwapsSchool(t *testing.T) {
	d := NewDynamic()
	d.Set(stubQuerier{}, SchoolConfig{AreaID: "83", AreaName: "甲大学"})
	if !d.Ready() {
		t.Fatal("Dynamic must report ready once a querier is set")
	}
	if got := d.QueryMeter(context.Background(), "m", QueryOptions{}); got.AreaID != "83" {
		t.Fatalf("AreaID = %q, want 83", got.AreaID)
	}

	d.Set(stubQuerier{}, SchoolConfig{AreaID: "99", AreaName: "乙大学"})
	got := d.QueryMeter(context.Background(), "m", QueryOptions{})
	if got.AreaID != "99" || got.AreaName != "乙大学" {
		t.Fatalf("after swap = %q/%q, want 99/乙大学", got.AreaID, got.AreaName)
	}

	// 置空回到未配置，禁止沿用上一所学校。
	d.Set(nil, SchoolConfig{})
	if d.Ready() {
		t.Fatal("clearing the querier must return Dynamic to the unconfigured state")
	}
}

// 上游已填学校字段时禁止覆盖。
func TestDynamicKeepsProviderSuppliedArea(t *testing.T) {
	d := NewDynamic()
	d.Set(areaEchoQuerier{}, SchoolConfig{AreaID: "83", AreaName: "甲大学"})
	if got := d.QueryMeter(context.Background(), "m", QueryOptions{}); got.AreaID != "upstream" {
		t.Fatalf("AreaID = %q, want the value the provider supplied", got.AreaID)
	}
}

type areaEchoQuerier struct{ stubQuerier }

func (areaEchoQuerier) QueryMeter(_ context.Context, meter string, _ QueryOptions) Result {
	return Result{Meter: meter, OK: true, Status: StatusValid, AreaID: "upstream", AreaName: "上游说了算"}
}

type stubProvider struct {
	name    string
	schools []School
}

func (p stubProvider) Name() string                     { return p.name }
func (p stubProvider) DisplayName() string              { return p.name }
func (p stubProvider) Schools() []School                { return p.schools }
func (stubProvider) DefaultBaseURL() string             { return "http://example.test" }
func (stubProvider) Open(SchoolConfig) (Querier, error) { return stubQuerier{}, nil }

// 仅一套时可省略 PROVIDER；多套留空必须报错。
func TestResolveRequiresANameOnlyWhenAmbiguous(t *testing.T) {
	withRegistry(t, map[string]Provider{"only": stubProvider{name: "only"}})
	if p, err := Resolve(""); err != nil || p.Name() != "only" {
		t.Fatalf("Resolve(\"\") = %v, %v; want the sole provider", p, err)
	}

	withRegistry(t, map[string]Provider{"a": stubProvider{name: "a"}, "b": stubProvider{name: "b"}})
	if _, err := Resolve(""); err == nil {
		t.Fatal("Resolve(\"\") must fail when two providers are registered")
	}
	if _, err := Resolve("missing"); err == nil {
		t.Fatal("Resolve of an unregistered name must fail")
	}
	if p, err := Resolve("b"); err != nil || p.Name() != "b" {
		t.Fatalf("Resolve(\"b\") = %v, %v", p, err)
	}
}

// 注册表进程级共享，测试间必须还原。
func withRegistry(t *testing.T, entries map[string]Provider) {
	t.Helper()
	registryMu.Lock()
	previous := registry
	registry = entries
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = previous
		registryMu.Unlock()
	})
}
