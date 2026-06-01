package projectregistry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
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

func TestProjectGraphRouter_RoutesPersistentProjectsToSeparateGraphs(t *testing.T) {
	ctx := context.Background()
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "first-project",
			DisplayName:    "First Project",
			RootPath:       firstRoot,
			Enabled:        true,
			GraphNamespace: "first",
			GraphStorage:   GraphStoragePersistent,
			DigestMode:     DigestModeContentGraph,
			UpdatePolicy:   UpdatePolicyManual,
		},
		{
			ID:             "second-project",
			DisplayName:    "Second Project",
			RootPath:       secondRoot,
			Enabled:        true,
			GraphNamespace: "second",
			GraphStorage:   GraphStoragePersistent,
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

	basePath := filepath.Join(t.TempDir(), "mivialabs.lbug")
	firstPath, err := ProjectGraphPath(basePath, "first-project")
	if err != nil {
		t.Fatalf("first path: %v", err)
	}
	secondPath, err := ProjectGraphPath(basePath, "second-project")
	if err != nil {
		t.Fatalf("second path: %v", err)
	}
	firstGraph, err := ladybug.OpenPersistentGraph(firstPath)
	if err != nil {
		t.Fatalf("open first graph: %v", err)
	}
	secondGraph, err := ladybug.OpenPersistentGraph(secondPath)
	if err != nil {
		t.Fatalf("open second graph: %v", err)
	}

	router := NewProjectScopedGraphRouter(registry, ladybug.NewMemoryGraph(), []ProjectGraphBackend{
		{ProjectID: "first-project", Graph: firstGraph, StorageKey: "first-project"},
		{ProjectID: "second-project", Graph: secondGraph, StorageKey: "second-project"},
	})
	if err := router.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap router: %v", err)
	}
	if err := router.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    "first:file",
		Properties: map[string]string{
			"project_id": "first-project",
			"id":         "first:file",
		},
	}); err != nil {
		t.Fatalf("put first node: %v", err)
	}
	if err := router.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    "second:file",
		Properties: map[string]string{
			"project_id": "second-project",
			"id":         "second:file",
		},
	}); err != nil {
		t.Fatalf("put second node: %v", err)
	}

	reopenedFirst, err := ladybug.OpenPersistentGraph(firstPath)
	if err != nil {
		t.Fatalf("reopen first graph: %v", err)
	}
	if _, err := reopenedFirst.GetNode(ctx, "RepoFile", "first:file"); err != nil {
		t.Fatalf("expected first node in first graph: %v", err)
	}
	if _, err := reopenedFirst.GetNode(ctx, "RepoFile", "second:file"); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected second node absent from first graph, got %v", err)
	}

	reopenedSecond, err := ladybug.OpenPersistentGraph(secondPath)
	if err != nil {
		t.Fatalf("reopen second graph: %v", err)
	}
	if _, err := reopenedSecond.GetNode(ctx, "RepoFile", "second:file"); err != nil {
		t.Fatalf("expected second node in second graph: %v", err)
	}
	if _, err := reopenedSecond.GetNode(ctx, "RepoFile", "first:file"); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected first node absent from second graph, got %v", err)
	}
}

