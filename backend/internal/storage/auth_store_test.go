package storage

import (
	"testing"
	"time"
)

func TestConcurrentRefreshRotationGrace(t *testing.T) {
	now := time.Now()
	reason := "rotated"
	recent := now.Add(-time.Second)
	concurrentStart := recent.Add(-time.Second)
	ua := []byte("same-browser")
	if !concurrentRefreshRotation(&reason, &recent, ua, ua, concurrentStart, now) {
		t.Fatal("recent rotation from the same browser was rejected")
	}

	old := now.Add(-refreshRotationGrace - time.Second)
	if concurrentRefreshRotation(&reason, &old, ua, ua, old.Add(-time.Second), now) {
		t.Fatal("old refresh-token reuse was accepted")
	}

	otherUA := []byte("other-browser")
	if concurrentRefreshRotation(&reason, &recent, ua, otherUA, concurrentStart, now) {
		t.Fatal("refresh-token reuse from another browser was accepted")
	}

	replayStart := recent.Add(time.Second)
	if concurrentRefreshRotation(&reason, &recent, ua, ua, replayStart, now) {
		t.Fatal("a replay started after rotation was accepted")
	}

	reason = "logout"
	if concurrentRefreshRotation(&reason, &recent, ua, ua, concurrentStart, now) {
		t.Fatal("a non-rotation revocation was accepted")
	}
}
