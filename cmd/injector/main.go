package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"discord_gateway/internal/config"
	"discord_gateway/internal/discord"
)

func main() {
	xid := flag.String("xid", "1234567", "Player XID to simulate")
	country := flag.String("country", "Torn", "Player Location (Country) to simulate")
	history := flag.String("history", "5", "Number of paid revives in last 90 days")
	rType := flag.String("type", "Regular", "Type of revive (Regular/Premium)")
	factionID := flag.String("faction_id", "", "Faction ID to simulate")

	flag.Parse()

	// 1. Fetch user dir to find configurations
	userDir, err := config.GetUserDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting user dir: %v\n", err)
		os.Exit(1)
	}

	// 2. Read the dynamic port assigned to the testing injection endpoint.
	portFile := filepath.Join(userDir, ".inject_port")
	portData, err := os.ReadFile(portFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read %s. Is the gateway running with the -testing flag?\n", portFile)
		os.Exit(1)
	}
	port := strings.TrimSpace(string(portData))

	// 3. Load config to grab a valid channel ID.
	// This ensures the injected payload passes the isTargetChannel byte scan.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	var channelID string
	for cid := range cfg.TargetChannelIDs {
		channelID = cid
		break // just grab the first one
	}

	if channelID == "" {
		fmt.Fprintf(os.Stderr, "No target channels configured in .env. Cannot spoof payload.\n")
		os.Exit(1)
	}

	// 4. Construct the mock payload
	playerFieldValue := fmt.Sprintf("[TestUser [%s]](https://www.torn.com/profiles.php?XID=%s)", *xid, *xid)

	var historyFieldValue string
	if *history == "0" {
		historyFieldValue = "No recorded history in the last 90 days"
	} else {
		historyFieldValue = fmt.Sprintf("**%s** confirmed paid revives in the last 90 days", *history)
	}

	var factionFieldValue string
	if *factionID == "" {
		factionFieldValue = "No Faction"
	} else {
		factionFieldValue = fmt.Sprintf("[TestFaction [%s]](https://www.torn.com/factions.php?step=profile&ID=%s)", *factionID, *factionID)
	}

	msg := discord.MessageCreate{
		ChannelID: channelID,
		Content:   fmt.Sprintf("<@&814254915403776050> %s has requested a revive.", playerFieldValue),
		Embeds: []discord.Embed{
			{
				Title: fmt.Sprintf("%s Revive Request", *rType),
				Fields: []discord.EmbedField{
					{Name: "Player", Value: playerFieldValue},
					{Name: "Faction", Value: factionFieldValue},
					{Name: "Country", Value: *country},
					{Name: "📊 Revive History", Value: historyFieldValue},
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal payload: %v\n", err)
		os.Exit(1)
	}

	// Hack: The gateway's zero-allocation hot path explicitly checks for a specific
	// JSON key order sent by Discord. Go's standard json.Marshal uses the struct order.
	// We manually flip the Country field here to ensure the payload passes the strict check.
	payloadBytes = bytes.ReplaceAll(payloadBytes,
		[]byte(fmt.Sprintf(`"name":"Country","value":"%s"`, *country)),
		[]byte(fmt.Sprintf(`"value":"%s","name":"Country"`, *country)))

	// 5. Fire off the payload to the local endpoint
	url := fmt.Sprintf("http://127.0.0.1:%s/inject", port)
	fmt.Printf("Injecting mock payload for XID %s into %s...\n", *xid, url)

	resp, err := http.Post(url, "application/json", bytes.NewReader(payloadBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Println("Success! Payload injected successfully.")
	} else {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("UNEXPECTED ERROR: HTTP %d from gateway: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}
}