func TestProjectGraphRouter_BatchProjectUsesOnlyTargetProjectScopedBackend(t *testing.T) {
	ctx := context.Background()
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "first-project",
			DisplayName:    "First Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "first",
			GraphStorage:   GraphStoragePersistent,
			DigestMode:     DigestModeContentGraph,
			UpdatePolicy:   UpdatePolicyManual,
		},
		{
			ID:             "second-project",
			DisplayName:    "Second Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "second",
			GraphStorage:   GraphStoragePersistent,
			DigestMode:     DigestModeContentGraph,
			UpdatePolicy:   UpdatePolicyManual,
		},
		{
			ID:             "memory-project",
			DisplayName:    "Memory Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "memory",
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

	memory := ladybug.NewMemoryGraph()
	firstGraph := &batchSpyGraph{name: "first", Graph: ladybug.NewMemoryGraph(), requireBatch: true}
	secondGraph := &batchSpyGraph{name: "second", Graph: ladybug.NewMemoryGraph(), requireBatch: true}
	router := NewProjectScopedGraphRouter(registry, memory, []ProjectGraphBackend{
		{ProjectID: "first-project", Graph: firstGraph, StorageKey: "first-project"},
		{ProjectID: "second-project", Graph: secondGraph, StorageKey: "second-project"},
	})
	if err := router.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap router: %v", err)
	}

	if err := router.BatchProject(ctx, "first-project", func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, ladybug.Node{
			Label: "RepoFile",
			ID:    "first:file",
			Properties: map[string]string{
				"project_id": "first-project",
				"id":         "first:file",
			},
		})
	}); err != nil {
		t.Fatalf("batch first project: %v", err)
	}
	if err := router.BatchProject(ctx, "second-project", func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, ladybug.Node{
			Label: "RepoFile",
			ID:    "second:file",
			Properties: map[string]string{
				"project_id": "second-project",
				"id":         "second:file",
			},
		})
	}); err != nil {
		t.Fatalf("batch second project: %v", err)
	}
	if err := router.BatchProject(ctx, "memory-project", func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, ladybug.Node{
			Label: "RepoFile",
			ID:    "memory:file",
			Properties: map[string]string{
				"project_id": "memory-project",
				"id":         "memory:file",
			},
		})
	}); err != nil {
		t.Fatalf("batch memory project: %v", err)
	}

	if firstGraph.batchCalls != 1 {
		t.Fatalf("expected first backend batch once, got %d", firstGraph.batchCalls)
	}
	if secondGraph.batchCalls != 1 {
		t.Fatalf("expected second backend batch once, got %d", secondGraph.batchCalls)
	}
	if len(firstGraph.putNodeIDs) != 1 || firstGraph.putNodeIDs[0] != "first-project:first:file" {
		t.Fatalf("expected first backend to receive only first project node, got %#v", firstGraph.putNodeIDs)
	}
	if len(secondGraph.putNodeIDs) != 1 || secondGraph.putNodeIDs[0] != "second-project:second:file" {
		t.Fatalf("expected second backend to receive only second project node, got %#v", secondGraph.putNodeIDs)
	}
	if _, err := memory.GetNode(ctx, "RepoFile", "memory:file"); err != nil {
		t.Fatalf("expected memory project node in shared memory backend: %v", err)
	}
	if _, err := firstGraph.GetNode(ctx, "RepoFile", "memory:file"); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected memory project node absent from first backend, got %v", err)
	}
	if _, err := secondGraph.GetNode(ctx, "RepoFile", "first:file"); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected first project node absent from second backend, got %v", err)
	}
}

func TestProjectGraphRouter_RoutesMetadataOnlyPersistentProjectToMemory(t *testing.T) {
	ctx := context.Background()
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "metadata-project",
			DisplayName:    "Metadata Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "metadata",
			GraphStorage:   GraphStoragePersistent,
			DigestMode:     DigestModeMetadataOnly,
			UpdatePolicy:   UpdatePolicyManual,
		},
	}, Options{
		LadybugPath: filepath.Join(t.TempDir(), "graph.lbug"),
		SQLitePath:  ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	graphPath := filepath.Join(t.TempDir(), "metadata.lbug")
	persistentGraph, err := ladybug.OpenPersistentGraph(graphPath)
	if err != nil {
		t.Fatalf("open persistent graph: %v", err)
	}
	router := NewProjectScopedGraphRouter(registry, ladybug.NewMemoryGraph(), []ProjectGraphBackend{
		{ProjectID: "metadata-project", Graph: persistentGraph, StorageKey: "metadata-project"},
	})
	if err := router.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap router: %v", err)
	}
	if err := router.PutNode(ctx, ladybug.Node{
		Label: "Project",
		ID:    "metadata-project",
		Properties: map[string]string{
			"id": "metadata-project",
		},
	}); err != nil {
		t.Fatalf("put metadata-only project node: %v", err)
	}

	reopenedPersistent, err := ladybug.OpenPersistentGraph(graphPath)
	if err != nil {
		t.Fatalf("reopen persistent graph: %v", err)
	}
	if _, err := reopenedPersistent.GetNode(ctx, "Project", "metadata-project"); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected metadata-only project node absent from persistent graph, got %v", err)
	}
}

