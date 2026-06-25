package nuke

import (
	"net/http"
	"sync"
	"time"
)

// ContractData holds the relevant information extracted from a Nuke API contract.
type ContractData struct {
	MinReviveChance int
	Note            string
	StartDate       *time.Time
	EndDate         *time.Time
	PStatus         string
}

// Client manages the fetching, caching, and periodic refreshing of Nuke API data.
type Client struct {
	token      string
	httpClient *http.Client
	baseURL    string

	mu               sync.RWMutex
	shitlistPlayers  map[int]bool
	shitlistFactions map[int]bool
	factionContracts map[int]ContractData
	playerContracts  map[int]ContractData
}

// NewClient initializes a new Nuke API client. It does not auto-refresh if it has a cache file to load.
// Use LoadOrFetch to perform the initial population.
func NewClient(token string) *Client {
	c := &Client{
		token:            token,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		baseURL:          "https://nuke.family/api",
		shitlistPlayers:  make(map[int]bool),
		shitlistFactions: make(map[int]bool),
		factionContracts: make(map[int]ContractData),
		playerContracts:  make(map[int]ContractData),
	}
	return c
}

// SetBaseURL allows overriding the default Nuke API base URL (useful for testing).
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// LoadOrFetch attempts to load from disk; if it fails or is missing, it fetches from the API.
// It also starts the periodic refresh background routine.
func (c *Client) LoadOrFetch(cachePath string) {
	if c.token == "" {
		// Needs slog
		return
	}

	err := c.LoadFromDisk(cachePath)
	if err != nil {
		c.refreshAll()
	}

	go c.startPeriodicRefresh()
}
// IsShitlisted checks if a given player ID or faction ID is on the active shitlist.
// It returns a boolean indicating if they are shitlisted, and a string indicating
// the reason ("player" or "faction").
func (c *Client) IsShitlisted(playerID, factionID int) (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.shitlistPlayers[playerID] {
		return true, "player"
	}
	if c.shitlistFactions[factionID] {
		return true, "faction"
	}
	return false, ""
}

func (c *Client) isContractActive(contract ContractData) bool {
	now := time.Now().UTC()
	if contract.StartDate != nil && now.Before(*contract.StartDate) {
		return false
	}
	if contract.EndDate != nil && now.After(*contract.EndDate) {
		return false
	}
	return true
}

// GetContract checks if there is an active contract for the given player or faction.
// It prioritizes individual player packages over faction-wide contracts.
func (c *Client) GetContract(playerID, factionID int) (ContractData, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if contract, ok := c.playerContracts[playerID]; ok && c.isContractActive(contract) {
		return contract, true
	}
	if contract, ok := c.factionContracts[factionID]; ok && c.isContractActive(contract) {
		return contract, true
	}
	return ContractData{}, false
}


