package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
)

func TestPoller_PollJiraPaginatesWithinPlannerBoundsAndExtractsMetadata(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		var request SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch requests {
		case 0:
			if request.JQL != "project in (ACME)" || request.MaxResults != 2 || request.NextPageToken != "" {
				t.Fatalf("unexpected first request: %#v", request)
			}
			if len(request.Fields) != 2 || request.Fields[0] != "summary" || request.Fields[1] != "updated" {
				t.Fatalf("unexpected fields: %#v", request.Fields)
			}
			_, _ = w.Write([]byte(`{"issues":[
				{"id":"10001","key":"ACME-1","fields":{"updated":"2026-05-31T10:00:00.000+0000","status":{"name":"Open"},"issuetype":{"name":"Task"}}},
				{"id":"10002","key":"ACME-2","fields":{"updated":"2026-05-31T10:01:00.000+0000","status":{"name":"Done"},"issuetype":{"name":"Bug"}}}
			],"nextPageToken":"transient-page-token"}`))
		case 1:
			if request.MaxResults != 1 || request.NextPageToken != "transient-page-token" {
				t.Fatalf("unexpected second request: %#v", request)
			}
			_, _ = w.Write([]byte(`{"issues":[
				{"id":"10003","key":"ACME-3","fields":{"updated":"2026-05-31T10:02:00Z","status":{"name":"Review"},"issuetype":{"name":"Story"}}}
			],"nextPageToken":"ignored-over-max"}`))
		default:
			t.Fatalf("unexpected extra request")
		}
		requests++
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	result, err := poller.PollJira(context.Background(), testCredentials(), projectintegrations.JiraQueryPlan{
		ProjectID:  "project-1",
		Provider:   projectintegrations.ProviderJira,
		Kind:       projectintegrations.SyncKindInitialFull,
		JQL:        "project in (ACME)",
		Fields:     []string{"summary", "updated"},
		PageSize:   2,
		MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("poll jira: %v", err)
	}
	if requests != 2 || len(result.Items) != 3 {
		t.Fatalf("unexpected requests/items: requests=%d items=%#v", requests, result.Items)
	}
	if result.Items[0].ID != "10001" || result.Items[0].Key != "ACME-1" || result.Items[0].Type != "Task" || result.Items[0].Status != "Open" {
		t.Fatalf("unexpected first item: %#v", result.Items[0])
	}
	if result.Items[2].UpdatedAt.IsZero() || result.Items[2].UpdatedAt.Location() != time.UTC {
		t.Fatalf("expected UTC updated timestamp: %#v", result.Items[2])
	}
	if len(result.RichContent) != 0 {
		t.Fatalf("metadata-only poll should not emit rich content: %#v", result.RichContent)
	}
}

func TestPoller_PollJiraEmitsRichContentOnlyWhenConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		_, _ = w.Write([]byte(`{"issues":[
			{"id":"10001","key":"ACME-1","fields":{
				"updated":"2026-05-31T10:00:00.000+0000",
				"status":{"name":"Open"},
				"issuetype":{"name":"Task"},
				"summary":"Safe summary",
				"description":"Description body",
				"comment":{"comments":[{"body":"Comment body"}]},
				"emailAddress":"agent@example.invalid",
				"api_token":"synthetic-token-value"
			}}
		]}`))
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	result, err := poller.PollJira(context.Background(), testCredentials(), projectintegrations.JiraQueryPlan{
		ProjectID:         "project-1",
		Provider:          projectintegrations.ProviderJira,
		Kind:              projectintegrations.SyncKindInitialFull,
		JQL:               "project in (ACME)",
		Fields:            []string{"summary", "updated", "description", "comment", "emailAddress", "api_token"},
		PageSize:          10,
		MaxResults:        10,
		IncludeRichFields: true,
		IncludeComments:   true,
	})
	if err != nil {
		t.Fatalf("poll jira: %v", err)
	}
	if len(result.Items) != 1 || len(result.RichContent) != 1 {
		t.Fatalf("expected one item and rich payload, got %#v", result)
	}
	payload := result.RichContent[0]
	if payload.Item.ItemID != "10001" || payload.Item.ItemKey != "ACME-1" || payload.Item.Provider != projectintegrations.ProviderJira {
		t.Fatalf("unexpected rich item identity: %#v", payload.Item)
	}
	rendered := renderPollChunks(payload.Chunks)
	if !strings.Contains(rendered, "Safe summary") || !strings.Contains(rendered, "Description body") || !strings.Contains(rendered, "Comment body") {
		t.Fatalf("expected configured rich content, got %q", rendered)
	}
	for _, forbidden := range []string{"agent@example.invalid", "synthetic-token-value", "api_token", "emailAddress"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rich content leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestPoller_PollJiraMalformedIssueReturnsRedactedDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		_, _ = w.Write([]byte(`{"issues":[{"key":"ACME-LEAK","fields":{"summary":"FORBIDDEN_REMOTE_BODY_MARKER"}}]}`))
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	_, err := poller.PollJira(context.Background(), testCredentials(), projectintegrations.JiraQueryPlan{
		ProjectID:  "project-1",
		Provider:   projectintegrations.ProviderJira,
		JQL:        "project in (ACME)",
		PageSize:   50,
		MaxResults: 50,
	})
	var providerErr *projectintegrations.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if providerErr.Category != projectintegrations.ErrorCategoryDecodeFailed || providerErr.Operation != "extract_issue_metadata" {
		t.Fatalf("unexpected provider error: %#v", providerErr)
	}
	assertErrorOmits(t, err, "FORBIDDEN_REMOTE_BODY_MARKER", "ACME-LEAK", "synthetic-token-value", "agent@example.invalid")
}

func renderPollChunks(chunks []projectintegrations.RichContentChunk) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		builder.WriteString(chunk.FieldName)
		builder.WriteByte('=')
		builder.WriteString(chunk.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}
