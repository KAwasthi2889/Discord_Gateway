package torn

import (
	"bytes"
	"strconv"

	"discord_gateway/internal/config"
)

// Global byte signatures used for zero-allocation extraction
var (
	// tornProfilePrefix is utilized for zero-allocation byte scanning against the raw payload.
	// It represents the standard URL prefix for Torn player profiles.
	tornProfilePrefix = []byte("https://www.torn.com/profiles.php?XID=")

	// tornFactionPrefix is used to extract the faction ID.
	tornFactionPrefix = []byte("https://www.torn.com/factions.php?step=profile&ID=")

	// tornCountryValue represents the expected minified JSON value for the country field.
	// Discord Gateway consistently sends minified payloads with this deterministic key ordering.
	tornCountryValue = []byte(`"value":"Torn","name":"Country"`)

	// tornRegularReviveTitle is the byte signature for standard revive request titles.
	// Intentionally omits JSON quotes so it matches as a substring within any title variant
	// (e.g. "Regular Revive Request", "Regular Revive Request 🔔", etc.)
	tornRegularReviveTitle = []byte(`Regular Revive Request`)
	tornregularReviveTitle = []byte(`regular Revive Request`)

	// tornPremiumReviveTitle is the byte signature for premium revive requests.
	tornPremiumReviveTitle = []byte(`Premium Revive Request`)
	tornpremiumReviveTitle = []byte(`premium Revive Request`)

	// tornNoReviveHistory is the byte signature indicating a player has no recent paid revives.
	tornNoReviveHistory = []byte("No recorded history in the last 90 days")

	// tornRequestedBy is the byte signature for "On Behalf" requests.
	tornRequestedBy = []byte("Requested By (On Behalf)")
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
	if bytes.Contains(data, tornRequestedBy) {
		return false, "on behalf"
	}
	if !bytes.Contains(data, tornRegularReviveTitle) &&
		!bytes.Contains(data, tornregularReviveTitle) {
		if bytes.Contains(data, tornPremiumReviveTitle) || bytes.Contains(data, tornpremiumReviveTitle) {
			return false, "Premium revive"
		}
		return false, "invalid title"
	}
	return true, ""
}

// ExtractProfileLinkAndXID scans a raw JSON byte slice for a Torn profile URL.
// It performs a direct byte index search for the known prefix and dynamically
// extracts the subsequent numeric XID (Player ID).
//
// Returns the complete URL string and the raw XID if found, or empty strings if omitted or malformed.
// Because it slices the original byte array, it maintains zero allocations until
// the final string cast.
func ExtractProfileLinkAndXID(cfg *config.Config, callbackPort int, callbackToken string, data []byte) (string, string) {
	idx := bytes.Index(data, tornProfilePrefix)
	if idx == -1 {
		return "", ""
	}

	// Calculate the start of the numeric XID
	end := idx + len(tornProfilePrefix)

	// Iterate forward until a non-numeric character is encountered
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}

	xidBytes := data[idx+len(tornProfilePrefix) : end]
	if len(xidBytes) == 0 {
		return "", "" // Invalid or missing XID
	}

	xidStr := string(xidBytes)

	minAge := cfg.MinAgeDays
	hasPaymentHistory := !bytes.Contains(data, tornNoReviveHistory)

	if hasPaymentHistory {
		minAge = 0 // Bypass age check for those with payment history (shortcut)
	}

	// Cast the extracted slice to a string. This is the only allocation in this function.
	// Append #autorevive={MinAgeDays}&cbport={callbackPort} so the userscript knows:
	//   1. This tab was opened by the gateway (autorevive trigger)
	//   2. The configured minimum age threshold (0 means bypass)
	//   3. Where to send the success callback
	//   4. The auth token to prevent localhost SSRF/injection
	link := string(data[idx:end]) + "#autorevive=" + strconv.Itoa(minAge) + "&cbport=" + strconv.Itoa(callbackPort) + "&token=" + callbackToken
	return link, xidStr
}

// ExtractFactionID scans a raw JSON byte slice for a Torn faction profile URL
// and dynamically extracts the numeric Faction ID.
//
// Returns the raw Faction ID as a string, or an empty string if omitted or malformed.
// Because it slices the original byte array, it maintains zero allocations until the string cast.
func ExtractFactionID(data []byte) string {
	idx := bytes.Index(data, tornFactionPrefix)
	if idx == -1 {
		return ""
	}

	end := idx + len(tornFactionPrefix)
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}

	factionBytes := data[idx+len(tornFactionPrefix) : end]
	if len(factionBytes) == 0 {
		return ""
	}

	return string(factionBytes)
}
