package push

import (
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
)

func TestDigestDueDailyWindow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	// 08:05 CST 在 15 分钟窗口内。
	local := time.Date(2026, 7, 27, 8, 5, 0, 0, loc)
	c := storage.PushCandidate{Period: "daily", PushTime: "08:00"}
	if !digestDue(c, local) {
		t.Fatal("expected digest due inside 15-minute window")
	}
	// 08:20 已出窗口。
	local = time.Date(2026, 7, 27, 8, 20, 0, 0, loc)
	if digestDue(c, local) {
		t.Fatal("expected digest not due outside window")
	}
}

func TestDigestDueSkipsSameSlot(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	local := time.Date(2026, 7, 27, 8, 3, 0, 0, loc)
	sent := time.Date(2026, 7, 27, 8, 1, 0, 0, loc)
	c := storage.PushCandidate{Period: "daily", PushTime: "08:00", LastDigestAt: &sent}
	if digestDue(c, local) {
		t.Fatal("should not re-send within the same push slot")
	}
}

func TestDigestDueWeeklyGap(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	local := time.Date(2026, 7, 27, 8, 2, 0, 0, loc)
	sent := local.Add(-8 * 24 * time.Hour)
	c := storage.PushCandidate{Period: "weekly", PushTime: "08:00", LastDigestAt: &sent}
	if !digestDue(c, local) {
		t.Fatal("weekly digest should fire after 6+ days")
	}
}

func TestParseHHMM(t *testing.T) {
	h, m, ok := parseHHMM("08:30")
	if !ok || h != 8 || m != 30 {
		t.Fatalf("got %d:%d ok=%v", h, m, ok)
	}
	if _, _, ok := parseHHMM("25:00"); ok {
		t.Fatal("invalid hour accepted")
	}
}
