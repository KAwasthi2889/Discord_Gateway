package nuke

import (
	"encoding/json"
	"os"
)

type cacheData struct {
	ShitlistPlayers  map[int][]int        `json:"shitlist_players"`
	ShitlistFactions []int                `json:"shitlist_factions"`
	FactionContracts map[int]ContractData `json:"faction_contracts"`
	PlayerContracts  map[int]ContractData `json:"player_contracts"`
}

// SaveToDisk serializes the current active cache (Shitlists and Contracts)
// to a JSON file at the specified path to survive application restarts.
func (c *Client) SaveToDisk(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	factionList := make([]int, 0, len(c.shitlistFactions))
	for fID := range c.shitlistFactions {
		factionList = append(factionList, fID)
	}

	data := cacheData{
		ShitlistPlayers:  c.shitlistPlayers,
		ShitlistFactions: factionList,
		FactionContracts: c.factionContracts,
		PlayerContracts:  c.playerContracts,
	}

	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, bytes, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
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
		c.shitlistFactions = make(map[int]struct{})
		for _, fID := range data.ShitlistFactions {
			c.shitlistFactions[fID] = struct{}{}
		}
	}
	if data.FactionContracts != nil {
		c.factionContracts = data.FactionContracts
	}
	if data.PlayerContracts != nil {
		c.playerContracts = data.PlayerContracts
	}

	return nil
}
