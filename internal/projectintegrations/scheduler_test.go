package projectintegrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

func TestScheduler_SchedulesOnlyEnabledIngestionProviders(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Jira.Polling.IngestionEnabled = true
	project.Integrations.Confluence.Polling.IngestionEnabled = true
	project.Integrations.Confluence.Enabled = false

	scheduler := newTestScheduler(t, []config.Project{project}, &schedulerFakeRunner{})
	schedules := scheduler.ScheduledProviders()
	if len(schedules) != 1 {
		t.Fatalf("expected one scheduled provider, got %#v", schedules)
	}
	if schedules[0].ProjectID != "project-1" || schedules[0].Provider != ProviderJira || schedules[0].IncrementalInterval != time.Minute {
		t.Fatalf("unexpected schedule: %#v", schedules[0])
	}
	assertOmits(t, mustJSON(t, schedules),
		"https://tenant.atlassian.net",
		"tenant-cloud-id",
		"ACME",
		"OPS",
		"MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
		"MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
		"/home/mac",
	)
}

func TestScheduler_StartRunsIncrementalPollForManualInitialSync(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Jira.Polling.IngestionEnabled = true
	project.Integrations.Jira.Polling.InitialFullSync = "manual"
	project.Integrations.Confluence = nil
	sleepCalls := make(chan time.Duration, 2)
	runner := &schedulerFakeRunner{started: make(chan schedulerPollCall, 1)}
	scheduler := newTestSchedulerWithSleep(t, []config.Project{project}, runner, sleepOnceThenBlock(sleepCalls))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })

	if delay := receiveDuration(t, sleepCalls); delay != time.Minute {
		t.Fatalf("expected first incremental interval, got %s", delay)
	}
	call := receiveSchedulerCall(t, runner.started)
	if call.kind != SyncKindIncremental {
		t.Fatalf("expected incremental poll, got %#v", call)
	}
}

func TestScheduler_InitialFullSyncOnStartRunsInitialFullPoll(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Jira.Polling.IngestionEnabled = true
	project.Integrations.Jira.Polling.InitialFullSync = "on_start"
	project.Integrations.Confluence = nil
	runner := &schedulerFakeRunner{started: make(chan schedulerPollCall, 1)}
	scheduler := newTestSchedulerWithSleep(t, []config.Project{project}, runner, blockSleep)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })

	call := receiveSchedulerCall(t, runner.started)
	if call.kind != SyncKindInitialFull {
		t.Fatalf("expected initial full poll, got %#v", call)
	}
}

func TestScheduler_NoOpIncrementalUsesReturnedIdleSleep(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Jira.Polling.IngestionEnabled = true
	project.Integrations.Jira.Polling.EmptyPollSleep = 5 * time.Minute
	project.Integrations.Jira.Polling.MaxIdleSleep = 12 * time.Minute
	project.Integrations.Confluence = nil
	sleepCalls := make(chan time.Duration, 3)
	runner := &schedulerFakeRunner{
		started: make(chan schedulerPollCall, 1),
		result: PollRunResult{Run: SyncRun{
			ID:        "run-1",
			ProjectID: "project-1",
			Provider:  ProviderJira,
			Kind:      SyncKindIncremental,
			Status:    SyncRunStatusNoOp,
			EmptyPoll: true,
			IdleSleep: 15 * time.Minute,
		}},
	}
	scheduler := newTestSchedulerWithSleep(t, []config.Project{project}, runner, sleepOnceThenBlock(sleepCalls))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })

	if delay := receiveDuration(t, sleepCalls); delay != time.Minute {
		t.Fatalf("expected first incremental interval, got %s", delay)
	}
	_ = receiveSchedulerCall(t, runner.started)
	if delay := receiveDuration(t, sleepCalls); delay != 12*time.Minute {
		t.Fatalf("expected idle sleep clamped to max_idle_sleep, got %s", delay)
	}
}

func TestScheduler_RunProviderPollSingleFlightPreventsOverlap(t *testing.T) {
	runner := &schedulerFakeRunner{
		started: make(chan schedulerPollCall, 1),
		release: make(chan struct{}),
	}
	scheduler := newTestScheduler(t, nil, runner)
	errs := make(chan error, 1)
	go func() {
		_, err := scheduler.RunProviderPoll(context.Background(), "project-1", ProviderJira, SyncKindIncremental)
		errs <- err
	}()
	_ = receiveSchedulerCall(t, runner.started)

	_, err := scheduler.RunProviderPoll(context.Background(), "project-1", ProviderJira, SyncKindIncremental)
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected overlapping poll rejection, got %v", err)
	}
	close(runner.release)
	if err := <-errs; err != nil {
		t.Fatalf("first poll returned error: %v", err)
	}
}

