package torn

import (
	"os"
	"sync"
	"testing"
)

func TestExtractRecord(t *testing.T) {
	// A mock Discord MESSAGE_CREATE payload representing a Torn Revive Request
	mockPayload := []byte(`{
		"id": "12345",
		"channel_id": "67890",
		"content": "",
		"author": {"username": "Bot"},
		"embeds": [
			{
				"title": "Regular Revive Request",
				"description": "Someone needs a revive",
				"fields": [
					{"name": "Player", "value": "[JohnDoe [1234567]](https://www.torn.com/profiles.php?XID=1234567)"},
					{"name": "Faction", "value": "[Some Faction](https://www.torn.com/factions.php?step=profile&ID=999)"},
					{"name": "Country", "value": "Torn"},
					{"name": "📊 Revive History", "value": "Past 90 days: **3** revives"}
				]
			}
		]
	}`)

	rec := ExtractRecord(mockPayload)
	if rec == nil {
		t.Fatalf("ExtractRecord returned nil")
	}

	if rec.PlayerName != "JohnDoe" {
		t.Errorf("Expected PlayerName 'JohnDoe', got '%s'", rec.PlayerName)
	}
	if rec.PlayerID != "1234567" {
		t.Errorf("Expected PlayerID '1234567', got '%s'", rec.PlayerID)
	}
	if rec.ReviveType != "regular" {
		t.Errorf("Expected ReviveType 'regular', got '%s'", rec.ReviveType)
	}
	if rec.Country != "Torn" {
		t.Errorf("Expected Country 'Torn', got '%s'", rec.Country)
	}
	if rec.Faction != "Some Faction" {
		t.Errorf("Expected Faction 'Some Faction', got '%s'", rec.Faction)
	}
	if rec.PaymentHistory != "3" {
		t.Errorf("Expected PaymentHistory '3', got '%s'", rec.PaymentHistory)
	}
}

func TestReviveRecord_ToCSVRow(t *testing.T) {
	rec := &ReviveRecord{
		PlayerName:     "Test,Name\"Quote",
		PlayerID:       "123",
		ReviveType:     "regular",
		Country:        "Torn",
		Faction:        "Test Faction",
		PaymentHistory: "5",
	}

	row := rec.ToCSVRow()
	if len(row) != 7 {
		t.Errorf("Expected 7 columns, got %d", len(row))
	}

	if row[0] != "Test,Name\"Quote" {
		t.Errorf("Expected 'Test,Name\"Quote', got '%s'", row[0])
	}
}

func TestParseFieldValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"[PlayerOne](https://link)", "PlayerOne"},
		{"Normal String", "Normal String"},
		{"[Incomplete Link", "[Incomplete Link"},
	}

	for _, tc := range tests {
		actual := parseFieldValue(tc.input)
		if actual != tc.expected {
			t.Errorf("parseFieldValue(%q) = %q, expected %q", tc.input, actual, tc.expected)
		}
	}
}

func TestIsTornCountry(t *testing.T) {
	valid := []byte(`{"value":"Torn","name":"Country"}`)
	invalid := []byte(`{"value":"Mexico","name":"Country"}`)

	if !IsTornCountry(valid) {
		t.Errorf("Expected IsTornCountry to return true for valid payload")
	}
	if IsTornCountry(invalid) {
		t.Errorf("Expected IsTornCountry to return false for invalid payload")
	}
}

func TestMessageLogger_Concurrent(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "logger_test_*.csv")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	logger := NewMessageLogger(tmpFile)

	mockPayload := []byte(`{"embeds": [{"title": "Regular Revive Request", "fields": [{"name": "Player", "value": "TestPlayer"}, {"name": "Country", "value": "Torn"}]}]}`)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Log(mockPayload, "TestNote")
		}()
	}

	wg.Wait()
}
