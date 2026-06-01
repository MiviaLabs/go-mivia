package ladybug

import (
	"context"
	"errors"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

func TestPebbleGraphPersistsNodesRelationshipsAndFileScopedDeletes(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	graph, err := OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("open pebble graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	fileID := "project:file"
	symbolA := Node{
		Label: "CodeSymbol",
		ID:    "project:file:a",
		Properties: map[string]string{
			"project_id":   "project",
			"repo_file_id": fileID,
			"name":         "A",
		},
	}
	symbolB := Node{
		Label: "CodeSymbol",
		ID:    "project:file:b",
		Properties: map[string]string{
			"project_id":   "project",
			"repo_file_id": fileID,
			"name":         "B",
		},
	}
	if err := graph.PutNode(ctx, symbolA); err != nil {
		t.Fatalf("put symbol A: %v", err)
	}
	if err := graph.PutNode(ctx, symbolB); err != nil {
		t.Fatalf("put symbol B: %v", err)
	}
	if err := graph.PutRelationship(ctx, Relationship{
		Type: "SYMBOL_CALLS_SYMBOL",
		From: NodeRef{Label: "CodeSymbol", ID: symbolA.ID},
		To:   NodeRef{Label: "CodeSymbol", ID: symbolB.ID},
		Properties: map[string]string{
			"project_id": "project",
		},
	}); err != nil {
		t.Fatalf("put relationship: %v", err)
	}
	if err := graph.Close(); err != nil {
		t.Fatalf("close graph: %v", err)
	}

	reopened, err := OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("reopen pebble graph: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.GetNode(ctx, "CodeSymbol", symbolA.ID); err != nil {
		t.Fatalf("expected persisted symbol A: %v", err)
	}
	relationships, err := reopened.ListRelationships(ctx, "SYMBOL_CALLS_SYMBOL", RelationshipFilter{
		From:       &NodeRef{Label: "CodeSymbol", ID: symbolA.ID},
		Properties: map[string]string{"project_id": "project"},
	})
	if err != nil {
		t.Fatalf("list relationships: %v", err)
	}
	if len(relationships) != 1 || relationships[0].To.ID != symbolB.ID {
		t.Fatalf("unexpected relationships: %#v", relationships)
	}
	if err := reopened.DeleteDerivedFileNodes(ctx, "project", fileID); err != nil {
		t.Fatalf("delete derived: %v", err)
	}
	if _, err := reopened.GetNode(ctx, "CodeSymbol", symbolA.ID); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected symbol A deleted, got %v", err)
	}
	relationships, err = reopened.ListRelationships(ctx, "SYMBOL_CALLS_SYMBOL", RelationshipFilter{
		From:       &NodeRef{Label: "CodeSymbol", ID: symbolA.ID},
		Properties: map[string]string{"project_id": "project"},
	})
	if err != nil {
		t.Fatalf("list relationships after delete: %v", err)
	}
	if len(relationships) != 0 {
		t.Fatalf("expected attached relationships deleted, got %#v", relationships)
	}
}

func TestPebbleGraphBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	graph, err := OpenPebbleGraph(t.TempDir())
	if err != nil {
		t.Fatalf("open pebble graph: %v", err)
	}
	defer graph.Close()
	err = graph.Batch(ctx, func(batch Graph) error {
		if err := batch.PutNode(ctx, Node{
			Label:      "RepoFile",
			ID:         "project:file",
			Properties: map[string]string{"project_id": "project"},
		}); err != nil {
			return err
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("expected batch error")
	}
	if _, err := graph.GetNode(ctx, "RepoFile", "project:file"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected aborted batch not committed, got %v", err)
	}
}
