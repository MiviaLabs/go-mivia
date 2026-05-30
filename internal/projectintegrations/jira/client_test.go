package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
)

func TestClient_SearchIssuesSendsBasicAuthAndJQLRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected headers: %#v", r.Header)
		}
		var request SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.JQL != "project in (ABC)" || request.MaxResults != 50 || request.NextPageToken != "cursor-1" {
			t.Fatalf("unexpected search request: %#v", request)
		}
		if len(request.Fields) != 2 || request.Fields[0] != "summary" || request.Fields[1] != "status" {
			t.Fatalf("unexpected fields: %#v", request.Fields)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"nextPageToken":"cursor-2"}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	response, err := client.SearchIssues(context.Background(), testCredentials(), SearchRequest{
		JQL:           "project in (ABC)",
		Fields:        []string{"summary", "status"},
		MaxResults:    50,
		NextPageToken: "cursor-1",
	})
	if err != nil {
		t.Fatalf("search issues: %v", err)
	}
	if response.NextPageToken != "cursor-2" || len(response.Issues) != 1 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestClient_GetIssueSendsFieldAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/ABC-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		assertBasicAuth(t, r)
		if r.URL.Query().Get("fields") != "summary,status" {
			t.Fatalf("unexpected fields query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"key":"ABC-1"}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	response, err := client.GetIssue(context.Background(), testCredentials(), "ABC-1", []string{"summary", "status"})
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if !strings.Contains(string(response.Raw), "ABC-1") {
		t.Fatalf("unexpected issue response: %s", response.Raw)
	}
}

func TestClient_MapsStatusWithoutLeakingResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "synthetic-token-value should not leak", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	_, err := client.SearchIssues(context.Background(), testCredentials(), SearchRequest{})
	var providerErr *projectintegrations.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if providerErr.Category != projectintegrations.ErrorCategoryRateLimited || providerErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unexpected provider error: %#v", providerErr)
	}
	if providerErr.RetryAfter.String() != "7s" {
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
			_, err := client.SearchIssues(context.Background(), testCredentials(), SearchRequest{})
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
