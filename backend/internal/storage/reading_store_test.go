package storage

import (
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

func TestPrepareOutcome(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	result := provider.Result{
		QueriedAt: time.Date(2026, 7, 26, 19, 0, 0, 0, loc),
		OK:        true,
		Status:    provider.StatusValid,
		Balance: provider.Balance{
			PrepaidYuan: "43.87",
			TotalYuan:   "43.87",
			TotalKWh:    "3356.48",
			ReadingTime: "2026-07-26 18:53:42",
		},
	}
	got := prepareOutcome(result, loc, 36*time.Hour)
	if got.Status != "valid" || got.TotalKWh == nil || *got.TotalKWh != "3356.4800" {
		t.Fatalf("prepareOutcome() = %+v", got)
	}
	if got.ReadingHash == "" {
		t.Fatal("expected reading hash")
	}
}

func TestPrepareOutcomeRejectsMissingReadingTime(t *testing.T) {
	result := provider.Result{
		OK:     true,
		Status: provider.StatusValid,
		Balance: provider.Balance{
			TotalYuan: "1",
			TotalKWh:  "2",
		},
	}
	got := prepareOutcome(result, time.UTC, time.Hour)
	if got.Status != "parse_error" || got.ErrorCode != "invalid_reading_time" {
		t.Fatalf("prepareOutcome() = %+v", got)
	}
}

func TestReadingHashIgnoresDescriptiveMetadata(t *testing.T) {
	base := provider.Result{
		OK:     true,
		Status: provider.StatusValid,
		Balance: provider.Balance{
			PrepaidYuan: "3.7",
			SubsidyYuan: "0",
			TotalYuan:   "3.7",
			TotalKWh:    "2339.05",
			ReadingTime: "2026-07-26 18:53:42",
			MeterStatus: "正常",
			ChargeType:  "预付费",
		},
	}
	withoutMetadata := base
	withoutMetadata.Balance.MeterStatus = ""
	withoutMetadata.Balance.ChargeType = ""

	first := prepareOutcome(base, time.UTC, 0)
	second := prepareOutcome(withoutMetadata, time.UTC, 0)
	if first.ReadingHash != second.ReadingHash {
		t.Fatalf("descriptive metadata changed the reading hash: %s != %s", first.ReadingHash, second.ReadingHash)
	}
	if !readingValuesMatch(first, "3.7000", "0.0000", "3.7000", "2339.0500") {
		t.Fatal("canonical numeric values should match the stored PostgreSQL representation")
	}
}
