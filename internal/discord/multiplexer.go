package discord

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

type replica struct {
	conn net.Conn
	ch   chan []byte
}

// Multiplexer manages the Primary/Replica TCP communication allowing multiple
// application instances to share a single Discord Gateway WebSocket connection.
type Multiplexer struct {
	replicas   map[*replica]struct{}
	replicasMu sync.Mutex
}

// NewMultiplexer creates a new Multiplexer instance.
func NewMultiplexer() *Multiplexer {
	return &Multiplexer{
		replicas: make(map[*replica]struct{}),
	}
}

// ServeReplicas listens on the provided net.Listener and registers incoming
// connections as replicas. It blocks until the context is canceled or the
// listener is closed.
func (m *Multiplexer) ServeReplicas(ctx context.Context, listener net.Listener) {
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return // Listener closed or accept failed
		}

		rep := &replica{
			conn: conn,
			ch:   make(chan []byte, 1000), // Buffered to handle rapid bursts without blocking the primary
		}

		m.replicasMu.Lock()
		m.replicas[rep] = struct{}{}
		m.replicasMu.Unlock()

		slog.Debug("Multiplex: New replica connected", "addr", conn.RemoteAddr().String())

		// Writer goroutine: fully decoupled from the primary's read pump
		go func(r *replica) {
			defer r.conn.Close()
			for payload := range r.ch {
				var sizeBuf [4]byte
				binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(payload)))

				r.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if _, err := r.conn.Write(sizeBuf[:]); err != nil {
					return
				}
				if _, err := r.conn.Write(payload); err != nil {
					return
				}
			}
		}(rep)

		// Reader goroutine: detects disconnects and cleans up
		go func(r *replica) {
			// Just wait for disconnect or EOF from replica
			io.Copy(io.Discard, r.conn)

			m.replicasMu.Lock()
			delete(m.replicas, r)
			m.replicasMu.Unlock()

			slog.Debug("Multiplex: Replica disconnected", "addr", r.conn.RemoteAddr().String())
			r.conn.Close()
		}(rep)
	}
}

// CloseAll terminates all active replica connections.
func (m *Multiplexer) CloseAll() {
	m.replicasMu.Lock()
	defer m.replicasMu.Unlock()
	for rep := range m.replicas {
		rep.conn.Close()
	}
	// Reset the map
	m.replicas = make(map[*replica]struct{})
}

// Broadcast sends the provided payload to all connected replicas asynchronously.
// It achieves this by pushing to a buffered channel. If a replica's buffer fills up,
// its connection is forcefully closed to protect the primary's memory and CPU.
func (m *Multiplexer) Broadcast(data []byte) {
	m.replicasMu.Lock()
	defer m.replicasMu.Unlock()

	for rep := range m.replicas {
		select {
		case rep.ch <- data:
			// Success
		default:
			// Buffer full: Replica is hung or drastically lagging behind.
			// Drop it aggressively to protect the Primary.
			rep.conn.Close()
		}
	}
}

// RunAsReplica connects to a Primary instance and forwards incoming payloads
// to the provided handler chain. It blocks until the connection drops or the
// context is canceled.
func RunAsReplica(ctx context.Context, conn net.Conn, handlers []MessageCreateHandler) error {
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		var sizeBuf [4]byte
		if _, err := io.ReadFull(conn, sizeBuf[:]); err != nil {
			if ctx.Err() != nil {
				return nil // Graceful shutdown, connection closed by context
			}
			return err
		}
		size := binary.BigEndian.Uint32(sizeBuf[:])
		if size > 10*1024*1024 { // 10MB limit
			return fmt.Errorf("payload too large from primary")
		}

		data := make([]byte, size)
		if _, err := io.ReadFull(conn, data); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		for _, handler := range handlers {
			handler(data)
		}
	}
}
