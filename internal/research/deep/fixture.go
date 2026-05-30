package deep

import (
	"context"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/provider"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/redaction"
)

type FixtureProvider struct {
	Now func() time.Time
}

func (fixture FixtureProvider) Collect(_ context.Context, req provider.RunRequest) ([]provider.SourceMetadata, error) {
	now := time.Now().UTC()
	if fixture.Now != nil {
		now = fixture.Now().UTC()
	}
	summary := redaction.Redact(req.GoalSummary)
	artifactRef := redaction.RedactURL("fixture://deep-research/source")
	hash := redaction.HashContent(artifactRef + "\n" + summary)
	return []provider.SourceMetadata{
		{
			ResearchRunID: req.ResearchRunID,
			ArtifactRef:   artifactRef,
			SourceType:    "deep_research_fixture",
			Summary:       summary,
			ContentHash:   hash,
			RetrievedAt:   now,
			PolicyMetadata: map[string]string{
				"provider":       "fixture",
				"live_network":   "false",
				"raw_content":    "not_returned",
				"pii_prohibited": "true",
			},
		},
	}, nil
}
