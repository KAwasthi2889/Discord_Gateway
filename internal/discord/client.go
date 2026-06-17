// Package discord provides a high-performance, robust WebSocket client
// for interacting with the Discord Gateway API.
// It manages connection lifecycles, automated heartbeats, intelligent reconnects,
// and session resumption, abstracting away the complexities of the Gateway protocol.
package discord

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
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
	cfg     atomic.Value 

	conn *websocket.Conn
	mu   sync.Mutex // Protects concurrent writes to the underlying websocket connection.

	sessionID        string
	resumeGatewayURL string
	
	// lastSequence tracks the highest sequence number received from Discord.
	// Used for session resumption if the connection drops.
	lastSequence     int32
	
	// ackReceived is an atomic flag indicating whether the last heartbeat was acknowledged.
	// 1 = ACK received, 0 = Pending/Unacknowledged. Used to detect zombie TCP connections.
	ackReceived      int32 

	// connectedOnce is set to 1 after a successful Gateway handshake (READY/RESUMED).
	// Reset to 0 at the start of each connection attempt. Used to distinguish
	// "never connected" failures from "was connected, then dropped" failures.
	connectedOnce    int32

	// onMessageCreate maintains a registry of callback functions executed synchronously
	// on the read pump when a MESSAGE_CREATE event arrives.
	onMessageCreate []MessageCreateHandler 
}

// NewClient initializes and returns a new Discord Gateway Client.
// It safely stores the initial configuration. Calling Run() is required to establish the connection.
func NewClient(cfg *config.Config) *Client {
	c := &Client{}
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
		atomic.StoreInt32(&c.connectedOnce, 0)
		err := c.connectAndListen(ctx)
		if err == nil || ctx.Err() != nil {
			return nil
		}

		// Only count failures where we never established a connection.
		// If we were connected and then dropped, reset the counter.
		if atomic.LoadInt32(&c.connectedOnce) == 1 {
			consecutiveFailures = 0
		} else {
			consecutiveFailures++
		}
		log.Printf("Gateway disconnected: %v", err)

		if consecutiveFailures >= maxConsecutiveRetries {
			return fmt.Errorf("exceeded %d consecutive connection failures, last error: %v", maxConsecutiveRetries, err)
		}

		log.Printf("Reconnecting in 5 seconds... (attempt %d/%d)", consecutiveFailures, maxConsecutiveRetries)

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
	log.Printf("Connecting to %s", dialURL)

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
	atomic.StoreInt32(&c.ackReceived, 1)

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

func (c *Client) readPump(ctx context.Context, done chan<- error) {
	for {
		_, rawMsg, err := c.conn.ReadMessage()
		if err != nil {
			done <- fmt.Errorf("read error: %w", err)
			return
		}

		var payload Payload
		if err := json.Unmarshal(rawMsg, &payload); err != nil {
			done <- fmt.Errorf("unmarshal payload: %w", err)
			return
		}

		if payload.Sequence != nil {
			atomic.StoreInt32(&c.lastSequence, *payload.Sequence)
		}

		if err := c.handlePayload(ctx, payload); err != nil {
			done <- err
			return
		}
	}
}

func (c *Client) handlePayload(ctx context.Context, p Payload) error {
	switch p.Op {
	case 10: // Hello
		var hello Hello
		if err := json.Unmarshal(p.Data, &hello); err != nil {
			return fmt.Errorf("parse hello: %w", err)
		}

		log.Printf("Received Hello. Heartbeat interval: %dms", hello.HeartbeatInterval)
		go c.heartbeat(ctx, hello.HeartbeatInterval)

		if c.sessionID != "" {
			return c.resume()
		}
		return c.identify()

	case 0: // Dispatch
		if p.Event == nil {
			return nil
		}
		switch *p.Event {
		case "READY":
			var ready Ready
			if err := json.Unmarshal(p.Data, &ready); err == nil {
				c.sessionID = ready.SessionID
				c.resumeGatewayURL = ready.ResumeGatewayURL
				atomic.StoreInt32(&c.connectedOnce, 1)
				log.Println("Gateway connection is READY")
			}
		case "RESUMED":
			atomic.StoreInt32(&c.connectedOnce, 1)
			log.Println("Gateway connection successfully RESUMED")
		case "MESSAGE_CREATE":
			// Dispatch to all registered handlers
			for _, handler := range c.onMessageCreate {
				handler(p.Data)
			}
		}

	case 7: // Reconnect
		return fmt.Errorf("server requested reconnect")

	case 9: // Invalid Session
		log.Println("Invalid Session received. Clearing cache to force full re-identify.")
		c.sessionID = ""
		c.resumeGatewayURL = ""
		return fmt.Errorf("invalid session")

	case 11: // Heartbeat ACK
		atomic.StoreInt32(&c.ackReceived, 1)
	}

	return nil
}

func (c *Client) identify() error {
	id := Identify{
		Token:   c.Config().Token,
		Intents: Intents,
	}
	id.Properties.OS = "linux"
	id.Properties.Browser = "GoClient"
	id.Properties.Device = "GoClient"

	log.Println("Identify sent")
	return c.writeJSON(Payload{Op: 2, Data: toJSON(id)})
}

func (c *Client) resume() error {
	res := Resume{
		Token:     c.Config().Token,
		SessionID: c.sessionID,
		Seq:       atomic.LoadInt32(&c.lastSequence),
	}

	log.Println("Resume sent")
	return c.writeJSON(Payload{Op: 6, Data: toJSON(res)})
}

func (c *Client) heartbeat(ctx context.Context, interval int) {
	jitter := time.Duration(float64(interval)*rand.Float64()) * time.Millisecond
	select {
	case <-time.After(jitter):
	case <-ctx.Done():
		return
	}

	c.sendHeartbeat()

	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if atomic.LoadInt32(&c.ackReceived) == 0 {
				log.Println("Zombie connection detected, forcing reconnect")
				c.mu.Lock()
				if c.conn != nil {
					c.conn.Close() // Force read loop to error out and trigger reconnect
				}
				c.mu.Unlock()
				return
			}
			c.sendHeartbeat()
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) sendHeartbeat() {
	atomic.StoreInt32(&c.ackReceived, 0)
	seq := atomic.LoadInt32(&c.lastSequence)
	var seqVal interface{}
	if seq > 0 {
		seqVal = seq
	}

	if err := c.writeJSON(Payload{Op: 1, Data: toJSON(seqVal)}); err != nil {
		log.Printf("Failed to send heartbeat: %v", err)
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
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

func (c *Client) fetchGatewayURL() (string, error) {
	resp, err := http.Get("https://discord.com/api/v" + APIVersion + "/gateway")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.URL, nil
}

func toJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
