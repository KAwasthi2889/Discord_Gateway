package torn

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
)

// StartCallbackServer boots a localized HTTP server on a dynamic OS-assigned port.
// It listens for GET requests from the userscript indicating success or failure.
// If onEmergencyShutdown is provided, it is invoked when out of energy is detected.
// It also provides a PongReceived channel to verify the userscript is active.
func StartCallbackServer(quota *DailyQuota, cache *PayloadCache, logger *MessageLogger, onEmergencyShutdown func()) (int, chan struct{}, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, err
	}

	port := listener.Addr().(*net.TCPAddr).Port

	pongReceived := make(chan struct{}, 1)

	mux := http.NewServeMux()

	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h1>Gateway Initializing...</h1><script>setTimeout(() => { document.body.innerHTML += "<p style='color:red'>Error: Gateway Userscript is not enabled or Tampermonkey is off!</p>"; }, 2000);</script></body></html>`))
	})

	mux.HandleFunc("/pong", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		select {
		case pongReceived <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/revive", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		xid := r.URL.Query().Get("xid")
		status := r.URL.Query().Get("status")
		reason := r.URL.Query().Get("reason")

		if reason != "" {
			slog.Debug("Callback received", "xid", xid, "status", status, "reason", reason)
		} else {
			slog.Debug("Callback received", "xid", xid, "status", status)
		}

		payload, contractNote, found := cache.Pop(xid)
		if !found {
			// Payload expired or already processed
			w.WriteHeader(http.StatusOK)
			return
		}

		if status == "success" {
			if strings.Contains(strings.ToLower(reason), "failed to revive") {
				slog.Info("Failed to revive", "xid", xid)
			} else {
				slog.Info("Revive successful", "xid", xid)
			}
			quota.RecordSuccess()
			go logger.Log(payload, contractNote)
		} else {
			slog.Info("Skipped auto-revive", "xid", xid, "reason", reason)
			if strings.Contains(strings.ToLower(reason), "not enough energy") {
				slog.Error("CRITICAL: Out of energy detected! Initiating emergency gateway shutdown.")
				if onEmergencyShutdown != nil {
					onEmergencyShutdown()
				} else {
					p, _ := os.FindProcess(os.Getpid())
					_ = p.Signal(os.Interrupt)
				}
			}
		}

		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := http.Serve(listener, mux); err != nil {
			slog.Error("Callback server fatal error", "error", err)
		}
	}()

	slog.Debug("Callback server listening", "host", "localhost", "port", port)
	return port, pongReceived, nil
}
