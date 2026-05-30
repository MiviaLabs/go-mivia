package provider

import (
	"context"
	"time"
)

type RunRequest struct {
	ResearchRunID string
	GoalSummary   string
}

type SourceMetadata struct {
	ID             string            `json:"id"`
	ResearchRunID  string            `json:"research_run_id"`
	ArtifactRef    string            `json:"artifact_ref"`
	SourceType     string            `json:"source_type"`
	Summary        string            `json:"summary"`
	ContentHash    string            `json:"content_hash"`
	RetrievedAt    time.Time         `json:"retrieved_at"`
	PolicyMetadata map[string]string `json:"policy_metadata"`
}

type Provider interface {
	Collect(context.Context, RunRequest) ([]SourceMetadata, error)
}
