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
func StartCallbackServer(quota *DailyQuota, cache *PayloadCache, logger *MessageLogger, onEmergencyShutdown func()) (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	port := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/revive", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		xid := r.URL.Query().Get("xid")
		status := r.URL.Query().Get("status")
		reason := r.URL.Query().Get("reason")

		payload, contractNote, found := cache.Pop(xid)
		if !found {
			// Payload expired or already processed
			w.WriteHeader(http.StatusOK)
			return
		}

		if status == "success" {
			slog.Info("Revive successful", "xid", xid)
			quota.RecordSuccess()
			// Launch a quick goroutine to handle the heavy json.Unmarshal and CSV I/O
			// without blocking the HTTP response.
			go logger.Log(payload, contractNote)
		} else {
			reasonLower := strings.ToLower(reason)
			if strings.Contains(reasonLower, "not in hospital") {
				reason = "[FastRevive] User not in hospital anymore"
			} else if strings.Contains(reasonLower, "revive button not found") {
				reason = "[FastRevive] Revive button not found (Timeout)"
			} else if strings.Contains(reasonLower, "disabled") {
				reason = "[FastRevive] Revive button is disabled"
			} else {
				slog.Warn("Revive failed or skipped", "xid", xid, "reason", reason)
				if strings.Contains(strings.ToLower(reason), "enough energy") {
					slog.Error("CRITICAL: Out of energy detected! Initiating emergency gateway shutdown.")
					if onEmergencyShutdown != nil {
						onEmergencyShutdown()
					} else {
						p, _ := os.FindProcess(os.Getpid())
						_ = p.Signal(os.Interrupt)
					}
				}
			}

			// We don't increment the quota, but we can log the failure for the rejection detail logger
			go func(p []byte, cn string) {
				slog.Info("Request rejected by browser script", "reason", reason)
				if rec := ExtractRecord(p); rec != nil {
					rec.ContractNote = cn
					slog.Debug("Rejected payload details", "record:", *rec)
				}
			}(payload, contractNote)
		}

		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := http.Serve(listener, mux); err != nil {
			slog.Error("Callback server fatal error", "error", err)
		}
	}()

	slog.Debug("Callback server listening", "host", "localhost", "port", port)
	return port, nil
}
