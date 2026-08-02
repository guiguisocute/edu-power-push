package statistics

import (
	"testing"
	"time"
)

func TestCalculateDelta(t *testing.T) {
	start := time.Date(2026, 7, 26, 6, 0, 0, 0, time.UTC)
	end := start.Add(12 * time.Hour)
	tests := []struct {
		name     string
		previous string
		current  string
		want     Delta
	}{
		{name: "positive", previous: "100.10", current: "103.35", want: Delta{Value: "3.2500", Status: DeltaValid}},
		{name: "zero", previous: "100.10", current: "100.10", want: Delta{Value: "0.0000", Status: DeltaUnchanged}},
		{name: "negative", previous: "100.10", current: "2.00", want: Delta{Value: "-98.1000", Status: DeltaNegativeReset}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CalculateDelta(test.previous, test.current, start, end)
			if err != nil {
				t.Fatalf("CalculateDelta() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("CalculateDelta() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCalculateDeltaTimeRegression(t *testing.T) {
	now := time.Now()
	got, err := CalculateDelta("1", "2", now, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CalculateDelta() error = %v", err)
	}
	if got.Status != DeltaTimeRegression {
		t.Fatalf("status = %s", got.Status)
	}
}
