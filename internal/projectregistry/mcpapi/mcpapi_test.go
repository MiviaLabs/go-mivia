package mcpapi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/mcpapi"
)

func TestCallTool_ListProjectsRedactsRootPath(t *testing.T) {
	registry, digest := newServices(t)

	result, err := mcpapi.CallTool(context.Background(), registry, digest, "projects.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call list tool: %v", err)
	}
	body := marshalResult(t, result)
	for _, forbidden := range []string{"root_path", "canonical", "package main"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("projects.list leaked %q: %s", forbidden, body)
		}
	}
}

func TestCallTool_DigestAndReadDigestRunResource(t *testing.T) {
	registry, digest := newServices(t)

	result, err := mcpapi.CallTool(context.Background(), registry, digest, "projects.digest", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("call digest tool: %v", err)
	}
	body := marshalResult(t, result)
	for _, forbidden := range []string{"package main", "content_sha256", "root_path"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("projects.digest leaked %q: %s", forbidden, body)
		}
	}
	runID := result["structuredContent"].(projectregistry.DigestRunMetadata).ID

	resource, err := mcpapi.ReadResource(context.Background(), registry, digest, "mivialabs://projects/example-service/digest-runs/"+runID)
	if err != nil {
		t.Fatalf("read digest resource: %v", err)
	}
	resourceBody := marshalResult(t, resource)
	if !strings.Contains(resourceBody, runID) {
		t.Fatalf("expected digest run id in resource: %s", resourceBody)
	}
	if strings.Contains(resourceBody, "package main") || strings.Contains(resourceBody, "content_sha256") {
		t.Fatalf("digest resource leaked content markers: %s", resourceBody)
	}
}

func TestReadResource_Project(t *testing.T) {
	registry, digest := newServices(t)

	resource, err := mcpapi.ReadResource(context.Background(), registry, digest, "mivialabs://projects/example-service")
	if err != nil {
		t.Fatalf("read project resource: %v", err)
	}
	body := marshalResult(t, resource)
	if !strings.Contains(body, "example-service") {
		t.Fatalf("expected project id in resource: %s", body)
	}
	if strings.Contains(body, "root_path") {
		t.Fatalf("project resource leaked root path: %s", body)
	}
}

func newServices(t *testing.T) (*projectregistry.Registry, *projectregistry.DigestService) {
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
	return registry, projectregistry.NewDigestService(registry, graph)
}

func marshalResult(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return string(encoded)
}
