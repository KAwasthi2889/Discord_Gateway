package nuke

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const MAX_SIZE = 5 * 1024 * 1024 // 5 MB

func parseDate(dateStr *string) *time.Time {
	if dateStr == nil || *dateStr == "" {
		return nil
	}
	// Try RFC3339 first
	t, err := time.Parse(time.RFC3339, *dateStr)
	if err == nil {
		return &t
	}
	// Try Laravel format
	t, err = time.Parse("2006-01-02 15:04:05", *dateStr)
	if err == nil {
		return &t
	}
	return nil
}

func (c *Client) startPeriodicRefresh() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.RefreshAll()
		case <-c.stopRefresh:
			return
		}
	}
}

func (c *Client) RefreshAll() bool {
	slog.Debug("Refreshing Nuke API Shitlist and Contracts data...")

	newShitlistPlayers := make(map[int][]int)
	newShitlistFactions := make(map[int]struct{})
	newFactionContracts := make(map[int]ContractData)
	newPlayerContracts := make(map[int]ContractData)

	success := true

	// Fetch Shitlist
	err := c.fetchPaginated(c.baseURL+"/shit-lists", func(item json.RawMessage) {
		var entry struct {
			PlayerID           *int        `json:"playerId"`
			FactionID          *int        `json:"factionId"`
			ShitListCategoryID *int        `json:"shitListCategoryId"`
			IsFactionBan       interface{} `json:"isFactionBan"`
			IsApproved         interface{} `json:"isApproved"`
			CreatedAt          *string     `json:"createdAt"`
		}
		if err := json.Unmarshal(item, &entry); err == nil {
			if entry.ShitListCategoryID != nil && *entry.ShitListCategoryID == 5 {
				return // skip Nuke Family entirely
			}

			catID := 0
			if entry.ShitListCategoryID != nil {
				catID = *entry.ShitListCategoryID
			}

			addUnique := func(slice []int, val int) []int {
				for _, v := range slice {
					if v == val {
						return slice
					}
				}
				return append(slice, val)
			}

			parseBool := func(v interface{}) bool {
				switch val := v.(type) {
				case bool:
					return val
				case float64:
					return val != 0
				case string:
					return val == "1" || val == "true"
				}
				return false
			}

			if !parseBool(entry.IsApproved) {
				return // Drop unapproved entries
			}

			if entry.PlayerID != nil {
				newShitlistPlayers[*entry.PlayerID] = addUnique(newShitlistPlayers[*entry.PlayerID], catID)
			}

			if parseBool(entry.IsFactionBan) && entry.FactionID != nil {
				isOldBan := false
				if entry.CreatedAt != nil {
					parsedDate := parseDate(entry.CreatedAt)
					if parsedDate != nil && time.Since(*parsedDate).Hours() > 2*365*24 {
						isOldBan = true
					}
				}
				if !isOldBan {
					newShitlistFactions[*entry.FactionID] = struct{}{}
				}
			}
		}
	})
	if err != nil {
		slog.Error("Failed to fetch shitlists", "error", err)
		success = false
	}

	// Fetch Faction Contracts
	if data, err := c.doRequest(c.baseURL + "/contracts/get_contracts"); err == nil {
		var contracts []struct {
			FactionID    int     `json:"faction_id"`
			ReviveChance int     `json:"rule_revive_chance_percentage"`
			PStatus      string  `json:"rule_player_status"`
			Note         string  `json:"note"`
			StartDate    *string `json:"contract_start_date"`
			EndDate      *string `json:"contract_end_date"`
		}
		if err := json.Unmarshal(data, &contracts); err == nil {
			for _, contract := range contracts {
				status := "ANY"
				if contract.PStatus != "" {
					status = contract.PStatus
				}
				newFactionContracts[contract.FactionID] = ContractData{
					MinReviveChance: contract.ReviveChance,
					PStatus:         status,
					Note:            contract.Note,
					StartDate:       parseDate(contract.StartDate),
					EndDate:         parseDate(contract.EndDate),
				}
			}
		} else {
			slog.Error("Failed to parse get_contracts", "error", err)
			success = false
		}
	} else {
		slog.Error("Failed to fetch get_contracts", "error", err)
		success = false
	}

	// Fetch Revive Packages
	err = c.fetchPaginated(c.baseURL+"/revive-packages", func(item json.RawMessage) {
		var pkg struct {
			FocusPlayerID int  `json:"focus_player_id"`
			IsActive      bool `json:"is_active"`
			Contracts     []struct {
				ReviveChance int     `json:"rule_revive_chance_percentage"`
				PStatus      string  `json:"rule_player_status"`
				Note         string  `json:"note"`
				StartDate    *string `json:"contract_start_date"`
				EndDate      *string `json:"contract_end_date"`
			} `json:"contracts"`
		}
		if err := json.Unmarshal(item, &pkg); err == nil {
			if pkg.IsActive && len(pkg.Contracts) > 0 {
				status := "ANY"
				if pkg.Contracts[0].PStatus != "" {
					status = pkg.Contracts[0].PStatus
				}
				newPlayerContracts[pkg.FocusPlayerID] = ContractData{
					MinReviveChance: pkg.Contracts[0].ReviveChance,
					PStatus:         status,
					Note:            pkg.Contracts[0].Note,
					StartDate:       parseDate(pkg.Contracts[0].StartDate),
					EndDate:         parseDate(pkg.Contracts[0].EndDate),
				}
			}
		}
	})
	if err != nil {
		slog.Error("Failed to fetch revive packages", "error", err)
		success = false
	}

	if !success {
		slog.Warn("Nuke API refresh encountered errors, retaining existing cached data.")
		return false
	}

	c.mu.Lock()
	c.shitlistPlayers = newShitlistPlayers
	c.shitlistFactions = newShitlistFactions
	c.factionContracts = newFactionContracts
	c.playerContracts = newPlayerContracts
	c.mu.Unlock()

	slog.Debug("Nuke API refresh complete",
		"shitlisted_players", len(c.shitlistPlayers),
		"shitlisted_factions", len(c.shitlistFactions),
		"faction_contracts", len(c.factionContracts),
		"player_contracts", len(c.playerContracts),
	)
	return true
}

// fetchPaginated handles endpoints that return standard Laravel-style pagination.
func (c *Client) fetchPaginated(startURL string, processItem func(json.RawMessage)) error {
	currentURL := startURL

	for currentURL != "" {
		data, err := c.doRequest(currentURL)
		if err != nil {
			return err
		}

		var page struct {
			Data        []json.RawMessage `json:"data"`
			NextPageURL *string           `json:"next_page_url"`
		}

		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}

		for _, item := range page.Data {
			processItem(item)
		}

		if page.NextPageURL != nil {
			parsedURL, err := url.Parse(*page.NextPageURL)
			if err != nil {
				return fmt.Errorf("invalid next_page_url: %w", err)
			}
			baseURLParsed, _ := url.Parse(c.baseURL)
			if parsedURL.Scheme != baseURLParsed.Scheme || parsedURL.Host != baseURLParsed.Host {
				return fmt.Errorf("SSRF detected: invalid next_page_url host/scheme %s", *page.NextPageURL)
			}
			currentURL = *page.NextPageURL
		} else {
			break
		}
	}
	return nil
}

func (c *Client) doRequest(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MAX_SIZE))
		return nil, fmt.Errorf("UNEXPECTED ERROR: HTTP %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, MAX_SIZE))
}
