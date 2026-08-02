package httpapi

import (
	"context"
	"testing"
	"time"
)

func TestMeterRefreshGateEnforcesCooldownPerMeter(t *testing.T) {
	gate := newMeterRefreshGate(30 * time.Second)
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	gate.now = func() time.Time { return now }

	next, ok := gate.take("meter-a")
	if !ok || !next.Equal(now.Add(30*time.Second)) {
		t.Fatalf("first take = (%v, %v), want allowed until +30s", next, ok)
	}
	if blockedUntil, ok := gate.take("meter-a"); ok || !blockedUntil.Equal(next) {
		t.Fatalf("second take = (%v, %v), want blocked until %v", blockedUntil, ok, next)
	}
	if _, ok := gate.take("meter-b"); !ok {
		t.Fatal("a different meter must have an independent cooldown")
	}

	now = now.Add(30 * time.Second)
	if _, ok := gate.take("meter-a"); !ok {
		t.Fatal("meter must be allowed when its cooldown expires")
	}
}

func TestAcquireRefreshSlotBoundsTheManualLane(t *testing.T) {
	s := &Server{refreshSlots: make(chan struct{}, 2)}

	first, ok := s.acquireRefreshSlot(context.Background())
	if !ok {
		t.Fatal("first refresh must get a slot")
	}
	second, ok := s.acquireRefreshSlot(context.Background())
	if !ok {
		t.Fatal("second refresh must get a slot")
	}

	// 通道满员时必须立刻失败。禁止干等到超时。
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if release, ok := s.acquireRefreshSlot(canceled); ok {
		release()
		t.Fatal("a saturated lane must not hand out a third slot")
	}

	first()
	third, ok := s.acquireRefreshSlot(context.Background())
	if !ok {
		t.Fatal("releasing a slot must let the next refresh through")
	}

	// release 会被 defer 调用。重复调用禁止归还他人槽位。
	third()
	third()
	second()
	if len(s.refreshSlots) != 0 {
		t.Fatalf("lane still holds %d slots, want 0", len(s.refreshSlots))
	}
}

func TestAcquireRefreshSlotWithoutLaneIsUnbounded(t *testing.T) {
	s := &Server{}
	release, ok := s.acquireRefreshSlot(context.Background())
	if !ok {
		t.Fatal("a server without a configured lane must not block refreshes")
	}
	release()
}
