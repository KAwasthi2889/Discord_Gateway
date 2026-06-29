package torn

import (
	"context"
	"testing"
	"time"
)

func TestPayloadCache_Expiration(t *testing.T) {
	// 50ms timeout, 10ms ticker interval
	cache := NewPayloadCache(context.Background(), 50*time.Millisecond, 10*time.Millisecond)

	cache.Add("test_xid", []byte("payload"), "contract_note")

	// It should exist immediately
	_, _, exists := cache.Pop("test_xid")
	if !exists {
		t.Fatal("Expected item to exist immediately")
	}

	// Add it back
	cache.Add("test_xid", []byte("payload"), "contract_note")

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// It should be gone
	_, _, exists = cache.Pop("test_xid")
	if exists {
		t.Fatal("Expected item to be expired and purged")
	}
}
