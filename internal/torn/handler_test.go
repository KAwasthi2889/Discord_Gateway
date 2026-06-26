package torn

import (
	"discord_gateway/internal/config"
	"discord_gateway/internal/nuke"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHandler_TargetChannel(t *testing.T) {
	cfg := &config.Config{
		TargetBytes: [][]byte{[]byte(`"channel_id":"123"`)},
	}

	if !isTargetChannel(cfg, []byte(`{"channel_id":"123","content":"test"}`)) {
		t.Error("Expected to match target channel")
	}

	if isTargetChannel(cfg, []byte(`{"channel_id":"456","content":"test"}`)) {
		t.Error("Expected to reject unknown channel")
	}
}

func TestHandler_UpdateConfig(t *testing.T) {
	cfg := &config.Config{DailyQuota: 10}
	dir := t.TempDir()
	quota := NewDailyQuota(10, dir)

	h := &Handler{
		cfg:   cfg,
		quota: quota,
	}

	newCfg := &config.Config{DailyQuota: 20}
	h.UpdateConfig(newCfg)

	if h.cfg.DailyQuota != 20 {
		t.Error("Expected config to be updated")
	}
}

func TestHandler_QuotaRejection(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.csv")
	f, _ := os.Create(logPath)
	defer f.Close()

	cfg := &config.Config{
		TargetBytes: [][]byte{[]byte(`"channel_id":"123"`)},
		DailyQuota:  0, // Set to 0 to simulate max quota
	}

	quota := NewDailyQuota(0, dir)
	quota.OnExhausted = func() {} // Prevent test process interruption

	nukeClient := nuke.NewClient("")

	launched := false
	browserOverride := func(url string) {
		launched = true
	}

	h := NewHandlerForTest(cfg, f, dir, nukeClient, quota, NewPayloadCache(1*time.Second, 0), NewMessageLogger(f), 8080, browserOverride)

	payload := []byte(`{"channel_id":"123","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"}]}]}`)

	h.OnMessageCreate(payload)

	if launched {
		t.Error("Expected browser launch to be blocked by quota")
	}
}

func TestHandler_GlobalRateLimit(t *testing.T) {
	cfg := &config.Config{
		TargetBytes: [][]byte{[]byte(`"channel_id":"123"`)},
		DailyQuota:  100, // Abundant quota
		RateLimit:   100, // Abundant browser launcher limit
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.csv")
	f, _ := os.Create(logPath)
	defer f.Close()

	quota := NewDailyQuota(100, dir)
	nukeClient := nuke.NewClient("")

	launchCount := 0
	browserOverride := func(url string) {
		launchCount++
	}

	h := NewHandlerForTest(cfg, f, dir, nukeClient, quota, NewPayloadCache(1*time.Second, 0), NewMessageLogger(f), 8080, browserOverride)

	// Fire 16 requests with different XIDs
	for i := 1; i <= 16; i++ {
		payload := []byte(fmt.Sprintf(`{"channel_id":"123","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=%d)"}]}]}`, i))
		h.OnMessageCreate(payload)
	}

	// Because global rate limit is 15 per minute, we expect exactly 15 launches.
	if launchCount != 15 {
		t.Errorf("Expected 15 launches due to global rate limit, got %d", launchCount)
	}
}
