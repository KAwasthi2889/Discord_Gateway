package nuke

import (
	"encoding/json"
	"os"
)

type cacheData struct {
	ShitlistPlayers  map[int]bool         `json:"shitlist_players"`
	ShitlistFactions map[int]bool         `json:"shitlist_factions"`
	FactionContracts map[int]ContractData `json:"faction_contracts"`
	PlayerContracts  map[int]ContractData `json:"player_contracts"`
}

// SaveToDisk serializes the current active cache (Shitlists and Contracts)
// to a JSON file at the specified path to survive application restarts.
func (c *Client) SaveToDisk(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data := cacheData{
		ShitlistPlayers:  c.shitlistPlayers,
		ShitlistFactions: c.shitlistFactions,
		FactionContracts: c.factionContracts,
		PlayerContracts:  c.playerContracts,
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0644)
}

// LoadFromDisk reads a serialized JSON cache from the specified path
// and populates the client's internal memory maps, bypassing a fresh API fetch.
func (c *Client) LoadFromDisk(path string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var data cacheData
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if data.ShitlistPlayers != nil {
		c.shitlistPlayers = data.ShitlistPlayers
	}
	if data.ShitlistFactions != nil {
		c.shitlistFactions = data.ShitlistFactions
	}
	if data.FactionContracts != nil {
		c.factionContracts = data.FactionContracts
	}
	if data.PlayerContracts != nil {
		c.playerContracts = data.PlayerContracts
	}

	return nil
}
