package discord

import (
	"context"
	"log/slog"
	"math/rand"
	"time"
)

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
			if !c.ackReceived.Load() {
				slog.Warn("Zombie connection detected, forcing reconnect")
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
	c.ackReceived.Store(false)
	seq := c.lastSequence.Load()
	var seqVal interface{}
	if seq > 0 {
		seqVal = seq
	}

	if err := c.writeJSON(Payload{Op: 1, Data: toJSON(seqVal)}); err != nil {
		slog.Error("Failed to send heartbeat", "error", err)
	}
}
