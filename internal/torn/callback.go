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

		if status == "success" {
			slog.Info("Revive successful", "xid", xid)
			quota.RecordSuccess()
			go logger.Log(payload, contractNote)
		} else {
			reasonLower := strings.ToLower(reason)

			if strings.Contains(reasonLower, "cloudflare") || strings.Contains(reasonLower, "captcha") || strings.HasPrefix(reason, "[CRITICAL]") {
				slog.Error("CRITICAL ERROR: Cloudflare/CAPTCHA detected", "xid", xid, "reason", reason)
				if onEmergencyShutdown != nil {
					slog.Warn("Initiating emergency shutdown due to Cloudflare/CAPTCHA")
					onEmergencyShutdown()
				}
			} else if strings.Contains(reasonLower, "enough energy") {
				slog.Info("Out of energy detected. Initiating emergency gateway shutdown.", "xid", xid)
				if onEmergencyShutdown != nil {
					onEmergencyShutdown()
				}
			} else if strings.Contains(reason, "UNEXPECTED ERROR:") {
				slog.Error("Unstandardized error", "xid", xid, "reason", reason)
			} else {
				if reason == "failed to revive" {
					slog.Warn("Revive failed", "xid", xid, "reason", reason)
				} else {
					slog.Warn("Skipped auto-revive", "xid", xid, "reason", reason)
				}
			}
		}

		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := http.Serve(listener, mux); err != nil && err != http.ErrServerClosed {
			slog.Error("Callback server crashed", "error", err)
		}
	}()

	slog.Info("Callback server started", "port", port)
	slog.Debug("Callback token generated", "token", token)
	return port, pongReceived, token, nil
}
