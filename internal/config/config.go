// Package config provides configuration parsing, validation, and hot-reloading support.
// It manages the parsing of user-specific .env files and environment variables,
// exposing a strongly typed configuration object to the rest of the application.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds the validated application configuration required for establishing
// the Gateway connection and filtering incoming events.
type Config struct {
	// Token is the Discord Bot authentication token.
	Token string

	// TargetChannelIDs is a lookup map of Discord Channel IDs that the application monitors.
	TargetChannelIDs map[string]bool

	// TargetBytes contains pre-computed byte slices representing the JSON fragments
	// for the target channels (e.g., []byte(`"channel_id":"123"`)).
	// This enables zero-allocation string searching on the hot path.
	TargetBytes [][]byte

	// NoHistoryAllowed determines if requests with no past revives are accepted.
	NoHistoryAllowed bool

	// RateLimit is the maximum number of browser launches allowed per minute.
	// Defaults to 5 if not specified.
	RateLimit int

	// MinAgeDays is the minimum account age in days for an auto-revive.
	// Defaults to 365 if not specified.
	MinAgeDays int

	// DailyQuota is the maximum number of successful revives allowed per day.
	// Clamped to 1–15. Defaults to 15 if not specified or out of range.
	DailyQuota int

	// NukeAPIToken is the API token used to authenticate with nuke.family for Contracts/Shitlist.
	NukeAPIToken string

	// MinChance is the default minimum revive chance percentage.
	MinChance int
}

// GetUserDir resolves the absolute path to the current user's dedicated configuration
// directory, specifically using the standard OS configuration directory.
// This ensures secure, user-isolated configuration loading.
func GetUserDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine user config directory: %w", err)
	}
	return filepath.Join(configDir, "discord_gateway"), nil
}

// Load parses the .env file from the user's configuration directory.
// It falls back to standard OS environment variables if the file cannot be parsed.
// It strictly validates required fields and pre-computes data structures optimized
// for the zero-allocation hot path.
func Load() (*Config, error) {
	dir, err := GetUserDir()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve user directory: %w", err)
	}
	envPath := filepath.Join(dir, ".env")

	// Attempt to load the .env file.
	// We intentionally suppress the error here because the godotenv library
	// throws an error if the file doesn't exist, but we still want to allow
	// falling back to system environment variables.
	envMap, err := godotenv.Read(envPath)
	if err != nil {
		envMap = make(map[string]string) // Fallback if file is missing or unreadable
	}

	// Resolve the Discord token, preferring the .env file over the system environment.
	token := envMap["DISCORD_TOKEN"]
	if token == "" {
		token = os.Getenv("DISCORD_TOKEN")
	}

	// Resolve the target channel IDs, preferring the .env file.
	channelIDsStr := envMap["CHANNEL_IDS"]
	if channelIDsStr == "" {
		channelIDsStr = os.Getenv("CHANNEL_IDS")
	}

	// Resolve the no history allowed flag.
	noHistoryStr := envMap["NO_HISTORY_ALLOWED"]
	if noHistoryStr == "" {
		noHistoryStr = os.Getenv("NO_HISTORY_ALLOWED")
	}

	// Resolve rate limit (default: 5)
	rateLimitStr := envMap["RATE_LIMIT"]
	if rateLimitStr == "" {
		rateLimitStr = os.Getenv("RATE_LIMIT")
	}
	rateLimit, err := strconv.Atoi(rateLimitStr)
	if err != nil || rateLimit <= 0 {
		rateLimit = 5
	}

	// Resolve min age days (default: 365)
	minAgeStr := envMap["MIN_AGE_DAYS"]
	if minAgeStr == "" {
		minAgeStr = os.Getenv("MIN_AGE_DAYS")
	}
	minAgeDays, err := strconv.Atoi(minAgeStr)
	if err != nil || minAgeDays <= 0 {
		minAgeDays = 365
	}

	// Resolve daily quota (default: 15, clamped to 1–15)
	dailyQuotaStr := envMap["DAILY_QUOTA"]
	if dailyQuotaStr == "" {
		dailyQuotaStr = os.Getenv("DAILY_QUOTA")
	}
	dailyQuota, err := strconv.Atoi(dailyQuotaStr)
	if err != nil || dailyQuota < 1 || dailyQuota > 15 {
		dailyQuota = 15
	}

	// Resolve Nuke API token
	nukeToken := envMap["NUKE_API_TOKEN"]
	if nukeToken == "" {
		nukeToken = os.Getenv("NUKE_API_TOKEN")
	}

	// Resolve min chance (default: 60)
	minChanceStr := envMap["MIN_CHANCE"]
	if minChanceStr == "" {
		minChanceStr = os.Getenv("MIN_CHANCE")
	}
	minChance, err := strconv.Atoi(minChanceStr)
	if err != nil || minChance < 0 || minChance > 100 {
		minChance = 60
	}

	// Enforce strict validation on required configuration keys.
	if token == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN must be set in %s or environment", envPath)
	}
	if channelIDsStr == "" {
		return nil, fmt.Errorf("CHANNEL_IDS must be set in %s or environment", envPath)
	}

	cfg := &Config{
		Token:                 token,
		TargetChannelIDs:      make(map[string]bool),
		NoHistoryAllowed:      strings.ToLower(noHistoryStr) == "true",
		RateLimit:             rateLimit,
		MinAgeDays:            minAgeDays,
		DailyQuota:            dailyQuota,
		NukeAPIToken:          nukeToken,
		MinChance:             minChance,
	}

	// Process the comma-separated channel IDs and compute the hot-path signatures.
	for _, id := range strings.Split(channelIDsStr, ",") {
		cleanID := strings.TrimSpace(id)
		if cleanID != "" {
			cfg.TargetChannelIDs[cleanID] = true

			// Pre-compute the byte string signatures for zero-allocation searching.
			// By compiling this pattern exactly as it appears in the Discord JSON payload,
			// the handler can bypass expensive standard library unmarshaling.
			cfg.TargetBytes = append(cfg.TargetBytes, []byte(`"channel_id":"`+cleanID+`"`))
		}
	}

	return cfg, nil
}
