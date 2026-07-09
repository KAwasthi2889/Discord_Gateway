package torn

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"discord_gateway/internal/config"
)

// StartCallbackServer boots a localized HTTP server on a dynamic OS-assigned port.
// It listens for GET requests from the userscript indicating success or failure.
// If onEmergencyShutdown is provided, it is invoked when out of energy is detected.
// It also provides a PongReceived channel to verify the userscript is active.
// Returns the port, the pong channel, the generated auth token, and any error.
func StartCallbackServer(getAppConfig func() *config.Config, quota *DailyQuota, cache *PayloadCache, logger *MessageLogger, onEmergencyShutdown func()) (int, chan struct{}, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, "", err
	}

	port := listener.Addr().(*net.TCPAddr).Port

	// Generate a secure random token
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return 0, nil, "", err
	}
	token := hex.EncodeToString(tokenBytes)

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
		if r.URL.Query().Get("token") != token {
			slog.Warn("Callback unauthorized", "ip", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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

		if status == "success" && reason != "" {
			cfg := getAppConfig()
			if cfg != nil && !cfg.BillableFailures {
				status = "fail"
			}
		}

		if status == "success" {
			if reason != "" {
				slog.Info("Revive failed but treated as success (billable)", "xid", xid, "reason", reason)
			} else {
				slog.Info("Revive successful", "xid", xid)
			}
			quota.RecordSuccess()
			go logger.Log(payload, contractNote)
		} else {
			if reason == "failed to revive" {
				slog.Info("Revive failed", "xid", xid, "reason", reason)
			} else if strings.HasPrefix(reason, "[CRITICAL]") {
				slog.Error("CRITICAL ERROR", "xid", xid, "reason", reason)
				if strings.Contains(reason, "CAPTCHA") && onEmergencyShutdown != nil {
					slog.Warn("Initiating emergency shutdown due to CAPTCHA")
					onEmergencyShutdown()
				}
			} else {
				slog.Info("Skipped auto-revive", "xid", xid, "reason", reason)
			}
			if strings.Contains(strings.ToLower(reason), "enough energy") {
				slog.Info("Out of energy detected", "xid", xid)
			}
		}

		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := http.Serve(listener, mux); err != nil && err != http.ErrServerClosed {
			slog.Error("Callback server crashed", "error", err)
		}
	}()

	slog.Info("Callback server started", "port", port, "token", token)
	return port, pongReceived, token, nil
}
