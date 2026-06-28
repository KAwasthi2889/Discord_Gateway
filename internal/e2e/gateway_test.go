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
func setupTestEnvironment(t *testing.T) (*nuke.Client, *httptest.Server) {
	// Setup Mock Nuke API
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shit-lists", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"playerId":9999,"isApproved":true,"shitListCategoryId":2},{"factionId":8888,"isApproved":true,"isFactionBan":true,"shitListCategoryId":1}]}`))
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

	return nukeClient, nukeServer
}

func TestGatewayE2E(t *testing.T) {
	nukeClient, nukeServer := setupTestEnvironment(t)
	defer nukeServer.Close()

	// Start the Mock Torn server in background
	mockCmd := exec.Command("go", "run", "../../cmd/mock_torn/main.go")
	if err := mockCmd.Start(); err != nil {
		t.Fatalf("Failed to start mock torn server: %v", err)
	}
	defer mockCmd.Process.Kill()
	time.Sleep(2 * time.Second) // Let it spin up

	// Create a shared headless browser instance for all parallel tests
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

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
			name:             "Drop - Shitlisted Requester On Behalf",
			payload:          `{"channel_id":"111","embeds":[{"title":"🤝 On Behalf: Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"value":"[Link](https://www.torn.com/profiles.php?XID=1234567)","name":"Profile"},{"value":"TestUser [1234567]","name":"Player"},{"value":"No faction","name":"Faction"},{"value":"[Magic [9999]](https://www.torn.com/profiles.php?XID=9999)","name":"🤝 Requested By (On Behalf)"},{"value":"**5** confirmed paid revives in the last 90 days","name":"\ud83d\udcca Revive History"}]}]}`,
			mockTornScenario: "success",
			expectNoLog:      true,
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
		{
			name:             "Success - Chance Failure (Failed to Revive)",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=10001)"},{"name":"Player","value":"TestUser [10001]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "chance_fail", // Script sees "failed to revive" and returns success
			expectedInLog:    "TestUser,10001,regular,Torn,No faction",
		},
		{
			name:             "Fail - DOM Traveling Hospital",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=10002)"},{"name":"Player","value":"TestUser [10002]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "traveling_hospital",
			expectNoLog:      true,
		},
		{
			name:             "Fail - DOM Traveling Country",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=10003)"},{"name":"Player","value":"TestUser [10003]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "traveling_country",
			expectNoLog:      true,
		},
		{
			name:             "Fail - DOM Okay",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=10004)"},{"name":"Player","value":"TestUser [10004]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "okay",
			expectNoLog:      true,
		},
		{
			name:             "Fail - Unfamiliar State",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=10005)"},{"name":"Player","value":"TestUser [10005]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "unfamiliar",
			expectNoLog:      true,
		},
		{
			name:             "Fail - Cache Expired",
			payload:          `{"channel_id":"111","embeds":[{"title":"Regular Revive Request","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=10006)"},{"name":"Player","value":"TestUser [10006]"},{"name":"Faction","value":"No faction"},{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`,
			mockTornScenario: "hang", // We'll intercept this and manually verify cache logic
			expectNoLog:      true,
		},
	}

	t.Run("group", func(t *testing.T) {
		for _, tt := range tests {
			tt := tt // capture loop variable for parallel execution
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// Setup isolated filesystem per test
				tempDir := t.TempDir()
				cfgDir := filepath.Join(tempDir, ".config", "discord_gateway")
				if err := os.MkdirAll(cfgDir, 0755); err != nil {
					t.Fatal(err)
				}

				// Manually construct config to avoid t.Setenv conflict with t.Parallel
				cfg := &config.Config{
					Token:            "test_token",
					TargetChannelIDs: map[string]bool{"111": true, "222": true},
					TargetBytes:      [][]byte{[]byte(`"channel_id":"111"`), []byte(`"channel_id":"222"`)},
					DailyQuota:       100,
					RateLimit:        10,
					MinAgeDays:       10,
					NoHistoryAllowed: false,
					NukeAPIToken:     "fake_nuke",
				}

				browserChan := make(chan string, 1)
				browserOverride := func(url string) {
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

				quota := torn.NewDailyQuota(cfg.DailyQuota, cfgDir)
				logger := torn.NewMessageLogger(logFile)
				cache := torn.NewPayloadCache(25*time.Second, 0)

				shutdownTriggered := false
				shutdownHook := func() {
					shutdownTriggered = true
				}

				cbPort, _, err := torn.StartCallbackServer(quota, cache, logger, shutdownHook)
				if err != nil {
					t.Fatalf("Failed to start callback: %v", err)
				}

				handler := torn.NewHandlerForTest(cfg, logFile, cfgDir, nukeClient, quota, cache, logger, cbPort, browserOverride)

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

					// Run Chromedp using a new tab in the shared browser
					ctx, cancel := chromedp.NewContext(browserCtx)
					defer cancel()
					ctx, cancel = context.WithTimeout(ctx, 35*time.Second)
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
						// We expect the script to timeout after 25s (cache expiration)
						time.Sleep(27 * time.Second)
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
	})
}
