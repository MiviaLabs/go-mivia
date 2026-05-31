package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
)

const defaultPageSize = 50

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
	limit, unlimited, maxResults := boundedRequest(plan.PageSize, plan.MaxResults)
	initialCapacity := limit
	if !unlimited {
		initialCapacity = minInt(limit, maxResults)
	}
	items := make([]projectintegrations.PollItem, 0, initialCapacity)
	richContent := make([]projectintegrations.RichContentPayload, 0, initialCapacity)
	cursor := ""
	for unlimited || len(items) < maxResults {
		if err := ctx.Err(); err != nil {
			return projectintegrations.PollResult{}, err
		}
		pageLimit := limit
		if !unlimited {
			pageLimit = minInt(limit, maxResults-len(items))
		}
		response, err := poller.searchPagesWithRetry(ctx, credentials, plan.CQL, pageLimit, cursor)
		if err != nil {
			return projectintegrations.PollResult{}, err
		}
		if len(response.Results) == 0 {
			break
		}
		for _, raw := range response.Results {
			if !unlimited && len(items) >= maxResults {
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
				payload, err := poller.richContentForPageWithRetry(ctx, credentials, plan, item.ID)
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

func (poller Poller) searchPagesWithRetry(ctx context.Context, credentials projectintegrations.Credentials, cql string, limit int, cursor string) (SearchResponse, error) {
	for {
		response, err := poller.Client.SearchPages(ctx, credentials, cql, limit, cursor)
		if !shouldRetryProviderError(err) {
			if err != nil && ctx.Err() != nil {
				return SearchResponse{}, ctx.Err()
			}
			return response, err
		}
		if err := sleepRetryAfter(ctx, retryAfter(err)); err != nil {
			return SearchResponse{}, err
		}
	}
}

func (poller Poller) richContentForPageWithRetry(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.ConfluenceQueryPlan, pageID string) (projectintegrations.RichContentPayload, error) {
	for {
		payload, err := poller.richContentForPage(ctx, credentials, plan, pageID)
		if !shouldRetryProviderError(err) {
			if err != nil && ctx.Err() != nil {
				return projectintegrations.RichContentPayload{}, ctx.Err()
			}
			return payload, err
		}
		if err := sleepRetryAfter(ctx, retryAfter(err)); err != nil {
			return projectintegrations.RichContentPayload{}, err
		}
	}
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
			Number int    `json:"number"`
			When   string `json:"when"`
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
		ID:              id,
		Type:            itemType,
		Status:          firstNonEmpty(result.Content.Status, result.Status),
		UpdatedAt:       updatedAt,
		ProviderVersion: providerVersion(result.Content.Version.Number),
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

func providerVersion(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func boundedRequest(pageSize int, maxResults int) (int, bool, int) {
	unlimited := maxResults <= 0
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if !unlimited && pageSize > maxResults {
		pageSize = maxResults
	}
	return pageSize, unlimited, maxResults
}

func shouldRetryProviderError(err error) bool {
	var providerErr *projectintegrations.ProviderError
	return errors.As(err, &providerErr) &&
		providerErr.Category == projectintegrations.ErrorCategoryRateLimited &&
		providerErr.RetryAfter > 0
}

func retryAfter(err error) time.Duration {
	var providerErr *projectintegrations.ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.RetryAfter
	}
	return 0
}

func sleepRetryAfter(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
