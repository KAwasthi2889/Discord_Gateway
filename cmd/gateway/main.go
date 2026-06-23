// Package main serves as the primary entry point for the Discord Gateway application.
// It is responsible for wiring up dependencies, initializing the configuration watcher,
// setting up the logging infrastructure, and starting the main event loop.
package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"

	"discord_gateway/internal/config"
	"discord_gateway/internal/discord"
	"discord_gateway/internal/logutil"
	"discord_gateway/internal/torn"
)

func main() {
	// Retrieve the user-specific directory (e.g., /opt/discord_gateway/$USER)
	// which contains the .env configuration file and runtime logs.
	userDir, err := config.GetUserDir()
	if err != nil {
		log.Fatalf("Failed to retrieve user directory: %v", err)
	}

	// Initialize the rotating log file for terminal output persistence.
	// Auto-rotates at 10 MB, retaining one backup generation (.1).
	rotLog, err := logutil.NewRotatingFile(filepath.Join(userDir, "gateway.log"), 10*1024*1024)
	if err != nil {
		log.Fatalf("Failed to initialize rotating log file: %v", err)
	}
	defer rotLog.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logutil.NewFilteredWriter(rotLog)))

	// Initial configuration load blocks startup if invalid or missing.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration error. Please verify your .env file at %s: %v", userDir, err)
	}

	// Initialize the CSV logging file which records all successfully processed events.
	logPath := filepath.Join(userDir, "records.csv")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatalf("Failed to open audit log %s: %v", logPath, err)
	}
	defer logFile.Close()

	// Establish a context that listens for standard OS termination signals
	// to ensure graceful shutdown of active connections and resources.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the generic Discord WebSocket client component.
	client := discord.NewClient(cfg)
	
	// Initialize the domain-specific logic handler for Torn events.
	tornHandler := torn.NewHandler(cfg, logFile, userDir)

	// Bind the Torn handler to the Discord client's MESSAGE_CREATE event pipeline.
	client.RegisterMessageCreateHandler(tornHandler.OnMessageCreate)

	// Setup a hot-reloading file watcher for the .env configuration file.
	// This allows seamless runtime updates to tokens and channels without dropping the WSS connection.
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		defer watcher.Close()
		
		go func() {
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					// Watch for Write, Create, or Rename operations (accommodates various text editor save patterns).
					if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Rename == fsnotify.Rename {
						newCfg, err := config.Load()
						if err == nil {
							client.UpdateConfig(newCfg)
							tornHandler.UpdateConfig(newCfg)
							log.Println("Configuration successfully hot-reloaded from disk.")
						} else {
							log.Printf("Failed to parse hot-reloaded configuration: %v", err)
						}
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					log.Printf("Configuration watcher encountered an error: %v", err)
				case <-ctx.Done():
					return
				}
			}
		}()
		
		envPath := filepath.Join(userDir, ".env")
		_ = watcher.Add(envPath)
		log.Printf("Monitoring configuration file %s for hot-reloads...", envPath)
	} else {
		log.Printf("Warning: Hot reload subsystem failed to initialize: %v", err)
	}

	log.Println("Starting Discord Gateway WSS client...")
	
	// Run is a blocking call that maintains the WebSocket connection until ctx is canceled.
	if err := client.Run(ctx); err != nil {
		log.Fatalf("Client terminated abnormally: %v", err)
	}

	log.Println("Graceful shutdown completed successfully.")
}
