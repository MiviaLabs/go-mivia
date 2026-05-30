package projectingestion

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSchedulerFullScansBothMakeProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	errs := make(chan error, 2)
	go func() {
		_, err := scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
		errs <- err
	}()
	go func() {
		_, err := scheduler.SubmitFullScan(ctx, "project-b", TriggerManual)
		errs <- err
	}()

	runner.waitStarted(t, "full_scan", "project-a")
	runner.waitStarted(t, "full_scan", "project-b")
	runner.release("full_scan", "project-a")
	runner.release("full_scan", "project-b")
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("submit full scan: %v", err)
		}
	}
}

func TestSchedulerSubmitFullScanAsyncReturnsBeforeExecutionCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	run, err := scheduler.SubmitFullScanAsync(ctx, "project-a", TriggerManual)
	if err != nil {
		t.Fatalf("submit async full scan: %v", err)
	}
	if run.ID != "prepared-project-a" || run.Status != RunStatusPending {
		t.Fatalf("expected pending prepared run, got %#v", run)
	}
	runner.waitStarted(t, "full_scan", "project-a")
	runner.release("full_scan", "project-a")
}

func TestSchedulerPathStartsWhileOtherProjectFullScanActive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	go scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
	runner.waitStarted(t, "full_scan", "project-a")

	pathDone := make(chan error, 1)
	go func() {
		_, err := scheduler.SubmitPath(ctx, "project-b", "src/app.go", TriggerLive)
		pathDone <- err
	}()
	runner.waitStarted(t, "path", "project-b")
	runner.release("path", "project-b")
	if err := <-pathDone; err != nil {
		t.Fatalf("submit path: %v", err)
	}
	runner.release("full_scan", "project-a")
}

func TestSchedulerPerProjectAndGlobalLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	go scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
	runner.waitStarted(t, "full_scan", "project-a")
	go scheduler.SubmitPath(ctx, "project-a", "src/app.go", TriggerLive)
	time.Sleep(20 * time.Millisecond)
	if started := runner.startedCount("path", "project-a"); started != 0 {
		t.Fatalf("path for same project started before per-project slot released: %d", started)
	}
	runner.release("full_scan", "project-a")
	runner.waitStarted(t, "path", "project-a")
	runner.release("path", "project-a")
}

func TestSchedulerShutdownCancelsQueuedWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	go scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
	runner.waitStarted(t, "full_scan", "project-a")
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := scheduler.Stop(stopCtx); err != nil {
		t.Fatalf("stop scheduler: %v", err)
	}
}

func TestSchedulerDiagnosticsAreSafeCounters(t *testing.T) {
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, LivePathPriority: true})
	diagnostics := scheduler.Diagnostics()
	if diagnostics.QueueDepth != 0 || diagnostics.LiveQueueDepth != 0 || diagnostics.FullScanQueueDepth != 0 || diagnostics.ActiveTaskCount != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
	if len(diagnostics.ActiveProjectTaskCount) != 0 {
		t.Fatalf("expected no active projects: %#v", diagnostics.ActiveProjectTaskCount)
	}
}

type blockingSchedulerRunner struct {
	mu       sync.Mutex
	started  map[string]int
	releases map[string]chan struct{}
	notify   chan string
}

func newBlockingSchedulerRunner() *blockingSchedulerRunner {
	return &blockingSchedulerRunner{
		started:  make(map[string]int),
		releases: make(map[string]chan struct{}),
		notify:   make(chan string, 32),
	}
}

func (runner *blockingSchedulerRunner) IngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return runner.block(ctx, "full_scan", projectID)
}

func (runner *blockingSchedulerRunner) IngestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	return runner.block(ctx, "path", projectID)
}

func (runner *blockingSchedulerRunner) PrepareProjectRun(context.Context, string, Trigger) (Run, error) {
	return Run{ID: "prepared-project-a", ProjectID: "project-a", Trigger: TriggerManual, Status: RunStatusPending}, nil
}

func (runner *blockingSchedulerRunner) ExecutePreparedProjectRun(ctx context.Context, run Run) (Run, error) {
	completed, err := runner.block(ctx, "full_scan", run.ProjectID)
	if err != nil {
		return run, err
	}
	run.Status = completed.Status
	return run, nil
}

func (runner *blockingSchedulerRunner) FailPreparedProjectRun(context.Context, Run, string) (Run, error) {
	return Run{}, nil
}

func (runner *blockingSchedulerRunner) block(ctx context.Context, taskType string, projectID string) (Run, error) {
	key := taskType + ":" + projectID
	runner.mu.Lock()
	runner.started[key]++
	release := runner.releases[key]
	if release == nil {
		release = make(chan struct{})
		runner.releases[key] = release
	}
	runner.mu.Unlock()
	runner.notify <- key
	select {
	case <-release:
		return Run{ProjectID: projectID, Trigger: TriggerManual, Status: RunStatusCompleted}, nil
	case <-ctx.Done():
		return Run{}, ctx.Err()
	}
}

func (runner *blockingSchedulerRunner) waitStarted(t *testing.T, taskType string, projectID string) {
	t.Helper()
	want := taskType + ":" + projectID
	deadline := time.After(time.Second)
	for runner.startedCount(taskType, projectID) == 0 {
		select {
		case <-runner.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func (runner *blockingSchedulerRunner) startedCount(taskType string, projectID string) int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.started[taskType+":"+projectID]
}

func (runner *blockingSchedulerRunner) release(taskType string, projectID string) {
	runner.mu.Lock()
	release := runner.releases[taskType+":"+projectID]
	runner.mu.Unlock()
	if release != nil {
		close(release)
	}
}
