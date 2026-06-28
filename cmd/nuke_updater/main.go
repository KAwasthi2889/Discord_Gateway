package main

import (
	"log"
	"path/filepath"
	"time"

	"discord_gateway/internal/config"
	"discord_gateway/internal/nuke"
)

func main() {
	// Attempt to load .env config which holds NUKE_API_TOKEN
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.NukeAPIToken == "" {
		log.Fatal("NUKE_API_TOKEN is not set in config or environment")
	}

	client := nuke.NewClient(cfg.NukeAPIToken)

	// Fetch cache location
	dir, err := config.GetUserDir()
	if err != nil {
		log.Fatalf("Failed to get user dir: %v", err)
	}
	cachePath := filepath.Join(dir, "nuke_cache.json")
	
	// Create a ticker for every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Perform initial fetch immediately
	log.Println("Performing initial fetch of Nuke API...")
	
	// Try loading from disk first so we have a fallback
	if err := client.LoadFromDisk(cachePath); err != nil {
		log.Printf("No existing cache found or failed to load: %v", err)
	}

	if client.RefreshAll() {
		if err := client.SaveToDisk(cachePath); err != nil {
			log.Printf("Error saving initial cache to disk: %v", err)
		} else {
			log.Printf("Successfully saved cache to %s", cachePath)
		}
	} else {
		log.Println("Initial API fetch failed. Keeping existing cache data (if any).")
	}

	log.Println("Updater started. Will refresh data every hour...")
	for {
		<-ticker.C
		log.Println("Refreshing Nuke API data...")
		if client.RefreshAll() {
			if err := client.SaveToDisk(cachePath); err != nil {
				log.Printf("Error saving cache to disk: %v", err)
			} else {
				log.Printf("Successfully saved updated cache to %s", cachePath)
			}
		} else {
			log.Println("API fetch failed. Kept existing cache data.")
		}
	}
}
