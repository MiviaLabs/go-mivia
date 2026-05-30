package web_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/research/provider"
	"github.com/MiviaLabs/go-mivia/internal/research/web"
)

func TestFixtureProvider_Collect_RedactsSummaryAndUsesNoNetwork(t *testing.T) {
	fixture := web.FixtureProvider{}
	sources, err := fixture.Collect(context.Background(), provider.RunRequest{
		ResearchRunID: "research_run_test",
		GoalSummary:   "Summarize user@example.com",
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected one source, got %d", len(sources))
	}
	if strings.Contains(sources[0].Summary, "user@example.com") {
		t.Fatalf("summary leaked raw email: %s", sources[0].Summary)
	}
	if sources[0].PolicyMetadata["live_network"] != "false" {
		t.Fatalf("expected fixture provider to declare no network")
	}
}
