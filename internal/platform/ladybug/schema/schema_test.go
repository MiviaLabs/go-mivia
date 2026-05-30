package schema_test

import (
	"context"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

func TestBootstrapSchema_Idempotent(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	bootstrap := schema.BootstrapSchema()

	if err := graph.Bootstrap(context.Background(), bootstrap); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := graph.Bootstrap(context.Background(), bootstrap); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	snapshot := graph.SchemaSnapshot()
	if len(snapshot.NodeLabels) != len(bootstrap.NodeLabels) {
		t.Fatalf("expected %d labels, got %d", len(bootstrap.NodeLabels), len(snapshot.NodeLabels))
	}
	if len(snapshot.Relationships) != len(bootstrap.Relationships) {
		t.Fatalf("expected %d relationships, got %d", len(bootstrap.Relationships), len(snapshot.Relationships))
	}
}
