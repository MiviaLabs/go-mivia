package confluence

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
)

type Poller struct {
	Client Client
}

func NewPoller(client Client) Poller {
	return Poller{Client: client}
}

func (poller Poller) PollConfluence(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.ConfluenceQueryPlan) (projectintegrations.PollResult, error) {
	return poller.PollConfluenceWithProgress(ctx, credentials, plan, nil)
}

func (poller Poller) PollConfluenceWithProgress(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.ConfluenceQueryPlan, progress projectintegrations.PollProgressFunc) (projectintegrations.PollResult, error) {
	if plan.Provider != projectintegrations.ProviderConfluence || strings.TrimSpace(plan.CQL) == "" {
		return projectintegrations.PollResult{}, projectintegrations.ErrInvalidInput
	}
	limit, maxResults := boundedRequest(plan.PageSize, plan.MaxResults)
	if limit > maxResults {
		limit = maxResults
	}
	items := make([]projectintegrations.PollItem, 0, minInt(limit, maxResults))
	richContent := make([]projectintegrations.RichContentPayload, 0, minInt(limit, maxResults))
	cursor := ""
	for len(items) < maxResults {
		pageLimit := minInt(limit, maxResults-len(items))
		response, err := poller.Client.SearchPages(ctx, credentials, plan.CQL, pageLimit, cursor)
		if err != nil {
			return projectintegrations.PollResult{}, err
		}
		if len(response.Results) == 0 {
			break
		}
		for _, raw := range response.Results {
			if len(items) >= maxResults {
				break
			}
			item, err := pollItemFromSearchResult(raw)
			if err != nil {
				return projectintegrations.PollResult{}, projectintegrations.DecodeError(provider, "extract_page_metadata")
			}
			items = append(items, item)
			if progress != nil {
				if err := progress(ctx, projectintegrations.PollProgress{ItemsSeen: len(items)}); err != nil {
					return projectintegrations.PollResult{}, err
				}
			}
			if shouldExtractRichContent(plan) {
				payload, err := poller.richContentForPage(ctx, credentials, plan, item.ID)
				if err != nil {
					return projectintegrations.PollResult{}, err
				}
				richContent = append(richContent, payload)
			}
		}
		nextCursor := response.NextCursor()
		if nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}
	return projectintegrations.PollResult{Items: items, RichContent: richContent}, nil
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

func shouldExtractRichContent(plan projectintegrations.ConfluenceQueryPlan) bool {
	return plan.IncludeBody || plan.IncludeComments || plan.IncludeLabels || plan.IncludeProperties
}

func (poller Poller) richContentForPage(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.ConfluenceQueryPlan, pageID string) (projectintegrations.RichContentPayload, error) {
	response, err := poller.Client.GetPage(ctx, credentials, pageID, plan.BodyRepresentation)
	if err != nil {
		return projectintegrations.RichContentPayload{}, err
	}
	item, chunks, err := projectintegrations.ExtractConfluenceRichContent(plan, response.Raw, projectintegrations.RichContentOptions{})
	if err != nil {
		return projectintegrations.RichContentPayload{}, projectintegrations.DecodeError(provider, "extract_page_rich_content")
	}
	return projectintegrations.RichContentPayload{Item: item, Chunks: chunks}, nil
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

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
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
