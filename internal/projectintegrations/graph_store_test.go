package projectintegrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

func TestRichContentGraphStore_PutRichContentItemWritesArtifactAndChunks(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewRichContentGraphStore(graph)
	item := RichContentItem{
		ProjectID: "example-service",
		Provider:  ProviderJira,
		ItemID:    "10001",
		ItemKey:   "ACME-1",
		ItemType:  "Task",
		UpdatedAt: time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		Fields: []RichContentField{
			{Name: "summary", Text: "Fix export"},
			{Name: "description", Text: "Detailed body"},
		},
	}
	chunks, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 1024, MaxChunkBytes: 64})
	if err != nil {
		t.Fatalf("chunk content: %v", err)
	}

	result, err := store.PutRichContentItem(ctx, item, chunks)
	if err != nil {
		t.Fatalf("put rich content item: %v", err)
	}
	if result.ArtifactID == "" || result.ChunksWritten != len(chunks) || result.ContentSHA256 == "" {
		t.Fatalf("unexpected graph result: %#v", result)
	}
	artifact, err := graph.GetNode(ctx, "IntegrationArtifact", result.ArtifactID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.Properties["project_id"] != "example-service" ||
		artifact.Properties["provider"] != "jira" ||
		artifact.Properties["item_id"] != "10001" ||
		artifact.Properties["item_key"] != "ACME-1" ||
		artifact.Properties["chunk_count"] != "2" {
		t.Fatalf("unexpected artifact props: %#v", artifact.Properties)
	}
	graphChunks, err := graph.ListNodes(ctx, "IntegrationContentChunk", map[string]string{"project_id": "example-service", "artifact_id": result.ArtifactID})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(graphChunks) != 2 {
		t.Fatalf("expected 2 chunks, got %#v", graphChunks)
	}
	rendered := renderGraphChunkText(graphChunks)
	assertRichContains(t, rendered, "Fix export", "Detailed body")
	assertRichMissing(t, rendered, "agent@example.invalid", "Bearer", "/home/mac")
	relationships, err := graph.ListRelationships(ctx, "INTEGRATION_ARTIFACT_HAS_CHUNK", ladybug.RelationshipFilter{
		From: &ladybug.NodeRef{Label: "IntegrationArtifact", ID: result.ArtifactID},
	})
	if err != nil {
		t.Fatalf("list relationships: %v", err)
	}
	if len(relationships) != 2 {
		t.Fatalf("expected chunk relationships, got %#v", relationships)
	}
}

func TestRichContentGraphStore_ReplacesPriorChunksForItem(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewRichContentGraphStore(graph)
	item := RichContentItem{
		ProjectID: "example-service",
		Provider:  ProviderConfluence,
		ItemID:    "20001",
		ItemType:  "page",
		Fields: []RichContentField{
			{Name: "title", Text: "Old title"},
			{Name: "body", Text: "Old body"},
		},
	}
	firstChunks, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 1024, MaxChunkBytes: 64})
	if err != nil {
		t.Fatalf("chunk first content: %v", err)
	}
	first, err := store.PutRichContentItem(ctx, item, firstChunks)
	if err != nil {
		t.Fatalf("put first content: %v", err)
	}

	item.Fields = []RichContentField{{Name: "title", Text: "New title"}}
	secondChunks, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 1024, MaxChunkBytes: 64})
	if err != nil {
		t.Fatalf("chunk second content: %v", err)
	}
	second, err := store.PutRichContentItem(ctx, item, secondChunks)
	if err != nil {
		t.Fatalf("put second content: %v", err)
	}
	if second.ArtifactID != first.ArtifactID {
		t.Fatalf("expected stable artifact ID, got %s then %s", first.ArtifactID, second.ArtifactID)
	}
	graphChunks, err := graph.ListNodes(ctx, "IntegrationContentChunk", map[string]string{"project_id": "example-service", "artifact_id": second.ArtifactID})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	rendered := renderGraphChunkText(graphChunks)
	assertRichContains(t, rendered, "New title")
	assertRichMissing(t, rendered, "Old title", "Old body")
}

func TestRichContentGraphStore_FiltersForbiddenChunkMaterial(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewRichContentGraphStore(graph)
	item := RichContentItem{
		ProjectID: "example-service",
		Provider:  ProviderJira,
		ItemID:    "10001",
		ItemKey:   "ACME-1",
		ItemType:  "Task",
	}
	chunks := []RichContentChunk{
		{FieldName: "summary", Text: "Safe text"},
		{FieldName: "description", Text: "Bearer secret-token-1234567890"},
		{FieldName: "comment", Text: "local root /home/mac/private"},
		{FieldName: "emailAddress", Text: "agent@example.invalid"},
	}

	result, err := store.PutRichContentItem(ctx, item, chunks)
	if err != nil {
		t.Fatalf("put content: %v", err)
	}
	if result.ChunksWritten != 1 {
		t.Fatalf("expected one safe chunk, got %#v", result)
	}
	graphChunks, err := graph.ListNodes(ctx, "IntegrationContentChunk", map[string]string{"project_id": "example-service", "artifact_id": result.ArtifactID})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	rendered := renderGraphChunkText(graphChunks)
	assertRichContains(t, rendered, "Safe text")
	assertRichMissing(t, rendered, "Bearer", "secret-token", "/home/mac", "agent@example.invalid", "emailAddress")
}

func renderGraphChunkText(nodes []ladybug.Node) string {
	var builder strings.Builder
	for _, node := range nodes {
		builder.WriteString(node.Properties["field_name"])
		builder.WriteByte('=')
		builder.WriteString(node.Properties["text"])
		builder.WriteByte('\n')
	}
	return builder.String()
}
