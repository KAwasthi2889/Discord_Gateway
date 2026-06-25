package torn

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	// Allow 3 requests per 100ms
	window := 100 * time.Millisecond
	rl := NewRateLimiter(3, window)

	// First 3 should succeed
	if !rl.Allow() {
		t.Errorf("Expected request 1 to be allowed")
	}
	if !rl.Allow() {
		t.Errorf("Expected request 2 to be allowed")
	}
	if !rl.Allow() {
		t.Errorf("Expected request 3 to be allowed")
	}

	// 4th should fail
	if rl.Allow() {
		t.Errorf("Expected request 4 to be rate limited")
	}

	// Wait for window to pass
	time.Sleep(110 * time.Millisecond)

	// Should be allowed again
	if !rl.Allow() {
		t.Errorf("Expected request 5 to be allowed after window passed")
	}
}
