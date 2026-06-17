// Package logutil provides lightweight, zero-dependency utilities for log management.
package logutil

import (
	"os"
	"sync"
)

// RotatingFile is a size-based auto-rotating file writer.
// When the current file exceeds MaxBytes, it is renamed to path.1 and a fresh
// file is opened. Only one backup generation is retained. This keeps disk usage
// bounded without requiring external dependencies (e.g., lumberjack).
//
// RotatingFile implements io.WriteCloser and is safe for concurrent use.
type RotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

// NewRotatingFile opens (or creates) the file at path in append mode and
// configures automatic rotation once the file exceeds maxBytes.
func NewRotatingFile(path string, maxBytes int64) (*RotatingFile, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}

	// Seed the current size from the existing file so we rotate correctly
	// even when resuming writes to a non-empty file.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	return &RotatingFile{
		path:     path,
		maxBytes: maxBytes,
		file:     f,
		size:     info.Size(),
	}, nil
}

// Write appends p to the current file and rotates if the size threshold is exceeded.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, err := r.file.Write(p)
	r.size += int64(n)

	if err == nil && r.size >= r.maxBytes {
		r.rotate()
	}
	return n, err
}

// Close closes the underlying file descriptor.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}

// rotate performs the actual file rotation: current → path.1, then opens a fresh file.
func (r *RotatingFile) rotate() {
	r.file.Close()

	backup := r.path + ".1"
	// Remove the old backup (if any), then rename current → backup.
	os.Remove(backup)
	os.Rename(r.path, backup)

	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		// Best-effort: if we can't reopen, keep the struct in a broken state
		// rather than panicking inside the write path.
		return
	}
	r.file = f
	r.size = 0
}
