package torn

import (
	"discord_gateway/internal/config"
	"discord_gateway/internal/nuke"
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
	nukeClient := nuke.NewClient("")

	h := NewHandlerForTest(cfg, f, dir, nukeClient, quota, NewPayloadCache(1*time.Second), NewMessageLogger(f), 8080)

	payload := []byte(`{"channel_id":"123","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"}]}]}`)

	// Set browser override to detect if it launches
	launched := false
	BrowserOverride = func(url string) {
		launched = true
	}

	h.OnMessageCreate(payload)

	if launched {
		t.Error("Expected browser launch to be blocked by quota")
	}
}
