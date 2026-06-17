package torn

import (
	"bytes"
	"log"
	"os"

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
}

// NewHandler initializes and returns a new Torn orchestrator.
// It wires up the necessary sub-components, such as the rate limiter and the CSV logger.
func NewHandler(cfg *config.Config, logFile *os.File) *Handler {
	return &Handler{
		cfg:     cfg,
		browser: NewBrowserLauncher(),
		logger:  NewMessageLogger(logFile),
	}
}

// UpdateConfig safely swaps the configuration pointer during a hot-reload event.
// Because the handler's execution is synchronous within the read pump, this pointer
// swap is safe as long as it occurs between message processing cycles.
func (h *Handler) UpdateConfig(cfg *config.Config) {
	h.cfg = cfg
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

	// Stage 2 & 3: High-priority extraction and browser launch (Zero Allocation Hot Path)
	if IsTornCountry(data) && IsPaidRegularRevive(data) {
		if link := ExtractProfileLink(data); link != "" {
			if h.browser.Open(link) {
				// Stage 4: Low-priority data persistence (Deferred Allocation)
				h.logger.Log(cfg, data)
			} else {
				log.Println("A new request arrived but rejected due to rate limit")
			}
		} else {
			log.Println("handler: malformed url extraction rejected")
		}
	} else {
		// Determine rejection reason
		var reason string
		if !IsTornCountry(data) {
			reason = "country"
		} else if !IsPaidRegularRevive(data) {
			reason = "0 revives"
		}
		log.Printf("A new request arrived but rejected due to %s", reason)
		// Log the record details to gateway.log for later verification
		if rec := ExtractRecord(cfg, data); rec != nil {
			log.Printf("  → %s", rec.FormatCSV())
		}
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

