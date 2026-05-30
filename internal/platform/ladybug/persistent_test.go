package ladybug_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

func TestPersistentGraph_ReloadsNodesAndRelationships(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.lbug")

	graph, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	if err := graph.PutNode(ctx, ladybug.Node{
		Label: "Project",
		ID:    "project-1",
		Properties: map[string]string{
			"id":        "project-1",
			"namespace": "ns",
		},
	}); err != nil {
		t.Fatalf("put project node: %v", err)
	}
	if err := graph.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    "file-1",
		Properties: map[string]string{
			"project_id": "project-1",
			"file_id":    "file-1",
		},
	}); err != nil {
		t.Fatalf("put file node: %v", err)
	}
	if err := graph.PutRelationship(ctx, ladybug.Relationship{
		Type: "PROJECT_HAS_REPO_FILE",
		From: ladybug.NodeRef{Label: "Project", ID: "project-1"},
		To:   ladybug.NodeRef{Label: "RepoFile", ID: "file-1"},
	}); err != nil {
		t.Fatalf("put relationship: %v", err)
	}

	reopened, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	node, err := reopened.GetNode(ctx, "RepoFile", "file-1")
	if err != nil {
		t.Fatalf("get reloaded node: %v", err)
	}
	if node.Properties["project_id"] != "project-1" {
		t.Fatalf("expected project_id to persist, got %#v", node.Properties)
	}
	listed, err := reopened.ListNodes(ctx, "RepoFile", map[string]string{"project_id": "project-1"})
	if err != nil {
		t.Fatalf("list reloaded nodes: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "file-1" {
		t.Fatalf("expected persisted file list, got %#v", listed)
	}
	if _, err := reopened.GetRelationship(ctx, "PROJECT_HAS_REPO_FILE", ladybug.NodeRef{Label: "Project", ID: "project-1"}, ladybug.NodeRef{Label: "RepoFile", ID: "file-1"}); err != nil {
		t.Fatalf("get reloaded relationship: %v", err)
	}
}

func TestPersistentGraph_RejectsInvalidStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.lbug")
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write corrupt graph: %v", err)
	}

	_, err := ladybug.OpenPersistentGraph(path)
	if err == nil {
		t.Fatal("expected corrupt graph store error")
	}
}

func TestPersistentGraph_MissingNode(t *testing.T) {
	graph, err := ladybug.OpenPersistentGraph(filepath.Join(t.TempDir(), "graph.lbug"))
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}

	_, err = graph.GetNode(context.Background(), "Project", "missing")
	if !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}
