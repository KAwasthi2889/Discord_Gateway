// Package torn encapsulates the domain-specific business logic for processing Torn events.
// It handles raw payload extraction, rate limiting, logging, and browser orchestration,
// ensuring strict adherence to the zero-allocation hot path philosophy.
package torn

import (
	"log"
	"os/exec"
	"time"

	"discord_gateway/internal/config"
)

// BrowserLauncher provides a controlled, rate-limited mechanism for launching
// the host OS's default web browser. It prevents the system from being overwhelmed
// by rapid bursts of target URLs.
type BrowserLauncher struct {
	limiter *RateLimiter
}

// NewBrowserLauncher creates a new BrowserLauncher instance initialized with a
// rate limit policy defined in the configuration (defaulting to 5 per minute).
// This limit is crucial to avoid locking up the desktop environment when high
// volumes of matched events occur.
func NewBrowserLauncher(cfg *config.Config) *BrowserLauncher {
	return &BrowserLauncher{
		limiter: NewRateLimiter(cfg.RateLimit, time.Minute),
	}
}

// Open evaluates the target URL against the active rate limits. If permitted,
// it dispatches an asynchronous `xdg-open` command to launch the browser in a
// detached goroutine. Returns true if the browser was launched, false if rate-limited.
func (b *BrowserLauncher) Open(url string) bool {
	if b.limiter.Allow() {
		// Launch the browser asynchronously to prevent blocking the WebSocket read pump.
		go func(target string) {
			cmd := exec.Command("xdg-open", target)
			if err := cmd.Run(); err != nil {
				log.Printf("browser_launcher: failed to execute xdg-open for %s: %v", target, err)
			}
		}(url)
		log.Printf("browser_launcher: dispatched xdg-open for %s", url)
		return true
	}
	log.Printf("browser_launcher: rate limit exceeded, dropped request for %s", url)
	return false
}
