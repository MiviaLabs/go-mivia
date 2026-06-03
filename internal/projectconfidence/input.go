package projectconfidence

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

type EvidenceClaimReader interface {
	GetClaim(ctx context.Context, projectID string, claimID string) (projectevidence.ClaimRecord, error)
}

type ContextHealthProvider interface {
	ContextHealth(ctx context.Context, projectID string) (projectreliability.ContextHealth, error)
}

type ClaimCheckProvider interface {
	Check(ctx context.Context, request projectreliability.ClaimCheckRequest) (projectreliability.ClaimCheckResult, error)
}

type ImpactAnalysisProvider interface {
	Analyze(ctx context.Context, request projectreliability.ImpactAnalysisRequest) (projectreliability.ImpactAnalysis, error)
}

type ReliabilityInputAdapter struct {
	evidence EvidenceClaimReader
	health   ContextHealthProvider
	checker  ClaimCheckProvider
	impact   ImpactAnalysisProvider
}

type ReliabilityInputOptions struct {
	ProjectID        string
	ClaimID          string
	ChangedPaths     []string
	ClaimCheckDocs   []projectreliability.ClaimDocument
	ClaimCheckPaths  []string
	ClaimCheckTools  []string
	ClaimCheckRoutes []string
	IncludeVerified  bool
}

type ReliabilityInputs struct {
	Claim      projectevidence.ClaimRecord
	Health     projectreliability.ContextHealth
	ClaimCheck *projectreliability.ClaimCheckResult
	Impact     *projectreliability.ImpactAnalysis
}

func NewReliabilityInputAdapter(evidence EvidenceClaimReader, health ContextHealthProvider, checker ClaimCheckProvider, impact ImpactAnalysisProvider) *ReliabilityInputAdapter {
	return &ReliabilityInputAdapter{evidence: evidence, health: health, checker: checker, impact: impact}
}

func (adapter *ReliabilityInputAdapter) Build(ctx context.Context, options ReliabilityInputOptions) (ReliabilityInputs, error) {
	if adapter == nil {
		return ReliabilityInputs{}, fmt.Errorf("%w: reliability input adapter is required", ErrInvalidInput)
	}
	if adapter.evidence == nil {
		return ReliabilityInputs{}, fmt.Errorf("%w: evidence claim reader is required", ErrInvalidInput)
	}
	if adapter.health == nil {
		return ReliabilityInputs{}, fmt.Errorf("%w: context health provider is required", ErrInvalidInput)
	}
	projectID, err := safeRefIdentifier(options.ProjectID, "project_id")
	if err != nil {
		return ReliabilityInputs{}, err
	}
	claimID, err := safeRefIdentifier(options.ClaimID, "claim_id")
	if err != nil {
		return ReliabilityInputs{}, err
	}
	record, err := adapter.evidence.GetClaim(ctx, projectID, claimID)
	if err != nil {
		return ReliabilityInputs{}, err
	}
	if record.Claim.ProjectID != "" && record.Claim.ProjectID != projectID {
		return ReliabilityInputs{}, fmt.Errorf("%w: claim project mismatch", ErrInvalidInput)
	}
	if record.Claim.ID != "" && record.Claim.ID != claimID {
		return ReliabilityInputs{}, fmt.Errorf("%w: claim id mismatch", ErrInvalidInput)
	}
	health, err := adapter.health.ContextHealth(ctx, projectID)
	if err != nil {
		return ReliabilityInputs{}, err
	}
	inputs := ReliabilityInputs{Claim: record, Health: health}
	if adapter.checker != nil && (len(options.ClaimCheckDocs) > 0 || len(options.ClaimCheckPaths) > 0) {
		request, err := claimCheckRequest(projectID, options)
		if err != nil {
			return ReliabilityInputs{}, err
		}
		result, err := adapter.checker.Check(ctx, request)
		if err != nil {
			return ReliabilityInputs{}, err
		}
		inputs.ClaimCheck = &result
	}
	changedPaths, err := confidenceChangedPaths(record, options.ChangedPaths)
	if err != nil {
		return ReliabilityInputs{}, err
	}
	if adapter.impact != nil && len(changedPaths) > 0 {
		impact, err := adapter.impact.Analyze(ctx, projectreliability.ImpactAnalysisRequest{ProjectID: projectID, ChangedPaths: changedPaths})
		if err != nil {
			return ReliabilityInputs{}, err
		}
		inputs.Impact = &impact
	}
	return inputs, nil
}

func (svc *Service) ScoreClaimWithInputs(ctx context.Context, adapter *ReliabilityInputAdapter, options ReliabilityInputOptions) (ConfidenceAssessment, error) {
	inputs, err := adapter.Build(ctx, options)
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	return svc.ScoreClaim(ctx, inputs.Claim, inputs.Health, inputs.ClaimCheck, inputs.Impact)
}

func claimCheckRequest(projectID string, options ReliabilityInputOptions) (projectreliability.ClaimCheckRequest, error) {
	docs := make([]projectreliability.ClaimDocument, 0, len(options.ClaimCheckDocs))
	for _, doc := range options.ClaimCheckDocs {
		path, err := safeClaimCheckPath(doc.Path)
		if err != nil {
			return projectreliability.ClaimCheckRequest{}, err
		}
		docs = append(docs, projectreliability.ClaimDocument{Path: path, Text: doc.Text})
	}
	paths := make([]string, 0, len(options.ClaimCheckPaths))
	for _, path := range options.ClaimCheckPaths {
		normalized, err := safeClaimCheckPath(path)
		if err != nil {
			return projectreliability.ClaimCheckRequest{}, err
		}
		paths = append(paths, normalized)
	}
	return projectreliability.ClaimCheckRequest{
		ProjectID:       projectID,
		Documents:       docs,
		SelectedPaths:   paths,
		KnownTools:      append([]string(nil), options.ClaimCheckTools...),
		KnownRoutes:     append([]string(nil), options.ClaimCheckRoutes...),
		IncludeVerified: options.IncludeVerified,
	}, nil
}

func confidenceChangedPaths(record projectevidence.ClaimRecord, requested []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	add := func(path string) error {
		normalized, err := safeChangedPath(path)
		if err != nil {
			return err
		}
		if normalized == "" {
			return nil
		}
		if _, ok := seen[normalized]; ok {
			return nil
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
		return nil
	}
	for _, path := range requested {
		if err := add(path); err != nil {
			return nil, err
		}
	}
	for _, action := range record.Actions {
		for _, path := range action.ChangedFiles {
			if err := add(path); err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func safeClaimCheckPath(path string) (string, error) {
	normalized, err := safeChangedPath(path)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return "", fmt.Errorf("%w: claim check path is required", ErrInvalidInput)
	}
	if normalized == "README.md" || strings.HasPrefix(normalized, "docs/") || strings.HasPrefix(normalized, "api/") || normalized == ".ai/skills/mivia-mcp/SKILL.md" {
		return normalized, nil
	}
	return "", fmt.Errorf("%w: claim check path is out of scope", ErrInvalidInput)
}

func safeChangedPath(path string) (string, error) {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" {
		return "", nil
	}
	if len(path) > 300 || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || filepath.IsAbs(path) || containsProhibitedData(path) || containsURL(path) || containsRootMarker(path) || looksLikeSourceDump(path) || strings.Contains(path, "//") {
		return "", fmt.Errorf("%w: changed path is unsafe", ErrInvalidInput)
	}
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}
		return "", fmt.Errorf("%w: changed path is unsafe", ErrInvalidInput)
	}
	return path, nil
}
