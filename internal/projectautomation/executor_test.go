package projectautomation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestExecutorDoesNothingWhenDisabled(t *testing.T) {
	ctx := context.Background()
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1})
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	executor := NewExecutor(svc, ExecutorOptions{Enabled: false, RunnerEnabled: true, RunnerExecution: RunnerExecutionInProcess, ProjectIDs: []string{"project-1"}})

	executor.pollOnce(ctx)

	run, err := svc.GetRun(ctx, queued.ProjectID, queued.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusQueued {
		t.Fatalf("expected queued run to remain untouched, got %q", run.Status)
	}
}

func TestExecutorStartStopCleanly(t *testing.T) {
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1})
	executor := NewExecutor(svc, ExecutorOptions{
		Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionInProcess,
		PollInterval: time.Hour, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, PerAgentWorkerLimit: 1,
		ProjectIDs: []string{"project-1"},
	})

	if err := executor.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := executor.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestExecutorCallsServiceForQueuedRuns(t *testing.T) {
	ctx := context.Background()
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1})
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	executor := NewExecutor(svc, ExecutorOptions{
		Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionInProcess,
		GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, PerAgentWorkerLimit: 1,
		ProjectIDs: []string{"project-1"},
	})

	executor.pollOnce(ctx)
	waitForRunStatus(t, svc, queued.ProjectID, queued.ID, RunStatusVerifying)

	store := svc.store.(*testStore)
	if len(store.attempts) != 1 {
		t.Fatalf("expected one service attempt, got %d", len(store.attempts))
	}
}

func TestExecutorProcessesManualQueuedRuns(t *testing.T) {
	ctx := context.Background()
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, AllowManualRunner: true, MaxParallelTasks: 1})
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindManual})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	executor := NewExecutor(svc, ExecutorOptions{
		Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionInProcess,
		GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, PerAgentWorkerLimit: 1,
		ProjectIDs: []string{"project-1"},
	})

	executor.pollOnce(ctx)
	waitForRunStatus(t, svc, queued.ProjectID, queued.ID, RunStatusVerifying)
}

func TestExecuteQueuedRunManualEndsInVerifying(t *testing.T) {
	ctx := context.Background()
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, AllowManualRunner: true, MaxParallelTasks: 1})
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindManual})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}

	run, err := svc.ExecuteQueuedRun(ctx, queued.ProjectID, queued.ID)
	if err != nil {
		t.Fatalf("ExecuteQueuedRun returned error: %v", err)
	}
	if run.Status != RunStatusVerifying || run.FailureCategory != "verification_required" {
		t.Fatalf("expected manual run to require verification, got %#v", run)
	}
}

func TestExecutorRespectsGlobalWorkerLimit(t *testing.T) {
	ctx := context.Background()
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1})
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	var mu sync.Mutex
	active := 0
	maxActive := 0
	svc.codexRunner = func(context.Context, CodexCommand, int64) (CodexRunResult, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		started <- struct{}{}
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return CodexRunResult{ExitCode: 0, Duration: time.Millisecond}, nil
	}
	createQueuedExecutorRuns(t, ctx, svc, 3, []string{"agent-a", "agent-b", "agent-c"})
	executor := NewExecutor(svc, ExecutorOptions{
		Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionInProcess,
		GlobalWorkerCount: 2, PerProjectWorkerLimit: 2, PerAgentWorkerLimit: 1,
		ProjectIDs: []string{"project-1"},
	})

	executor.pollOnce(ctx)
	waitForStarts(t, started, 2)
	mu.Lock()
	got := maxActive
	mu.Unlock()
	if got > 2 {
		t.Fatalf("global worker limit exceeded: %d", got)
	}
	close(release)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := executor.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestExecutorRespectsPerAgentLimit(t *testing.T) {
	ctx := context.Background()
	svc := newExecutorTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1})
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	var mu sync.Mutex
	active := 0
	maxActive := 0
	svc.codexRunner = func(context.Context, CodexCommand, int64) (CodexRunResult, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		started <- struct{}{}
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return CodexRunResult{ExitCode: 0, Duration: time.Millisecond}, nil
	}
	createQueuedExecutorRuns(t, ctx, svc, 3, []string{"agent-a", "agent-a", "agent-a"})
	executor := NewExecutor(svc, ExecutorOptions{
		Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionInProcess,
		GlobalWorkerCount: 3, PerProjectWorkerLimit: 3, PerAgentWorkerLimit: 1,
		ProjectIDs: []string{"project-1"},
	})

	executor.pollOnce(ctx)
	waitForStarts(t, started, 1)
	mu.Lock()
	got := maxActive
	mu.Unlock()
	if got > 1 {
		t.Fatalf("per-agent worker limit exceeded: %d", got)
	}
	close(release)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := executor.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func newExecutorTestService(t *testing.T, options Options) *Service {
	t.Helper()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "task-a", []string{"internal/foo.go"}),
		"task-b": readyTask("task-b", "task-b", []string{"internal/bar.go"}),
		"task-c": readyTask("task-c", "task-c", []string{"internal/baz.go"}),
	}}
	svc := New(newTestStore(), fake, options)
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	svc.codexAvailable = func() bool { return true }
	svc.codexPath = func() (string, bool) { return "/usr/local/bin/codex", true }
	svc.codexRunner = func(context.Context, CodexCommand, int64) (CodexRunResult, error) {
		return CodexRunResult{ExitCode: 0, Duration: time.Millisecond}, nil
	}
	return svc
}

func createQueuedExecutorRuns(t *testing.T, ctx context.Context, svc *Service, count int, agents []string) {
	t.Helper()
	taskIDs := []string{"task-a", "task-b", "task-c"}
	for i := 0; i < count; i++ {
		agentID := agents[i]
		automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
			ProjectID:     "project-1",
			AutomationRef: "auto/ref/" + taskIDs[i],
			Title:         "Automation",
			Purpose:       "Run safe work tasks",
			AgentID:       agentID,
			PlanID:        "plan-1",
			PermissionRef: "permission/default",
		})
		if err != nil {
			t.Fatalf("CreateAutomation returned error: %v", err)
		}
		if _, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: taskIDs[i], RunnerKind: RunnerKindCodexCLI}); err != nil {
			t.Fatalf("SubmitRun returned error: %v", err)
		}
	}
}

func waitForRunStatus(t *testing.T, svc *Service, projectID, runID, status string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		run, err := svc.GetRun(context.Background(), projectID, runID)
		if err != nil {
			t.Fatalf("GetRun returned error: %v", err)
		}
		if run.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	run, _ := svc.GetRun(context.Background(), projectID, runID)
	t.Fatalf("timed out waiting for status %q, got %q", status, run.Status)
}

func waitForStarts(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for start %d", i+1)
		}
	}
	select {
	case <-started:
		t.Fatalf("unexpected extra worker start")
	case <-time.After(20 * time.Millisecond):
	}
}
