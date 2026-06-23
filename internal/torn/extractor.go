package torn

import (
	"bytes"
	"strconv"

	"discord_gateway/internal/config"
)

var (
	// tornProfilePrefix is utilized for zero-allocation byte scanning against the raw payload.
	// It represents the standard URL prefix for Torn player profiles.
	tornProfilePrefix = []byte("https://www.torn.com/profiles.php?XID=")

	// tornCountryValue represents the expected minified JSON value for the country field.
	// Discord Gateway consistently sends minified payloads with this deterministic key ordering.
	tornCountryValue = []byte(`"value":"Torn","name":"Country"`)

	// tornRegularReviveTitle is the byte signature for standard revive request titles.
	// Intentionally omits JSON quotes so it matches as a substring within any title variant
	// (e.g. "Regular Revive Request", "Regular Revive Request 🔔", etc.)
	tornRegularReviveTitle = []byte(`Regular Revive Request`)
	tornregularReviveTitle = []byte(`regular Revive Request`)

	// tornNoReviveHistory is the byte signature indicating a player has no recent paid revives.
	tornNoReviveHistory = []byte("No recorded history in the last 90 days")
)

// IsTornCountry analyzes a raw JSON byte slice for the exact Torn country field
// signature. This approach circumvents the need for a full JSON unmarshal via
// reflection, providing significant latency reductions and eliminating heap
// allocations on the critical hot path.
func IsTornCountry(data []byte) bool {
	return bytes.Contains(data, tornCountryValue)
}

// IsPaidRegularRevive performs zero-allocation checking to determine if the
// payload represents a regular revive request and if the user has made any
// payments in the last 90 days. It achieves this by scanning for specific
// substring markers within the raw JSON bytes.
func IsPaidRegularRevive(cfg *config.Config, data []byte) (bool, string) {
	if !bytes.Contains(data, tornRegularReviveTitle) &&
		!bytes.Contains(data, tornregularReviveTitle) {
		return false, "invalid title"
	}
	if !cfg.NoHistoryAllowed && bytes.Contains(data, tornNoReviveHistory) {
		return false, "0 revives"
	}
	return true, ""
}

// ExtractProfileLink scans a raw JSON byte slice for a Torn profile URL.
// It performs a direct byte index search for the known prefix and dynamically
// extracts the subsequent numeric XID (Player ID).
//
// Returns the complete URL string if found, or an empty string if omitted or malformed.
// Because it slices the original byte array, it maintains zero allocations until
// the final string cast.
func ExtractProfileLink(cfg *config.Config, data []byte) string {
	idx := bytes.Index(data, tornProfilePrefix)
	if idx == -1 {
		return ""
	}

	// Calculate the start of the numeric XID
	end := idx + len(tornProfilePrefix)

	// Iterate forward until a non-numeric character is encountered
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}

	xidBytes := data[idx+len(tornProfilePrefix) : end]
	if len(xidBytes) == 0 {
		return "" // Invalid or missing XID
	}

	// Cast the extracted slice to a string. This is the only allocation in this function.
	// Append #autorevive={MinAgeDays} so the userscript knows this was opened by the gateway
	// and what the configured minimum age threshold is.
	return string(data[idx:end]) + "#autorevive=" + strconv.Itoa(cfg.MinAgeDays)
}
