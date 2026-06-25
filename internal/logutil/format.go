package logutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// PlainTextHandler is a custom slog handler that completely bypasses structured logging formats
// (like logfmt or JSON) and renders simple, human-readable sentences exactly as requested.
type PlainTextHandler struct {
	w     io.Writer
	level slog.Leveler
}

func NewPlainTextHandler(w io.Writer, level slog.Leveler) *PlainTextHandler {
	return &PlainTextHandler{w: w, level: level}
}

func (h *PlainTextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *PlainTextHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := r.Message

	var reason, xid string
	var hasQuota bool
	var limit, used, remaining int64
	var attrs []string

	// Extract standard attributes and intercept specific ones to build conversational sentences.
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "reason":
			reason = a.Value.String()
		case "xid":
			xid = a.Value.String()
		case "limit":
			limit = a.Value.Int64()
			hasQuota = true
		case "used":
			used = a.Value.Int64()
		case "remaining":
			remaining = a.Value.Int64()
		default:
			// Just format remaining attributes as key=value inline strings
			attrs = append(attrs, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
		}
		return true
	})

	// Construct the conversational sentence format based on extracted attributes
	if xid != "" && reason != "" {
		msg = fmt.Sprintf("%s for xid=%s Reason: %s", msg, xid, reason)
	} else if xid != "" {
		msg = fmt.Sprintf("%s for xid=%s", msg, xid)
	} else if reason != "" {
		msg = fmt.Sprintf("%s Reason: %s", msg, reason)
	} else if hasQuota {
		msg = fmt.Sprintf("%s: limit=%d used=%d remaining=%d", msg, limit, used, remaining)
	}

	// Append any leftover unhandled attributes natively
	if len(attrs) > 0 {
		msg = fmt.Sprintf("%s %s", msg, strings.Join(attrs, " "))
	}

	// Format output strictly as: [LEVEL] message
	fmt.Fprintf(h.w, "[%s] %s\n", r.Level.String(), msg)
	return nil
}

// WithAttrs returns a new Handler with additional attributes.
// To keep things simple and zero-allocation, this formatter drops group attributes.
func (h *PlainTextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

// WithGroup returns a new Handler with a group appended to the current groups.
func (h *PlainTextHandler) WithGroup(name string) slog.Handler {
	return h
}
