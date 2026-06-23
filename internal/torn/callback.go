package torn

import (
	"log"
	"net"
	"net/http"
)

// StartCallbackServer launches a lightweight HTTP server on a random OS-assigned
// port, bound exclusively to localhost. It exposes a single endpoint that the
// browser-side userscript calls to report the result of a revive attempt.
//
// The server runs in a background goroutine and returns the assigned port number
// so it can be embedded in the profile URL hash for the userscript to discover.
//
// This design eliminates port conflicts entirely — the OS guarantees a free port.
func StartCallbackServer(quota *DailyQuota, cache *PayloadCache, logger *MessageLogger) (int, error) {
	listener, err := net.Listen("tcp", "localhost:0")
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

		payload, found := cache.Pop(xid)
		if !found {
			// Payload expired or already processed
			w.WriteHeader(http.StatusOK)
			return
		}

		if status == "success" {
			quota.RecordSuccess()
			go logger.Log(payload)
		} else {
			log.Printf("Revive failed for XID=%s: %s", xid, reason)
		}

		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := http.Serve(listener, mux); err != nil {
			log.Printf("callback_server: fatal error: %v", err)
		}
	}()

	log.Printf("callback_server: listening on localhost:%d", port)
	return port, nil
}
