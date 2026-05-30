package projectregistry

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

func TestProjectGraphRouter_RoutesByProjectStorage(t *testing.T) {
	ctx := context.Background()
	persistentProjectRoot := t.TempDir()
	memoryProjectRoot := t.TempDir()
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "persistent_project",
			DisplayName:    "Persistent Project",
			RootPath:       persistentProjectRoot,
			Enabled:        true,
			GraphNamespace: "persistns",
			GraphStorage:   GraphStoragePersistent,
			DigestMode:     DigestModeContentGraph,
			UpdatePolicy:   UpdatePolicyManual,
		},
		{
			ID:             "memory_project",
			DisplayName:    "Memory Project",
			RootPath:       memoryProjectRoot,
			Enabled:        true,
			GraphNamespace: "memns",
			GraphStorage:   GraphStorageInMemory,
			DigestMode:     DigestModeContentGraph,
			UpdatePolicy:   UpdatePolicyManual,
		},
	}, Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  filepath.Join(t.TempDir(), "graph.lbug"),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	graphPath := filepath.Join(t.TempDir(), "graph.lbug")
	persistentGraph, err := ladybug.OpenPersistentGraph(graphPath)
	if err != nil {
		t.Fatalf("open persistent graph: %v", err)
	}
	router := NewProjectGraphRouter(registry, ladybug.NewMemoryGraph(), persistentGraph)
	if err := router.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap router: %v", err)
	}

	if err := router.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    "persistns:file",
		Properties: map[string]string{
			"project_id": "persistent_project",
			"id":         "persistns:file",
		},
	}); err != nil {
		t.Fatalf("put persistent node: %v", err)
	}
	if err := router.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    "memns:file",
		Properties: map[string]string{
			"project_id": "memory_project",
			"id":         "memns:file",
		},
	}); err != nil {
		t.Fatalf("put memory node: %v", err)
	}

	reopenedPersistent, err := ladybug.OpenPersistentGraph(graphPath)
	if err != nil {
		t.Fatalf("reopen persistent graph: %v", err)
	}
	restarted := NewProjectGraphRouter(registry, ladybug.NewMemoryGraph(), reopenedPersistent)
	persisted, err := restarted.GetNode(ctx, "RepoFile", "persistns:file")
	if err != nil {
		t.Fatalf("expected persistent node after restart: %v", err)
	}
	if persisted.Properties["project_id"] != "persistent_project" {
		t.Fatalf("unexpected persisted node: %#v", persisted)
	}
	if _, err := restarted.GetNode(ctx, "RepoFile", "memns:file"); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected in-memory project node to be absent after restart, got %v", err)
	}
}

func TestProjectGraphRouter_DefaultsProjectStorageToPersistent(t *testing.T) {
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "default_project",
			DisplayName:    "Default Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "defaultns",
			DigestMode:     DigestModeMetadataOnly,
			UpdatePolicy:   UpdatePolicyManual,
		},
	}, Options{LadybugPath: filepath.Join(t.TempDir(), "graph.lbug"), SQLitePath: ":memory:"})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	project, ok := registry.Get("default_project")
	if !ok {
		t.Fatal("expected project")
	}
	if project.GraphStorage != GraphStoragePersistent {
		t.Fatalf("expected persistent default, got %q", project.GraphStorage)
	}
}
