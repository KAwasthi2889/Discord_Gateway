package discord

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/goccy/go-json"
)

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
			c.lastSequence.Store(*payload.Sequence)
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

		slog.Debug("Received Hello", "heartbeat_interval", hello.HeartbeatInterval)
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
				c.connectedOnce.Store(true)
				slog.Debug("Gateway connection is READY")
			}
		case "RESUMED":
			c.connectedOnce.Store(true)
			slog.Info("Gateway connection successfully RESUMED")
		case "MESSAGE_CREATE":
			// Broadcast to any local replicas
			c.multiplexer.Broadcast(p.Data)

			// Dispatch to all registered handlers
			for _, handler := range c.onMessageCreate {
				handler(p.Data)
			}
		}

	case 7: // Reconnect
		return fmt.Errorf("server requested reconnect")

	case 9: // Invalid Session
		slog.Warn("Invalid Session received. Clearing cache to force full re-identify.")
		c.sessionID = ""
		c.resumeGatewayURL = ""
		return fmt.Errorf("invalid session")

	case 11: // Heartbeat ACK
		c.ackReceived.Store(true)
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

	slog.Debug("Identify sent")
	return c.writeJSON(Payload{Op: 2, Data: toJSON(id)})
}

func (c *Client) resume() error {
	res := Resume{
		Token:     c.Config().Token,
		SessionID: c.sessionID,
		Seq:       c.lastSequence.Load(),
	}

	slog.Debug("Resume sent")
	return c.writeJSON(Payload{Op: 6, Data: toJSON(res)})
}
