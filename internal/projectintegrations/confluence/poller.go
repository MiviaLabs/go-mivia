package confluence

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
)

type Poller struct {
	Client Client
}

func NewPoller(client Client) Poller {
	return Poller{Client: client}
}

func (poller Poller) PollConfluence(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.ConfluenceQueryPlan) (projectintegrations.PollResult, error) {
	if plan.Provider != projectintegrations.ProviderConfluence || strings.TrimSpace(plan.CQL) == "" {
		return projectintegrations.PollResult{}, projectintegrations.ErrInvalidInput
	}
	limit, maxResults := boundedRequest(plan.PageSize, plan.MaxResults)
	if limit > maxResults {
		limit = maxResults
	}
	response, err := poller.Client.SearchPages(ctx, credentials, plan.CQL, limit)
	if err != nil {
		return projectintegrations.PollResult{}, err
	}
	items := make([]projectintegrations.PollItem, 0, len(response.Results))
	for _, raw := range response.Results {
		if len(items) >= maxResults {
			break
		}
		item, err := pollItemFromSearchResult(raw)
		if err != nil {
			return projectintegrations.PollResult{}, projectintegrations.DecodeError(provider, "extract_page_metadata")
		}
		items = append(items, item)
	}
	return projectintegrations.PollResult{Items: items}, nil
}

type searchResultMetadata struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	LastModified string `json:"lastModified"`
	Content      struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Status  string `json:"status"`
		Version struct {
			When string `json:"when"`
		} `json:"version"`
		History struct {
			LastUpdated struct {
				When string `json:"when"`
			} `json:"lastUpdated"`
		} `json:"history"`
	} `json:"content"`
}

func pollItemFromSearchResult(raw json.RawMessage) (projectintegrations.PollItem, error) {
	var result searchResultMetadata
	if err := json.Unmarshal(raw, &result); err != nil {
		return projectintegrations.PollItem{}, err
	}
	id := firstNonEmpty(result.Content.ID, result.ID)
	if id == "" {
		return projectintegrations.PollItem{}, projectintegrations.ErrInvalidInput
	}
	itemType := firstNonEmpty(result.Content.Type, result.Type)
	if itemType == "" {
		itemType = "page"
	}
	updatedAt, err := parseProviderTime(firstNonEmpty(
		result.LastModified,
		result.Content.Version.When,
		result.Content.History.LastUpdated.When,
	))
	if err != nil {
		return projectintegrations.PollItem{}, err
	}
	return projectintegrations.PollItem{
		ID:        id,
		Type:      itemType,
		Status:    firstNonEmpty(result.Content.Status, result.Status),
		UpdatedAt: updatedAt,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boundedRequest(pageSize int, maxResults int) (int, int) {
	if maxResults <= 0 {
		maxResults = 100
	}
	if pageSize <= 0 || pageSize > maxResults {
		pageSize = maxResults
	}
	return pageSize, maxResults
}

func parseProviderTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
