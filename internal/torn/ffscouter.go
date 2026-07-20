package torn

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// FFScouterStat represents the JSON response from FFScouter.
type FFScouterStat struct {
	PlayerID   int  `json:"player_id"`
	BSEstimate *int `json:"bs_estimate"` // Pointer to correctly parse JSON null
}

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
}

// GetBattleStats fetches the Battle Stat estimate for a given XID using FFScouter.
// If the API fails, times out, or returns null stats, it safely returns 0 (Fail-Closed).
func GetBattleStats(apiKey string, xid string) int {
	if apiKey == "" || xid == "" {
		return 0
	}

	url := fmt.Sprintf("https://ffscouter.com/api/v1/get-stats?key=%s&targets=%s", apiKey, xid)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		slog.Error("Failed to create FFScouter request", "xid", xid, "error", err)
		return 0
	}
	req.Header.Set("User-Agent", "DiscordGateway/1.0 (Automated Target Fetch)")

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Warn("FFScouter API request failed", "xid", xid, "error", err)
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("FFScouter API returned non-200 status", "xid", xid, "status", resp.StatusCode)
		return 0
	}

	// Cap response body at 1MB to prevent potential memory exhaustion
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		slog.Error("Failed to read FFScouter response body", "xid", xid, "error", err)
		return 0
	}

	var stats []FFScouterStat
	if err := json.Unmarshal(body, &stats); err != nil {
		slog.Error("Failed to unmarshal FFScouter response", "xid", xid, "error", err)
		return 0
	}

	// Safely dereference pointer if not nil
	if len(stats) > 0 && stats[0].BSEstimate != nil {
		return *stats[0].BSEstimate
	}

	return 0
}
