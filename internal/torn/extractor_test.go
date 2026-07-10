package torn

import (
	"testing"

	"discord_gateway/internal/config"
)

func TestExtractProfileLinkAndXID(t *testing.T) {
	cfg := &config.Config{MinBattleStats: 100000}

	tests := []struct {
		data         []byte
		expectedLink string
		expectedXID  string
	}{
		{
			data:         []byte(`"value":"[Link](https://www.torn.com/profiles.php?XID=12345)" Paid`),
			expectedLink: "https://www.torn.com/profiles.php?XID=12345#autorevive=1&cbport=8080&token=test_token",
			expectedXID:  "12345",
		},
		{
			data:         []byte(`"value":"[Link](https://www.torn.com/profiles.php?XID=9876543) [User]" Paid`),
			expectedLink: "https://www.torn.com/profiles.php?XID=9876543#autorevive=1&cbport=8080&token=test_token",
			expectedXID:  "9876543",
		},
		{
			data:         []byte(`invalid payload`),
			expectedLink: "",
			expectedXID:  "",
		},
	}

	for _, tt := range tests {
		link, xid := ExtractProfileLinkAndXID(cfg, 8080, "test_token", tt.data)
		if link != tt.expectedLink {
			t.Errorf("Expected link %q, got %q", tt.expectedLink, link)
		}
		if xid != tt.expectedXID {
			t.Errorf("Expected xid %q, got %q", tt.expectedXID, xid)
		}
	}
}

func TestExtractFactionID(t *testing.T) {
	tests := []struct {
		data       []byte
		expectedID string
	}{
		{
			data:       []byte(`"value":"[Faction](https://www.torn.com/factions.php?step=profile&ID=5555)"`),
			expectedID: "5555",
		},
		{
			data:       []byte(`no faction`),
			expectedID: "",
		},
	}

	for _, tt := range tests {
		id := ExtractFactionID(tt.data)
		if id != tt.expectedID {
			t.Errorf("Expected id %q, got %q", tt.expectedID, id)
		}
	}
}

func TestIsPaidRegularRevive(t *testing.T) {
	cfg := &config.Config{}

	validPayload := []byte(`"title":"Regular Revive Request","value":"5 paid"`)
	if ok, _ := IsPaidRegularRevive(cfg, validPayload); !ok {
		t.Error("Expected valid payload to be accepted")
	}

	premiumPayload := []byte(`"title":"Premium Revive Request"`)
	if ok, reason := IsPaidRegularRevive(cfg, premiumPayload); ok || reason != "Premium revive" {
		t.Error("Expected premium payload to be rejected")
	}

	onBehalfPayload := []byte(`"title":"Regular Revive Request","value":"[Link](...)", "name":"🤝 Requested By (On Behalf)"`)
	if ok, reason := IsPaidRegularRevive(cfg, onBehalfPayload); ok || reason != "on behalf" {
		t.Error("Expected on behalf request to be rejected")
	}
}
