package relay

import (
	"testing"
	"time"
)

func TestTopologyRefreshDelayUsesBackoffCapAndJitter(t *testing.T) {
	base := 15 * time.Minute
	if got := nextTopologyRefreshDelay(base, 0, 0.5); got != base {
		t.Fatalf("success delay = %v", got)
	}
	if got := nextTopologyRefreshDelay(base, 1, 0.5); got != 30*time.Second {
		t.Fatalf("first failure delay = %v", got)
	}
	if got := nextTopologyRefreshDelay(base, 20, 0.5); got != base {
		t.Fatalf("backoff was not capped: %v", got)
	}
	low := nextTopologyRefreshDelay(base, 2, 0)
	high := nextTopologyRefreshDelay(base, 2, 1)
	if low != 48*time.Second || high != 72*time.Second {
		t.Fatalf("unexpected jitter range: %v..%v", low, high)
	}
}
