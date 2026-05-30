package jira

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

func (poller Poller) PollJira(ctx context.Context, credentials projectintegrations.Credentials, plan projectintegrations.JiraQueryPlan) (projectintegrations.PollResult, error) {
	if plan.Provider != projectintegrations.ProviderJira || strings.TrimSpace(plan.JQL) == "" {
		return projectintegrations.PollResult{}, projectintegrations.ErrInvalidInput
	}
	pageSize, maxResults := boundedRequest(plan.PageSize, plan.MaxResults)
	var items []projectintegrations.PollItem
	var richContent []projectintegrations.RichContentPayload
	var nextPageToken string
	for len(items) < maxResults {
		remaining := maxResults - len(items)
		limit := pageSize
		if remaining < limit {
			limit = remaining
		}
		response, err := poller.Client.SearchIssues(ctx, credentials, SearchRequest{
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
			if shouldExtractRichContent(plan) {
				payload, err := richContentFromIssue(plan, raw)
				if err != nil {
					return projectintegrations.PollResult{}, projectintegrations.DecodeError(provider, "extract_issue_rich_content")
				}
				richContent = append(richContent, payload)
			}
			if len(items) >= maxResults {
				break
			}
		}
		if response.NextPageToken == "" || len(response.Issues) == 0 {
			break
		}
		nextPageToken = response.NextPageToken
	}
	return projectintegrations.PollResult{Items: items, RichContent: richContent}, nil
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
