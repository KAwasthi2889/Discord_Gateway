package torn

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"discord_gateway/internal/config"
	"discord_gateway/internal/nuke"
)

// Handler serves as the primary orchestrator for the Torn integration business logic.
// It acts as the bridge between the raw Discord WebSocket events and the highly
// optimized extraction, rate-limiting, and logging subsystems.
type Handler struct {
	// cfg holds the active configuration. It is swapped safely during hot-reloads.
	cfg atomic.Pointer[config.Config]

	// browser orchestrates the rate-limited execution of the host OS web browser.
	browser atomic.Pointer[BrowserLauncher]

	// logger persists matched payloads to disk asynchronously.
	logger *MessageLogger

	// quota enforces the daily revive ceiling. Persisted to disk across restarts.
	quota *DailyQuota

	// cache holds Discord payloads temporarily until the JS callback confirms success/failure.
	cache *PayloadCache

	// nukeClient provides fast access to the cached Nuke API Shitlist and Contracts.
	nukeClient *nuke.Client

	// callbackPort is the random OS-assigned port for the JS → Go success callback.
	callbackPort int

	// callbackToken is the secure auth token for the callback server.
	callbackToken string

	// globalRateLimiter enforces a strict maximum on the total number of tabs opened per minute.
	globalRateLimiter *RateLimiter

	// pongChan receives a signal when the userscript responds to the /ping endpoint.
	pongChan chan struct{}
}

// NewHandler initializes and returns a new Torn orchestrator.
// It wires up the necessary sub-components, such as the rate limiter, the CSV logger,
// the daily quota system, and the HTTP callback server.
func NewHandler(ctx context.Context, cfg *config.Config, logFile *os.File, userDir string, nukeClient *nuke.Client) *Handler {
	quota := NewDailyQuota(cfg.DailyQuota, userDir)
	logger := NewMessageLogger(logFile)
	cache := NewPayloadCache(ctx, 25*time.Second, 0)

	cbPort, pongChan, cbToken, err := StartCallbackServer(quota, cache, logger, nil)
	if err != nil {
		slog.Warn("Callback server failed to start", "error", err)
		slog.Info("Daily quota will still gate browser launches, but success tracking is disabled")
	}

	h := &Handler{
		logger:            logger,
		quota:             quota,
		cache:             cache,
		nukeClient:        nukeClient,
		callbackPort:      cbPort,
		callbackToken:     cbToken,
		globalRateLimiter: NewRateLimiter(15, time.Minute),
		pongChan:          pongChan,
	}
	h.cfg.Store(cfg)
	h.browser.Store(NewBrowserLauncher(cfg))
	return h
}

// NewHandlerForTest allows test injection of mocked or explicitly configured dependencies
// without automatically spawning side-effect goroutines like StartCallbackServer.
func NewHandlerForTest(ctx context.Context, cfg *config.Config, logFile *os.File, userDir string, nukeClient *nuke.Client, quota *DailyQuota, cache *PayloadCache, logger *MessageLogger, callbackPort int, callbackToken string, browserLauncher func(url string)) *Handler {
	b := NewBrowserLauncher(cfg)
	b.Launcher = browserLauncher
	h := &Handler{
		logger:            logger,
		quota:             quota,
		cache:             cache,
		nukeClient:        nukeClient,
		callbackPort:      callbackPort,
		callbackToken:     callbackToken,
		globalRateLimiter: NewRateLimiter(15, time.Minute),
		pongChan:          make(chan struct{}, 1),
	}
	h.cfg.Store(cfg)
	h.browser.Store(b)
	return h
}

// UpdateConfig safely swaps the configuration pointer during a hot-reload event.
// Because the handler's execution is synchronous within the read pump, this pointer
// swap is safe as long as it occurs between message processing cycles.
//
// The daily quota limit is updated but the used count is preserved to prevent
// gaming the quota by editing .env mid-day.
func (h *Handler) UpdateConfig(cfg *config.Config) {
	h.cfg.Store(cfg)
	h.browser.Store(NewBrowserLauncher(cfg))
	h.quota.UpdateLimit(cfg.DailyQuota)
}

// Quota returns the underlying DailyQuota instance.
func (h *Handler) Quota() *DailyQuota {
	return h.quota
}

// CallbackPort returns the dynamically assigned callback server port.
func (h *Handler) CallbackPort() int {
	return h.callbackPort
}

