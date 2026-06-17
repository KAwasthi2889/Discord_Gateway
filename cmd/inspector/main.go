// Package main provides the Discord Inspector utility.
// This tool connects to the Discord Gateway and dumps raw, pretty-printed JSON
// payloads for MESSAGE_CREATE events matching configured target channels.
// It is intended exclusively for debugging and reverse-engineering payload structures.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"discord_gateway/internal/config"
	"discord_gateway/internal/discord"
	"discord_gateway/internal/logutil"
)

func main() {
	// Retrieve the shared user directory to locate the .env configuration file.
	userDir, err := config.GetUserDir()
	if err != nil {
		log.Fatalf("Failed to retrieve user directory: %v", err)
	}

	// Load the shared configuration settings, ensuring consistency with the main gateway.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Initialize a rotating dump file for raw JSON message payloads.
	// Auto-rotates at 10 MB, retaining one backup generation (.1).
	dumpPath := filepath.Join(userDir, "raw_message.txt")
	dumpFile, err := logutil.NewRotatingFile(dumpPath, 10*1024*1024)
	if err != nil {
		log.Fatalf("Failed to open dump file %s: %v", dumpPath, err)
	}
	defer dumpFile.Close()

	// Establish a context for graceful shutdown handling upon receiving termination signals.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the underlying generic Discord WebSocket client.
	client := discord.NewClient(cfg)

	// Register an anonymous inspection handler to process incoming MESSAGE_CREATE events.
	client.RegisterMessageCreateHandler(func(data []byte) {
		// Filter incoming payloads to only those originating from our configured TargetChannels.
		// This prevents the dump file from being flooded by irrelevant server chatter.
		matched := false
		for _, tcb := range cfg.TargetBytes {
			if bytes.Contains(data, tcb) {
				matched = true
				break
			}
		}

		if !matched {
			return
		}

		// Re-marshal the raw byte payload with indentation for human readability.
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, data, "", "  "); err != nil {
			log.Printf("Failed to parse incoming JSON payload: %v\nRaw: %s", err, string(data))
			return
		}

		// Construct a clearly delimited output block including a timestamp.
		output := fmt.Sprintf("\n========== NEW MESSAGE AT %s ==========\n%s\n======================================================\n",
			time.Now().Format("2006-01-02 15:04:05"),
			prettyJSON.String(),
		)

		// Print a concise notification to standard output to indicate arrival without cluttering the terminal.
		fmt.Println("A new message has arrived...")

		// Persist the full formatted payload to the rotating dump file for deeper analysis.
		if _, err := dumpFile.Write([]byte(output)); err != nil {
			log.Printf("Failed to write payload to raw dump file: %v", err)
		}
	})

	log.Println("Starting Discord Inspector utility...")
	log.Printf("Raw JSON payloads will be saved to: %s", dumpPath)

	// Block and maintain the WebSocket connection until the application is terminated.
	if err := client.Run(ctx); err != nil {
		log.Fatalf("Client terminated abnormally: %v", err)
	}

	log.Println("Inspector shutdown complete.")
}
