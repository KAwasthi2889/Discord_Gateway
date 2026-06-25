package logutil

import (
	"context"
	"log/slog"
)

// MultiHandler implements slog.Handler and broadcasts records to multiple underlying handlers.
type MultiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler creates a new MultiHandler that delegates to the provided handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{
		handlers: handlers,
	}
}

// Enabled returns true if any of the underlying handlers are enabled for the given level.
func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle dispatches the record to all underlying handlers that are enabled for the record's level.
func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errs[0] // Just return the first error for simplicity
	}
	return nil
}

// WithAttrs returns a new MultiHandler with the attributes appended to all underlying handlers.
func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var newHandlers []slog.Handler
	for _, h := range m.handlers {
		newHandlers = append(newHandlers, h.WithAttrs(attrs))
	}
	return NewMultiHandler(newHandlers...)
}

// WithGroup returns a new MultiHandler with the group appended to all underlying handlers.
func (m *MultiHandler) WithGroup(name string) slog.Handler {
	var newHandlers []slog.Handler
	for _, h := range m.handlers {
		newHandlers = append(newHandlers, h.WithGroup(name))
	}
	return NewMultiHandler(newHandlers...)
}
