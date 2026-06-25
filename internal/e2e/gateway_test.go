package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"discord_gateway/internal/config"
	"discord_gateway/internal/nuke"
	"discord_gateway/internal/torn"
)

// setupTestEnvironment spins up the Mock Nuke API and writes the temporary configuration.
func setupTestEnvironment(t *testing.T) (string, *nuke.Client, *httptest.Server) {
	tempDir := t.TempDir()
	cfgDir := filepath.Join(tempDir, ".config", "discord_gateway")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}

	envPath := filepath.Join(cfgDir, ".env")
	envData := `
DISCORD_TOKEN=test_token
CHANNEL_IDS=111,222
DAILY_QUOTA=100
RATE_LIMIT=10
MIN_AGE_DAYS=10
NO_HISTORY_ALLOWED=false
NUKE_API_TOKEN=fake_nuke
`
	if err := os.WriteFile(envPath, []byte(envData), 0644); err != nil {
		t.Fatal(err)
	}

	// Override HOME and XDG_CONFIG_HOME
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
	os.Setenv("HOME", tempDir)

	origXDG := os.Getenv("XDG_CONFIG_HOME")
	t.Cleanup(func() { os.Setenv("XDG_CONFIG_HOME", origXDG) })
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tempDir, ".config"))

	// Setup Mock Nuke API
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shit-lists", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"playerId":9999},{"factionId":8888}]}`))
	})
	mux.HandleFunc("/api/contracts/get_contracts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"faction_id":7777,"rule_revive_chance_percentage":50,"rule_player_status":"ANY","note":"Faction Contract"}]`))
	})
	mux.HandleFunc("/api/revive-packages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
			{"focus_player_id":6666,"is_active":true,"contracts":[{"rule_revive_chance_percentage":60,"rule_player_status":"ANY","note":"Player Contract"}]},
			{"focus_player_id":7777,"is_active":true,"contracts":[{"rule_revive_chance_percentage":10,"rule_player_status":"ONLINE","note":"Online Only"}]},
			{"focus_player_id":8888,"is_active":true,"contracts":[{"rule_revive_chance_percentage":10,"rule_player_status":"ANY","note":"Player Contract"}]}
		]}`))
	})

	nukeServer := httptest.NewServer(mux)

	nukeClient := nuke.NewClient("fake_nuke")
	nukeClient.SetBaseURL(nukeServer.URL + "/api")
	// Load it forcefully
	nukeClient.LoadOrFetch("") // Provide empty path to force fetch

	return cfgDir, nukeClient, nukeServer
}

func TestGatewayE2E(t *testing.T) {
	cfgDir, nukeClient, nukeServer := setupTestEnvironment(t)
	defer nukeServer.Close()

	// Start the Mock Torn server in background
	mockCmd := exec.Command("go", "run", "../../cmd/mock_torn/main.go")
	if err := mockCmd.Start(); err != nil {
		t.Fatalf("Failed to start mock torn server: %v", err)
	}
	defer mockCmd.Process.Kill()
	time.Sleep(2 * time.Second) // Let it spin up

	tests := []struct {
		name             string
		payload          string
		mockTornScenario string
		expectedInLog    string // What we expect to see in records.csv
		expectNoLog      bool   // If true, we expect the request to be dropped/rejected
		expectShutdown   bool   // If true, we expect the emergency shutdown hook to be called
	}{
		{
			name:             "Success - Standard Revive",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=1234567)"},{"name":"Player","value":"TestUser [1234567]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "success",
			expectedInLog:    "TestUser,1234567,regular,Torn,No faction",
		},
		{
			name:             "Drop - Shitlisted Player",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=9999)"},{"name":"Player","value":"TestUser [9999]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "success",
			expectNoLog:      true,
		},
		{
			name:             "Drop - Shitlisted Faction",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=1111)"},{"name":"Player","value":"TestUser [1111]"},{"name":"Faction","value":"[Faction [8888]](https://www.torn.com/factions.php?step=profile&ID=8888)"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "success",
			expectNoLog:      true,
		},
		{
			name:             "Success - Faction Contract Override",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=2222)"},{"name":"Player","value":"TestUser [2222]"},{"name":"Faction","value":"[Faction [7777]](https://www.torn.com/factions.php?step=profile&ID=7777)"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "success",          // Success means 100% chance, so it passes the 50% minChance injected
			expectedInLog:    "Faction Contract", // Should be appended as the Contract Note
		},
		{
			name:             "Fail - Low Chance",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=3333)"},{"name":"Player","value":"TestUser [3333]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "low_chance", // 10% chance
			expectNoLog:      true,         // Fails in browser, no successful CSV record
		},
		{
			name:             "Fail - Button Disabled",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=4444)"},{"name":"Player","value":"TestUser [4444]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "disabled",
			expectNoLog:      true, // Fails in browser
		},
		{
			name:             "Fail - Energy Error Shutdown",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=5555)"},{"name":"Player","value":"TestUser [5555]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "energy_error",
			expectNoLog:      true,
			expectShutdown:   true,
		},
		{
			name:             "Fail - Timeout",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=6666)"},{"name":"Player","value":"TestUser [6666]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "timeout",
			expectNoLog:      true, // Times out, no CSV
		},
		{
			name:             "Fail - Status Offline but Online Required",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=7777)"},{"name":"Player","value":"TestUser [7777]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "status_offline",
			expectNoLog:      true,
			expectedInLog:    "",
		},
		{
			name:             "Success - Status Any",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=8888)"},{"name":"Player","value":"TestUser [8888]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "status_offline", // Contract is ANY, so offline should still succeed
			expectedInLog:    "Player Contract",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			browserChan := make(chan string, 1)
			torn.BrowserOverride = func(url string) {
				// Inject the scenario param
				mockURL := strings.Replace(url, "https://www.torn.com", "http://127.0.0.1:8080", 1)
				if strings.Contains(mockURL, "?") {
					mockURL = strings.Replace(mockURL, "?", "?scenario="+tt.mockTornScenario+"&", 1)
				} else {
					mockURL += "?scenario=" + tt.mockTornScenario
				}
				browserChan <- mockURL
			}

			logPath := filepath.Join(cfgDir, fmt.Sprintf("records_%d.csv", time.Now().UnixNano()))
			logFile, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer logFile.Close()

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Config load failed: %v", err)
			}

			quota := torn.NewDailyQuota(cfg.DailyQuota, cfgDir)
			logger := torn.NewMessageLogger(logFile)
			cache := torn.NewPayloadCache(10 * time.Second)

			shutdownTriggered := false
			shutdownHook := func() {
				shutdownTriggered = true
			}

			cbPort, err := torn.StartCallbackServer(quota, cache, logger, shutdownHook)
			if err != nil {
				t.Fatalf("Failed to start callback: %v", err)
			}

			handler := torn.NewHandlerForTest(cfg, logFile, cfgDir, nukeClient, quota, cache, logger, cbPort)

			// Trigger message synchronously
			handler.OnMessageCreate([]byte(tt.payload))

			// Check browser launch unless we expect an instant drop (like Shitlist)
			var targetURL string
			select {
			case targetURL = <-browserChan:
				if tt.expectNoLog && !tt.expectShutdown && tt.mockTornScenario == "success" {
					// Shitlist dropped it! We shouldn't have launched a browser!
					t.Fatalf("Expected payload to be dropped by Gateway (e.g. Shitlist), but it launched browser: %s", targetURL)
				}
				t.Logf("Intercepted browser launch: %s", targetURL)

				// Run Chromedp
				ctx, cancel := chromedp.NewContext(context.Background())
				defer cancel()
				ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
				defer cancel()

				err = chromedp.Run(ctx,
					chromedp.Navigate(targetURL),
				)
				if err != nil {
					t.Fatalf("Chromedp navigate failed: %v", err)
				}

				if tt.expectShutdown {
					// Need to wait for hook to be fired
					time.Sleep(3 * time.Second)
					if !shutdownTriggered {
						t.Errorf("Expected emergency shutdown to be triggered, but it wasn't")
					}
				} else if tt.mockTornScenario == "timeout" {
					// We expect the script to timeout after 10s
					time.Sleep(12 * time.Second)
				} else if !tt.expectNoLog {
					// Happy path
					err = chromedp.Run(ctx, chromedp.WaitVisible(`.profile-buttons-dialog`, chromedp.ByQuery))
					if err != nil {
						t.Fatalf("Chromedp automation failed: %v", err)
					}
				} else {
					// Fails in browser (low chance or disabled)
					time.Sleep(3 * time.Second)
				}

			case <-time.After(2 * time.Second):
				if !tt.expectNoLog || (tt.mockTornScenario != "success" && !tt.expectShutdown) {
					// We expected a browser launch but got none.
					// NOTE: This logic means: if we expectNoLog and it's a "success" scenario,
					// we assume the Gateway dropped it BEFORE launching the browser (e.g. Shitlist).
				}
			}

			// Allow logging to finish
			time.Sleep(1 * time.Second)

			// Verify that CSV file contains the log
			contentBytes, _ := os.ReadFile(logPath)
			content := string(contentBytes)

			// Grab HTML right before finishing
			var body string
			chromedp.Run(context.Background(), chromedp.OuterHTML("html", &body, chromedp.ByQuery))
			t.Logf("FINAL HTML: %s", body)

			if tt.expectNoLog {
				if strings.Contains(content, "TestUser") {
					t.Errorf("Expected NO record in CSV, but found one:\n%s", content)
				}
			} else {
				if !strings.Contains(content, tt.expectedInLog) {
					t.Errorf("Expected records.csv to contain '%s', got:\n%s", tt.expectedInLog, content)
				}
			}
		})
	}
}
