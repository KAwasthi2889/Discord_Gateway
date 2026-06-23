package torn

import (
	"bytes"
	"log"
	"os"
	"time"

	"discord_gateway/internal/config"
)

// Handler serves as the primary orchestrator for the Torn integration business logic.
// It acts as the bridge between the raw Discord WebSocket events and the highly
// optimized extraction, rate-limiting, and logging subsystems.
type Handler struct {
	// cfg holds the active configuration. It is swapped safely during hot-reloads.
	cfg *config.Config

	// browser orchestrates the rate-limited execution of the host OS web browser.
	browser *BrowserLauncher

	// logger persists matched payloads to disk asynchronously.
	logger *MessageLogger

	// quota enforces the daily revive ceiling. Persisted to disk across restarts.
	quota *DailyQuota

	// cache holds Discord payloads temporarily until the JS callback confirms success/failure.
	cache *PayloadCache

	// callbackPort is the random OS-assigned port for the JS → Go success callback.
	callbackPort int
}

// NewHandler initializes and returns a new Torn orchestrator.
// It wires up the necessary sub-components, such as the rate limiter, the CSV logger,
// the daily quota system, and the HTTP callback server.
func NewHandler(cfg *config.Config, logFile *os.File, userDir string) *Handler {
	quota := NewDailyQuota(cfg.DailyQuota, userDir)
	logger := NewMessageLogger(logFile)
	cache := NewPayloadCache(40 * time.Second)

	cbPort, err := StartCallbackServer(quota, cache, logger)
	if err != nil {
		log.Printf("handler: WARNING — callback server failed to start: %v", err)
		log.Println("handler: daily quota will still gate browser launches, but success tracking is disabled")
	}

	return &Handler{
		cfg:          cfg,
		browser:      NewBrowserLauncher(cfg),
		logger:       logger,
		quota:        quota,
		cache:        cache,
		callbackPort: cbPort,
	}
}

// UpdateConfig safely swaps the configuration pointer during a hot-reload event.
// Because the handler's execution is synchronous within the read pump, this pointer
// swap is safe as long as it occurs between message processing cycles.
//
// The daily quota limit is updated but the used count is preserved to prevent
// gaming the quota by editing .env mid-day.
func (h *Handler) UpdateConfig(cfg *config.Config) {
	h.cfg = cfg
	h.browser = NewBrowserLauncher(cfg)
	h.quota.UpdateLimit(cfg.DailyQuota)
}

// OnMessageCreate is the primary event sink registered with the Discord Client.
// It implements a rigorous, multi-stage processing pipeline designed for absolute
// minimal latency on the critical path.
//
// Pipeline Architecture:
//  1. Fast Channel Validation: Uses zero-allocation byte scanning to discard irrelevant chatter.
//  2. Signature Extraction: Uses zero-allocation byte scanning to detect targeted Torn events.
//  3. OS Interaction: Dispatches an asynchronous browser launch if the rate limit permits.
//  4. Data Persistence: Offloads the heavy JSON unmarshaling to a background logging routine.
func (h *Handler) OnMessageCreate(data []byte) {
	cfg := h.cfg

	// Stage 1: Discard events not originating from our target channels.
	if !isTargetChannel(cfg, data) {
		return
	}

	// Stage 1.5: Discard normal chat messages and other bot alerts.
	// We only care about payloads that contain "Revive Request".
	if !bytes.Contains(data, []byte("Revive Request")) {
		return
	}

	// Stage 2 & 3: High-priority extraction and browser launch (Zero Allocation Hot Path)
	isCountry := IsTornCountry(data)
	var ok bool
	var rejectReason string
	if isCountry {
		ok, rejectReason = IsPaidRegularRevive(cfg, data)
	}

	if isCountry && ok {
		// Stage 2.5: Daily quota gate — reject early if today's ceiling is hit.
		if !h.quota.Allow() {
			payloadCopy := make([]byte, len(data))
			copy(payloadCopy, data)
			go func(payload []byte) {
				log.Println("A new request arrived but rejected due to daily quota")
				if rec := ExtractRecord(payload); rec != nil {
					log.Printf("  → %s", rec.FormatCSV())
				}
			}(payloadCopy)
			return
		}

		if link, xid := ExtractProfileLinkAndXID(h.cfg, h.callbackPort, data); link != "" {
			if h.browser.Open(link) {
				// Stage 4: Cache payload and wait for callback (Deferred Allocation)
				// Copy the payload to avoid a data race with the websocket read buffer.
				logCopy := make([]byte, len(data))
				copy(logCopy, data)
				h.cache.Add(xid, logCopy)
			} else {
				log.Println("A new request arrived but rejected due to rate limit")
			}
		} else {
			log.Println("handler: malformed url extraction rejected")
		}
	} else {
		// Determine rejection reason
		var reason string
		if !isCountry {
			reason = "country"
		} else if !ok {
			reason = rejectReason
		}

		// Log asynchronously to prevent the heavy json.Unmarshal in ExtractRecord
		// from blocking the WebSocket read pump on rapid rejected events.
		//
		// CRITICAL FIX: The underlying websocket read buffer might be overwritten by
		// the next incoming message. We MUST create a copy of the payload slice
		// before passing it to the asynchronous goroutine.
		payloadCopy := make([]byte, len(data))
		copy(payloadCopy, data)

		go func(r string, payload []byte) {
			log.Printf("A new request arrived but rejected due to %s", r)
			if rec := ExtractRecord(payload); rec != nil {
				log.Printf("  → %s", rec.FormatCSV())
			}
		}(reason, payloadCopy)
	}
}

// isTargetChannel evaluates the raw payload against the pre-computed channel signatures.
// By iterating over bytes.Contains rather than unmarshaling the JSON to check the channel_id,
// the application saves thousands of heap allocations per second under high load.
func isTargetChannel(cfg *config.Config, data []byte) bool {
	for _, tcb := range cfg.TargetBytes {
		if bytes.Contains(data, tcb) {
			return true
		}
	}
	return false
}
