// Package main provides the Discord Inspector utility.
// This tool connects to the Discord Gateway and dumps raw, pretty-printed JSON
// payloads for MESSAGE_CREATE events matching configured target channels.
// It is intended exclusively for debugging and reverse-engineering payload structures.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
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
	// Setup standard structured logging to stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Retrieve the shared user directory to locate the .env configuration file.
	userDir, err := config.GetUserDir()
	if err != nil {
		slog.Error("Failed to retrieve user directory", "error", err)
		os.Exit(1)
	}

	// Load the shared configuration settings, ensuring consistency with the main gateway.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Configuration error", "error", err)
		os.Exit(1)
	}

	// Initialize a rotating dump file for raw JSON message payloads.
	// Auto-rotates at 10 MB, retaining one backup generation (.1).
	dumpPath := filepath.Join(userDir, "raw_message.txt")
	dumpFile, err := logutil.NewRotatingFile(dumpPath, 10*1024*1024)
	if err != nil {
		slog.Error("Failed to open dump file", "path", dumpPath, "error", err)
		os.Exit(1)
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

		// Dump the exact raw payload as received from Discord, unmodified.
		// No json.Indent or re-marshaling — preserves the exact byte sequence
		// so we can verify what Discord actually sends.
		output := fmt.Sprintf("\n========== NEW MESSAGE AT %s ==========\n%s\n======================================================\n",
			time.Now().Format("2006-01-02 15:04:05"),
			string(data),
		)

		// Print a concise notification to standard output to indicate arrival without cluttering the terminal.
		slog.Info("A new message has arrived...")

		// Persist the full raw payload to the rotating dump file for deeper analysis.
		if _, err := dumpFile.Write([]byte(output)); err != nil {
			slog.Error("Failed to write payload to raw dump file", "error", err)
		}
	})

	slog.Info("Starting Discord Inspector utility")
	slog.Info("Raw JSON payloads will be saved to", "path", dumpPath)

	// Block and maintain the WebSocket connection until the application is terminated.
	if err := client.Run(ctx); err != nil {
		slog.Error("Client terminated abnormally", "error", err)
		os.Exit(1)
	}

	slog.Info("Inspector shutdown complete.")
}
