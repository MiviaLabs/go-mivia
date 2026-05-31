package projectingestion

import (
	"context"
	"runtime"
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

func TestSchedulerDefaultsUseRuntimeCPUCount(t *testing.T) {
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{})
	want := runtime.NumCPU()
	if want <= 0 {
		want = 1
	}
	if scheduler.options.GlobalWorkerCount != want || scheduler.options.PerProjectWorkerLimit != want {
		t.Fatalf("expected scheduler defaults to use runtime CPU count %d, got %+v", want, scheduler.options)
	}
}

func TestSchedulerProjectWideTasksDoNotOverlapForSameProject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 2, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	fullDone := make(chan error, 1)
	go func() {
		_, err := scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
		fullDone <- err
	}()
	runner.waitStarted(t, "full_scan", "project-a")

	if _, err := scheduler.SubmitRebuildSearchIndex(ctx, "project-a"); err != nil {
		t.Fatalf("submit search index rebuild: %v", err)
	}
	runner.assertNotStarted(t, "search_index_rebuild", "project-a", 30*time.Millisecond)

	runner.release("full_scan", "project-a")
	if err := <-fullDone; err != nil {
		t.Fatalf("full scan: %v", err)
	}
	runner.waitStarted(t, "search_index_rebuild", "project-a")
	runner.release("search_index_rebuild", "project-a")
}

func TestSchedulerProjectWideTasksRunInParallelForDifferentProjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 2, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	fullDone := make(chan error, 1)
	go func() {
		_, err := scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
		fullDone <- err
	}()
	runner.waitStarted(t, "full_scan", "project-a")

	if _, err := scheduler.SubmitRebuildSearchIndex(ctx, "project-b"); err != nil {
		t.Fatalf("submit search index rebuild: %v", err)
	}
	runner.waitStarted(t, "search_index_rebuild", "project-b")
	runner.release("search_index_rebuild", "project-b")
	runner.release("full_scan", "project-a")
	if err := <-fullDone; err != nil {
		t.Fatalf("full scan: %v", err)
	}
}

func TestSchedulerPathRunsDuringSameProjectFullScan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	fullDone := make(chan error, 1)
	go func() {
		_, err := scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
		fullDone <- err
	}()
	runner.waitStarted(t, "full_scan", "project-a")

	pathDone := make(chan error, 1)
	go func() {
		_, err := scheduler.SubmitPath(ctx, "project-a", "src/app.go", TriggerLive)
		pathDone <- err
	}()
	runner.waitStarted(t, "path", "project-a")
	runner.release("path", "project-a")
	if err := <-pathDone; err != nil {
		t.Fatalf("path ingest: %v", err)
	}

	runner.release("full_scan", "project-a")
	if err := <-fullDone; err != nil {
		t.Fatalf("full scan: %v", err)
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

func TestSchedulerAsyncPreparedRunMarkedFailedWhenQueueFull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 1, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	if _, err := scheduler.SubmitFullScanAsync(ctx, "project-a", TriggerManual); err != nil {
		t.Fatalf("submit first async full scan: %v", err)
	}
	runner.waitStarted(t, "full_scan", "project-a")
	if _, err := scheduler.SubmitFullScanAsync(ctx, "project-b", TriggerManual); err != nil {
		t.Fatalf("submit queued async full scan: %v", err)
	}
	failed, err := scheduler.SubmitFullScanAsync(ctx, "project-c", TriggerManual)
	if err == nil {
		t.Fatalf("expected queue full error")
	}
	if failed.Status != RunStatusFailed || failed.ErrorCategory != "queue_full" {
		t.Fatalf("expected failed queue_full run, got %#v", failed)
	}

	runner.release("full_scan", "project-a")
	runner.waitStarted(t, "full_scan", "project-b")
	runner.release("full_scan", "project-b")
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

func TestSchedulerPathDuringFullScanStillUsesPerProjectLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 3, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	go scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
	runner.waitStarted(t, "full_scan", "project-a")
	go scheduler.SubmitPath(ctx, "project-a", "src/app.go", TriggerLive)
	runner.waitStarted(t, "path", "project-a")
	go scheduler.SubmitPath(ctx, "project-a", "src/other.go", TriggerLive)
	runner.assertStartedCount(t, "path", "project-a", 1, 30*time.Millisecond)
	runner.release("path", "project-a")
	runner.waitStartedCount(t, "path", "project-a", 2)
	runner.release("path", "project-a")
	runner.release("full_scan", "project-a")
}

