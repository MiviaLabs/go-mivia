package confluence

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
)

func TestClient_SearchPagesSendsBasicAuthAndCQLRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/wiki/rest/api/search" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected accept header: %s", r.Header.Get("Accept"))
		}
		if r.URL.Query().Get("cql") != `space in ("ENG") and type=page` || r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("cursor") != "next-cursor" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"123"}],"_links":{"next":"/wiki/rest/api/search?cursor=next"}}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	response, err := client.SearchPages(context.Background(), testCredentials(), `space in ("ENG") and type=page`, 25, "next-cursor")
	if err != nil {
		t.Fatalf("search pages: %v", err)
	}
	if len(response.Results) != 1 || response.NextCursor() != "next" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestClient_GetPageSendsBodyRepresentation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/wiki/api/v2/pages/123" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		if r.URL.Query().Get("body-format") != "storage" {
			t.Fatalf("unexpected body format: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"id":"123","title":"Synthetic Page"}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	response, err := client.GetPage(context.Background(), testCredentials(), "123", "storage")
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	if !strings.Contains(string(response.Raw), "Synthetic Page") {
		t.Fatalf("unexpected page response: %s", response.Raw)
	}
}

func TestClient_MapsStatusWithoutLeakingResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "9")
		http.Error(w, "synthetic-token-value should not leak", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	_, err := client.SearchPages(context.Background(), testCredentials(), "type=page", 10)
	var providerErr *projectintegrations.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if providerErr.Category != projectintegrations.ErrorCategoryRateLimited || providerErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unexpected provider error: %#v", providerErr)
	}
	if providerErr.RetryAfter.String() != "9s" {
		t.Fatalf("expected retry-after, got %s", providerErr.RetryAfter)
	}
	assertErrorOmits(t, err, "synthetic-token-value", "agent@example.invalid")
}

func TestClient_MapsAuthPermissionAndNotFoundStatuses(t *testing.T) {
	tests := []struct {
		status   int
		category projectintegrations.ErrorCategory
	}{
		{status: http.StatusUnauthorized, category: projectintegrations.ErrorCategoryAuthFailed},
		{status: http.StatusForbidden, category: projectintegrations.ErrorCategoryPermissionDenied},
		{status: http.StatusNotFound, category: projectintegrations.ErrorCategoryNotFound},
	}
	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "provider body", tt.status)
			}))
			defer server.Close()

			client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
			_, err := client.SearchPages(context.Background(), testCredentials(), "type=page", 10)
			var providerErr *projectintegrations.ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("expected provider error, got %v", err)
			}
			if providerErr.Category != tt.category || providerErr.StatusCode != tt.status {
				t.Fatalf("unexpected provider error: %#v", providerErr)
			}
			assertErrorOmits(t, err, "provider body")
		})
	}
}

func assertBasicAuth(t *testing.T, r *http.Request) {
	t.Helper()
	email, token, ok := r.BasicAuth()
	if !ok {
		t.Fatal("missing basic auth")
	}
	if email != "agent@example.invalid" || token != "synthetic-token-value" {
		t.Fatalf("unexpected basic auth values")
	}
}

func testCredentials() projectintegrations.Credentials {
	return projectintegrations.Credentials{
		Email:    "agent@example.invalid",
		APIToken: "synthetic-token-value",
	}
}

func assertErrorOmits(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	for _, value := range forbidden {
		if value != "" && strings.Contains(message, value) {
			t.Fatalf("error leaked %q: %s", value, message)
		}
	}
}
