package research

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/research/provider"
	"github.com/MiviaLabs/go-mivia/internal/research/redaction"
	"github.com/MiviaLabs/go-mivia/internal/research/store"
)

var ErrInvalidInput = errors.New("invalid research input")

type Service struct {
	store store.MetadataStore
	now   func() time.Time
	newID func(string) string
}

type CreateSourceInput struct {
	ResearchRunID  string            `json:"research_run_id"`
	ArtifactRef    string            `json:"artifact_ref"`
	SourceType     string            `json:"source_type"`
	Summary        string            `json:"summary"`
	PolicyMetadata map[string]string `json:"policy_metadata,omitempty"`
}

func NewService(store store.MetadataStore) *Service {
	return &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
		newID: newID,
	}
}

func (svc *Service) CreateSource(ctx context.Context, input CreateSourceInput) (provider.SourceMetadata, error) {
	runID := strings.TrimSpace(input.ResearchRunID)
	if runID == "" {
		return provider.SourceMetadata{}, fmt.Errorf("%w: research_run_id is required", ErrInvalidInput)
	}
	artifactRef := redaction.RedactURL(strings.TrimSpace(input.ArtifactRef))
	if artifactRef == "" {
		return provider.SourceMetadata{}, fmt.Errorf("%w: artifact_ref is required", ErrInvalidInput)
	}
	sourceType := strings.TrimSpace(input.SourceType)
	if sourceType == "" {
		return provider.SourceMetadata{}, fmt.Errorf("%w: source_type is required", ErrInvalidInput)
	}
	summary := redaction.Redact(strings.TrimSpace(input.Summary))
	if summary == "" {
		return provider.SourceMetadata{}, fmt.Errorf("%w: summary is required", ErrInvalidInput)
	}
	policy := map[string]string{
		"raw_content":    "not_stored",
		"pii_prohibited": "true",
	}
	for key, value := range input.PolicyMetadata {
		policy[strings.TrimSpace(key)] = redaction.Redact(strings.TrimSpace(value))
	}
	source := provider.SourceMetadata{
		ID:             svc.newID("source"),
		ResearchRunID:  runID,
		ArtifactRef:    artifactRef,
		SourceType:     sourceType,
		Summary:        summary,
		ContentHash:    redaction.HashContent(artifactRef + "\n" + sourceType + "\n" + summary),
		RetrievedAt:    svc.now(),
		PolicyMetadata: policy,
	}
	return svc.store.SaveSource(ctx, source)
}

func (svc *Service) SaveProviderSources(ctx context.Context, sources []provider.SourceMetadata) ([]provider.SourceMetadata, error) {
	out := make([]provider.SourceMetadata, 0, len(sources))
	for _, source := range sources {
		if source.ID == "" {
			source.ID = svc.newID("source")
		}
		source.ArtifactRef = redaction.RedactURL(source.ArtifactRef)
		source.Summary = redaction.Redact(source.Summary)
		if source.ContentHash == "" {
			source.ContentHash = redaction.HashContent(source.ArtifactRef + "\n" + source.SourceType + "\n" + source.Summary)
		}
		if source.RetrievedAt.IsZero() {
			source.RetrievedAt = svc.now()
		}
		saved, err := svc.store.SaveSource(ctx, source)
		if err != nil {
			return nil, err
		}
		out = append(out, saved)
	}
	return out, nil
}

func (svc *Service) GetSource(ctx context.Context, id string) (provider.SourceMetadata, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return provider.SourceMetadata{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return svc.store.GetSource(ctx, id)
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate id: %w", err))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