func TestSchedulerGlobalLimitStillCapsPathPromotion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := newBlockingSchedulerRunner()
	scheduler := NewScheduler(runner, SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1, LivePathPriority: true})
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	defer scheduler.Stop(context.Background())

	go scheduler.SubmitFullScan(ctx, "project-a", TriggerManual)
	runner.waitStarted(t, "full_scan", "project-a")
	go scheduler.SubmitPath(ctx, "project-a", "src/app.go", TriggerLive)
	runner.assertNotStarted(t, "path", "project-a", 30*time.Millisecond)
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
	if len(diagnostics.ProjectWideTaskCount) != 0 {
		t.Fatalf("expected no active project-wide tasks: %#v", diagnostics.ProjectWideTaskCount)
	}
}

type blockingSchedulerRunner struct {
	mu       sync.Mutex
	started  map[string]int
	releases map[string]chan struct{}
	notify   chan string
	failed   map[string]string
}

func newBlockingSchedulerRunner() *blockingSchedulerRunner {
	return &blockingSchedulerRunner{
		started:  make(map[string]int),
		releases: make(map[string]chan struct{}),
		notify:   make(chan string, 32),
		failed:   make(map[string]string),
	}
}

func (runner *blockingSchedulerRunner) IngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return runner.block(ctx, "full_scan", projectID)
}

func (runner *blockingSchedulerRunner) IngestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	return runner.block(ctx, "path", projectID)
}

func (runner *blockingSchedulerRunner) PrepareProjectRun(_ context.Context, projectID string, trigger Trigger) (Run, error) {
	return Run{ID: "prepared-" + projectID, ProjectID: projectID, Trigger: trigger, Status: RunStatusPending}, nil
}

func (runner *blockingSchedulerRunner) ExecutePreparedProjectRun(ctx context.Context, run Run) (Run, error) {
	completed, err := runner.block(ctx, "full_scan", run.ProjectID)
	if err != nil {
		return run, err
	}
	run.Status = completed.Status
	return run, nil
}

func (runner *blockingSchedulerRunner) ExecutePreparedSearchIndexRebuild(ctx context.Context, run Run) (Run, error) {
	completed, err := runner.block(ctx, "search_index_rebuild", run.ProjectID)
	if err != nil {
		return run, err
	}
	run.Status = completed.Status
	return run, nil
}

func (runner *blockingSchedulerRunner) FailPreparedProjectRun(_ context.Context, run Run, category string) (Run, error) {
	runner.mu.Lock()
	runner.failed[run.ID] = category
	runner.mu.Unlock()
	run.Status = RunStatusFailed
	run.ErrorCategory = category
	return run, nil
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
	runner.waitStartedCount(t, taskType, projectID, 1)
}

func (runner *blockingSchedulerRunner) waitStartedCount(t *testing.T, taskType string, projectID string, count int) {
	t.Helper()
	want := taskType + ":" + projectID
	deadline := time.After(time.Second)
	for runner.startedCount(taskType, projectID) < count {
		select {
		case <-runner.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %s count %d", want, count)
		}
	}
}

func (runner *blockingSchedulerRunner) startedCount(taskType string, projectID string) int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.started[taskType+":"+projectID]
}

func (runner *blockingSchedulerRunner) assertNotStarted(t *testing.T, taskType string, projectID string, wait time.Duration) {
	t.Helper()
	runner.assertStartedCount(t, taskType, projectID, 0, wait)
}

func (runner *blockingSchedulerRunner) assertStartedCount(t *testing.T, taskType string, projectID string, count int, wait time.Duration) {
	t.Helper()
	time.Sleep(wait)
	if started := runner.startedCount(taskType, projectID); started != count {
		t.Fatalf("expected %s:%s started count %d, got %d", taskType, projectID, count, started)
	}
}

func (runner *blockingSchedulerRunner) release(taskType string, projectID string) {
	runner.mu.Lock()
	key := taskType + ":" + projectID
	release := runner.releases[key]
	delete(runner.releases, key)
	runner.mu.Unlock()
	if release != nil {
		close(release)
	}
}
