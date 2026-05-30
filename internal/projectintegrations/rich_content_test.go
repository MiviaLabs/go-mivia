package projectintegrations

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestExtractJiraRichContentIncludesConfiguredFieldsOnly(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"10001",
		"key":"ACME-1",
		"fields":{
			"summary":"Fix billing export",
			"description":{"type":"doc","content":[{"content":[{"type":"text","text":"Description body"}]}]},
			"customfield_12345":{"value":"Customer tier gold"},
			"comment":{"comments":[{"body":{"content":[{"content":[{"text":"Comment body"}]}]}}]},
			"emailAddress":"agent@example.invalid",
			"api_token":"token-should-not-appear-123456"
		}
	}`)
	plan := JiraQueryPlan{
		ProjectID:         "example-service",
		Provider:          ProviderJira,
		Fields:            []string{"summary", "description", "customfield_12345", "comment", "emailAddress", "api_token"},
		IncludeRichFields: true,
		IncludeComments:   true,
	}

	item, chunks, err := ExtractJiraRichContent(plan, raw, RichContentOptions{MaxItemTextBytes: 4096, MaxChunkBytes: 64})
	if err != nil {
		t.Fatalf("extract jira content: %v", err)
	}
	if item.ItemID != "10001" || item.ItemKey != "ACME-1" || item.Provider != ProviderJira {
		t.Fatalf("unexpected item identity: %#v", item)
	}
	rendered := renderRichChunks(chunks)
	assertRichContains(t, rendered, "Fix billing export", "Description body", "Customer tier gold", "Comment body")
	assertRichMissing(t, rendered, "agent@example.invalid", "token-should-not-appear", "api_token", "emailAddress")
}

func TestExtractJiraRichContentOmitsRichFieldsWhenDisabled(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"10001",
		"key":"ACME-1",
		"fields":{
			"summary":"Safe summary",
			"description":"Description body",
			"comment":{"comments":[{"body":"Comment body"}]}
		}
	}`)
	plan := JiraQueryPlan{
		ProjectID: "example-service",
		Provider:  ProviderJira,
		Fields:    []string{"summary", "description", "comment"},
	}

	_, chunks, err := ExtractJiraRichContent(plan, raw, RichContentOptions{MaxItemTextBytes: 4096, MaxChunkBytes: 128})
	if err != nil {
		t.Fatalf("extract jira content: %v", err)
	}
	rendered := renderRichChunks(chunks)
	assertRichContains(t, rendered, "Safe summary")
	assertRichMissing(t, rendered, "Description body", "Comment body")
}

func TestExtractConfluenceRichContentHonorsConfiguredFlags(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"20001",
		"type":"page",
		"title":"Runbook",
		"body":{"storage":{"value":"Storage body"},"atlas_doc_format":{"value":"ADF body"}},
		"comments":{"results":[{"body":{"value":"Footer comment"}}]},
		"labels":{"results":[{"name":"ops"},{"name":"internal"}]},
		"properties":{"results":[{"key":"owner","value":{"displayName":"Ops Lead","email":"ops@example.invalid"}}]}
	}`)
	plan := ConfluenceQueryPlan{
		ProjectID:          "example-service",
		Provider:           ProviderConfluence,
		BodyRepresentation: "storage",
		IncludeBody:        true,
		IncludeComments:    true,
		IncludeLabels:      true,
		IncludeProperties:  true,
	}

	item, chunks, err := ExtractConfluenceRichContent(plan, raw, RichContentOptions{MaxItemTextBytes: 4096, MaxChunkBytes: 96})
	if err != nil {
		t.Fatalf("extract confluence content: %v", err)
	}
	if item.ItemID != "20001" || item.ItemType != "page" || item.Provider != ProviderConfluence {
		t.Fatalf("unexpected item identity: %#v", item)
	}
	rendered := renderRichChunks(chunks)
	assertRichContains(t, rendered, "Runbook", "Storage body", "Footer comment", "ops", "internal", "Ops Lead")
	assertRichMissing(t, rendered, "ADF body", "ops@example.invalid")
}

func TestExtractConfluenceRichContentOmitsRichFieldsWhenDisabled(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"20001",
		"title":"Runbook",
		"body":{"storage":{"value":"Storage body"}},
		"comments":{"results":[{"body":{"value":"Footer comment"}}]},
		"labels":{"results":[{"name":"ops"}]},
		"properties":{"results":[{"key":"owner","value":"Ops Lead"}]}
	}`)
	plan := ConfluenceQueryPlan{
		ProjectID: "example-service",
		Provider:  ProviderConfluence,
	}

	_, chunks, err := ExtractConfluenceRichContent(plan, raw, RichContentOptions{MaxItemTextBytes: 4096, MaxChunkBytes: 128})
	if err != nil {
		t.Fatalf("extract confluence content: %v", err)
	}
	rendered := renderRichChunks(chunks)
	assertRichContains(t, rendered, "Runbook")
	assertRichMissing(t, rendered, "Storage body", "Footer comment", "ops", "Ops Lead")
}

func TestChunkRichContentItemBoundsDeterministicallyAndSuppressesForbiddenMaterial(t *testing.T) {
	item := RichContentItem{
		ProjectID: "example-service",
		Provider:  ProviderJira,
		ItemID:    "10001",
		ItemKey:   "ACME-1",
		ItemType:  "Task",
		Fields: []RichContentField{
			{Name: "summary", Text: "abcdef"},
			{Name: "description", Text: "Bearer secret-token-1234567890"},
			{Name: "comment", Text: "/home/mac/private/root"},
			{Name: "customfield_12345", Text: "ghijkl"},
		},
	}

	chunks, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 12, MaxChunkBytes: 3})
	if err != nil {
		t.Fatalf("chunk content: %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %#v", chunks)
	}
	gotIDs := []string{chunks[0].ID, chunks[1].ID, chunks[2].ID, chunks[3].ID}
	again, err := ChunkRichContentItem(item, RichContentOptions{MaxItemTextBytes: 12, MaxChunkBytes: 3})
	if err != nil {
		t.Fatalf("chunk content again: %v", err)
	}
	for i := range gotIDs {
		if gotIDs[i] != again[i].ID {
			t.Fatalf("chunk id not deterministic at %d: %s != %s", i, gotIDs[i], again[i].ID)
		}
		if len([]byte(chunks[i].Text)) > 3 {
			t.Fatalf("chunk exceeds max bytes: %#v", chunks[i])
		}
	}
	rendered := renderRichChunks(chunks)
	assertRichContains(t, rendered, "abc", "def", "ghi", "jkl")
	assertRichMissing(t, rendered, "Bearer", "secret-token", "/home/mac")
}

func TestExtractRichContentErrorsAreRedacted(t *testing.T) {
	_, _, err := ExtractJiraRichContent(JiraQueryPlan{ProjectID: "example-service", Provider: ProviderJira}, json.RawMessage(`{"id":`), RichContentOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	assertRichMissing(t, fmt.Sprint(err), "agent@example.invalid", "secret-token", `{"id":`)
}

func renderRichChunks(chunks []RichContentChunk) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		builder.WriteString(chunk.FieldName)
		builder.WriteByte('=')
		builder.WriteString(chunk.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func assertRichContains(t *testing.T, value string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			t.Fatalf("expected %q to contain %q", value, needle)
		}
	}
}

func assertRichMissing(t *testing.T, value string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			t.Fatalf("expected %q to omit %q", value, needle)
		}
	}
}
