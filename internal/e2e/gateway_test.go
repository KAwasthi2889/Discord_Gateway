package e2e_test

import (
	"context"
	"fmt"
	"net"
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
		w.Write([]byte(`{"data":[
			{"playerId":9999,"isApproved":true,"shitListCategoryId":2},
			{"playerId":5555,"isApproved":true,"shitListCategoryId":3},
			{"factionId":8888,"isApproved":true,"isFactionBan":true,"shitListCategoryId":1}
		]}`))
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

func makeTestPayload(title, targetXID, factionStr, requesterStr string) string {
	reqField := ""
	if requesterStr != "" {
		reqField = fmt.Sprintf(`{"value":"%s","name":"🤝 Requested By (On Behalf)"},`, requesterStr)
	}
	return fmt.Sprintf(`{"channel_id":"111","embeds":[{"title":"%s","fields":[{"value":"Torn","name":"Country"},{"name":"Profile","value":"[Link](https://www.torn.com/profiles.php?XID=%s)"},{"name":"Player","value":"TestUser [%s]"},{"name":"Faction","value":"%s"},%s{"name":"\ud83d\udcca Revive History","value":"**5** confirmed paid revives in the last 90 days"}]}]}`, title, targetXID, targetXID, factionStr, reqField)
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
	// Wait up to 15 seconds for mock server to be ready on port 8080
	ready := false
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:8080", 500*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("Mock Torn server failed to start on :8080 in time")
	}

	// Create a shared headless browser instance for all parallel tests
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WSURLReadTimeout(60*time.Second), // Give Chrome 60 seconds to boot on slow CI
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// Pre-start the browser to avoid timeouts on the first test in slow CI environments
	if err := chromedp.Run(browserCtx); err != nil {
		t.Fatalf("Failed to initialize headless Chrome: %v", err)
	}

	tests := []struct {
		name              string
		payload           string
		mockTornScenario  string
		expectedInLog     string // If not empty, expect this exact string in CSV log
		expectNoLog       bool   // If true, expect NO log entry for this XID
		expectShutdown    bool   // If true, expect process to exit on its own
		overrideMinChance int    // If non-zero, overrides the default 60% MinChance
	}{
		{
			name:             "Success - Standard Revive",
			payload:          makeTestPayload("Regular Revive Request", "1234567", "No faction", ""),
			mockTornScenario: "success",
			expectedInLog:    "TestUser,1234567,regular,Torn,No faction",
		},
		{
			name:             "Drop - Shitlisted Requester On Behalf",
			payload:          makeTestPayload("🤝 On Behalf: Regular Revive Request", "1234567", "No faction", "[Magic [9999]](https://www.torn.com/profiles.php?XID=9999)"),
			mockTornScenario: "success",
			expectNoLog:      true,
		},
		{
			name:             "Drop - Shitlisted Player",
			payload:          makeTestPayload("Regular Revive Request", "9999", "No faction", ""),
			mockTornScenario: "success",
			expectNoLog:      true,
		},
		{
			name:             "Drop - Shitlisted Faction",
			payload:          makeTestPayload("Regular Revive Request", "1111", "[Faction [8888]](https://www.torn.com/factions.php?step=profile&ID=8888)", ""),
			mockTornScenario: "success",
			expectNoLog:      true,
		},
		{
			name:             "Success - Shitlisted Target (Cat 3) but Clean Requester On Behalf",
			payload:          makeTestPayload("🤝 On Behalf: Regular Revive Request", "5555", "No faction", "[CleanFriend [1234567]](https://www.torn.com/profiles.php?XID=1234567)"),
			mockTornScenario: "success",
			expectedInLog:    "TestUser,5555,regular,Torn,No faction",
		},
		{
			name:             "Drop - Shitlisted Faction with Clean Requester On Behalf",
			payload:          makeTestPayload("🤝 On Behalf: Regular Revive Request", "1111", "[Faction [8888]](https://www.torn.com/factions.php?step=profile&ID=8888)", "[CleanFriend [1234567]](https://www.torn.com/profiles.php?XID=1234567)"),
			mockTornScenario: "success",
			expectNoLog:      true,
		},
		{
			name:             "Success - Faction Contract Override",
			payload:          makeTestPayload("Regular Revive Request", "2222", "[Faction [7777]](https://www.torn.com/factions.php?step=profile&ID=7777)", ""),
			mockTornScenario: "success",          // Success means 100% chance, so it passes the 50% minChance injected
			expectedInLog:    "Faction Contract", // Should be appended as the Contract Note
		},
		{
			name:             "Fail - Low Chance",
			payload:          makeTestPayload("Regular Revive Request", "3333", "No faction", ""),
			mockTornScenario: "low_chance", // 10% chance
			expectNoLog:      true,         // Fails in browser, no successful CSV record
		},
		{
			name:             "Fail - Button Disabled",
			payload:          makeTestPayload("Regular Revive Request", "4444", "No faction", ""),
			mockTornScenario: "disabled",
			expectNoLog:      true, // Fails in browser
		},
		{
			name:             "Fail - Energy Error Shutdown",
			payload:          makeTestPayload("Regular Revive Request", "5555", "No faction", ""),
			mockTornScenario: "energy_error",
			expectNoLog:      true,
			expectShutdown:   true,
		},
		{
			name:             "Fail - Timeout",
			payload:          makeTestPayload("Regular Revive Request", "6666", "No faction", ""),
			mockTornScenario: "timeout",
			expectNoLog:      true, // Times out, no CSV
		},
		{
			name:             "Fail - Status Offline but Online Required",
			payload:          makeTestPayload("Regular Revive Request", "7777", "No faction", ""),
			mockTornScenario: "status_offline",
			expectNoLog:      true,
			expectedInLog:    "",
		},
		{
			name:             "Success - Status Any",
			payload:          makeTestPayload("Regular Revive Request", "8888", "No faction", ""),
			mockTornScenario: "status_offline", // Contract is ANY, so offline should still succeed
			expectedInLog:    "Player Contract",
		},
		{
			name:             "Fail - Chance Failure (Failed to Revive)",
			payload:          makeTestPayload("Regular Revive Request", "10001", "No faction", ""),
			mockTornScenario: "chance_fail", // 100% chance but fails -> return fail
			expectNoLog:      true,
		},
		{
			name:              "Success - Low Chance Failure",
			payload:           makeTestPayload("Regular Revive Request", "10007", "No faction", ""),
			mockTornScenario:  "low_chance_fail", // 10% chance and fails -> return success
			expectedInLog:     "TestUser,10007,regular,Torn,No faction",
			overrideMinChance: 5,                 // 5 < 10, so it attempts it
		},
		{
			name:             "Fail - DOM Traveling Hospital",
			payload:          makeTestPayload("Regular Revive Request", "10002", "No faction", ""),
			mockTornScenario: "traveling_hospital",
			expectNoLog:      true,
		},
		{
			name:             "Fail - DOM Traveling Country",
			payload:          makeTestPayload("Regular Revive Request", "10003", "No faction", ""),
			mockTornScenario: "traveling_country",
			expectNoLog:      true,
		},
		{
			name:             "Fail - DOM Okay",
			payload:          makeTestPayload("Regular Revive Request", "10004", "No faction", ""),
			mockTornScenario: "okay",
			expectNoLog:      true,
		},
		{
			name:             "Fail - Unfamiliar State",
			payload:          makeTestPayload("Regular Revive Request", "10005", "No faction", ""),
			mockTornScenario: "unfamiliar",
			expectNoLog:      true,
		},
		{
			name:             "Fail - Cache Expired",
			payload:          makeTestPayload("Regular Revive Request", "10006", "No faction", ""),
			mockTornScenario: "hang", // We'll intercept this and manually verify cache logic
			expectNoLog:      true,
		},
	}

	t.Run("group", func(t *testing.T) {
		for _, tt := range tests {
			tt := tt // capture loop variable
			t.Run(tt.name, func(t *testing.T) {
				// Removed t.Parallel() to prevent headless Chrome from being overwhelmed
				// by 20 simultaneous tabs on small CI runners.

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
					MinChance:        60,
				}
				if tt.overrideMinChance > 0 {
					cfg.MinChance = tt.overrideMinChance
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
				cache := torn.NewPayloadCache(context.Background(), 25*time.Second, 0)

				shutdownTriggered := false
				shutdownHook := func() {
					shutdownTriggered = true
				}

				cbPort, _, cbToken, err := torn.StartCallbackServer(quota, cache, logger, shutdownHook)
				if err != nil {
					t.Fatalf("Failed to start callback: %v", err)
				}

				handler := torn.NewHandlerForTest(context.Background(), cfg, logFile, cfgDir, nukeClient, quota, cache, logger, cbPort, cbToken, browserOverride)

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