type batchSpyGraph struct {
	ladybug.Graph
	name         string
	requireBatch bool
	inBatch      bool
	batchCalls   int
	putNodeIDs   []string
}

func (graph *batchSpyGraph) Batch(ctx context.Context, fn func(ladybug.Graph) error) error {
	if fn == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.batchCalls++
	wasInBatch := graph.inBatch
	graph.inBatch = true
	err := fn(graph)
	graph.inBatch = wasInBatch
	return err
}

func (graph *batchSpyGraph) PutNode(ctx context.Context, node ladybug.Node) error {
	if graph.requireBatch && !graph.inBatch {
		return fmt.Errorf("%s backend put node outside batch", graph.name)
	}
	graph.putNodeIDs = append(graph.putNodeIDs, node.Properties["project_id"]+":"+node.ID)
	return graph.Graph.PutNode(ctx, node)
}

func TestProjectGraphRouter_GraphStorageDiagnosticsAreRedacted(t *testing.T) {
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "persistent-project",
			DisplayName:    "Persistent Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "persistent",
			GraphStorage:   GraphStoragePersistent,
			DigestMode:     DigestModeContentGraph,
			UpdatePolicy:   UpdatePolicyManual,
		},
		{
			ID:             "memory-project",
			DisplayName:    "Memory Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "memory",
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

	router := NewProjectScopedGraphRouter(registry, ladybug.NewMemoryGraph(), []ProjectGraphBackend{
		{ProjectID: "persistent-project", Graph: ladybug.NewMemoryGraph(), StorageKey: "persistent-project"},
	})
	diagnostics := router.GraphStorageDiagnostics()
	if len(diagnostics) != 2 {
		t.Fatalf("expected two diagnostics, got %#v", diagnostics)
	}
	if diagnostics[0].Backend != "persistent_pebble_project" || diagnostics[0].StorageKey != "persistent-project" {
		t.Fatalf("unexpected persistent diagnostic: %#v", diagnostics[0])
	}
	if diagnostics[1].Backend != "in_memory_shared" || diagnostics[1].StorageKey != "" {
		t.Fatalf("unexpected memory diagnostic: %#v", diagnostics[1])
	}
}

func TestProjectGraphRouter_GraphStorageDiagnosticsIncludeLazyLifecycleState(t *testing.T) {
	ctx := context.Background()
	registry, err := NewRegistry([]config.Project{
		{
			ID:             "persistent-project",
			DisplayName:    "Persistent Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "persistent",
			GraphStorage:   GraphStoragePersistent,
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

	backend := ladybug.NewLazyPebbleGraph(t.TempDir(), ladybug.NewPebbleGraphLRU(1))
	if err := backend.PutNode(ctx, ladybug.Node{
		Label:      "RepoFile",
		ID:         "persistent:file",
		Properties: map[string]string{"project_id": "persistent-project"},
	}); err != nil {
		t.Fatalf("open lazy backend: %v", err)
	}
	router := NewProjectScopedGraphRouter(registry, ladybug.NewMemoryGraph(), []ProjectGraphBackend{
		{ProjectID: "persistent-project", Graph: backend, StorageKey: "persistent-project"},
	})

	diagnostics := router.GraphStorageDiagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %#v", diagnostics)
	}
	if diagnostics[0].StorageKey != "persistent-project" || !diagnostics[0].Open || diagnostics[0].Leases != 0 || diagnostics[0].OpenTotal != 1 {
		t.Fatalf("unexpected lazy lifecycle diagnostic: %#v", diagnostics[0])
	}
}