func TestScheduler_SubmitProviderPollReturnsBeforeExecutionCompletes(t *testing.T) {
	runner := &schedulerFakeRunner{
		started: make(chan schedulerPollCall, 1),
		release: make(chan struct{}),
	}
	scheduler := newTestScheduler(t, nil, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })

	run, err := scheduler.SubmitProviderPoll(context.Background(), "project-1", ProviderJira, SyncKindInitialFull)
	if err != nil {
		t.Fatalf("submit provider poll: %v", err)
	}
	if run.ID == "" || run.Status != SyncRunStatusPending || run.Kind != SyncKindInitialFull {
		t.Fatalf("unexpected submitted run: %#v", run)
	}
	call := receiveSchedulerCall(t, runner.started)
	if call.projectID != "project-1" || call.provider != ProviderJira || call.kind != SyncKindInitialFull {
		t.Fatalf("unexpected async execution call: %#v", call)
	}
	if _, err := scheduler.SubmitProviderPoll(context.Background(), "project-1", ProviderJira, SyncKindIncremental); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected overlapping async poll rejection, got %v", err)
	}
	close(runner.release)
}

func TestScheduler_ErrorsAreRedacted(t *testing.T) {
	runner := &schedulerFakeRunner{
		err: fmt.Errorf("provider failure MIVIA_ATLASSIAN_TOKEN_PROJECT_1 /home/mac/secret-atlassian-credentials.json ACME raw-provider-cursor-token"),
	}
	scheduler := newTestScheduler(t, nil, runner)

	_, err := scheduler.RunProviderPoll(context.Background(), "project-1", ProviderJira, SyncKindIncremental)
	if !errors.Is(err, ErrProviderRequestFailed) {
		t.Fatalf("expected provider request failure, got %v", err)
	}
	assertOmits(t, err.Error(),
		"MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
		"/home/mac/secret-atlassian-credentials.json",
		"ACME",
		"raw-provider-cursor-token",
	)
}

type schedulerPollCall struct {
	projectID string
	provider  Provider
	kind      SyncKind
}

type schedulerFakeRunner struct {
	mu      sync.Mutex
	calls   []schedulerPollCall
	started chan schedulerPollCall
	release chan struct{}
	result  PollRunResult
	err     error
}

func (runner *schedulerFakeRunner) RunProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (PollRunResult, error) {
	call := schedulerPollCall{projectID: projectID, provider: provider, kind: kind}
	runner.mu.Lock()
	runner.calls = append(runner.calls, call)
	runner.mu.Unlock()
	if runner.started != nil {
		select {
		case runner.started <- call:
		default:
		}
	}
	if runner.release != nil {
		select {
		case <-runner.release:
		case <-ctx.Done():
			return PollRunResult{}, ctx.Err()
		}
	}
	if runner.err != nil {
		return PollRunResult{}, runner.err
	}
	if runner.result.Run.ID != "" {
		return runner.result, nil
	}
	return PollRunResult{Run: SyncRun{
		ID:        "run-1",
		ProjectID: projectID,
		Provider:  provider,
		Kind:      kind,
		Status:    SyncRunStatusCompleted,
	}}, nil
}

func (runner *schedulerFakeRunner) PrepareProviderPoll(_ context.Context, projectID string, provider Provider, kind SyncKind) (SyncRun, error) {
	if runner.err != nil {
		return SyncRun{}, runner.err
	}
	return SyncRun{
		ID:        "run-async-1",
		ProjectID: projectID,
		Provider:  provider,
		Kind:      kind,
		Status:    SyncRunStatusPending,
		StartedAt: time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
	}, nil
}

func (runner *schedulerFakeRunner) ExecutePreparedProviderPoll(ctx context.Context, run SyncRun) (PollRunResult, error) {
	call := schedulerPollCall{projectID: run.ProjectID, provider: run.Provider, kind: run.Kind}
	runner.mu.Lock()
	runner.calls = append(runner.calls, call)
	runner.mu.Unlock()
	if runner.started != nil {
		select {
		case runner.started <- call:
		default:
		}
	}
	if runner.release != nil {
		select {
		case <-runner.release:
		case <-ctx.Done():
			return PollRunResult{}, ctx.Err()
		}
	}
	if runner.err != nil {
		return PollRunResult{}, runner.err
	}
	run.Status = SyncRunStatusCompleted
	return PollRunResult{Run: run}, nil
}

func (runner *schedulerFakeRunner) FailPreparedProviderPoll(_ context.Context, run SyncRun, category string) (SyncRun, error) {
	run.Status = SyncRunStatusFailed
	run.ErrorCategory = category
	return run, nil
}

func newTestScheduler(t *testing.T, projects []config.Project, runner PollRunner) *Scheduler {
	t.Helper()
	return newTestSchedulerWithSleep(t, projects, runner, blockSleep)
}

func newTestSchedulerWithSleep(t *testing.T, projects []config.Project, runner PollRunner, sleep func(context.Context, time.Duration) error) *Scheduler {
	t.Helper()
	scheduler, err := NewScheduler(projects, runner, SchedulerOptions{Sleep: sleep})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	return scheduler
}

func blockSleep(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func sleepOnceThenBlock(calls chan<- time.Duration) func(context.Context, time.Duration) error {
	var mu sync.Mutex
	count := 0
	return func(ctx context.Context, duration time.Duration) error {
		calls <- duration
		mu.Lock()
		count++
		current := count
		mu.Unlock()
		if current == 1 {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
}

func receiveSchedulerCall(t *testing.T, calls <-chan schedulerPollCall) schedulerPollCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler call")
		return schedulerPollCall{}
	}
}

func receiveDuration(t *testing.T, calls <-chan time.Duration) time.Duration {
	t.Helper()
	select {
	case duration := <-calls:
		return duration
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sleep call")
		return 0
	}
}
