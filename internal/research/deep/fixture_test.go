package deep_test

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/research/deep"
	"github.com/MiviaLabs/go-mivia/internal/research/provider"
)

func TestFixtureProvider_Collect_DeclaresNoNetwork(t *testing.T) {
	fixture := deep.FixtureProvider{}
	sources, err := fixture.Collect(context.Background(), provider.RunRequest{
		ResearchRunID: "research_run_test",
		GoalSummary:   "Fixture-only deep research metadata",
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected one source, got %d", len(sources))
	}
	if sources[0].PolicyMetadata["live_network"] != "false" {
		t.Fatalf("expected fixture provider to declare no network")
	}
}
