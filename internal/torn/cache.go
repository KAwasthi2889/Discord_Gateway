package torn

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type cachedPayload struct {
	data         []byte
	contractNote string
	timestamp    time.Time
}

// PayloadCache temporarily holds Discord event payloads while waiting for the
// browser userscript to confirm success or failure.
//
// If the browser does not respond within the defined timeout (e.g. 40 seconds),
// the payload is purged and an error is logged.
type PayloadCache struct {
	mu      sync.Mutex
	items   map[string]cachedPayload
	timeout time.Duration
	ticker  time.Duration
}

// NewPayloadCache initializes a new thread-safe cache and starts the background
// cleanup routine.
func NewPayloadCache(ctx context.Context, timeout time.Duration, tickerInterval time.Duration) *PayloadCache {
	if tickerInterval == 0 {
		tickerInterval = 10 * time.Second
	}
	pc := &PayloadCache{
		items:   make(map[string]cachedPayload),
		timeout: timeout,
		ticker:  tickerInterval,
	}
	go pc.cleanupRoutine(ctx)
	return pc
}

// Add stores a copy of the payload for the given XID.
func (pc *PayloadCache) Add(xid string, payload []byte, contractNote string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.items[xid] = cachedPayload{
		data:         payload,
		contractNote: contractNote,
		timestamp:    time.Now(),
	}
}

// Pop retrieves and removes the payload for the given XID.
// Returns the payload, the contract note, and a boolean indicating if it was found.
func (pc *PayloadCache) Pop(xid string) ([]byte, string, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if item, exists := pc.items[xid]; exists {
		delete(pc.items, xid)
		return item.data, item.contractNote, true
	}
	return nil, "", false
}

// cleanupRoutine runs periodically to evict payloads that have exceeded the timeout.
// Evicted payloads are logged to the console as failures.
func (pc *PayloadCache) cleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(pc.ticker)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			pc.mu.Lock()
			now := time.Now()
			for xid, item := range pc.items {
				if now.Sub(item.timestamp) > pc.timeout {
					delete(pc.items, xid)
					// We don't hold the lock while logging, but since we're just
					// printing strings, it's safe to do this here.
					slog.Warn("Timeout / No response from browser for XID, flushing it", "xid", xid)
				}
			}
			pc.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
