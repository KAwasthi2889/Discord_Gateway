package torn

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// quotaState is the JSON-serializable representation of the daily quota counter.
// It is persisted to disk so the quota survives process restarts within the same day.
type quotaState struct {
	Date string `json:"date"`
	Used int    `json:"used"`
}

// DailyQuota enforces a per-day ceiling on the number of successful revives.
// It is thread-safe and persists its state to a JSON file on disk.
//
// The counter resets automatically when the calendar date rolls over.
// On startup, the previous day's state is loaded from disk; if the date
// matches today, the used count is restored. Otherwise, the counter starts fresh.
type DailyQuota struct {
	mu       sync.Mutex
	limit    int
	used     int
	date     string // "2006-01-02" format
	filePath string

	// OnExhausted is called when the daily quota is reached.
	// Defaults to sending os.Interrupt to the current process.
	OnExhausted func()
}

// NewDailyQuota creates a DailyQuota with the given limit and persistence directory.
// It loads existing state from quota.json if the file exists and the date matches today.
func NewDailyQuota(limit int, dir string) *DailyQuota {
	dq := &DailyQuota{
		limit:    limit,
		filePath: filepath.Join(dir, "quota.json"),
		date:     today(),
		OnExhausted: func() {
			slog.Info("Quota exhausted, shutting down")
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(os.Interrupt)
			}
		},
	}

	// Attempt to restore state from disk.
	dq.loadFromDisk()

	slog.Debug("Daily quota loaded from disk", "limit", dq.limit, "used", dq.used, "remaining", dq.limit-dq.used)

	return dq
}

// Allow checks whether the daily quota permits another revive attempt.
// It handles date rollover automatically. This method is called from the
// WebSocket read pump to gate browser launches.
func (dq *DailyQuota) Allow() bool {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	dq.rolloverIfNeeded()
	return dq.used < dq.limit
}

// RecordSuccess increments the used counter and persists the updated state to disk.
// Called from the HTTP callback server when the JS userscript confirms a successful revive.
func (dq *DailyQuota) RecordSuccess() {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	dq.rolloverIfNeeded()
	dq.used++
	remaining := dq.limit - dq.used

	slog.Info("Revive recorded", "used", dq.used, "limit", dq.limit, "remaining", remaining)

	if remaining <= 0 {
		if dq.OnExhausted != nil {
			dq.OnExhausted()
		}
	}

	dq.saveToDisk()
}

// Remaining returns the number of revives remaining today.
func (dq *DailyQuota) Remaining() int {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	dq.rolloverIfNeeded()
	r := dq.limit - dq.used
	if r < 0 {
		return 0
	}
	return r
}

// UpdateLimit changes the quota limit (e.g., during a hot-reload) without
// resetting the used count. This prevents gaming the quota by editing .env.
func (dq *DailyQuota) UpdateLimit(newLimit int) {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	if newLimit != dq.limit {
		slog.Info("Quota limit updated", "old", dq.limit, "new", newLimit)
		dq.limit = newLimit
	}
}

// rolloverIfNeeded resets the counter if the calendar date has changed.
// Must be called with dq.mu held.
func (dq *DailyQuota) rolloverIfNeeded() {
	t := today()
	if t != dq.date {
		slog.Info("New day detected, resetting quota counter", "old_date", dq.date, "new_date", t)
		dq.used = 0
		dq.date = t
		dq.saveToDisk()
	}
}

// loadFromDisk reads the quota state from the JSON file.
// If the file is missing, corrupt, or from a previous day, the counter starts at 0.
func (dq *DailyQuota) loadFromDisk() {
	data, err := os.ReadFile(dq.filePath)
	if err != nil {
		return // File doesn't exist yet, fresh start.
	}

	var state quotaState
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("Corrupt quota.json, starting fresh", "error", err)
		return
	}

	if state.Date == dq.date {
		dq.used = state.Used
	}
	// If dates don't match, used stays at 0 (fresh day).
}

// saveToDisk persists the current quota state to the JSON file.
// Must be called with dq.mu held.
func (dq *DailyQuota) saveToDisk() {
	state := quotaState{
		Date: dq.date,
		Used: dq.used,
	}

	data, err := json.Marshal(state)
	if err != nil {
		slog.Error("Failed to marshal quota state", "error", err)
		return
	}

	tmpPath := dq.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		slog.Error("Failed to write temp quota.json", "error", err)
		return
	}

	if err := os.Rename(tmpPath, dq.filePath); err != nil {
		slog.Error("Failed to atomically rename quota.json", "error", err)
		os.Remove(tmpPath) // Cleanup on failure
	}
}

// today returns the current date in "2006-01-02" format.
func today() string {
	return time.Now().Format("2006-01-02")
}
