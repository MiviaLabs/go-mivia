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

func TestBootstrapSchema_ProjectDigestMetadataLabels(t *testing.T) {
	bootstrap := schema.BootstrapSchema()

	assertLabel(t, bootstrap, "Project")
	assertLabel(t, bootstrap, "DigestRun")
	assertLabel(t, bootstrap, "RepoFile")
	assertRelationship(t, bootstrap, "PROJECT_HAS_REPO_FILE", "Project", "RepoFile")
	assertRelationship(t, bootstrap, "PROJECT_HAS_DIGEST_RUN", "Project", "DigestRun")
}

func assertLabel(t *testing.T, bootstrap schema.GraphSchema, expected string) {
	t.Helper()
	for _, label := range bootstrap.NodeLabels {
		if label == expected {
			return
		}
	}
	t.Fatalf("expected label %q in %#v", expected, bootstrap.NodeLabels)
}

func assertRelationship(t *testing.T, bootstrap schema.GraphSchema, expectedType string, expectedFrom string, expectedTo string) {
	t.Helper()
	for _, rel := range bootstrap.Relationships {
		if rel.Type == expectedType && rel.From == expectedFrom && rel.To == expectedTo {
			return
		}
	}
	t.Fatalf("expected relationship %q from %q to %q in %#v", expectedType, expectedFrom, expectedTo, bootstrap.Relationships)
}
