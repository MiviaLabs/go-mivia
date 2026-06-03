package projectautomation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestSubmitRunRequiresCodexWhenAvailable(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, AllowManualRunner: true, MaxParallelTasks: 2})
	svc.codexAvailable = func() bool { return true }
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, RunnerKind: RunnerKindManual})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.Status != RunStatusPolicyDenied {
		t.Fatalf("expected policy_denied, got %q", run.Status)
	}
	if run.FailureCategory != "invalid_project_automation_input:_codex_cli_required" {
		t.Fatalf("unexpected failure category: %q", run.FailureCategory)
	}
}

func TestSubmitRunDefaultsToCodexWhenAvailable(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, MaxParallelTasks: 2})
	svc.codexAvailable = func() bool { return true }
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		t.Fatalf("expected codex runner, got %q", run.RunnerKind)
	}
	if run.Status != RunStatusQueued {
		t.Fatalf("expected queued, got %q", run.Status)
	}
}

func TestComputeParallelBatchRejectsConflictingFiles(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
		"task-b": readyTask("task-b", "b", []string{"internal/foo.go"}),
		"task-c": readyTask("task-c", "c", []string{"internal/bar.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 3})
	svc.codexAvailable = func() bool { return true }

	batch, err := svc.ComputeParallelBatch(ctx, ComputeParallelBatchInput{ProjectID: "project-1", PlanID: "plan-1", OrchestratorRunID: "run-orchestrator"})
	if err != nil {
		t.Fatalf("ComputeParallelBatch returned error: %v", err)
	}
	if len(batch.TaskIDs) != 2 {
		t.Fatalf("expected 2 non-conflicting tasks, got %#v", batch.TaskIDs)
	}
	seen := map[string]bool{}
	for _, id := range batch.TaskIDs {
		seen[id] = true
	}
	if seen["task-a"] && seen["task-b"] {
		t.Fatalf("conflicting tasks were batched together: %#v", batch.TaskIDs)
	}
	if !seen["task-c"] {
		t.Fatalf("expected non-conflicting task-c in batch: %#v", batch.TaskIDs)
	}
}

func TestComputeParallelBatchSkipsNotReadyTasks(t *testing.T) {
	ctx := context.Background()
	notReady := readyTask("task-a", "a", []string{"internal/foo.go"})
	notReady.DecompositionQuality = projectworkplan.DecompositionMissingVerification
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": notReady}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 2})

	if _, err := svc.ComputeParallelBatch(ctx, ComputeParallelBatchInput{ProjectID: "project-1", PlanID: "plan-1", OrchestratorRunID: "run-orchestrator"}); err == nil {
		t.Fatal("expected no parallel-safe tasks error")
	}
}

func TestRunNowExecutesCodexCLIAndLeavesTaskForVerification(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, MaxParallelTasks: 1, DefaultMaxRuntime: 3 * time.Minute})
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	svc.codexAvailable = func() bool { return true }
	svc.codexPath = func() (string, bool) { return "/usr/local/bin/codex", true }
	var sawInput bool
	svc.codexRunner = func(_ context.Context, command CodexCommand, maxOutputBytes int64) (CodexRunResult, error) {
		if command.Path != "/usr/local/bin/codex" {
			t.Fatalf("unexpected codex path: %q", command.Path)
		}
		if command.Timeout != 3*time.Minute {
			t.Fatalf("unexpected timeout: %s", command.Timeout)
		}
		if maxOutputBytes != 64*1024 {
			t.Fatalf("unexpected output cap: %d", maxOutputBytes)
		}
		inputPath := command.Args[len(command.Args)-1]
		data, err := os.ReadFile(inputPath)
		if err != nil {
			t.Fatalf("expected transient input file: %v", err)
		}
		var payload CodexTaskInput
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode input: %v", err)
		}
		if payload.TaskID != "task-a" || payload.AutomationRunID == "" || payload.VerificationRequirement == "" {
			t.Fatalf("unexpected codex input: %+v", payload)
		}
		sawInput = true
		return CodexRunResult{ExitCode: 0, Duration: 2 * time.Second}, nil
	}
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if !sawInput {
		t.Fatal("expected codex runner to execute")
	}
	if run.Status != RunStatusVerifying {
		t.Fatalf("expected verifier-required status, got %q", run.Status)
	}
	if run.SafeSummary != "codex_cli_completed_verification_required" {
		t.Fatalf("unexpected safe summary: %q", run.SafeSummary)
	}
	if len(svc.store.(*testStore).attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(svc.store.(*testStore).attempts))
	}
}

func TestExternalRunNowQueuesWithoutServerSideCodex(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	svc.codexRunner = func(context.Context, CodexCommand, int64) (CodexRunResult, error) {
		t.Fatal("external mode must not execute codex in the server process")
		return CodexRunResult{}, nil
	}
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		t.Fatalf("expected codex runner in external mode, got %q", run.RunnerKind)
	}
	if run.Status != RunStatusQueued {
		t.Fatalf("expected queued run, got %q", run.Status)
	}
	if run.SafeSummary != "external_runner_queued" {
		t.Fatalf("unexpected summary: %q", run.SafeSummary)
	}
}

