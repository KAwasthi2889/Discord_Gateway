// Package logutil provides lightweight, zero-dependency utilities for log management.
package logutil

import (
	"bytes"
	"io"
	"strings"
)

// suppressedPrefixes contains the set of log message prefixes that should be
// excluded from the persistent log file. These are operational noise from the
// connection lifecycle and provide no audit value.
var suppressedPrefixes = []string{
	"Monitoring configuration file",
	"Starting Discord Gateway WSS client...",
	"Connecting to wss://",
	"Received Hello. Heartbeat interval:",
	"Identify sent",
	"Graceful shutdown completed successfully.",
	"Reconnecting in 5 seconds...",
	"Gateway disconnected:",
	"Resume sent",
	"Invalid Session received.",
	"Zombie connection detected",
}

// FilteredWriter wraps an io.Writer and silently drops log lines that match
// any of the suppressed prefixes. This allows the terminal (stderr) to receive
// all messages while the persistent log file only stores meaningful events.
type FilteredWriter struct {
	inner io.Writer
}

// NewFilteredWriter creates a new FilteredWriter that wraps the provided writer.
func NewFilteredWriter(w io.Writer) *FilteredWriter {
	return &FilteredWriter{inner: w}
}

// Write checks each log line against the suppressed prefixes. If a match is found,
// the write is silently dropped (returning len(p), nil to satisfy the io.Writer contract).
// The log stdlib prepends a timestamp like "2026/06/16 11:49:00 ", so we strip that
// before checking prefixes.
func (fw *FilteredWriter) Write(p []byte) (int, error) {
	// The standard logger format is: "YYYY/MM/DD HH:MM:SS message\n"
	// We need to extract the message portion after the timestamp.
	line := bytes.TrimRight(p, "\n")

	// Find the message start after the timestamp prefix.
	// Standard log format has the timestamp followed by a space.
	msg := string(line)
	if spaceIdx := findMessageStart(msg); spaceIdx >= 0 {
		msg = msg[spaceIdx:]
	}

	for _, prefix := range suppressedPrefixes {
		if strings.HasPrefix(msg, prefix) {
			return len(p), nil // Silently drop
		}
	}

	return fw.inner.Write(p)
}

// findMessageStart locates the start of the actual log message content,
// skipping the "YYYY/MM/DD HH:MM:SS " timestamp prefix that the standard
// library logger prepends.
func findMessageStart(s string) int {
	// Standard log format: "2006/01/02 15:04:05 " (20 chars)
	// With microseconds: "2006/01/02 15:04:05.000000 " (27 chars)
	// We find the space after the time portion.
	if len(s) < 20 {
		return -1
	}

	// Skip past "YYYY/MM/DD HH:MM:SS" and any fractional seconds
	idx := 19 // position after "YYYY/MM/DD HH:MM:SS"
	for idx < len(s) && (s[idx] == '.' || (s[idx] >= '0' && s[idx] <= '9')) {
		idx++
	}
	if idx < len(s) && s[idx] == ' ' {
		return idx + 1
	}
	return -1
}
