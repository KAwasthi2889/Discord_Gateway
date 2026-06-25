package torn

import (
	"sync"
	"time"
)

// RateLimiter implements a robust sliding-window concurrency limiter.
// It tracks recent execution timestamps and prevents the application from
// launching OS-level processes too rapidly, mitigating the risk of system resource exhaustion.
// It is fully thread-safe and designed for concurrent use across multiple event-handling goroutines.
type RateLimiter struct {
	mu     sync.Mutex
	times  []time.Time
	limit  int
	window time.Duration
}

// NewRateLimiter initializes and returns a RateLimiter configured with a strict
// operations ceiling (limit) enforced over a rolling time window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:  limit,
		window: window,
	}
}

// Allow evaluates the current timestamp against the rolling window constraint.
// It aggressively purges expired execution timestamps from its internal state.
//
// Returns true and records a new execution if the operation is permitted.
// Returns false if the maximum limit has been reached within the current window.
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// Re-slice without allocating new backing memory where possible.
	valid := r.times[:0]

	for _, t := range r.times {
		if now.Sub(t) < r.window {
			valid = append(valid, t)
		}
	}
	r.times = valid

	// Enforce the threshold against the cleaned history.
	if len(r.times) >= r.limit {
		return false
	}

	r.times = append(r.times, now)
	return true
}
