package confluence

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
)

func TestPoller_PollConfluenceBoundsRequestAndExtractsMetadata(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/wiki/rest/api/search" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		if r.URL.Query().Get("cql") != `space in ("ENG") and type=page` || r.URL.Query().Get("limit") != "2" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		requests++
		_, _ = w.Write([]byte(`{"results":[
			{"content":{"id":"20001","type":"page","status":"current","version":{"when":"2026-05-31T10:00:00Z"}}},
			{"content":{"id":"20002","type":"page","status":"archived","history":{"lastUpdated":{"when":"2026-05-31T10:01:00Z"}}}},
			{"content":{"id":"20003","type":"page","status":"current","version":{"when":"2026-05-31T10:02:00Z"}}}
		],"_links":{"next":"/wiki/rest/api/search?cursor=transient-cursor"}}`))
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	result, err := poller.PollConfluence(context.Background(), testCredentials(), projectintegrations.ConfluenceQueryPlan{
		ProjectID:  "project-1",
		Provider:   projectintegrations.ProviderConfluence,
		Kind:       projectintegrations.SyncKindInitialFull,
		CQL:        `space in ("ENG") and type=page`,
		PageSize:   50,
		MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("poll confluence: %v", err)
	}
	if requests != 1 || len(result.Items) != 2 {
		t.Fatalf("unexpected requests/items: requests=%d items=%#v", requests, result.Items)
	}
	if result.Items[0].ID != "20001" || result.Items[0].Type != "page" || result.Items[0].Status != "current" || result.Items[0].Key != "" {
		t.Fatalf("unexpected first item: %#v", result.Items[0])
	}
	if result.Items[1].UpdatedAt.IsZero() || result.Items[1].UpdatedAt.Location() != time.UTC {
		t.Fatalf("expected UTC updated timestamp: %#v", result.Items[1])
	}
	if len(result.RichContent) != 0 {
		t.Fatalf("metadata-only poll should not emit rich content: %#v", result.RichContent)
	}
}

func TestPoller_PollConfluencePaginatesUntilMaxResults(t *testing.T) {
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/wiki/rest/api/search" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		switch r.URL.Query().Get("cursor") {
		case "":
			if r.URL.Query().Get("limit") != "2" {
				t.Fatalf("unexpected first page limit: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"results":[
				{"content":{"id":"20001","type":"page","status":"current","version":{"when":"2026-05-31T10:00:00Z"}}},
				{"content":{"id":"20002","type":"page","status":"current","version":{"when":"2026-05-31T10:01:00Z"}}}
			],"_links":{"next":"/wiki/rest/api/search?cql=type%3Dpage&limit=2&cursor=page-2"}}`))
		case "page-2":
			if r.URL.Query().Get("limit") != "1" {
				t.Fatalf("unexpected second page limit: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"results":[
				{"content":{"id":"20003","type":"page","status":"current","version":{"when":"2026-05-31T10:02:00Z"}}}
			],"_links":{"next":"/wiki/rest/api/search?cursor=page-3"}}`))
		default:
			t.Fatalf("unexpected cursor %q", r.URL.Query().Get("cursor"))
		}
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	result, err := poller.PollConfluence(context.Background(), testCredentials(), projectintegrations.ConfluenceQueryPlan{
		ProjectID:  "project-1",
		Provider:   projectintegrations.ProviderConfluence,
		Kind:       projectintegrations.SyncKindInitialFull,
		CQL:        `space in ("ENG") and type=page`,
		PageSize:   2,
		MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("poll confluence: %v", err)
	}
	if got := strings.Join(cursors, ","); got != ",page-2" {
		t.Fatalf("unexpected cursors: %q", got)
	}
	if len(result.Items) != 3 || result.Items[2].ID != "20003" {
		t.Fatalf("unexpected paginated items: %#v", result.Items)
	}
}

func TestPoller_PollConfluenceFetchesPageDetailsForConfiguredRichContent(t *testing.T) {
	var searchRequests int
	var pageRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		switch r.URL.Path {
		case "/wiki/rest/api/search":
			searchRequests++
			if r.URL.Query().Get("limit") != "1" {
				t.Fatalf("unexpected search limit: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"results":[{"content":{"id":"20001","type":"page","status":"current","version":{"when":"2026-05-31T10:00:00Z"}}}]}`))
		case "/wiki/api/v2/pages/20001":
			pageRequests++
			if r.URL.Query().Get("body-format") != "storage" {
				t.Fatalf("unexpected body format: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
				"id":"20001",
				"type":"page",
				"title":"Runbook",
				"version":{"createdAt":"2026-05-31T10:00:00Z"},
				"body":{"storage":{"value":"Storage body"}},
				"labels":{"results":[{"name":"ops"}]},
				"properties":{"results":[{"key":"owner","value":{"displayName":"Ops Lead","email":"ops@example.invalid"}}]}
			}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	result, err := poller.PollConfluence(context.Background(), testCredentials(), projectintegrations.ConfluenceQueryPlan{
		ProjectID:          "project-1",
		Provider:           projectintegrations.ProviderConfluence,
		Kind:               projectintegrations.SyncKindInitialFull,
		CQL:                `space in ("ENG") and type=page`,
		PageSize:           1,
		MaxResults:         1,
		BodyRepresentation: "storage",
		IncludeBody:        true,
		IncludeLabels:      true,
		IncludeProperties:  true,
	})
	if err != nil {
		t.Fatalf("poll confluence: %v", err)
	}
	if searchRequests != 1 || pageRequests != 1 || len(result.Items) != 1 || len(result.RichContent) != 1 {
		t.Fatalf("unexpected requests/result: search=%d page=%d result=%#v", searchRequests, pageRequests, result)
	}
	payload := result.RichContent[0]
	if payload.Item.ItemID != "20001" || payload.Item.Provider != projectintegrations.ProviderConfluence {
		t.Fatalf("unexpected rich item: %#v", payload.Item)
	}
	if payload.Item.UpdatedAt.IsZero() {
		t.Fatalf("expected rich item updated timestamp: %#v", payload.Item)
	}
	rendered := renderPollChunks(payload.Chunks)
	for _, expected := range []string{"Runbook", "Storage body", "ops", "Ops Lead"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected %q in rich chunks: %s", expected, rendered)
		}
	}
	for _, forbidden := range []string{"ops@example.invalid", "synthetic-token-value", "/home/mac"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rich content leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestPoller_PollConfluenceMalformedResultReturnsRedactedDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBasicAuth(t, r)
		_, _ = w.Write([]byte(`{"results":[{"title":"FORBIDDEN_REMOTE_BODY_MARKER","content":{"status":"current"}}]}`))
	}))
	defer server.Close()

	poller := NewPoller(NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()}))
	_, err := poller.PollConfluence(context.Background(), testCredentials(), projectintegrations.ConfluenceQueryPlan{
		ProjectID:  "project-1",
		Provider:   projectintegrations.ProviderConfluence,
		CQL:        `space in ("ENG") and type=page`,
		PageSize:   50,
		MaxResults: 50,
	})
	var providerErr *projectintegrations.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if providerErr.Category != projectintegrations.ErrorCategoryDecodeFailed || providerErr.Operation != "extract_page_metadata" {
		t.Fatalf("unexpected provider error: %#v", providerErr)
	}
	assertErrorOmits(t, err, "FORBIDDEN_REMOTE_BODY_MARKER", "synthetic-token-value", "agent@example.invalid")
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
