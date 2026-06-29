package torn

import (
	"testing"

	"discord_gateway/internal/config"
)

func TestExtractProfileLinkAndXID(t *testing.T) {
	cfg := &config.Config{MinAgeDays: 10}

	tests := []struct {
		data         []byte
		expectedLink string
		expectedXID  string
	}{
		{
			data:         []byte(`"value":"[Link](https://www.torn.com/profiles.php?XID=12345)"`),
			expectedLink: "https://www.torn.com/profiles.php?XID=12345#autorevive=10&cbport=8080&token=test_token",
			expectedXID:  "12345",
		},
		{
			data:         []byte(`"value":"[Link](https://www.torn.com/profiles.php?XID=9876543) [User]"`),
			expectedLink: "https://www.torn.com/profiles.php?XID=9876543#autorevive=10&cbport=8080&token=test_token",
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
	cfg := &config.Config{NoHistoryAllowed: false}

	validPayload := []byte(`"title":"Regular Revive Request","value":"5 paid"`)
	if ok, _ := IsPaidRegularRevive(cfg, validPayload); !ok {
		t.Error("Expected valid payload to be accepted")
	}

	premiumPayload := []byte(`"title":"Premium Revive Request"`)
	if ok, reason := IsPaidRegularRevive(cfg, premiumPayload); ok || reason != "Premium revive" {
		t.Error("Expected premium payload to be rejected")
	}

	noHistoryPayload := []byte(`"title":"Regular Revive Request","value":"No recorded history in the last 90 days"`)
	if ok, reason := IsPaidRegularRevive(cfg, noHistoryPayload); ok || reason != "0 revives" {
		t.Error("Expected no history to be rejected when not allowed")
	}

	cfg.NoHistoryAllowed = true
	if ok, _ := IsPaidRegularRevive(cfg, noHistoryPayload); !ok {
		t.Error("Expected no history to be accepted when allowed")
	}
}

func TestExtractRequesterXID(t *testing.T) {
	data := []byte(`{"value":"[JonnyCase [2185985]](https://www.torn.com/profiles.php?XID=2185985)","name":"Player","inline":false},{"value":"[PT-ShadowRazers [478]](https://www.torn.com/factions.php?step=profile&ID=478)","name":"Faction","inline":false},{"value":"Torn","name":"Country","inline":false},{"value":"[Magic [2471842]](https://www.torn.com/profiles.php?XID=2471842) - **Contact THIS player for payment!!**","name":"🤝 Requested By (On Behalf)","inline":false}`)
	expectedXID := "2471842"
	actualXID := ExtractRequesterXID(data)
	if actualXID != expectedXID {
		t.Errorf("Expected Requester XID %s, got %s", expectedXID, actualXID)
	}

	// Test when no requester is present
	dataWithoutRequester := []byte(`{"value":"[JonnyCase [2185985]](https://www.torn.com/profiles.php?XID=2185985)","name":"Player","inline":false}`)
	if ExtractRequesterXID(dataWithoutRequester) != "" {
		t.Errorf("Expected empty string when no requester present")
	}
}
