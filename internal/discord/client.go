// Package discord provides a high-performance, robust WebSocket client
// for interacting with the Discord Gateway API.
// It manages connection lifecycles, automated heartbeats, intelligent reconnects,
// and session resumption, abstracting away the complexities of the Gateway protocol.
package discord

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"

	"discord_gateway/internal/config"
)

// MessageCreateHandler is a function signature for callbacks triggered upon receiving
// a valid MESSAGE_CREATE event from the Discord Gateway.
type MessageCreateHandler func(data []byte)

// Client encapsulates the state and logic required to maintain a persistent
// WebSocket connection to the Discord Gateway. It is designed to be highly concurrent,
// leveraging atomic operations and mutexes to ensure thread safety across
// dynamic configuration updates and connection state changes.
type Client struct {
	// cfg holds a pointer to the current *config.Config.
	// It is accessed via atomic.Value to allow lock-free, zero-downtime hot reloading.
	cfg atomic.Value

	conn *websocket.Conn
	mu   sync.Mutex // Protects concurrent writes to the underlying websocket connection.

	sessionID        string
	resumeGatewayURL string

	// lastSequence tracks the highest sequence number received from Discord.
	// Used for session resumption if the connection drops.
	lastSequence atomic.Int64

	// ackReceived is an atomic flag indicating whether the last heartbeat was acknowledged.
	// true = ACK received, false = Pending/Unacknowledged. Used to detect zombie TCP connections.
	ackReceived atomic.Bool

	// connectedOnce is set to true after a successful Gateway handshake (READY/RESUMED).
	// Reset to false at the start of each connection attempt. Used to distinguish
	// "never connected" failures from "was connected, then dropped" failures.
	connectedOnce atomic.Bool

	// onMessageCreate maintains a registry of callback functions executed synchronously
	// on the read pump when a MESSAGE_CREATE event arrives.
	onMessageCreate []MessageCreateHandler

	// multiplexer handles distributing events to secondary replicas if running as Primary.
	multiplexer *Multiplexer
}

// NewClient initializes and returns a new Discord Gateway Client.
// It safely stores the initial configuration. Calling Run() is required to establish the connection.
func NewClient(cfg *config.Config) *Client {
	c := &Client{
		multiplexer: NewMultiplexer(),
	}
	c.cfg.Store(cfg)
	return c
}

// RegisterMessageCreateHandler appends a new callback to the execution chain
// for MESSAGE_CREATE events. Note that handlers block the primary read pump,
// so they must execute quickly (e.g., zero-allocation parsing) or offload heavy work.
func (c *Client) RegisterMessageCreateHandler(handler MessageCreateHandler) {
	c.onMessageCreate = append(c.onMessageCreate, handler)
}

// Config provides lock-free, thread-safe access to the current active configuration.
func (c *Client) Config() *config.Config {
	return c.cfg.Load().(*config.Config)
}

// UpdateConfig safely hot-swaps the active configuration at runtime.
// Subsequent operations will utilize the new settings without dropping the connection.
func (c *Client) UpdateConfig(cfg *config.Config) {
	c.cfg.Store(cfg)
}

// maxConsecutiveRetries is the maximum number of consecutive connection failures
// allowed before the client terminates. This prevents infinite retry loops when
// the network is completely unavailable.
const maxConsecutiveRetries = 10

// Run initiates the connection to the Discord Gateway and enters a blocking reconnection loop.
// It automatically handles session resumption and gracefully shuts down when the provided context is canceled.
// If the connection fails maxConsecutiveRetries times in a row, it exits with a fatal error.
func (c *Client) Run(ctx context.Context) error {
	consecutiveFailures := 0

	for {
		if ctx.Err() != nil {
			return nil
		}

		// 1. Attempt to act as Replica
		replicaConn, err := net.Dial("tcp", "127.0.0.1:44188")
		if err == nil {
			slog.Debug("Multiplex: Connected to existing primary. Acting as Replica.")
			err = RunAsReplica(ctx, replicaConn, c.onMessageCreate)
			if err != nil {
				slog.Warn("Multiplex Replica disconnected with error", "error", err)
			} else {
				slog.Debug("Multiplex Replica disconnected gracefully")
			}
			time.Sleep(1 * time.Second) // Prevent tight loops
			continue
		}

		// 2. Attempt to act as Primary
		listener, err := net.Listen("tcp", "127.0.0.1:44188")
		if err != nil {
			slog.Warn("Multiplex: Failed to bind or connect. Retrying", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		slog.Debug("Multiplex: Bound to 127.0.0.1:44188. Acting as Primary.")
		go c.multiplexer.ServeReplicas(ctx, listener)

		c.connectedOnce.Store(false)
		err = c.connectAndListen(ctx)

		// Tear down primary state
		listener.Close()
		c.multiplexer.CloseAll()

		if err == nil || ctx.Err() != nil {
			return nil
		}

		// Only count failures where we never established a connection.
		// If we were connected and then dropped, reset the counter.
		if c.connectedOnce.Load() {
			consecutiveFailures = 0
		} else {
			consecutiveFailures++
		}

		if err == io.EOF {
			slog.Debug("Graceful Gateway disconnection")
		} else if err != nil && strings.Contains(err.Error(), "server requested reconnect") {
			slog.Info("Gateway disconnected (server requested reconnect)")
		} else {
			slog.Warn("Gateway disconnected", "error", err)
		}

		if consecutiveFailures >= maxConsecutiveRetries {
			return fmt.Errorf("exceeded %d consecutive connection failures, last error: %v", maxConsecutiveRetries, err)
		}

		slog.Info("Reconnecting...", "seconds", 5, "attempt", consecutiveFailures, "max", maxConsecutiveRetries)

		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *Client) connectAndListen(ctx context.Context) error {
	url := c.resumeGatewayURL
	if url == "" {
		gw, err := c.fetchGatewayURL()
		if err != nil {
			return fmt.Errorf("fetch gateway url: %w", err)
		}
		url = gw
	}

	dialURL := fmt.Sprintf("%s?v=%s&encoding=json", url, APIVersion)
	slog.Debug("Connecting to Gateway", "url", dialURL)

	conn, _, err := websocket.DefaultDialer.Dial(dialURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	// Initialize the ACK flag for the new connection so the first heartbeat isn't falsely marked as zombie
	c.ackReceived.Store(true)

	hbCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go c.readPump(hbCtx, done)

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Client shutting down"))
		}
		c.mu.Unlock()
		time.Sleep(1 * time.Second) // Allow TCP a moment to flush the close message
		return nil
	}
}

func (c *Client) writeJSON(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("connection closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

func (c *Client) fetchGatewayURL() (string, error) {
	resp, err := http.Get("https://discord.com/api/v" + APIVersion + "/gateway")
	if err != nil {
		return "", fmt.Errorf("UNEXPECTED ERROR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024))
		return "", fmt.Errorf("UNEXPECTED ERROR: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024)).Decode(&result); err != nil {
		return "", fmt.Errorf("UNEXPECTED ERROR: decode json: %w", err)
	}
	return result.URL, nil
}

func toJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
