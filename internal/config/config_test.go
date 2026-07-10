package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWithEnvironmentVariables(t *testing.T) {
	// Force os.UserConfigDir() to return a temporary directory
	// so it doesn't read the actual user's .env file and fail the test.
	tempDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tempDir) // Linux
	os.Setenv("APPDATA", tempDir)         // Windows

	// Temporarily set environment variables to bypass the need for an actual .env file
	os.Setenv("DISCORD_TOKEN", "test_token_123")
	os.Setenv("CHANNEL_IDS", "111,222,333")
	os.Setenv("RATE_LIMIT", "10")
	os.Setenv("MIN_AGE_DAYS", "100")
	os.Setenv("DAILY_QUOTA", "5")
	os.Setenv("MIN_CHANCE", "40")

	defer func() {
		// Cleanup
		os.Unsetenv("XDG_CONFIG_HOME")
		os.Unsetenv("APPDATA")
		os.Unsetenv("DISCORD_TOKEN")
		os.Unsetenv("CHANNEL_IDS")
		os.Unsetenv("RATE_LIMIT")
		os.Unsetenv("MIN_AGE_DAYS")
		os.Unsetenv("DAILY_QUOTA")
		os.Unsetenv("MIN_CHANCE")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Token != "test_token_123" {
		t.Errorf("Expected token 'test_token_123', got '%s'", cfg.Token)
	}
	if !cfg.TargetChannelIDs["222"] {
		t.Errorf("Expected TargetChannelIDs to contain '222'")
	}
	if cfg.RateLimit != 10 {
		t.Errorf("Expected RateLimit 10, got %d", cfg.RateLimit)
	}
	if cfg.MinAgeDays != 100 {
		t.Errorf("Expected MinAgeDays 100, got %d", cfg.MinAgeDays)
	}
	if cfg.DailyQuota != 5 {
		t.Errorf("Expected DailyQuota 5, got %d", cfg.DailyQuota)
	}
	if cfg.MinChance != 40 {
		t.Errorf("Expected MinChance 40, got %d", cfg.MinChance)
	}

	// Verify pre-computed byte slices for fast scanning
	found := false
	for _, b := range cfg.TargetBytes {
		if string(b) == `"channel_id":"222"` {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected TargetBytes to contain '\"channel_id\":\"222\"'")
	}
}

func TestLoadDefaults(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", tempDir)
	os.Setenv("APPDATA", tempDir)

	// Only set the required variables to test the fallback defaults
	os.Setenv("DISCORD_TOKEN", "test_token")
	os.Setenv("CHANNEL_IDS", "111")
	defer func() {
		os.Unsetenv("XDG_CONFIG_HOME")
		os.Unsetenv("APPDATA")
		os.Unsetenv("DISCORD_TOKEN")
		os.Unsetenv("CHANNEL_IDS")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Defaults should be populated
	if cfg.RateLimit != 5 {
		t.Errorf("Expected default RateLimit 5, got %d", cfg.RateLimit)
	}
	if cfg.MinAgeDays != 365 {
		t.Errorf("Expected default MinAgeDays 365, got %d", cfg.MinAgeDays)
	}
	if cfg.DailyQuota != 15 {
		t.Errorf("Expected default DailyQuota 15, got %d", cfg.DailyQuota)
	}
	if cfg.MinChance != 60 {
		t.Errorf("Expected default MinChance 60, got %d", cfg.MinChance)
	}
}

func TestGetUserDir(t *testing.T) {
	dir, err := GetUserDir()
	if err != nil {
		t.Fatalf("GetUserDir failed: %v", err)
	}

	if filepath.Base(dir) != "discord_gateway" {
		t.Errorf("Expected basename 'discord_gateway', got '%s'", filepath.Base(dir))
	}
}
