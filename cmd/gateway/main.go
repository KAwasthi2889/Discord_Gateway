// Package main serves as the primary entry point for the Discord Gateway application.
// It is responsible for wiring up dependencies, initializing the configuration watcher,
// setting up the logging infrastructure, and starting the main event loop.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"discord_gateway/internal/config"
	"discord_gateway/internal/discord"
	"discord_gateway/internal/logutil"
	"discord_gateway/internal/nuke"
	"discord_gateway/internal/torn"
)

func main() {
	// Parse command-line flags.
	testingMode := flag.Bool("testing", false, "Enable the dynamic injection endpoint for local testing")
	flag.Parse()

	// Retrieve the user-specific directory (e.g., ~/.config/discord_gateway)
	// which contains the .env configuration file and runtime logs.
	userDir, err := config.GetUserDir()
	if err != nil {
		slog.Error("Failed to retrieve user directory", "error", err)
		os.Exit(1)
	}

	// Initialize the rotating log file for terminal output persistence.
	// Auto-rotates at 10 MB, retaining one backup generation (.1).
	rotLog, err := logutil.NewRotatingFile(filepath.Join(userDir, "gateway.log"), 10*1024*1024)
	if err != nil {
		slog.Error("Failed to initialize rotating log file", "error", err)
		os.Exit(1)
	}
	defer rotLog.Close()

	// Setup structured logging (slog) with a multi-handler.
	// Terminal receives all logs (Debug+), while the file only stores Info+ to reduce noise.
	consoleHandler := logutil.NewPlainTextHandler(os.Stderr, slog.LevelDebug)
	fileHandler := logutil.NewPlainTextHandler(rotLog, slog.LevelInfo)

	logger := slog.New(logutil.NewMultiHandler(consoleHandler, fileHandler))
	slog.SetDefault(logger)

	// Initial configuration load blocks startup if invalid or missing.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Configuration error. Please verify your .env file", "dir", userDir, "error", err)
		os.Exit(1)
	}

	// Initialize the CSV logging file which records all successfully processed events.
	logPath := filepath.Join(userDir, "records.csv")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		slog.Error("Failed to open audit log", "path", logPath, "error", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// 6. Initialize the Nuke API Client for Shitlist & Contracts
	nukeClient := nuke.NewClient(cfg.NukeAPIToken)
	nukeCachePath := filepath.Join(userDir, "nuke_cache.json")
	nukeClient.LoadOrFetch(nukeCachePath)

	// 7. Establish a context for graceful shutdown handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the generic Discord WebSocket client component.
	client := discord.NewClient(cfg)

	// Initialize the domain-specific logic handler for Torn events.
	tornHandler := torn.NewHandler(ctx, cfg, logFile, userDir, nukeClient)

	// Immediately check quota on startup
	if !tornHandler.Quota().Allow() {
		slog.Info("Daily quota limit already reached on startup! Exiting gracefully.")
		os.Exit(0)
	}

	// Startup verification: ping userscript to ensure it is active.
	cbPort := tornHandler.CallbackPort()
	pingURL := fmt.Sprintf("http://127.0.0.1:%d/ping", cbPort)
	slog.Info("Verifying Userscript status...", "url", pingURL)

	cmd := exec.Command("xdg-open", pingURL)
	if err := cmd.Start(); err != nil {
		slog.Warn("Failed to auto-launch browser for ping verification", "error", err)
	} else {
		go cmd.Wait()
	}

	select {
	case <-tornHandler.PongChan():
		slog.Info("Userscript verification successful! System ready.")
	case <-time.After(30 * time.Second):
		slog.Error("CRITICAL ERROR: Userscript did not respond to /ping within 30 seconds. Ensure Tampermonkey is running and the script is enabled!")
		os.Exit(1)
	}

	// Bind the Torn handler to the Discord client's MESSAGE_CREATE event pipeline.
	client.RegisterMessageCreateHandler(tornHandler.OnMessageCreate)

	// If running in testing mode, spin up the local injection server on a dynamic port.
	if *testingMode {
		startTestingServer(ctx, userDir, tornHandler)
	}

	// Setup a hot-reloading file watcher for the .env configuration file.
	// This allows seamless runtime updates to tokens and channels without dropping the WSS connection.
	if err := config.StartWatcher(ctx, userDir, client, tornHandler); err != nil {
		slog.Warn("Hot reload subsystem failed to initialize", "error", err)
	}

	slog.Debug("Starting Discord Gateway WSS client")

	// Run is a blocking call that maintains the WebSocket connection until ctx is canceled.
	if err := client.Run(ctx); err != nil {
		slog.Error("Client terminated abnormally", "error", err)
		os.Exit(1)
	}

	slog.Info("Saving Nuke cache to disk...")
	if err := nukeClient.SaveToDisk(nukeCachePath); err != nil {
		slog.Error("Failed to save Nuke cache", "error", err)
	}

	slog.Info("Gateway shutdown completed successfully")
}
