package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dashboardconfig "github.com/MiviaLabs/go-mivia/internal/dashboardapp/config"
)

func TestNewServerUsesDashboardConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"projects":[]}`))
		case "/readyz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := dashboardconfig.Config{
		HTTPAddr:          "127.0.0.1:18081",
		UpstreamURL:       upstream.URL,
		MaxRequestBytes:   1024,
		RequestTimeout:    time.Second,
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   time.Second,
	}
	server, err := newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if server.Addr != cfg.HTTPAddr {
		t.Fatalf("expected addr %q, got %q", cfg.HTTPAddr, server.Addr)
	}

	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("expected proxied projects 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"projects":[]`) {
		t.Fatalf("expected upstream response, got %s", res.Body.String())
	}
}
