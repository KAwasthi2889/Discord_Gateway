package torn

import (
	"os"
	"testing"
)

func TestDailyQuota(t *testing.T) {
	dir, err := os.MkdirTemp("", "quota_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	limit := 2
	dq := NewDailyQuota(limit, dir)

	exhaustedCalled := false
	dq.OnExhausted = func() {
		exhaustedCalled = true
	}

	if dq.Remaining() != 2 {
		t.Errorf("Expected 2 remaining, got %d", dq.Remaining())
	}
	if !dq.Allow() {
		t.Errorf("Expected Allow() to return true")
	}

	dq.RecordSuccess()
	if dq.Remaining() != 1 {
		t.Errorf("Expected 1 remaining, got %d", dq.Remaining())
	}

	dq.RecordSuccess()
	if dq.Remaining() != 0 {
		t.Errorf("Expected 0 remaining, got %d", dq.Remaining())
	}

	if !exhaustedCalled {
		t.Errorf("Expected OnExhausted to be called")
	}

	if dq.Allow() {
		t.Errorf("Expected Allow() to return false when exhausted")
	}

	// Test persistence by creating a new instance pointing to the same dir
	dq2 := NewDailyQuota(limit, dir)
	if dq2.Remaining() != 0 {
		t.Errorf("Expected new instance to load state and have 0 remaining, got %d", dq2.Remaining())
	}
	if dq2.Allow() {
		t.Errorf("Expected new instance to deny")
	}
}