func TestExternalClaimAndCompleteAttempt(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1, DefaultMaxRuntime: 7 * time.Minute})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != queued.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("unexpected claimed run: %+v", claimed.Run)
	}
	if claimed.CodexInput.TaskID != "task-a" || claimed.CodexInput.AutomationRunID != queued.ID {
		t.Fatalf("unexpected codex input: %+v", claimed.CodexInput)
	}
	if claimed.TimeoutMS != (7 * time.Minute).Milliseconds() {
		t.Fatalf("unexpected timeout: %d", claimed.TimeoutMS)
	}

	done, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted, DurationMS: 1234})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if done.Status != RunStatusVerifying {
		t.Fatalf("expected verifying after external completion, got %q", done.Status)
	}
	if len(svc.store.(*testStore).attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(svc.store.(*testStore).attempts))
	}
}

func newTestService(t *testing.T, options Options) *Service {
	t.Helper()
	svc := New(newTestStore(), &fakeWorkTasks{}, options)
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	return svc
}

func createTestAutomation(t *testing.T, ctx context.Context, svc *Service) Automation {
	t.Helper()
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{ProjectID: "project-1", AutomationRef: "auto/ref", Title: "Automation", Purpose: "Run safe work tasks", AgentID: "agent-1", PlanID: "plan-1", PermissionRef: "permission/default"})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	return automation
}

func readyTask(id string, ref string, files []string) projectworkplan.WorkTask {
	return projectworkplan.WorkTask{
		ID:                      id,
		ProjectID:               "project-1",
		PlanID:                  "plan-1",
		TaskRef:                 ref,
		Title:                   "Task " + ref,
		Status:                  projectworkplan.WorkTaskStatusReady,
		LikelyFilesAffected:     files,
		VerificationRequirement: "orchestrator runs focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
}

type fakeWorkTasks struct {
	tasks map[string]projectworkplan.WorkTask
}

type testStore struct {
	automation map[string]Automation
	runs       map[string]AutomationRun
	batches    map[string]AutomationParallelBatch
	attempts   map[string]AutomationAttempt
}

func newTestStore() *testStore {
	return &testStore{automation: map[string]Automation{}, runs: map[string]AutomationRun{}, batches: map[string]AutomationParallelBatch{}, attempts: map[string]AutomationAttempt{}}
}

func (store *testStore) CreateAutomation(_ context.Context, value Automation) (Automation, error) {
	store.automation[value.ID] = value
	return value, nil
}

func (store *testStore) GetAutomation(_ context.Context, projectID string, automationID string) (Automation, error) {
	value, ok := store.automation[automationID]
	if !ok || value.ProjectID != projectID {
		return Automation{}, errors.New("not found")
	}
	return value, nil
}

func (store *testStore) ListAutomations(_ context.Context, filter AutomationFilter) ([]Automation, error) {
	var out []Automation
	for _, value := range store.automation {
		if value.ProjectID == filter.ProjectID {
			out = append(out, value)
		}
	}
	return out, nil
}

func (store *testStore) UpdateAutomation(_ context.Context, value Automation) (Automation, error) {
	store.automation[value.ID] = value
	return value, nil
}

func (store *testStore) CreateRun(_ context.Context, value AutomationRun) (AutomationRun, error) {
	store.runs[value.ID] = value
	return value, nil
}

func (store *testStore) GetRun(_ context.Context, projectID string, runID string) (AutomationRun, error) {
	value, ok := store.runs[runID]
	if !ok || value.ProjectID != projectID {
		return AutomationRun{}, errors.New("not found")
	}
	return value, nil
}

func (store *testStore) ListRuns(_ context.Context, filter RunFilter) ([]AutomationRun, error) {
	var out []AutomationRun
	for _, value := range store.runs {
		if value.ProjectID == filter.ProjectID {
			out = append(out, value)
		}
	}
	return out, nil
}

func (store *testStore) UpdateRun(_ context.Context, value AutomationRun) (AutomationRun, error) {
	store.runs[value.ID] = value
	return value, nil
}

func (store *testStore) CreateAttempt(_ context.Context, value AutomationAttempt) (AutomationAttempt, error) {
	store.attempts[value.ID] = value
	return value, nil
}

func (store *testStore) CreateParallelBatch(_ context.Context, value AutomationParallelBatch) (AutomationParallelBatch, error) {
	store.batches[value.ID] = value
	return value, nil
}

func (store *testStore) GetParallelBatch(_ context.Context, projectID string, batchID string) (AutomationParallelBatch, error) {
	value, ok := store.batches[batchID]
	if !ok || value.ProjectID != projectID {
		return AutomationParallelBatch{}, errors.New("not found")
	}
	return value, nil
}

func (fake *fakeWorkTasks) GetWorkTask(_ context.Context, _ string, taskID string) (projectworkplan.WorkTask, error) {
	if fake.tasks == nil {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	task, ok := fake.tasks[taskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	return task, nil
}

func (fake *fakeWorkTasks) ListOpenWorkTasks(_ context.Context, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	var out []projectworkplan.WorkTask
	for _, task := range fake.tasks {
		if task.ProjectID != filter.ProjectID {
			continue
		}
		if filter.PlanID != "" && task.PlanID != filter.PlanID {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}

func (fake *fakeWorkTasks) ClaimWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) StartWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) AttachEvidence(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachVerifierResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) CompleteWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) FailWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) BlockWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}
