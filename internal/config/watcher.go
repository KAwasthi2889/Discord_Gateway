package config

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ConfigReloader provides an interface for components that need to react
// to configuration changes at runtime.
type ConfigReloader interface {
	UpdateConfig(cfg *Config)
}

// StartWatcher initializes a filesystem watcher on the configuration directory
// and triggers hot-reloads when the .env file is modified.
func StartWatcher(ctx context.Context, userDir string, reloaders ...ConfigReloader) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to initialize fsnotify watcher: %w", err)
	}

	var reloadTimer *time.Timer

	// Start the background monitoring routine.
	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) != ".env" {
					continue
				}
				// Watch for Write, Create, or Rename operations
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Rename == fsnotify.Rename {
					// Debounce rapid save events
					if reloadTimer != nil {
						reloadTimer.Stop()
					}
					reloadTimer = time.AfterFunc(100*time.Millisecond, func() {
						handleReload(reloaders...)
					})
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Configuration watcher encountered an error", "error", err)
			case <-ctx.Done():
				if reloadTimer != nil {
					reloadTimer.Stop()
				}
				return
			}
		}
	}()

	if err := watcher.Add(userDir); err != nil {
		slog.Warn("Failed to add configuration directory to watcher, hot reload may not work", "error", err)
	} else {
		slog.Debug("Monitoring configuration directory for hot-reloads", "dir", userDir)
	}

	return nil
}

// handleReload parses the new config and notifies all registered reloaders.
func handleReload(reloaders ...ConfigReloader) {
	// Attempt to load the new config.
	newCfg, err := Load()
	if err != nil {
		slog.Error("Failed to parse hot-reloaded configuration", "error", err)
		return
	}

	// For logging purposes, we could keep track of old config via a package global
	// or we can just let components handle their own diffing.
	// Since the components (like the Torn handler and Discord client) just swap pointers,
	// we will push the new config to them.
	for _, reloader := range reloaders {
		reloader.UpdateConfig(newCfg)
	}

	slog.Info("Configuration successfully hot-reloaded from disk")
}
