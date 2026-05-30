package confluence

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
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
