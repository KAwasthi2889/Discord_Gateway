package main

import (
	"fmt"
	"io"
	"net/http"
	"discord_gateway/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("Error loading config:", err)
		return
	}

	// Test Discord Gateway URL
	resp1, err := http.Get("https://discord.com/api/v10/gateway")
	if err == nil {
		body1, _ := io.ReadAll(resp1.Body)
		resp1.Body.Close()
		fmt.Printf("Discord Gateway payload size: %d bytes\n", len(body1))
	} else {
		fmt.Println("Error Discord:", err)
	}

	// Test Nuke API Shitlist
	req, _ := http.NewRequest("GET", "https://nuke.family/api/shit-lists", nil)
	req.Header.Set("Authorization", "Bearer " + cfg.NukeAPIToken)
	req.Header.Set("Accept", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err == nil {
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		fmt.Printf("Nuke API Shitlist first page size: %d bytes\n", len(body2))
	} else {
		fmt.Println("Error Nuke API:", err)
	}
}
