package projectintegrations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
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

func TestRichContentGraphStore_GetRichContentItemReturnsBoundedChunks(t *testing.T) {
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
			{Name: "summary", Text: "Long searchable body"},
		},
	}
	chunks, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 1024, MaxChunkBytes: 128})
	if err != nil {
		t.Fatalf("chunk content: %v", err)
	}
	written, err := store.PutRichContentItem(ctx, item, chunks)
	if err != nil {
		t.Fatalf("put content: %v", err)
	}

	read, err := store.GetRichContentItem(ctx, "example-service", ProviderJira, "10001", RichContentReadOptions{MaxChunkBytes: 4})
	if err != nil {
		t.Fatalf("get content: %v", err)
	}
	if read.Artifact.ID != written.ArtifactID || read.Artifact.ItemKey != "ACME-1" || read.Artifact.ChunkCount != 1 {
		t.Fatalf("unexpected artifact: %#v", read.Artifact)
	}
	if len(read.Chunks) != 1 || read.Chunks[0].Text != "Long" || !read.Chunks[0].TextTruncated {
		t.Fatalf("expected bounded chunk, got %#v", read.Chunks)
	}
}

func TestRichContentGraphStore_SearchRichContentFindsProviderScopedMatches(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewRichContentGraphStore(graph)
	jiraItem := RichContentItem{
		ProjectID: "example-service",
		Provider:  ProviderJira,
		ItemID:    "10001",
		ItemKey:   "ACME-1",
		ItemType:  "Task",
		Fields:    []RichContentField{{Name: "summary", Text: "Billing export failed for route planner"}},
	}
	confluenceItem := RichContentItem{
		ProjectID: "example-service",
		Provider:  ProviderConfluence,
		ItemID:    "20001",
		ItemType:  "page",
		Fields:    []RichContentField{{Name: "title", Text: "Billing export runbook"}},
	}
	writeRichFixture(t, ctx, store, jiraItem)
	writeRichFixture(t, ctx, store, confluenceItem)

	results, err := store.SearchRichContent(ctx, "example-service", RichContentSearchOptions{
		Provider:        ProviderJira,
		Query:           "export",
		MaxResults:      10,
		MaxSnippetBytes: 12,
	})
	if err != nil {
		t.Fatalf("search content: %v", err)
	}
	if len(results) != 1 || results[0].Artifact.Provider != ProviderJira || results[0].Artifact.ItemKey != "ACME-1" {
		t.Fatalf("expected jira-only result, got %#v", results)
	}
	if !strings.Contains(strings.ToLower(results[0].Snippet), "export") || !results[0].SnippetTruncated {
		t.Fatalf("expected bounded snippet around match, got %#v", results[0])
	}
}

func TestRichContentGraphStore_SearchAndReadAreRedacted(t *testing.T) {
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
	}
	chunks := []RichContentChunk{
		{FieldName: "body", Text: "safe searchable context"},
		{FieldName: "comments", Text: "Bearer secret-token-1234567890"},
		{FieldName: "properties", Text: "agent@example.invalid"},
	}
	if _, err := store.PutRichContentItem(ctx, item, chunks); err != nil {
		t.Fatalf("put content: %v", err)
	}
	read, err := store.GetRichContentItem(ctx, "example-service", ProviderConfluence, "20001", RichContentReadOptions{MaxChunkBytes: 1024})
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	search, err := store.SearchRichContent(ctx, "example-service", RichContentSearchOptions{Query: "searchable", MaxResults: 10})
	if err != nil {
		t.Fatalf("search content: %v", err)
	}
	rendered := renderReadAndSearch(read, search)
	assertRichContains(t, rendered, "safe searchable context")
	assertRichMissing(t, rendered, "Bearer", "secret-token", "agent@example.invalid", "/home/mac")
}

func TestRichContentGraphStore_GetRichContentItemMissingIsStable(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewRichContentGraphStore(graph)
	_, err := store.GetRichContentItem(ctx, "example-service", ProviderJira, "10001", RichContentReadOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
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

func writeRichFixture(t *testing.T, ctx context.Context, store *RichContentGraphStore, item RichContentItem) {
	t.Helper()
	chunks, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 1024, MaxChunkBytes: 128})
	if err != nil {
		t.Fatalf("chunk content: %v", err)
	}
	if _, err := store.PutRichContentItem(ctx, item, chunks); err != nil {
		t.Fatalf("put content: %v", err)
	}
}

func renderReadAndSearch(read RichContentReadResult, search []RichContentSearchResult) string {
	var builder strings.Builder
	for _, chunk := range read.Chunks {
		builder.WriteString(chunk.Text)
		builder.WriteByte('\n')
	}
	for _, result := range search {
		builder.WriteString(result.Snippet)
		builder.WriteByte('\n')
	}
	return builder.String()
}
