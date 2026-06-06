package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesServeStaticFallback(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, Options{})

	index := httptest.NewRecorder()
	mux.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("expected index 200, got %d", index.Code)
	}
	for _, want := range []string{"Mivia Dashboard", "./app.js", "./styles.css"} {
		if !strings.Contains(index.Body.String(), want) {
			t.Fatalf("expected index to contain %q", want)
		}
	}

	app := httptest.NewRecorder()
	mux.ServeHTTP(app, httptest.NewRequest(http.MethodGet, "/dashboard/app.js", nil))
	if app.Code != http.StatusOK {
		t.Fatalf("expected app 200, got %d", app.Code)
	}
	if !strings.Contains(app.Body.String(), "EventSource") || !strings.Contains(app.Body.String(), "/api/v1/projects") {
		t.Fatalf("expected fallback app to load read-only project state and SSE")
	}
}

func TestRoutesFallbackForDashboardSubpaths(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, Options{})

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/dashboard/projects/example", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("expected dashboard fallback 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "Mivia Dashboard") {
		t.Fatalf("expected dashboard fallback index, got %s", res.Body.String())
	}
}

func TestRoutesUseExternalStaticDirWhenPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(`"use strict"; window.externalStatic = true;`), 0o600); err != nil {
		t.Fatalf("write external app: %v", err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, Options{StaticDir: dir})

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/dashboard/app.js", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("expected external app 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "externalStatic") {
		t.Fatalf("expected external static app, got %s", res.Body.String())
	}
}

func TestRoutesApplySecurityHeadersWithoutCORS(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, Options{})

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if got := res.Header().Get("Content-Security-Policy"); !strings.Contains(got, "connect-src 'self'") {
		t.Fatalf("expected restrictive CSP, got %q", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("dashboard shell should not set CORS headers, got %q", got)
	}
}

func TestRoutesProxyAPIScopeOnly(t *testing.T) {
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			t.Fatalf("unexpected proxied path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux := http.NewServeMux()
	RegisterRoutes(mux, Options{Proxy: proxy})

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected proxied API status, got %d", res.Code)
	}

	mcp := httptest.NewRecorder()
	mux.ServeHTTP(mcp, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if mcp.Code != http.StatusNotFound {
		t.Fatalf("expected mcp to stay outside dashboard proxy, got %d", mcp.Code)
	}
}

func TestFallbackAssetsAvoidForbiddenFirstReleaseMarkers(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, Options{})

	for _, path := range []string{"/dashboard/", "/dashboard/app.js", "/dashboard/styles.css"} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("expected %s 200, got %d", path, res.Code)
		}
		body := res.Body.String()
		for _, forbidden := range []string{
			"method: \"POST\"",
			"method: 'POST'",
			"/compile",
			"/score",
			"/reuse-events",
			"/workspace/files/edit",
			"/workspace/files/create",
			"/workspace/files/delete",
			"api_token",
			"content_sha256",
			"root_path",
			"provider payload",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s contained forbidden first-release marker %q", path, forbidden)
			}
		}
	}
}
