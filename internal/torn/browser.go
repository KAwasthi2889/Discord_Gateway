package torn

import (
	"context"
	"fmt"
	"log/slog"
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

var BrowserOverride func(url string)

// Open evaluates the target URL against the active rate limits. If permitted,
// it dispatches an asynchronous `xdg-open` command to launch the browser in a
// detached goroutine. Returns true if the browser was launched, false if rate-limited.
func (b *BrowserLauncher) Open(url string, xid string) bool {
	if b.limiter.Allow() {
		if BrowserOverride == nil {
			// Launch the browser asynchronously to prevent blocking the WebSocket read pump.
			go func(target string) {
				// Give xdg-open a maximum of 5 seconds to hand off the URL to the browser.
				// This prevents dangling xdg-open processes from leaking goroutines if the desktop environment hangs it.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				cmd := exec.CommandContext(ctx, "xdg-open", target)
				if err := cmd.Run(); err != nil {
					if ctx.Err() != context.DeadlineExceeded {
						slog.Error("Failed to execute xdg-open", "target", target, "error", err)
					}
				}
			}(url)
		} else {
			BrowserOverride(url)
		}
		slog.Debug(fmt.Sprintf("Dispatched xdg-open for %s", xid))
		return true
	}
	slog.Warn("Rate limit exceeded, dropped request", "xid", xid)
	return false
}
