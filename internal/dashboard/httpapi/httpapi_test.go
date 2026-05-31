package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutes_RootRedirectsToDashboard(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))

	if res.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", res.Code)
	}
	if got := res.Header().Get("Location"); got != "/dashboard" {
		t.Fatalf("expected /dashboard redirect, got %q", got)
	}
}

func TestRoutes_DashboardServesEmbeddedAssets(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	index := httptest.NewRecorder()
	mux.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", index.Code, index.Body.String())
	}
	body := index.Body.String()
	for _, want := range []string{"Mivia Dashboard", "./app.js", "./styles.css"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected index to contain %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"root_path", "content_sha256", "api_token", "/home/", "\\\\wsl.localhost"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("dashboard index leaked forbidden marker %q", forbidden)
		}
	}

	app := httptest.NewRecorder()
	mux.ServeHTTP(app, httptest.NewRequest(http.MethodGet, "/dashboard/app.js", nil))
	if app.Code != http.StatusOK {
		t.Fatalf("expected app asset 200, got %d", app.Code)
	}
	if !strings.Contains(app.Body.String(), "/api/v1/projects") {
		t.Fatalf("expected app to fetch project metadata")
	}
}

func TestRoutes_UnknownRootSubpathNotFound(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.Code)
	}
}

func TestRoutes_CanCoexistWithMethodlessMCPRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected MCP route to remain reachable, got %d", res.Code)
	}
}
