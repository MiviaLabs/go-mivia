package jira

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
)

const defaultPageSize = 100

type Poller struct {
	Client Client
}

func NewPoller(client Client) Poller {
	return Poller{Client: client}
}

func (poller Poller) PollJira(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.JiraQueryPlan) (projectintegrations.PollResult, error) {
	return poller.PollJiraWithProgress(ctx, credentials, plan, nil)
}

func (poller Poller) PollJiraWithProgress(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.JiraQueryPlan, progress projectintegrations.PollProgressFunc) (projectintegrations.PollResult, error) {
	if plan.Provider != projectintegrations.ProviderJira || strings.TrimSpace(plan.JQL) == "" {
		return projectintegrations.PollResult{}, projectintegrations.ErrInvalidInput
	}
	pageSize, unlimited, maxResults := boundedRequest(plan.PageSize, plan.MaxResults)
	var items []projectintegrations.PollItem
	var richContent []projectintegrations.RichContentPayload
	var nextPageToken string
	seenTokens := map[string]struct{}{}
	for unlimited || len(items) < maxResults {
		if err := ctx.Err(); err != nil {
			return projectintegrations.PollResult{}, err
		}
		limit := pageSize
		if !unlimited && maxResults-len(items) < limit {
			remaining := maxResults - len(items)
			limit = remaining
		}
		response, err := poller.searchIssuesWithRetry(ctx, credentials, SearchRequest{
			JQL:           plan.JQL,
			Fields:        append([]string(nil), plan.Fields...),
			MaxResults:    limit,
			NextPageToken: nextPageToken,
		})
		if err != nil {
			return projectintegrations.PollResult{}, err
		}
		for _, raw := range response.Issues {
			item, err := pollItemFromIssue(raw)
			if err != nil {
				return projectintegrations.PollResult{}, projectintegrations.DecodeError(provider, "extract_issue_metadata")
			}
			items = append(items, item)
			if progress != nil {
				if err := progress(ctx, projectintegrations.PollProgress{ItemsSeen: len(items)}); err != nil {
					return projectintegrations.PollResult{}, err
				}
			}
			if shouldExtractRichContent(plan) {
				payload, err := richContentFromIssue(plan, raw)
				if err != nil {
					return projectintegrations.PollResult{}, projectintegrations.DecodeError(provider, "extract_issue_rich_content")
				}
				richContent = append(richContent, payload)
			}
			if !unlimited && len(items) >= maxResults {
				break
			}
		}
		if response.NextPageToken == "" || len(response.Issues) == 0 {
			break
		}
		if _, ok := seenTokens[response.NextPageToken]; ok {
			break
		}
		seenTokens[response.NextPageToken] = struct{}{}
		nextPageToken = response.NextPageToken
	}
	return projectintegrations.PollResult{Items: items, RichContent: richContent}, nil
}

func (poller Poller) searchIssuesWithRetry(ctx context.Context, credentials projectintegrations.Credentials, request SearchRequest) (SearchResponse, error) {
	for {
		response, err := poller.Client.SearchIssues(ctx, credentials, request)
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

type issueMetadata struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Updated   string `json:"updated"`
		Status    named  `json:"status"`
		IssueType named  `json:"issuetype"`
	} `json:"fields"`
}

type named struct {
	Name string `json:"name"`
}

func pollItemFromIssue(raw json.RawMessage) (projectintegrations.PollItem, error) {
	var issue issueMetadata
	if err := json.Unmarshal(raw, &issue); err != nil {
		return projectintegrations.PollItem{}, err
	}
	id := strings.TrimSpace(issue.ID)
	if id == "" {
		return projectintegrations.PollItem{}, projectintegrations.ErrInvalidInput
	}
	itemType := strings.TrimSpace(issue.Fields.IssueType.Name)
	if itemType == "" {
		itemType = "issue"
	}
	updatedAt, err := parseProviderTime(issue.Fields.Updated)
	if err != nil {
		return projectintegrations.PollItem{}, err
	}
	return projectintegrations.PollItem{
		ID:        id,
		Key:       strings.TrimSpace(issue.Key),
		Type:      itemType,
		Status:    strings.TrimSpace(issue.Fields.Status.Name),
		UpdatedAt: updatedAt,
	}, nil
}

func shouldExtractRichContent(plan projectintegrations.JiraQueryPlan) bool {
	return plan.IncludeRichFields || plan.IncludeComments
}

func richContentFromIssue(plan projectintegrations.JiraQueryPlan, raw json.RawMessage) (projectintegrations.RichContentPayload, error) {
	item, chunks, err := projectintegrations.ExtractJiraRichContent(plan, raw, projectintegrations.RichContentOptions{})
	if err != nil {
		return projectintegrations.RichContentPayload{}, err
	}
	return projectintegrations.RichContentPayload{Item: item, Chunks: chunks}, nil
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

func parseProviderTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05-0700",
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, projectintegrations.ErrInvalidInput
}
