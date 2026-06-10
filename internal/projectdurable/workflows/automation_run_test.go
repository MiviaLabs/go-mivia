package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/client"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
)

// --- fake ports -------------------------------------------------------------

type fakeRunObserver struct {
	mu    sync.Mutex
	calls int
	snap  projectdurable.DurableRunSnapshot
}

func (f *fakeRunObserver) LoadRunSnapshot(_ context.Context, _ projectdurable.SafeAutomationRunRef) (projectdurable.DurableRunSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snap, nil
}

func (f *fakeRunObserver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeTaskObserver struct {
	mu    sync.Mutex
	calls int
	snap  projectdurable.SafeTaskSnapshot
}

func (f *fakeTaskObserver) LoadTaskStatus(_ context.Context, _ projectdurable.SafeWorkTaskRef) (projectdurable.SafeTaskSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snap, nil
}

func (f *fakeTaskObserver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeShadowWriter struct {
	mu      sync.Mutex
	calls   int
	lastRef projectdurable.SafeAutomationRunRef
	fields  map[string]string
	err     error
}

func (f *fakeShadowWriter) WriteShadowComparison(_ context.Context, runRef projectdurable.SafeAutomationRunRef, fields map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls++
	f.lastRef = runRef
	copied := make(map[string]string, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	f.fields = copied
	return nil
}

func (f *fakeShadowWriter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeShadowWriter) capturedFields() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fields
}

// --- fixtures (neutral ids only) ---------------------------------------------

func testRunRef() projectdurable.SafeAutomationRunRef {
	return projectdurable.SafeAutomationRunRef{
		ProjectID:    "project-1",
		AutomationID: "automation-1",
		RunID:        "run-0001",
		TraceID:      "trace-0001",
	}
}

func testInput(taskID string) AutomationRunWorkflowInput {
	return AutomationRunWorkflowInput{
		ProjectID:    "project-1",
		AutomationID: "automation-1",
		RunID:        "run-0001",
		TaskID:       taskID,
		TraceID:      "trace-0001",
		ShadowOnly:   true,
	}
}

func snapshotWithStatus(status string, category projectdurable.DurableFailureCategory) projectdurable.DurableRunSnapshot {
	return projectdurable.DurableRunSnapshot{
		Run:             testRunRef(),
		Status:          status,
		FailureCategory: category,
		AttemptCount:    1,
		ObservedAt:      time.Unix(1750000000, 0).UTC(),
	}
}

// verifyingSnapshot models the current queued->running->verifying contract:
// the run parked at verifying with verifier and review results attached.
func verifyingSnapshot() projectdurable.DurableRunSnapshot {
	snap := snapshotWithStatus("verifying", projectdurable.FailureCategoryNone)
	snap.VerifierResultRefs = []string{"verifier:check-0001", "verifier:check-0002"}
	snap.ReviewResultRefs = []string{"review:result-0001"}
	snap.EvidenceRefs = []string{"evidence:proof-0001"}
	snap.ClaimRefs = []string{"claim:item-0001"}
	return snap
}

func taskSnapshot() projectdurable.SafeTaskSnapshot {
	return projectdurable.SafeTaskSnapshot{
		Ref: projectdurable.SafeWorkTaskRef{
			ProjectID: "project-1",
			PlanID:    "plan-0001",
			TaskID:    "task-0001",
			TaskRef:   "jira:PROJ-1044",
		},
		Status: "in_progress",
		Refs: map[string][]string{
			"verifier_result_refs": {"verifier:check-0001", "verifier:check-0002"},
			"evidence_refs":        {"evidence:proof-0001"},
		},
	}
}

// --- harness ------------------------------------------------------------------

func runShadowWorkflow(t *testing.T, input AutomationRunWorkflowInput, runs *fakeRunObserver, tasks *fakeTaskObserver, shadow *fakeShadowWriter) (ShadowRunTrace, error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	engine := projectdurable.NewMemoryEngine()
	defer cleanupMemoryEngine(t, engine, cancel)

	if err := engine.Orchestrator.RegisterWorkflow(MiviaAutomationRunShadowWorkflow); err != nil {
		t.Fatalf("register workflow: %v", err)
	}
	if err := engine.Orchestrator.RegisterActivity(&activities.AutomationRunActivities{
		Runs:   runs,
		Tasks:  tasks,
		Shadow: shadow,
	}); err != nil {
		t.Fatalf("register activities: %v", err)
	}
	if err := engine.Orchestrator.Start(ctx); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}

	instance, err := engine.Orchestrator.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: "shadow-instance-" + input.RunID + "-" + t.Name(),
	}, MiviaAutomationRunShadowWorkflow, input)
	if err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}

	return client.GetWorkflowResult[ShadowRunTrace](ctx, engine.Orchestrator.Client, instance, 10*time.Second)
}

