package projectintegrations

import (
	"context"
	"testing"
)

func TestPhase0BReadLocalJiraContentDoesNotPollProvider(t *testing.T) {
	runner := &fakePollRunner{}
	rich := &phase0BRichContentReader{
		result: RichContentReadResult{
			Artifact: RichContentArtifact{
				ID:      "integration-artifact-phase0b",
				ItemID:  "10001",
				ItemKey: "PROJ-1044",
			},
			Chunks: []RichContentChunkView{{
				ItemKey:   "PROJ-1044",
				FieldName: "summary",
				Text:      "Bounded governed intake",
			}},
		},
	}
	service := newTestServiceWithOptions(t, nil, ServiceOptions{Runner: runner, RichContent: rich}, testIntegrationProject())

	result, err := service.ReadLocalContent(context.Background(), LocalReadInput{
		ProjectID:     "project-1",
		Provider:      ProviderJira,
		ItemIDOrKey:   "PROJ-1044",
		MaxChunkBytes: 256,
		MaxChunks:     5,
	})
	if err != nil {
		t.Fatalf("read local content: %v", err)
	}
	if runner.called || runner.submitCalled {
		t.Fatalf("local read must not poll provider: %#v", runner)
	}
	if rich.calls != 1 || rich.item != "PROJ-1044" || result.Artifact.ItemKey != "PROJ-1044" {
		t.Fatalf("expected one local rich-content read, got calls=%d item=%q result=%#v", rich.calls, rich.item, result)
	}
	if rich.provider != ProviderJira || rich.projectID != "project-1" {
		t.Fatalf("local read used wrong provider/project boundary: %#v", rich)
	}
	if rich.options.MaxChunkBytes != 256 || rich.options.MaxChunks != 5 {
		t.Fatalf("local read lost bounded read options: %#v", rich.options)
	}
}

type phase0BRichContentReader struct {
	result    RichContentReadResult
	calls     int
	projectID string
	provider  Provider
	item      string
	options   RichContentReadOptions
}

func (reader *phase0BRichContentReader) GetRichContentItem(_ context.Context, projectID string, provider Provider, item string, options RichContentReadOptions) (RichContentReadResult, error) {
	reader.calls++
	reader.projectID = projectID
	reader.provider = provider
	reader.item = item
	reader.options = options
	return reader.result, nil
}

func (reader *phase0BRichContentReader) SearchRichContent(context.Context, string, RichContentSearchOptions) ([]RichContentSearchResult, error) {
	return nil, nil
}
