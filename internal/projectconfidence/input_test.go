package projectconfidence

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

func TestReliabilityInputAdapterBuildsMetadataInputs(t *testing.T) {
	record := highRecord()
	record.Actions[0].ChangedFiles = []string{"internal/projectevidence/service.go"}
	evidence := &fakeEvidenceReader{record: record}
	health := &fakeHealthProvider{health: readyHealth(fixedNow)}
	checker := &fakeClaimCheckProvider{result: *verifiedClaims()}
	impact := &fakeImpactProvider{result: projectreliability.ImpactAnalysis{ProjectID: "project_1"}}
	adapter := NewReliabilityInputAdapter(evidence, health, checker, impact)

	inputs, err := adapter.Build(context.Background(), ReliabilityInputOptions{
		ProjectID:       "project_1",
		ClaimID:         "claim_1",
		ChangedPaths:    []string{"internal/projectconfidence/input.go"},
		ClaimCheckDocs:  []projectreliability.ClaimDocument{{Path: "api/mcp/agent-control.v1.md", Text: "projects.evidence_graph.claims.get"}},
		ClaimCheckPaths: []string{".ai/skills/mivia-mcp/SKILL.md"},
		IncludeVerified: true,
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if inputs.Claim.Claim.ID != "claim_1" || inputs.Health.Status != projectreliability.ContextHealthReady || inputs.ClaimCheck == nil || inputs.Impact == nil {
		t.Fatalf("adapter did not build expected inputs: %+v", inputs)
	}
	wantPaths := []string{"internal/projectconfidence/input.go", "internal/projectevidence/service.go"}
	if !reflect.DeepEqual(impact.request.ChangedPaths, wantPaths) {
		t.Fatalf("unexpected changed paths: got %#v want %#v", impact.request.ChangedPaths, wantPaths)
	}
	if impact.request.DiffScope != "" {
		t.Fatalf("impact adapter must not request workspace diff scope, got %q", impact.request.DiffScope)
	}
	if checker.request.ProjectID != "project_1" || len(checker.request.Documents) != 1 || len(checker.request.SelectedPaths) != 1 || !checker.request.IncludeVerified {
		t.Fatalf("unexpected claim check request: %+v", checker.request)
	}
}

func TestReliabilityInputAdapterSkipsOptionalInputsWhenMissing(t *testing.T) {
	evidence := &fakeEvidenceReader{record: baseRecord()}
	health := &fakeHealthProvider{health: projectreliability.ContextHealth{ProjectID: "project_1", Status: projectreliability.ContextHealthSyncing}}
	adapter := NewReliabilityInputAdapter(evidence, health, nil, nil)

	assessment, err := testService().ScoreClaimWithInputs(context.Background(), adapter, ReliabilityInputOptions{ProjectID: "project_1", ClaimID: "claim_1"})
	if err != nil {
		t.Fatalf("ScoreClaimWithInputs returned error: %v", err)
	}
	if assessment.Inputs.ClaimCheckVerified != 0 || assessment.Inputs.ImpactResidualUnknownCount != 0 {
		t.Fatalf("optional inputs should stay neutral when omitted: %+v", assessment.Inputs)
	}
	if assessment.Recommendation != RecommendationInsufficientEvidence {
		t.Fatalf("missing optional inputs should not panic and no evidence should be insufficient, got %s", assessment.Recommendation)
	}
}

func TestReliabilityInputAdapterOnlyRunsClaimChecksForSelectedSafeDocs(t *testing.T) {
	evidence := &fakeEvidenceReader{record: highRecord()}
	health := &fakeHealthProvider{health: readyHealth(fixedNow)}
	checker := &fakeClaimCheckProvider{result: *verifiedClaims()}
	adapter := NewReliabilityInputAdapter(evidence, health, checker, nil)

	if _, err := adapter.Build(context.Background(), ReliabilityInputOptions{ProjectID: "project_1", ClaimID: "claim_1"}); err != nil {
		t.Fatalf("Build without claim docs returned error: %v", err)
	}
	if checker.calls != 0 {
		t.Fatalf("claim checker should not run without selected docs, calls=%d", checker.calls)
	}
	_, err := adapter.Build(context.Background(), ReliabilityInputOptions{ProjectID: "project_1", ClaimID: "claim_1", ClaimCheckPaths: []string{"internal/projectconfidence/input.go"}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected out-of-scope claim check path rejection, got %v", err)
	}
}

func TestReliabilityInputAdapterOnlyRunsImpactForSafeChangedPaths(t *testing.T) {
	record := baseRecord()
	evidence := &fakeEvidenceReader{record: record}
	health := &fakeHealthProvider{health: readyHealth(fixedNow)}
	impact := &fakeImpactProvider{result: projectreliability.ImpactAnalysis{ProjectID: "project_1"}}
	adapter := NewReliabilityInputAdapter(evidence, health, nil, impact)

	if _, err := adapter.Build(context.Background(), ReliabilityInputOptions{ProjectID: "project_1", ClaimID: "claim_1"}); err != nil {
		t.Fatalf("Build without changed paths returned error: %v", err)
	}
	if impact.calls != 0 {
		t.Fatalf("impact analyzer should not run without safe changed paths, calls=%d", impact.calls)
	}
	_, err := adapter.Build(context.Background(), ReliabilityInputOptions{ProjectID: "project_1", ClaimID: "claim_1", ChangedPaths: []string{"../secret.txt"}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unsafe changed path rejection, got %v", err)
	}
}

func TestReliabilityInputAdapterRejectsCrossProjectClaim(t *testing.T) {
	record := baseRecord()
	record.Claim.ProjectID = "project_2"
	adapter := NewReliabilityInputAdapter(&fakeEvidenceReader{record: record}, &fakeHealthProvider{health: readyHealth(fixedNow)}, nil, nil)
	_, err := adapter.Build(context.Background(), ReliabilityInputOptions{ProjectID: "project_1", ClaimID: "claim_1"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected cross-project claim rejection, got %v", err)
	}
}

type fakeEvidenceReader struct {
	record projectevidence.ClaimRecord
	err    error
}

func (fake *fakeEvidenceReader) GetClaim(context.Context, string, string) (projectevidence.ClaimRecord, error) {
	return fake.record, fake.err
}

type fakeHealthProvider struct {
	health projectreliability.ContextHealth
	err    error
}

func (fake *fakeHealthProvider) ContextHealth(context.Context, string) (projectreliability.ContextHealth, error) {
	return fake.health, fake.err
}

type fakeClaimCheckProvider struct {
	result  projectreliability.ClaimCheckResult
	request projectreliability.ClaimCheckRequest
	calls   int
	err     error
}

func (fake *fakeClaimCheckProvider) Check(_ context.Context, request projectreliability.ClaimCheckRequest) (projectreliability.ClaimCheckResult, error) {
	fake.calls++
	fake.request = request
	return fake.result, fake.err
}

type fakeImpactProvider struct {
	result  projectreliability.ImpactAnalysis
	request projectreliability.ImpactAnalysisRequest
	calls   int
	err     error
}

func (fake *fakeImpactProvider) Analyze(_ context.Context, request projectreliability.ImpactAnalysisRequest) (projectreliability.ImpactAnalysis, error) {
	fake.calls++
	fake.request = request
	return fake.result, fake.err
}
