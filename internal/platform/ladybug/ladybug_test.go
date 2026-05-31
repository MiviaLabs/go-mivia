package ladybug

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

func TestMemoryGraph_ListNodesByRepoFileIDTracksUpdates(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}

	if err := graph.PutNode(ctx, Node{
		Label: "CodeSymbol",
		ID:    "symbol-1",
		Properties: map[string]string{
			"project_id":   "project-1",
			"repo_file_id": "file-a",
		},
	}); err != nil {
		t.Fatalf("put initial symbol: %v", err)
	}
	if err := graph.PutNode(ctx, Node{
		Label: "CodeSymbol",
		ID:    "symbol-1",
		Properties: map[string]string{
			"project_id":   "project-1",
			"repo_file_id": "file-b",
		},
	}); err != nil {
		t.Fatalf("move symbol: %v", err)
	}

	oldFileNodes, err := graph.ListNodes(ctx, "CodeSymbol", map[string]string{"repo_file_id": "file-a"})
	if err != nil {
		t.Fatalf("list old file symbols: %v", err)
	}
	if len(oldFileNodes) != 0 {
		t.Fatalf("expected old repo_file_id index to be empty, got %#v", oldFileNodes)
	}
	newFileNodes, err := graph.ListNodes(ctx, "CodeSymbol", map[string]string{"repo_file_id": "file-b"})
	if err != nil {
		t.Fatalf("list new file symbols: %v", err)
	}
	if len(newFileNodes) != 1 || newFileNodes[0].ID != "symbol-1" {
		t.Fatalf("expected moved symbol from repo_file_id index, got %#v", newFileNodes)
	}
}

func TestMemoryGraph_ListRelationshipsByEndpointTracksUpdates(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	from := NodeRef{Label: "CodeSymbol", ID: "caller"}
	firstTo := NodeRef{Label: "CodeSymbol", ID: "callee-1"}
	secondTo := NodeRef{Label: "CodeSymbol", ID: "callee-2"}

	if err := graph.PutRelationship(ctx, Relationship{Type: "SYMBOL_CALLS_SYMBOL", From: from, To: firstTo}); err != nil {
		t.Fatalf("put first relationship: %v", err)
	}
	if err := graph.PutRelationship(ctx, Relationship{Type: "SYMBOL_CALLS_SYMBOL", From: from, To: secondTo}); err != nil {
		t.Fatalf("put second relationship: %v", err)
	}
	if err := graph.DeleteNodes(ctx, "CodeSymbol", map[string]string{"id": "unused"}); err != nil {
		t.Fatalf("unrelated delete should not fail: %v", err)
	}

	calleeRels, err := graph.ListRelationships(ctx, "SYMBOL_CALLS_SYMBOL", RelationshipFilter{To: &secondTo})
	if err != nil {
		t.Fatalf("list by callee: %v", err)
	}
	if len(calleeRels) != 1 || calleeRels[0].To != secondTo {
		t.Fatalf("expected one relationship from endpoint index, got %#v", calleeRels)
	}
	callerRels, err := graph.ListRelationships(ctx, "SYMBOL_CALLS_SYMBOL", RelationshipFilter{From: &from})
	if err != nil {
		t.Fatalf("list by caller: %v", err)
	}
	if len(callerRels) != 2 {
		t.Fatalf("expected two caller relationships from endpoint index, got %#v", callerRels)
	}
}