func cleanupMemoryEngine(t *testing.T, engine *projectdurable.Engine, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	if err := engine.Orchestrator.WaitForCompletion(); err != nil {
		t.Fatalf("wait for orchestrator completion: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}
}

func findStep(t *testing.T, trace ShadowRunTrace, activity string) projectdurable.DurableActivityResult {
	t.Helper()
	for _, step := range trace.Steps {
		if step.Activity == activity {
			return step
		}
	}
	t.Fatalf("trace has no step for activity %q", activity)
	return projectdurable.DurableActivityResult{}
}

// --- scenario a: queued->running->verifying happy path -------------------------

func TestShadowWorkflowVerifyingHappyPathAllStepsOK(t *testing.T) {
	runs := &fakeRunObserver{snap: verifyingSnapshot()}
	tasks := &fakeTaskObserver{snap: taskSnapshot()}
	shadow := &fakeShadowWriter{}

	trace, err := runShadowWorkflow(t, testInput("task-0001"), runs, tasks, shadow)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	if len(trace.Steps) != 8 {
		t.Fatalf("expected 8 trace steps, got %d", len(trace.Steps))
	}
	for _, step := range trace.Steps {
		if step.Status != projectdurable.ActivityStatusOK {
			t.Fatalf("step %q not ok: %q", step.Activity, step.Status)
		}
	}
	if trace.FinalStatus != "verifying" {
		t.Fatalf("expected final status verifying, got %q", trace.FinalStatus)
	}
	if trace.FailureCategory != projectdurable.FailureCategoryNone {
		t.Fatalf("expected no failure category, got %q", trace.FailureCategory)
	}

	taskStep := findStep(t, trace, activities.ActivityObserveWorkTask)
	wantRefs := map[string]bool{
		"task-status:in_progress": true,
		"verifier-result-refs:2":  true,
		"evidence-refs:1":         true,
	}
	for _, ref := range taskStep.Refs {
		delete(wantRefs, ref)
	}
	if len(wantRefs) != 0 {
		t.Fatalf("observe-work-task refs missing %v, got %v", wantRefs, taskStep.Refs)
	}

	if shadow.callCount() != 1 {
		t.Fatalf("expected exactly 1 shadow write, got %d", shadow.callCount())
	}
	fields := shadow.capturedFields()
	if fields["final_status"] != "verifying" {
		t.Fatalf("shadow final_status = %q", fields["final_status"])
	}
	if fields["step_count"] != "7" {
		t.Fatalf("shadow step_count = %q (write step is appended after the write)", fields["step_count"])
	}
}

// TestValidateExecutableClassifiesQueuedOK pins the queued part of the
// queued->running->verifying contract via the pure classification activity.
func TestValidateExecutableClassifiesQueuedOK(t *testing.T) {
	acts := &activities.AutomationRunActivities{}
	for _, status := range []string{"queued", "running", "verifying"} {
		result, err := acts.ValidateAutomationRunExecutable(context.Background(), snapshotWithStatus(status, projectdurable.FailureCategoryNone))
		if err != nil {
			t.Fatalf("classify %q: %v", status, err)
		}
		if result.Status != projectdurable.ActivityStatusOK {
			t.Fatalf("status %q classified %q, want ok", status, result.Status)
		}
	}
}

// --- scenario b: manual verifying-required path ---------------------------------

func TestShadowWorkflowManualVerifyingRunStaysVerifying(t *testing.T) {
	runs := &fakeRunObserver{snap: verifyingSnapshot()}
	tasks := &fakeTaskObserver{snap: taskSnapshot()}
	shadow := &fakeShadowWriter{}

	// Manual run: no task id, review + verifier refs present.
	trace, err := runShadowWorkflow(t, testInput(""), runs, tasks, shadow)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	taskStep := findStep(t, trace, activities.ActivityObserveWorkTask)
	if taskStep.Status != projectdurable.ActivityStatusSkipped {
		t.Fatalf("expected observe-work-task skipped without task id, got %q", taskStep.Status)
	}
	if tasks.callCount() != 0 {
		t.Fatalf("task port must not be called without a task id, got %d calls", tasks.callCount())
	}
	reviewStep := findStep(t, trace, activities.ActivityObserveReviewQueue)
	if reviewStep.Status != projectdurable.ActivityStatusOK {
		t.Fatalf("review step with review refs present must be ok, got %q", reviewStep.Status)
	}
	verifierStep := findStep(t, trace, activities.ActivityObserveVerifierState)
	if verifierStep.Status != projectdurable.ActivityStatusOK {
		t.Fatalf("verifier step with verifier refs present must be ok, got %q", verifierStep.Status)
	}
	if trace.FinalStatus != "verifying" {
		t.Fatalf("expected final status verifying (not completed), got %q", trace.FinalStatus)
	}
}

// --- scenario c: codex auth unavailable ------------------------------------------

func TestShadowWorkflowCodexAuthUnavailableClassifiedSafely(t *testing.T) {
	runs := &fakeRunObserver{snap: snapshotWithStatus("blocked", projectdurable.FailureCategoryCodexAuthUnavailable)}
	shadow := &fakeShadowWriter{}

	trace, err := runShadowWorkflow(t, testInput(""), runs, &fakeTaskObserver{}, shadow)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	codexStep := findStep(t, trace, activities.ActivityObserveCodexAttempt)
	if codexStep.Status != projectdurable.ActivityStatusBlocked {
		t.Fatalf("codex step status = %q, want blocked", codexStep.Status)
	}
	if codexStep.FailureCategory != projectdurable.FailureCategoryCodexAuthUnavailable {
		t.Fatalf("codex step category = %q, want codex_auth_unavailable", codexStep.FailureCategory)
	}
	if trace.FailureCategory != projectdurable.FailureCategoryCodexAuthUnavailable {
		t.Fatalf("trace category = %q, want codex_auth_unavailable", trace.FailureCategory)
	}
	if trace.FinalStatus != "blocked" {
		t.Fatalf("trace final status = %q, want blocked (mirrors snapshot)", trace.FinalStatus)
	}
	assertTraceJSONIsSafe(t, trace)
}

// --- scenario d: timeout ----------------------------------------------------------

func TestShadowWorkflowTimeoutClassifiedSafely(t *testing.T) {
	runs := &fakeRunObserver{snap: snapshotWithStatus("timeout", projectdurable.FailureCategoryTimeout)}
	shadow := &fakeShadowWriter{}

	trace, err := runShadowWorkflow(t, testInput(""), runs, &fakeTaskObserver{}, shadow)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	validateStep := findStep(t, trace, activities.ActivityValidateExecutable)
	if validateStep.Status != projectdurable.ActivityStatusBlocked || validateStep.FailureCategory != projectdurable.FailureCategoryTimeout {
		t.Fatalf("validate step = %q/%q, want blocked/timeout", validateStep.Status, validateStep.FailureCategory)
	}
	if trace.FailureCategory != projectdurable.FailureCategoryTimeout {
		t.Fatalf("trace category = %q, want timeout", trace.FailureCategory)
	}
	if trace.FinalStatus != "timeout" {
		t.Fatalf("trace final status = %q, want timeout", trace.FinalStatus)
	}
	assertTraceJSONIsSafe(t, trace)
}

// --- scenario e: missing verifier refs --------------------------------------------

func TestShadowWorkflowMissingVerifierNeverCompletes(t *testing.T) {
	snap := verifyingSnapshot()
	snap.VerifierResultRefs = nil
	runs := &fakeRunObserver{snap: snap}
	shadow := &fakeShadowWriter{}

	trace, err := runShadowWorkflow(t, testInput(""), runs, &fakeTaskObserver{}, shadow)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	verifierStep := findStep(t, trace, activities.ActivityObserveVerifierState)
	if verifierStep.Status != projectdurable.ActivityStatusBlocked {
		t.Fatalf("verifier step status = %q, want blocked", verifierStep.Status)
	}
	if verifierStep.FailureCategory != projectdurable.FailureCategoryVerificationRequired {
		t.Fatalf("verifier step category = %q, want verification_required", verifierStep.FailureCategory)
	}
	if trace.FinalStatus == "completed" {
		t.Fatalf("run without verifier refs must never classify completed")
	}
	if trace.FinalStatus != "verifying" {
		t.Fatalf("trace final status = %q, want verifying", trace.FinalStatus)
	}
	if trace.FailureCategory != projectdurable.FailureCategoryVerificationRequired {
		t.Fatalf("trace category = %q, want verification_required", trace.FailureCategory)
	}
}

// --- scenario f: shadow history safety ---------------------------------------------

func TestShadowTraceAndComparisonAreSafeBoundedMetadata(t *testing.T) {
	runs := &fakeRunObserver{snap: verifyingSnapshot()}
	tasks := &fakeTaskObserver{snap: taskSnapshot()}
	shadow := &fakeShadowWriter{}

	trace, err := runShadowWorkflow(t, testInput("task-0001"), runs, tasks, shadow)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	assertTraceJSONIsSafe(t, trace)

	fields := shadow.capturedFields()
	if len(fields) == 0 {
		t.Fatalf("shadow writer captured no fields")
	}
	for key, value := range fields {
		if err := projectdurable.ValidateSafeRef(key); err != nil {
			t.Fatalf("shadow field key %q failed safe-ref checks: %v", key, err)
		}
		if err := projectdurable.ValidateSafeSummary(value); err != nil {
			t.Fatalf("shadow field %q value failed safe-summary checks: %v", key, err)
		}
	}
}

func assertTraceJSONIsSafe(t *testing.T, trace ShadowRunTrace) {
	t.Helper()
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	payload := string(raw)
	for _, forbidden := range []string{"raw_prompt", "raw_completion", "raw_stderr", "api_key", "/home/"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("trace JSON contains forbidden marker %q", forbidden)
		}
	}
	for _, step := range trace.Steps {
		if len(step.SafeSummary) > 512 {
			t.Fatalf("step %q summary exceeds 512 chars: %d", step.Activity, len(step.SafeSummary))
		}
	}
}

// --- scenario g: ShadowOnly=false fails closed --------------------------------------

func TestShadowWorkflowRejectsNonShadowOnlyWithoutPortCalls(t *testing.T) {
	runs := &fakeRunObserver{snap: verifyingSnapshot()}
	tasks := &fakeTaskObserver{snap: taskSnapshot()}
	shadow := &fakeShadowWriter{}

	input := testInput("task-0001")
	input.ShadowOnly = false

	_, err := runShadowWorkflow(t, input, runs, tasks, shadow)
	if err == nil {
		t.Fatalf("expected fail-closed error for shadow_only=false")
	}
	if !strings.Contains(err.Error(), "shadow_only=true") {
		t.Fatalf("unexpected error: %v", err)
	}
	if runs.callCount() != 0 {
		t.Fatalf("run port called %d times, want 0", runs.callCount())
	}
	if tasks.callCount() != 0 {
		t.Fatalf("task port called %d times, want 0", tasks.callCount())
	}
	if shadow.callCount() != 0 {
		t.Fatalf("shadow port called %d times, want 0", shadow.callCount())
	}
}
