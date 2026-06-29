package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"discord_gateway/internal/torn"
)

// startTestingServer spins up a local HTTP server on a dynamic port for injecting
// mocked Discord Gateway payloads during local testing. It writes the assigned port
// to a dotfile so the inspector/injector CLI tools can discover it.
func startTestingServer(ctx context.Context, userDir string, tornHandler *torn.Handler) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		slog.Error("Failed to bind injection server", "error", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Write the dynamically assigned port to a dotfile so the injector CLI can discover it.
	portFile := filepath.Join(userDir, ".inject_port")
	if err := os.WriteFile(portFile, []byte(fmt.Sprintf("%d", port)), 0600); err != nil {
		slog.Error("Failed to write .inject_port file", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/inject", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		slog.Debug("Received injected payload for testing", "size", len(body))

		// Inject directly into the pipeline!
		tornHandler.OnMessageCreate(body)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Injected successfully\n"))
	})

	server := &http.Server{Handler: mux}

	go func() {
		slog.Debug("Testing injection server is running", "port", port, "endpoint", "/inject")
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("Injection server fatal error", "error", err)
		}
	}()

	// Ensure the server closes nicely when context cancels
	go func() {
		<-ctx.Done()
		server.Close()
		os.Remove(portFile)
	}()
}
