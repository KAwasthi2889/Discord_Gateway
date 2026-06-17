package torn

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/goccy/go-json"

	"discord_gateway/internal/config"
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
}

// FormatCSV returns the record as a quoted CSV row string (no trailing newline).
func (r *ReviveRecord) FormatCSV() string {
	return fmt.Sprintf(`"%s","%s","%s","%s","%s","%s"`,
		strings.ReplaceAll(r.PlayerName, `"`, `""`),
		strings.ReplaceAll(r.PlayerID, `"`, `""`),
		strings.ReplaceAll(r.ReviveType, `"`, `""`),
		strings.ReplaceAll(r.Country, `"`, `""`),
		strings.ReplaceAll(r.Faction, `"`, `""`),
		strings.ReplaceAll(r.PaymentHistory, `"`, `""`))
}

// ExtractRecord unmarshals a raw Discord payload and extracts the Torn-specific
// fields into a ReviveRecord. Returns nil if the payload cannot be parsed or
// does not belong to a target channel.
func ExtractRecord(cfg *config.Config, data []byte) *ReviveRecord {
	var msg discord.MessageCreate
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}

	if !cfg.TargetChannelIDs[msg.ChannelID] {
		return nil
	}

	rec := &ReviveRecord{}

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
	file *os.File
}

// NewMessageLogger constructs a MessageLogger bound to the provided file descriptor.
// The file descriptor is expected to be opened in append mode.
func NewMessageLogger(file *os.File) *MessageLogger {
	return &MessageLogger{file: file}
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
func (l *MessageLogger) Log(cfg *config.Config, data []byte) {
	rec := ExtractRecord(cfg, data)
	if rec == nil {
		return
	}

	if _, err := l.file.WriteString(rec.FormatCSV() + "\n"); err != nil {
		log.Printf("message_logger: failed to append to csv: %v", err)
	}
}
