package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/research/provider"
	"github.com/MiviaLabs/go-mivia/internal/research/store"
)

func TestLadybugMetadataStore_DeduplicatesByHash(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	metadataStore := store.NewLadybugMetadataStore(graph)
	source := provider.SourceMetadata{
		ID:            "source_one",
		ResearchRunID: "research_run_test",
		ArtifactRef:   "fixture://source",
		SourceType:    "web_fixture",
		Summary:       "Redacted summary",
		ContentHash:   "sha256:duplicate",
		RetrievedAt:   time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}

	first, err := metadataStore.SaveSource(context.Background(), source)
	if err != nil {
		t.Fatalf("save source: %v", err)
	}
	source.ID = "source_two"
	second, err := metadataStore.SaveSource(context.Background(), source)
	if err != nil {
		t.Fatalf("save duplicate source: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("expected duplicate hash to return existing source, got %s want %s", second.ID, first.ID)
	}
}

func TestLadybugMetadataStore_DeduplicatesByHashAfterStoreRebuild(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	firstStore := store.NewLadybugMetadataStore(graph)
	source := provider.SourceMetadata{
		ID:            "source_one",
		ResearchRunID: "research_run_test",
		ArtifactRef:   "fixture://source",
		SourceType:    "web_fixture",
		Summary:       "Redacted summary",
		ContentHash:   "sha256:rebuilt",
		RetrievedAt:   time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
	first, err := firstStore.SaveSource(context.Background(), source)
	if err != nil {
		t.Fatalf("save source: %v", err)
	}

	restartedStore := store.NewLadybugMetadataStore(graph)
	source.ID = "source_two"
	second, err := restartedStore.SaveSource(context.Background(), source)
	if err != nil {
		t.Fatalf("save duplicate source after rebuild: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected rebuilt hash index to return existing source, got %s want %s", second.ID, first.ID)
	}
}

func TestLadybugMetadataStore_DeduplicatesByHashAfterPersistentGraphReopen(t *testing.T) {
	ctx := context.Background()
	graphPath := filepath.Join(t.TempDir(), "graph.lbug")
	graph, err := ladybug.OpenPersistentGraph(graphPath)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	firstStore := store.NewLadybugMetadataStore(graph)
	source := provider.SourceMetadata{
		ID:            "source_one",
		ResearchRunID: "research_run_test",
		ArtifactRef:   "fixture://source",
		SourceType:    "web_fixture",
		Summary:       "Redacted summary",
		ContentHash:   "sha256:persistent",
		RetrievedAt:   time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
	first, err := firstStore.SaveSource(ctx, source)
	if err != nil {
		t.Fatalf("save source: %v", err)
	}

	reopened, err := ladybug.OpenPersistentGraph(graphPath)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	restartedStore := store.NewLadybugMetadataStore(reopened)
	source.ID = "source_two"
	second, err := restartedStore.SaveSource(ctx, source)
	if err != nil {
		t.Fatalf("save duplicate source after persistent reopen: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected persistent hash index to return existing source, got %s want %s", second.ID, first.ID)
	}
}

func TestLadybugMetadataStore_PersistsPolicyMetadata(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	metadataStore := store.NewLadybugMetadataStore(graph)
	source := provider.SourceMetadata{
		ID:            "source_policy",
		ResearchRunID: "research_run_test",
		ArtifactRef:   "fixture://source",
		SourceType:    "web_fixture",
		Summary:       "Redacted summary",
		ContentHash:   "sha256:policy",
		RetrievedAt:   time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		PolicyMetadata: map[string]string{
			"raw_content": "not_stored",
			"provider":    "fixture",
		},
	}
	if _, err := metadataStore.SaveSource(context.Background(), source); err != nil {
		t.Fatalf("save source: %v", err)
	}

	fetched, err := metadataStore.GetSource(context.Background(), source.ID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if fetched.PolicyMetadata["provider"] != "fixture" {
		t.Fatalf("expected persisted policy metadata, got %#v", fetched.PolicyMetadata)
	}
}
