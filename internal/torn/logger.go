// Package torn encapsulates the domain-specific business logic for processing Torn events.
// It handles raw payload extraction, rate limiting, logging, and browser orchestration,
// ensuring strict adherence to the zero-allocation hot path philosophy.
package torn

import (
	"encoding/csv"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/goccy/go-json"

	"discord_gateway/internal/discord"
)

// paymentRe is a compiled regular expression used to extract the numeric
// payment history count from Discord's markdown-formatted embed fields.
var paymentRe = regexp.MustCompile(`\*\*(\d+)\*\*`)

// ReviveRecord holds the structured, extracted fields from a Torn revive
// request embed. It serves as the single canonical representation used by
// both the CSV logger and the rejection detail logger.
type ReviveRecord struct {
	PlayerName     string
	PlayerID       string
	ReviveType     string
	Country        string
	Faction        string
	PaymentHistory string
	ContractNote   string
}

// ToCSVRow converts the record into a slice of strings suitable for encoding/csv.
func (r *ReviveRecord) ToCSVRow() []string {
	return []string{
		r.PlayerName,
		r.PlayerID,
		r.ReviveType,
		r.Country,
		r.Faction,
		r.PaymentHistory,
		r.ContractNote,
	}
}

// ExtractRecord unmarshals a raw Discord payload and extracts the Torn-specific
// fields into a ReviveRecord. Returns nil if the payload cannot be parsed.
// Note: It assumes the caller has already validated that the payload originates
// from a target channel.
func ExtractRecord(data []byte) *ReviveRecord {
	var msg discord.MessageCreate
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}

	rec := &ReviveRecord{
		PaymentHistory: "0",
	}

	for _, embed := range msg.Embeds {
		if strings.Contains(embed.Title, "Regular") {
			rec.ReviveType = "regular"
		} else if strings.Contains(embed.Title, "Premium") {
			rec.ReviveType = "premium"
		} else {
			rec.ReviveType = embed.Title
		}

		for _, field := range embed.Fields {
			switch field.Name {
			case "Player":
				val := parseFieldValue(field.Value)
				if idx := strings.LastIndex(val, " ["); idx != -1 && strings.HasSuffix(val, "]") {
					rec.PlayerName = val[:idx]
					rec.PlayerID = val[idx+2 : len(val)-1]
				} else {
					rec.PlayerName = val
				}
			case "Faction":
				rec.Faction = parseFieldValue(field.Value)
			case "Country":
				rec.Country = field.Value
			case "📊 Revive History":
				matches := paymentRe.FindStringSubmatch(field.Value)
				if len(matches) >= 2 {
					rec.PaymentHistory = matches[1]
				} else {
					rec.PaymentHistory = "0"
				}
			}
		}
	}

	return rec
}

// MessageLogger provides asynchronous parsing and persistence for Discord Gateway
// payloads. It unmarshals the raw JSON into structured models, extracts relevant
// metadata, and appends formatted CSV records to a configured log file.
//
// This component is intentionally separated from the hot path. By performing
// the expensive reflection-based JSON unmarshaling here, the critical event loop
// remains unblocked.
type MessageLogger struct {
	mu   sync.Mutex
	file *os.File
	csv  *csv.Writer
}

// NewMessageLogger constructs a MessageLogger bound to the provided file descriptor.
// The file descriptor is expected to be opened in append mode.
func NewMessageLogger(file *os.File) *MessageLogger {
	return &MessageLogger{
		file: file,
		csv:  csv.NewWriter(file),
	}
}

// parseFieldValue is an internal helper that sanitizes Discord markdown links.
// For example, it converts "[PlayerName](https://link)" into "PlayerName".
func parseFieldValue(val string) string {
	if strings.HasPrefix(val, "[") {
		idx := strings.Index(val, "](")
		if idx != -1 {
			return val[1:idx]
		}
	}
	return val
}

// Log processes the raw Gateway payload, extracts a ReviveRecord, and appends
// the resulting CSV row to the log file.
func (l *MessageLogger) Log(data []byte, contractNote string) {
	rec := ExtractRecord(data)
	if rec == nil {
		return
	}
	rec.ContractNote = contractNote

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.csv.Write(rec.ToCSVRow()); err != nil {
		slog.Error("Failed to write CSV record", "error", err)
		return
	}
	l.csv.Flush()

	if err := l.csv.Error(); err != nil {
		slog.Error("Failed to flush CSV writer", "error", err)
	}
}