// PongChan returns the channel used to receive the /pong startup signal.
func (h *Handler) PongChan() chan struct{} {
	return h.pongChan
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
	cfg := h.cfg.Load()

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
		// Stage 2.5: Daily quota reached, reject early if today's ceiling is hit.
		if !h.quota.Allow() {
			slog.Warn("Daily quota limit reached. Dropping request silently and shutting down.")
			if h.quota.OnExhausted != nil {
				h.quota.OnExhausted()
			}
			return
		}

		if link, xid := ExtractProfileLinkAndXID(cfg, h.callbackPort, h.callbackToken, data); link != "" {
			// Extract IDs for Nuke API checks
			xidInt, _ := strconv.Atoi(xid)
			factionIDInt, _ := strconv.Atoi(ExtractFactionID(data))

			shitlistTargetXID := xidInt
			isOnBehalf := false

			// check if it's an "on behalf of" request for remaining checks
			if reqStr := ExtractRequesterXID(data); reqStr != "" {
				if reqInt, err := strconv.Atoi(reqStr); err == nil {
					// Only use requester if it's different from the target
					if reqStr != xid {
						// Overwrite the shitlist target to be the requester
						shitlistTargetXID = reqInt
						isOnBehalf = true
					}
				}
			}

			hasPaymentHistory := !bytes.Contains(data, tornNoReviveHistory)

			if hasPaymentHistory && !isOnBehalf {
				// Shortcut: If they have payment history > 0 AND it's a standard request (revivee paying), skip most shitlist checks.
				// ONLY check if the evaluated person has Category 3 (Revive No-Payment).
				cats := h.nukeClient.GetShitlistCategories(shitlistTargetXID)
				hasCat3 := false
				for _, cat := range cats {
					if cat == 3 {
						hasCat3 = true
						break
					}
				}
				if hasCat3 {
					slog.Info("Request dropped silently due to shitlist (player has Category 3 despite payment history)", "xid", shitlistTargetXID)
					return
				}
			} else {
				// Normal checks
				// Rule 1: NO MATTER WHAT check on the original target.
				// If target faction is globally banned, block unconditionally.
				if h.nukeClient.IsFactionBanned(factionIDInt) {
					slog.Info("Request dropped silently, target is strictly shitlisted (faction ban)", "xid", xid)
					return
				}

				if h.checkShitlist(shitlistTargetXID) {
					return
				}
			}

			if !h.globalRateLimiter.Allow() {
				slog.Info("Global rate limit hit (>15/min). Dropping request silently.")
				return
			}

			link, contractNote := h.buildContractUrl(cfg, link, xidInt, factionIDInt)

			if h.browser.Load().Open(link, xid) {
				// Stage 4: Cache payload and wait for callback (Deferred Allocation)
				// Copy the payload to avoid a data race with the websocket read buffer.
				logCopy := make([]byte, len(data))
				copy(logCopy, data)
				h.cache.Add(xid, logCopy, contractNote)
			} else {
				slog.Info("Request rejected due to rate limit")
			}
		} else {
			slog.Warn("Malformed url extraction rejected")
		}
	} else {
		// Determine rejection reason
		var reason string
		if !isCountry {
			reason = "country"
		} else if !ok {
			reason = rejectReason
		}
		h.handleRejection(reason, data)
	}
}

// checkShitlist performs configurable evaluation of shitlist categories on a target.
func (h *Handler) checkShitlist(xidInt int) bool {
	playerCats := h.nukeClient.GetShitlistCategories(xidInt)
	if len(playerCats) == 0 {
		return false // Not shitlisted
	}

	// Use pre-loaded configuration to evaluate dynamic categories zero-allocation
	slCfg := h.cfg.Load().Shitlist
	if slCfg == nil {
		slCfg = &config.ShitlistConfig{}
	}

	isAllowed := func(cats []int) bool {
		for _, cat := range cats {
			// Rule 2 check is handled in API parse (cat 5 is skipped), but we check explicitly to be safe
			if cat == 5 {
				continue
			}

			// Cat 3 (Revive No-Payment) is ALWAYS blocked for the evaluated person
			if cat == 3 {
				return false
			}

			// Rule 3 checks
			var allowed bool
			switch cat {
			case 1, 2:
				allowed = slCfg.AllowBuyMugger
			case 4:
				allowed = slCfg.AllowAbsoluteScumLords
			case 6:
				allowed = slCfg.AllowOther
			default:
				// As requested, treat unknown as 6
				allowed = slCfg.AllowOther
			}

			if !allowed {
				return false // blocked
			}
		}
		return true // all categories allowed
	}

	if !isAllowed(playerCats) {
		slog.Info("Request dropped silently due to shitlist (player)", "xid", xidInt)
		return true // drop
	}

	return false
}

// buildContractUrl resolves the effective revive requirements and appends them to the browser URL.
// It sets minChance to the max of the global config and the specific player/faction contract.
// It returns the augmented URL and any associated contract note for logging.
func (h *Handler) buildContractUrl(cfg *config.Config, link string, xidInt, factionIDInt int) (string, string) {
	contractNote := ""
	effectiveMinChance := cfg.MinChance

	if h.nukeClient != nil {
		if contract, ok := h.nukeClient.GetContract(xidInt, factionIDInt); ok {
			if contract.MinReviveChance > effectiveMinChance {
				effectiveMinChance = contract.MinReviveChance
			}
			link += "&status=" + contract.PStatus
			contractNote = contract.Note
		}
	}
	link += "&minChance=" + strconv.Itoa(effectiveMinChance)

	return link, contractNote
}

func (h *Handler) handleRejection(reason string, data []byte) {
	// Log asynchronously to prevent the heavy json.Unmarshal in ExtractRecord
	// from blocking the WebSocket read pump on rapid rejected events.
	//
	// CRITICAL FIX: The underlying websocket read buffer might be overwritten by
	// the next incoming message. We MUST create a copy of the payload slice
	// before passing it to the asynchronous goroutine.
	payloadCopy := make([]byte, len(data))
	copy(payloadCopy, data)

	go func(r string, payload []byte) {
		slog.Debug("Request rejected", "reason", r)
		if rec := ExtractRecord(payload); rec != nil {
			// Dereference the pointer (*rec) so slog prints {PlayerName...} instead of &{PlayerName...}
			slog.Debug("Rejected payload details", "record:", *rec)
		}
	}(reason, payloadCopy)
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
