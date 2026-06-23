package torn

import (
	"encoding/json"
	"log"
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
}

// NewDailyQuota creates a DailyQuota with the given limit and persistence directory.
// It loads existing state from quota.json if the file exists and the date matches today.
func NewDailyQuota(limit int, dir string) *DailyQuota {
	dq := &DailyQuota{
		limit:    limit,
		filePath: filepath.Join(dir, "quota.json"),
		date:     today(),
	}

	// Attempt to restore state from disk.
	dq.loadFromDisk()

	log.Printf("daily_quota: initialized (limit=%d, used=%d, remaining=%d)",
		dq.limit, dq.used, dq.limit-dq.used)

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

	log.Printf("daily_quota: revive recorded (%d/%d used, %d remaining)", dq.used, dq.limit, remaining)

	if remaining <= 0 {
		log.Println("daily_quota: ⚠ quota exhausted — no more revives will be opened today")
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
		log.Printf("daily_quota: limit updated %d → %d", dq.limit, newLimit)
		dq.limit = newLimit
	}
}

// rolloverIfNeeded resets the counter if the calendar date has changed.
// Must be called with dq.mu held.
func (dq *DailyQuota) rolloverIfNeeded() {
	t := today()
	if t != dq.date {
		log.Printf("daily_quota: new day detected (%s → %s), resetting counter", dq.date, t)
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
		return // File doesn't exist yet — fresh start.
	}

	var state quotaState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("daily_quota: corrupt quota.json, starting fresh: %v", err)
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
		log.Printf("daily_quota: failed to marshal state: %v", err)
		return
	}

	if err := os.WriteFile(dq.filePath, data, 0600); err != nil {
		log.Printf("daily_quota: failed to write quota.json: %v", err)
	}
}

// today returns the current date in "2006-01-02" format.
func today() string {
	return time.Now().Format("2006-01-02")
}
