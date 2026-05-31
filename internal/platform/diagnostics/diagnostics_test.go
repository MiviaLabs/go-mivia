package diagnostics_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/diagnostics"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
)

type fakeIngestionSnapshotter struct {
	snapshot projectingestion.DiagnosticsSnapshot
}

func (fake fakeIngestionSnapshotter) IngestionDiagnostics() projectingestion.DiagnosticsSnapshot {
	return fake.snapshot
}

func TestRegisterRoutes_IngestionDiagnosticsAreRedacted(t *testing.T) {
	svc := diagnostics.NewService(fakeIngestionSnapshotter{snapshot: projectingestion.DiagnosticsSnapshot{
		Scheduler: projectingestion.SchedulerDiagnostics{
			QueueDepth:           2,
			ActiveTaskCount:      1,
			ProjectWideTaskCount: map[string]int{"example-service": 1},
		},
		Watchers: []projectingestion.WatchState{{
			ProjectID:             "example-service",
			Status:                projectingestion.WatchStatusLive,
			WatchedDirectoryCount: 3,
		}},
		Stages: map[string]projectingestion.StageDiagnostic{
			"storage.state_write": {Count: 2, TotalMillis: 10, MaxMillis: 7, LastMillis: 3},
		},
	}}, diagnostics.RuntimeOptions{})

	mux := http.NewServeMux()
	diagnostics.RegisterRoutes(mux, svc)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/ingestion", nil)
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	body := res.Body.String()
	for _, forbidden := range []string{"/home/mac", `C:\`, "MIVIA_", "token", "credential", "password", "package main", "prompt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("diagnostics leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "storage.state_write") || !strings.Contains(body, "example-service") {
		t.Fatalf("expected safe diagnostics fields, got %s", body)
	}
}

func TestEnabledRequiresDebugAndLoopback(t *testing.T) {
	if diagnostics.Enabled(false, "127.0.0.1:8080") {
		t.Fatal("debug disabled must not enable diagnostics")
	}
	if diagnostics.Enabled(true, "0.0.0.0:8080") {
		t.Fatal("non-loopback bind must not enable diagnostics")
	}
	if !diagnostics.Enabled(true, "127.0.0.1:8080") {
		t.Fatal("debug loopback bind should enable diagnostics")
	}
}
