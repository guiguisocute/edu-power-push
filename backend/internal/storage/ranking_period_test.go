package storage

import (
	"testing"
	"time"
)

func TestRankingPeriodsUseCompletedNaturalPeriods(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 29, 16, 30, 0, 0, loc)
	tests := []struct {
		period, currentFrom, currentTo, previousFrom, previousTo string
		currentDays, previousDays                                int
		updatedAt, nextUpdateAt                                  string
	}{
		{"day", "2026-07-28", "2026-07-28", "2026-07-27", "2026-07-27", 1, 1, "2026-07-29T09:00:00+08:00", "2026-07-30T09:00:00+08:00"},
		{"week", "2026-07-20", "2026-07-26", "2026-07-13", "2026-07-19", 7, 7, "2026-07-27T09:00:00+08:00", "2026-08-03T09:00:00+08:00"},
		{"month", "2026-06-01", "2026-06-30", "2026-05-01", "2026-05-31", 30, 31, "2026-07-01T09:00:00+08:00", "2026-08-01T09:00:00+08:00"},
	}
	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			window := rankingPeriods(tt.period, now, DefaultRankingRefreshTime)
			if window.Current.From != tt.currentFrom || window.Current.To != tt.currentTo ||
				window.Previous.From != tt.previousFrom || window.Previous.To != tt.previousTo ||
				window.CurrentDays != tt.currentDays || window.PreviousDays != tt.previousDays {
				t.Fatalf("window=%+v", window)
			}
			if window.UpdatedAt.Format(time.RFC3339) != tt.updatedAt || window.NextUpdateAt.Format(time.RFC3339) != tt.nextUpdateAt {
				t.Fatalf("updated=%s next=%s", window.UpdatedAt, window.NextUpdateAt)
			}
		})
	}
}

func TestRankingPeriodsDoNotAdvanceBeforeNineAM(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	tests := []struct {
		name, period, currentFrom, currentTo, nextUpdate string
		now                                              time.Time
	}{
		{"daily", "day", "2026-07-27", "2026-07-27", "2026-07-29T09:00:00+08:00", time.Date(2026, 7, 29, 8, 59, 0, 0, loc)},
		{"monday", "week", "2026-07-13", "2026-07-19", "2026-07-27T09:00:00+08:00", time.Date(2026, 7, 27, 8, 59, 0, 0, loc)},
		{"month first", "month", "2026-05-01", "2026-05-31", "2026-07-01T09:00:00+08:00", time.Date(2026, 7, 1, 8, 59, 0, 0, loc)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window := rankingPeriods(tt.period, tt.now, DefaultRankingRefreshTime)
			if window.Current.From != tt.currentFrom || window.Current.To != tt.currentTo || window.NextUpdateAt.Format(time.RFC3339) != tt.nextUpdate {
				t.Fatalf("window=%+v", window)
			}
		})
	}
}

func TestRankingPeriodsUseConfiguredRefreshTime(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	before := rankingPeriods("day", time.Date(2026, 7, 29, 3, 29, 0, 0, loc), "03:30")
	after := rankingPeriods("day", time.Date(2026, 7, 29, 3, 30, 0, 0, loc), "03:30")
	if before.Current.To != "2026-07-27" || after.Current.To != "2026-07-28" {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	if after.NextUpdateAt.Format(time.RFC3339) != "2026-07-30T03:30:00+08:00" {
		t.Fatalf("next=%s", after.NextUpdateAt)
	}
}
