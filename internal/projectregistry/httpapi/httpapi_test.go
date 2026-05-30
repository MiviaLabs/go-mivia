package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/httpapi"
)

func TestProjectRoutes_ListAndGetRedactRootPath(t *testing.T) {
	mux, projectID := newMux(t)

	listRes := httptest.NewRecorder()
	mux.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listRes.Code, listRes.Body.String())
	}
	assertProjectResponseSafe(t, listRes.Body.String())

	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID, nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}
	assertProjectResponseSafe(t, getRes.Body.String())
}

func TestProjectRoutes_CreateDigestRunMetadataOnly(t *testing.T) {
	mux, projectID := newMux(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/digest-runs", bytes.NewReader(nil))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, forbidden := range []string{"package main", "root_path", "content_sha256"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("digest response leaked %q: %s", forbidden, body)
		}
	}
	var run projectregistry.DigestRunMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode digest run: %v", err)
	}
	if run.Status != projectregistry.DigestStatusCompleted || run.FilesStored != 1 {
		t.Fatalf("unexpected digest run response: %#v", run)
	}
}

func TestProjectRoutes_UnknownProjectReturnsNotFound(t *testing.T) {
	mux, _ := newMux(t)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/projects/missing", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
}

func newMux(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "example-service",
		DisplayName:    "Example Service",
		Description:    "Synthetic local service",
		RootPath:       root,
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     projectregistry.DigestModeMetadataOnly,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
		Include:        []string{"**/*.go"},
		FollowSymlinks: false,
	}}, projectregistry.Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, registry, digest)
	return mux, "example-service"
}

func assertProjectResponseSafe(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"root_path", "canonical", "/tmp/", `\home\`, "include", "exclude"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("project response leaked %q: %s", forbidden, body)
		}
	}
}
