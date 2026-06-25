package nuke

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	rfc3339Date := "2026-03-27T08:08:04Z"
	laravelDate := "2026-03-27 08:08:04"

	if parseDate(&rfc3339Date) == nil {
		t.Error("Failed to parse RFC3339 date")
	}

	if parseDate(&laravelDate) == nil {
		t.Error("Failed to parse Laravel date")
	}

	invalidDate := "not-a-date"
	if parseDate(&invalidDate) != nil {
		t.Error("Expected nil for invalid date")
	}

	if parseDate(nil) != nil {
		t.Error("Expected nil for nil string")
	}
}

func TestClient_IsShitlisted(t *testing.T) {
	c := NewClient("token")
	c.shitlistPlayers = map[int]bool{123: true}
	c.shitlistFactions = map[int]bool{456: true}

	if isShitlisted, slType := c.IsShitlisted(123, 0); !isShitlisted || slType != "player" {
		t.Error("Expected player 123 to be shitlisted")
	}
	if isShitlisted, slType := c.IsShitlisted(0, 456); !isShitlisted || slType != "faction" {
		t.Error("Expected faction 456 to be shitlisted")
	}
	if isShitlisted, _ := c.IsShitlisted(999, 999); isShitlisted {
		t.Error("Expected 999 not to be shitlisted")
	}
}

func TestClient_GetContract(t *testing.T) {
	c := NewClient("token")

	futureDate := time.Now().Add(24 * time.Hour)
	pastDate := time.Now().Add(-24 * time.Hour)

	c.playerContracts = map[int]ContractData{
		1: {MinReviveChance: 50, Note: "Active Player"},
		2: {MinReviveChance: 50, StartDate: &futureDate}, // Not started
		3: {MinReviveChance: 50, EndDate: &pastDate},     // Expired
	}
	c.factionContracts = map[int]ContractData{
		10: {MinReviveChance: 60, Note: "Active Faction"},
	}

	// Test Active
	if contract, ok := c.GetContract(1, 0); !ok || contract.Note != "Active Player" {
		t.Error("Expected active player contract")
	}

	// Test Not Started
	if _, ok := c.GetContract(2, 0); ok {
		t.Error("Expected future contract to be invalid")
	}

	// Test Expired
	if _, ok := c.GetContract(3, 0); ok {
		t.Error("Expected expired contract to be invalid")
	}

	// Test Faction
	if contract, ok := c.GetContract(999, 10); !ok || contract.Note != "Active Faction" {
		t.Error("Expected active faction contract")
	}
}

func TestClient_Persistence(t *testing.T) {
	c := NewClient("token")
	c.shitlistPlayers = map[int]bool{1: true}
	c.playerContracts = map[int]ContractData{
		1: {MinReviveChance: 50, Note: "Test"},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	if err := c.SaveToDisk(path); err != nil {
		t.Fatalf("Failed to save to disk: %v", err)
	}

	c2 := NewClient("token")
	if err := c2.LoadFromDisk(path); err != nil {
		t.Fatalf("Failed to load from disk: %v", err)
	}

	if isShitlisted, _ := c2.IsShitlisted(1, 0); !isShitlisted {
		t.Error("Expected shitlist to be loaded")
	}
	if _, ok := c2.GetContract(1, 0); !ok {
		t.Error("Expected player contract to be loaded")
	}
}

func TestClient_API_Refresh(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/shit-lists", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"playerId": 111},
			},
		})
	})
	mux.HandleFunc("/api/contracts/get_contracts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"faction_id": 222, "rule_revive_chance_percentage": 50, "note": "fact"},
		})
	})
	mux.HandleFunc("/api/revive-packages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"focus_player_id": 333, "is_active": true, "contracts": []map[string]interface{}{
					{"rule_revive_chance_percentage": 60, "note": "ply"},
				}},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := NewClient("token")
	c.SetBaseURL(server.URL + "/api")

	c.refreshAll()

	if isShitlisted, _ := c.IsShitlisted(111, 0); !isShitlisted {
		t.Error("Expected player 111 to be shitlisted")
	}
	if _, ok := c.GetContract(0, 222); !ok {
		t.Error("Expected faction 222 contract")
	}
	if _, ok := c.GetContract(333, 0); !ok {
		t.Error("Expected player 333 contract")
	}
}
