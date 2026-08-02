package main

import (
	"context"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestBoundTickStaysDueWhileAnotherBalanceRunRuns(t *testing.T) {
	w := &worker{scanRunning: true}
	w.markBoundDue(context.Background())

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.boundDue {
		t.Fatal("hourly bound-meter tick was dropped while another balance run was active")
	}
	if w.boundScheduling {
		t.Fatal("bound-meter scheduling started while another balance run was running")
	}
}

func TestFullTickStaysDueWhileAnotherBalanceRunRuns(t *testing.T) {
	w := &worker{scanRunning: true}
	w.markFullDue(context.Background())

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.fullDue {
		t.Fatal("campus scan tick was dropped while another balance run was active")
	}
	if w.fullScheduling {
		t.Fatal("campus scan scheduling started while another balance run was running")
	}
}

func TestDailyDetailDueToday(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := cron.ParseStandard("30 0 * * *")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before occurrence", time.Date(2026, 7, 29, 0, 29, 59, 0, location), false},
		{"at occurrence", time.Date(2026, 7, 29, 0, 30, 0, 0, location), true},
		{"later same day after restart", time.Date(2026, 7, 29, 13, 57, 0, 0, location), true},
		{"UTC input for same instant", time.Date(2026, 7, 29, 5, 57, 0, 0, time.UTC), true},
		{"next day before occurrence", time.Date(2026, 7, 30, 0, 29, 59, 0, location), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dailyDetailDueToday(test.now, location, schedule); got != test.want {
				t.Fatalf("dailyDetailDueToday()=%v, want %v", got, test.want)
			}
		})
	}
}

func TestDailyDetailDueTodayRejectsMissingConfiguration(t *testing.T) {
	now := time.Date(2026, 7, 29, 13, 57, 0, 0, time.UTC)
	if dailyDetailDueToday(now, time.UTC, nil) {
		t.Fatal("nil schedule must disable catch-up")
	}
	if dailyDetailDueToday(now, nil, cron.Every(time.Minute)) {
		t.Fatal("nil location must disable catch-up")
	}
}

func TestDailyDetailTargetDateUsesConfiguredTimezone(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 22, 30, 0, 0, time.UTC)
	if got := dailyDetailTargetDate(now, location); got != "2026-07-29" {
		t.Fatalf("target date=%q, want 2026-07-29", got)
	}
}

// SCHEDULE 为 MANUAL 时，禁止自动续跑 schedule 中断任务。
// 否则关闭调度后仍无法停止扫描。
// manual / retry / recovery 任务仍须续跑至终态。
// 否则 smoke 的中断恢复契约失效。
func TestScheduledRunsStopResumingWhenCollectorDisabled(t *testing.T) {
	all := []string{"balance", "bill", "daily detail"}

	w := &worker{}
	w.controlSettings.Balance.Enabled = true
	w.controlSettings.Bills.Enabled = true
	w.controlSettings.DailyDetails.Enabled = true
	for _, kind := range all {
		if !w.resumable(kind, "schedule") {
			t.Fatalf("%s: scheduled runs must resume while the collector is on", kind)
		}
	}

	w.controlSettings.Balance.Enabled = false
	w.controlSettings.Bills.Enabled = false
	w.controlSettings.DailyDetails.Enabled = false
	for _, kind := range all {
		if w.resumable(kind, "schedule") {
			t.Fatalf("%s: a scheduled run resumed for a disabled collector", kind)
		}
		// manual / retry / recovery 不受 SCHEDULE 开关约束。
		for _, trigger := range []string{"manual", "retry", "recovery"} {
			if !w.resumable(kind, trigger) {
				t.Fatalf("%s: %s run must still resume; only scheduling is off", kind, trigger)
			}
		}
	}

	// 三个开关必须相互独立。
	w.controlSettings.Bills.Enabled = true
	if w.resumable("balance", "schedule") || w.resumable("daily detail", "schedule") {
		t.Fatal("enabling bills must not re-enable the other collectors")
	}
	if !w.resumable("bill", "schedule") {
		t.Fatal("bills should follow its own switch")
	}
}
