package research_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research"
	researchstore "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/store"
)

func TestCreateSource_RedactsBeforePersisting(t *testing.T) {
	svc := newService(t)
	source, err := svc.CreateSource(context.Background(), research.CreateSourceInput{
		ResearchRunID: "research_run_test",
		ArtifactRef:   "https://example.test/doc?token=abc123",
		SourceType:    "web_fixture",
		Summary:       "Contact user@example.com",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if strings.Contains(source.ArtifactRef, "abc123") || strings.Contains(source.Summary, "user@example.com") {
		t.Fatalf("source leaked raw sensitive data: %#v", source)
	}
}

func newService(t *testing.T) *research.Service {
	t.Helper()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return research.NewService(researchstore.NewLadybugMetadataStore(graph))
}
