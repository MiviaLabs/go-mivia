package projectconfidence

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

var fixedNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestScoreHighConfidence(t *testing.T) {
	svc := testService()
	assessment, err := svc.Score(highRecord(), readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if assessment.Score != 100 || assessment.Band != ScoreBandHigh || assessment.Recommendation != RecommendationPromote {
		t.Fatalf("unexpected high assessment: score=%d band=%s recommendation=%s", assessment.Score, assessment.Band, assessment.Recommendation)
	}
	assertFactorsValid(t, assessment)
}

func TestScoreMediumConfidence(t *testing.T) {
	svc := testService()
	record := baseRecord()
	record.Evidence = []projectevidence.Evidence{{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "file_ref", EvidenceKind: projectevidence.EvidenceKindFile}}
	record.Decisions = []projectevidence.Decision{{ID: "decision_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision_ref", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"}}
	assessment, err := svc.Score(record, readyHealth(fixedNow.Add(-time.Hour)), nil, nil)
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if assessment.Band != ScoreBandMedium || assessment.Recommendation != RecommendationVerify {
		t.Fatalf("unexpected medium assessment: score=%d band=%s recommendation=%s", assessment.Score, assessment.Band, assessment.Recommendation)
	}
}

func TestScoreLowConfidence(t *testing.T) {
	svc := testService()
	record := baseRecord()
	record.Evidence = []projectevidence.Evidence{{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "file_ref", EvidenceKind: projectevidence.EvidenceKindFile}}
	assessment, err := svc.Score(record, readyHealth(fixedNow.Add(-time.Hour)), nil, nil)
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if assessment.Band != ScoreBandLow || assessment.Recommendation != RecommendationReview {
		t.Fatalf("unexpected low assessment: score=%d band=%s recommendation=%s", assessment.Score, assessment.Band, assessment.Recommendation)
	}
}

func TestScoreUnknownWhenRequiredHealthMissing(t *testing.T) {
	svc := testService()
	record := baseRecord()
	record.Evidence = []projectevidence.Evidence{{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "file_ref", EvidenceKind: projectevidence.EvidenceKindFile}}
	assessment, err := svc.Score(record, projectreliability.ContextHealth{ProjectID: "project_1"}, nil, nil)
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if assessment.Band != ScoreBandUnknown || assessment.Recommendation != RecommendationInsufficientEvidence {
		t.Fatalf("unexpected unknown assessment: score=%d band=%s recommendation=%s", assessment.Score, assessment.Band, assessment.Recommendation)
	}
}

func TestRejectionDominantRecommendation(t *testing.T) {
	svc := testService()
	record := highRecord()
	record.Decisions = append(record.Decisions, projectevidence.Decision{ID: "decision_rejected", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision_rejected_ref", State: projectevidence.DecisionStateRejected, VerifierRef: "verifier_ref", Rationale: "metadata rejected"})
	assessment, err := svc.Score(record, readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if assessment.Recommendation != RecommendationReject || factorDelta(assessment, "evidence_graph_rejected_decision") != -30 {
		t.Fatalf("rejection did not dominate: recommendation=%s rejected_delta=%d", assessment.Recommendation, factorDelta(assessment, "evidence_graph_rejected_decision"))
	}
}

func TestFailedOutcomeDominantRecommendation(t *testing.T) {
	svc := testService()
	record := highRecord()
	record.Outcomes = append(record.Outcomes, projectevidence.Outcome{ID: "outcome_failed", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action_1", OutcomeRef: "outcome_failed_ref", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusFailed})
	assessment, err := svc.Score(record, readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if assessment.Recommendation != RecommendationReject || factorDelta(assessment, "evidence_graph_failed_outcome") != -20 {
		t.Fatalf("failed outcome did not dominate: recommendation=%s failed_delta=%d", assessment.Recommendation, factorDelta(assessment, "evidence_graph_failed_outcome"))
	}
}

func TestStaleAndDegradedContextFactors(t *testing.T) {
	svc := testService()
	record := highRecord()
	stale, err := svc.Score(record, healthWithStatus(projectreliability.ContextHealthStale, fixedNow.Add(-8*24*time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("stale score returned error: %v", err)
	}
	if factorDelta(stale, "context_health") != -10 || factorDelta(stale, "latest_ingestion_freshness") != -10 {
		t.Fatalf("unexpected stale factors: context=%d freshness=%d", factorDelta(stale, "context_health"), factorDelta(stale, "latest_ingestion_freshness"))
	}
	degraded, err := svc.Score(record, healthWithStatus(projectreliability.ContextHealthDegraded, fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("degraded score returned error: %v", err)
	}
	if factorDelta(degraded, "context_health") != -20 {
		t.Fatalf("unexpected degraded context delta: %d", factorDelta(degraded, "context_health"))
	}
}

func TestPartialImpactAndActionableClaimCheckFactors(t *testing.T) {
	svc := testService()
	partialImpact := &projectreliability.ImpactAnalysis{ProjectID: "project_1", Partial: true, ResidualUnknowns: []string{"index_syncing"}}
	assessment, err := svc.Score(highRecord(), readyHealth(fixedNow.Add(-time.Hour)), actionableClaims(), partialImpact)
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	if factorDelta(assessment, "impact_analysis") != -10 || factorDelta(assessment, "claim_checks") != -15 {
		t.Fatalf("unexpected partial/actionable factors: impact=%d claim_checks=%d", factorDelta(assessment, "impact_analysis"), factorDelta(assessment, "claim_checks"))
	}
	if assessment.Inputs.ImpactPartial != true || assessment.Inputs.ClaimCheckActionable != 1 {
		t.Fatalf("unexpected inputs: %+v", assessment.Inputs)
	}
}

func TestScoringRuleBranches(t *testing.T) {
	svc := testService()

	noEvidence, err := svc.Score(baseRecord(), readyHealth(fixedNow.Add(-time.Hour)), nil, nil)
	if err != nil {
		t.Fatalf("no evidence score returned error: %v", err)
	}
	if factorDelta(noEvidence, "evidence_count") != -25 || noEvidence.Recommendation != RecommendationInsufficientEvidence {
		t.Fatalf("unexpected no evidence result: score=%d delta=%d recommendation=%s", noEvidence.Score, factorDelta(noEvidence, "evidence_count"), noEvidence.Recommendation)
	}

	twoKinds := baseRecord()
	twoKinds.Evidence = []projectevidence.Evidence{
		{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "file_ref", EvidenceKind: projectevidence.EvidenceKindFile},
		{ID: "evidence_2", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "verifier_ref", EvidenceKind: projectevidence.EvidenceKindVerifier},
	}
	twoKindAssessment, err := svc.Score(twoKinds, readyHealth(fixedNow.Add(-time.Hour)), nil, nil)
	if err != nil {
		t.Fatalf("two-kind score returned error: %v", err)
	}
	if factorDelta(twoKindAssessment, "evidence_count") != 10 || factorDelta(twoKindAssessment, "evidence_kind_diversity") != 5 {
		t.Fatalf("unexpected two-kind factors: evidence=%d diversity=%d", factorDelta(twoKindAssessment, "evidence_count"), factorDelta(twoKindAssessment, "evidence_kind_diversity"))
	}

	weekFresh, err := svc.Score(highRecord(), readyHealth(fixedNow.Add(-48*time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("week freshness score returned error: %v", err)
	}
	if factorDelta(weekFresh, "latest_ingestion_freshness") != 0 {
		t.Fatalf("unexpected 24h-7d freshness delta: %d", factorDelta(weekFresh, "latest_ingestion_freshness"))
	}

	unavailableFresh, err := svc.Score(highRecord(), projectreliability.ContextHealth{ProjectID: "project_1", Status: projectreliability.ContextHealthReady}, verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("unavailable freshness score returned error: %v", err)
	}
	if factorDelta(unavailableFresh, "latest_ingestion_freshness") != -10 {
		t.Fatalf("unexpected unavailable freshness delta: %d", factorDelta(unavailableFresh, "latest_ingestion_freshness"))
	}

	missingReliability, err := svc.Score(highRecord(), readyHealth(fixedNow.Add(-time.Hour)), nil, nil)
	if err != nil {
		t.Fatalf("missing reliability score returned error: %v", err)
	}
	if missingReliability.Recommendation != RecommendationVerify || factorDelta(missingReliability, "claim_checks") != 0 || factorDelta(missingReliability, "impact_analysis") != 0 {
		t.Fatalf("missing reliability should verify with neutral factors: recommendation=%s claim=%d impact=%d", missingReliability.Recommendation, factorDelta(missingReliability, "claim_checks"), factorDelta(missingReliability, "impact_analysis"))
	}

	securityImpact, err := svc.Score(highRecord(), readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), &projectreliability.ImpactAnalysis{ProjectID: "project_1", SecurityFlags: []string{"auth_boundary"}})
	if err != nil {
		t.Fatalf("security impact score returned error: %v", err)
	}
	if factorDelta(securityImpact, "impact_analysis") != -5 {
		t.Fatalf("unexpected security impact delta: %d", factorDelta(securityImpact, "impact_analysis"))
	}
}

func TestUnsafeMetadataRejection(t *testing.T) {
	svc := testService()
	cases := []projectevidence.ClaimRecord{
		withClaimSummary("raw" + " prompt marker"),
		withClaimSummary("raw" + " completion marker"),
		withClaimSummary("package" + " main"),
		withClaimSummary("Authorization" + ": bearer marker"),
		withClaimSummary("provider" + " payload marker"),
		withClaimSummary("token" + "=marker"),
		withClaimSummary("secret" + "=marker"),
		withClaimSummary("password" + "=marker"),
		withClaimSummary("owner" + "@" + "example" + ".invalid"),
		withClaimSummary("+1 555 123 4567"),
		withEvidenceSource("https:" + "//" + "invalid.example/context"),
		withEvidenceSource("/" + "home" + "/agent/source"),
		withEvidenceSource("docs/../source"),
		withDecisionRationale("raw" + " stderr marker"),
	}
	for _, record := range cases {
		if _, err := svc.Score(record, readyHealth(fixedNow.Add(-time.Hour)), nil, nil); err == nil {
			t.Fatalf("expected unsafe metadata rejection for record: %+v", record)
		}
	}
}

func TestUnsafeFactorSummaryRejection(t *testing.T) {
	assessment, err := testService().Score(highRecord(), readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("Score returned error: %v", err)
	}
	assessment.Factors[0].Summary = "raw" + " source marker"
	if err := validateAssessment(assessment); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unsafe factor summary rejection, got %v", err)
	}
}

func TestScoreClamping(t *testing.T) {
	svc := testService()
	high, err := svc.Score(highRecord(), readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("high score returned error: %v", err)
	}
	if high.Score != 100 {
		t.Fatalf("expected high score clamp to 100, got %d", high.Score)
	}
	low := highRecord()
	low.Decisions = []projectevidence.Decision{{ID: "decision_rejected", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision_rejected_ref", State: projectevidence.DecisionStateRejected, VerifierRef: "verifier_ref", Rationale: "metadata rejected"}}
	low.Outcomes = append(low.Outcomes, projectevidence.Outcome{ID: "outcome_failed", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action_1", OutcomeRef: "outcome_failed_ref", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusFailed})
	lowAssessment, err := svc.Score(low, healthWithStatus(projectreliability.ContextHealthDegraded, fixedNow.Add(-8*24*time.Hour)), actionableClaims(), &projectreliability.ImpactAnalysis{ProjectID: "project_1", Partial: true, ResidualUnknowns: []string{"graph_timeout"}, SecurityFlags: []string{"provider_payload_boundary"}})
	if err != nil {
		t.Fatalf("low score returned error: %v", err)
	}
	if lowAssessment.Score != 0 {
		t.Fatalf("expected low score clamp to 0, got %d", lowAssessment.Score)
	}
}

func TestDeterministicBehaviorAndStoreInterface(t *testing.T) {
	svc := testService()
	fakeStore := newRecordingStore()
	svc.store = fakeStore
	record := highRecord()
	first, err := svc.ScoreClaim(context.Background(), record, readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("first score returned error: %v", err)
	}
	second, err := svc.ScoreClaim(context.Background(), record, readyHealth(fixedNow.Add(-time.Hour)), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("second score returned error: %v", err)
	}
	if !reflect.DeepEqual(first.Factors, second.Factors) || first.Score != second.Score || first.Band != second.Band || first.Recommendation != second.Recommendation || !reflect.DeepEqual(first.Inputs, second.Inputs) || first.ID != second.ID {
		t.Fatalf("scoring is not deterministic\nfirst=%+v\nsecond=%+v", first, second)
	}
	got, err := svc.GetAssessment(context.Background(), "project_1", "claim_1")
	if err != nil {
		t.Fatalf("GetAssessment returned error: %v", err)
	}
	if got.ID != first.ID {
		t.Fatalf("stored assessment mismatch: got %s want %s", got.ID, first.ID)
	}
	listed, err := svc.ListAssessments(context.Background(), "project_1", AssessmentFilter{Band: ScoreBandHigh, Recommendation: RecommendationPromote})
	if err != nil {
		t.Fatalf("ListAssessments returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("unexpected list result: %+v", listed)
	}
	if _, err := svc.ListAssessments(context.Background(), "project_1", AssessmentFilter{Band: "critical"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid filter rejection, got %v", err)
	}
	minScore := 90
	maxScore := 10
	if _, err := svc.ListAssessments(context.Background(), "project_1", AssessmentFilter{MinScore: &minScore, MaxScore: &maxScore}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid score bounds rejection, got %v", err)
	}
	if _, err := svc.Score(highRecord(), projectreliability.ContextHealth{ProjectID: "project_2", Status: projectreliability.ContextHealthReady}, verifiedClaims(), cleanImpact()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected cross-project health rejection, got %v", err)
	}
}

type recordingStore struct {
	assessments map[string]ConfidenceAssessment
}

func newRecordingStore() *recordingStore {
	return &recordingStore{assessments: map[string]ConfidenceAssessment{}}
}

func (store *recordingStore) CreateAssessment(_ context.Context, assessment ConfidenceAssessment) (ConfidenceAssessment, error) {
	store.assessments[assessment.ProjectID+"\x00"+assessment.ClaimID] = assessment
	return assessment, nil
}

func (store *recordingStore) GetAssessment(_ context.Context, projectID string, claimID string) (ConfidenceAssessment, error) {
	return store.assessments[projectID+"\x00"+claimID], nil
}

func (store *recordingStore) ListAssessments(_ context.Context, projectID string, filter AssessmentFilter) ([]ConfidenceAssessment, error) {
	out := []ConfidenceAssessment{}
	for _, assessment := range store.assessments {
		if assessment.ProjectID != projectID {
			continue
		}
		if filter.Band != "" && assessment.Band != filter.Band {
			continue
		}
		if filter.Recommendation != "" && assessment.Recommendation != filter.Recommendation {
			continue
		}
		out = append(out, assessment)
	}
	return out, nil
}

func testService() *Service {
	svc := New(nil)
	svc.now = func() time.Time { return fixedNow }
	return svc
}

func baseRecord() projectevidence.ClaimRecord {
	return projectevidence.ClaimRecord{Claim: projectevidence.Claim{ID: "claim_1", ProjectID: "project_1", RunID: "run_1", TraceID: "trace_1", ClaimRef: "claim_ref", Summary: "metadata only summary", Status: projectevidence.ClaimStatusCandidate}}
}

func highRecord() projectevidence.ClaimRecord {
	record := baseRecord()
	record.Claim.Status = projectevidence.ClaimStatusValidated
	record.Evidence = []projectevidence.Evidence{
		{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "context_pack_ref", EvidenceKind: projectevidence.EvidenceKindContextPack},
		{ID: "evidence_2", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "file_ref", EvidenceKind: projectevidence.EvidenceKindFile},
		{ID: "evidence_3", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "verifier_ref", EvidenceKind: projectevidence.EvidenceKindVerifier},
		{ID: "evidence_4", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "claim_check_ref", EvidenceKind: projectevidence.EvidenceKindClaimCheck},
	}
	record.Decisions = []projectevidence.Decision{{ID: "decision_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision_ref", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"}}
	record.Actions = []projectevidence.Action{{ID: "action_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionID: "decision_1", ActionRef: "action_ref", ActionKind: projectevidence.ActionKindVerifierRun, RunID: "run_1"}}
	record.Outcomes = []projectevidence.Outcome{{ID: "outcome_1", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action_1", OutcomeRef: "outcome_ref", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier_ref"}}
	record.ArtifactLinks = []projectevidence.ArtifactLink{{ProjectID: "project_1", ClaimID: "claim_1", ArtifactRef: "artifact_ref", ArtifactKind: "report", RunID: "run_1"}}
	record.PromotionLinks = []projectevidence.PromotionLink{{ProjectID: "project_1", ClaimID: "claim_1", RunID: "run_1", ArtifactRef: "artifact_ref", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion_source", VerifierRef: "verifier_ref", DecisionRef: "decision_ref", ActionRef: "action_ref", OutcomeRef: "outcome_ref"}}
	return record
}

func readyHealth(progress time.Time) projectreliability.ContextHealth {
	return healthWithStatus(projectreliability.ContextHealthReady, progress)
}

func healthWithStatus(status projectreliability.ContextHealthStatus, progress time.Time) projectreliability.ContextHealth {
	return projectreliability.ContextHealth{ProjectID: "project_1", Status: status, StatusReason: "metadata_only", LatestRun: &projectreliability.RunSummary{ID: "ingest_1", Status: "completed", LastProgressAt: progress}, IndexedContentAvailable: true, CheckedAt: fixedNow}
}

func verifiedClaims() *projectreliability.ClaimCheckResult {
	return &projectreliability.ClaimCheckResult{ProjectID: "project_1", Summary: projectreliability.ClaimCheckSummary{Total: 2, Verified: 2, Actionable: 0}, AllVerified: true}
}

func actionableClaims() *projectreliability.ClaimCheckResult {
	return &projectreliability.ClaimCheckResult{ProjectID: "project_1", Summary: projectreliability.ClaimCheckSummary{Total: 2, Verified: 1, Actionable: 1}, Claims: []projectreliability.ClaimFinding{{Path: "docs/contract.md", Status: "stale", SafeMessage: "route claim needs review"}}}
}

func cleanImpact() *projectreliability.ImpactAnalysis {
	return &projectreliability.ImpactAnalysis{ProjectID: "project_1"}
}

func withClaimSummary(summary string) projectevidence.ClaimRecord {
	record := highRecord()
	record.Claim.Summary = summary
	return record
}

func withEvidenceSource(sourceRef string) projectevidence.ClaimRecord {
	record := highRecord()
	record.Evidence[0].SourceRef = sourceRef
	return record
}

func withDecisionRationale(rationale string) projectevidence.ClaimRecord {
	record := highRecord()
	record.Decisions[0].Rationale = rationale
	return record
}

func factorDelta(assessment ConfidenceAssessment, name string) int {
	for _, factor := range assessment.Factors {
		if factor.Name == name {
			return factor.ScoreDelta
		}
	}
	return 0
}

func assertFactorsValid(t *testing.T, assessment ConfidenceAssessment) {
	t.Helper()
	if len(assessment.Factors) == 0 {
		t.Fatal("expected factors")
	}
	for _, factor := range assessment.Factors {
		if factor.Name == "" || factor.Summary == "" || factor.SourceRef == "" {
			t.Fatalf("factor missing metadata: %+v", factor)
		}
	}
}
