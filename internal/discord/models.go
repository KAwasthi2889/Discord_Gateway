package discord

import "github.com/goccy/go-json"

// APIVersion specifies the targeted Discord API version.
// Version 10 is the current standard for Gateway operations.
const APIVersion = "10"

// Intents defines the integer bitmask of events the client wishes to receive.
// We request GUILD_MESSAGES (1<<9) and MESSAGE_CONTENT (1<<15).
// Note: MESSAGE_CONTENT is a privileged intent and requires explicit bot enablement.
const Intents = (1 << 9) | (1 << 15)

// Payload represents the base structure of all data transmitted over the Discord Gateway.
// It is used both for incoming events and outgoing commands.
type Payload struct {
	// Op is the opcode indicating the nature of the payload (e.g., 0 for Dispatch, 10 for Hello).
	Op int `json:"op"`

	// Data contains the event payload. Deferred parsing via json.RawMessage
	// allows us to selectively unmarshal based on the Op or Event type.
	Data json.RawMessage `json:"d"`

	// Sequence is the sequence number, used for resuming sessions and heartbeating.
	// It is only present on Opcode 0 (Dispatch) events.
	Sequence *int32 `json:"s"`

	// Event is the event name for Opcode 0 (e.g., "MESSAGE_CREATE").
	Event *string `json:"t"`
}

// Identify encapsulates the initial authentication handshake sent to Discord.
// It provides the bot token, requested intents, and basic client metadata.
type Identify struct {
	Token      string `json:"token"`
	Intents    int    `json:"intents"`
	Properties struct {
		OS      string `json:"os"`
		Browser string `json:"browser"`
		Device  string `json:"device"`
	} `json:"properties"`
}

// Resume contains the payload required to replay missed events after a disconnect.
// If the sequence number is valid, Discord replays missed data without requiring a full reconnect.
type Resume struct {
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	Seq       int32  `json:"seq"`
}

// Ready represents the acknowledgment from Discord upon successful identification.
// It provides the session ID and the specific resume URL to use if the connection drops.
type Ready struct {
	SessionID        string `json:"session_id"`
	ResumeGatewayURL string `json:"resume_gateway_url"`
}

// Hello is the initial payload received upon establishing a WebSocket connection.
// It dictates the required frequency of heartbeat responses to keep the connection alive.
type Hello struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

// EmbedField represents a single named field within a Discord embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// EmbedAuthor represents the author block of a Discord embed.
type EmbedAuthor struct {
	Name string `json:"name"`
}

// Embed represents rich media content embedded within a Discord message.
type Embed struct {
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Fields      []EmbedField `json:"fields"`
	Author      *EmbedAuthor `json:"author"`
}

// MessageCreate represents the complete structure of a new message sent in a channel.
// While our hot path bypasses this struct using direct byte scanning for performance,
// this struct remains available for secondary, non-performance-critical operations.
type MessageCreate struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
	Embeds []Embed `json:"embeds"`
}
