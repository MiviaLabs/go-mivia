package projectingestion

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestOrchestrator_GlobalDisabledDoesNotStartWatchers(t *testing.T) {
	registry := newLiveRegistry(t)
	created := false
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{
		LiveUpdatesEnabled: false,
	})
	orchestrator.SetWatcherFactory(func() (FileWatcher, error) {
		created = true
		return newFakeWatcher(), nil
	})

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	if created {
		t.Fatal("expected no watcher when global live updates are disabled")
	}
}

func TestNewOrchestrator_PreservesDisabledLivePathPriority(t *testing.T) {
	registry := newLiveRegistry(t)
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{
		LiveUpdatesEnabled: true,
		LivePathPriority:   false,
	})

	if orchestrator.options.LivePathPriority {
		t.Fatal("expected explicit disabled live path priority to be preserved")
	}
}

func TestOrchestrator_WatchesDirectoriesAndShutdownClosesWatcher(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	if err := os.MkdirAll(filepath.Join(project.CanonicalRootPath, "src", "nested"), 0o700); err != nil {
		t.Fatalf("mkdir source dirs: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project.CanonicalRootPath, "data", "ignored"), 0o700); err != nil {
		t.Fatalf("mkdir data dirs: %v", err)
	}
	watcher := newFakeWatcher()
	orchestrator := newTestOrchestrator(registry, &fakeIngestionRunner{}, watcher)

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	added := watcher.addedPaths()
	sort.Strings(added)
	expected := []string{
		project.CanonicalRootPath,
		filepath.Join(project.CanonicalRootPath, "src"),
		filepath.Join(project.CanonicalRootPath, "src", "nested"),
	}
	if !sameStrings(added, expected) {
		t.Fatalf("unexpected watched dirs:\n got %#v\nwant %#v", added, expected)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := orchestrator.Stop(stopCtx); err != nil {
		t.Fatalf("stop orchestrator: %v", err)
	}
	select {
	case <-watcher.closed:
	case <-time.After(time.Second):
		t.Fatal("expected watcher close")
	}
}

func TestOrchestrator_DebouncesCreateWriteRemoveRenameEvents(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	watcher := newFakeWatcher()
	ingestion := &fakeIngestionRunner{}
	orchestrator := newTestOrchestrator(registry, ingestion, watcher)

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	defer orchestrator.Stop(context.Background())

	watcher.events <- WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "created.go"), Op: WatchCreate}
	watcher.events <- WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "written.go"), Op: WatchWrite}
	watcher.events <- WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "removed.go"), Op: WatchRemove}
	watcher.events <- WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "renamed.go"), Op: WatchRename}

	got := ingestion.waitPaths(t, 4)
	sort.Strings(got)
	want := []string{"created.go", "removed.go", "renamed.go", "written.go"}
	if !sameStrings(got, want) {
		t.Fatalf("unexpected ingested paths: got %#v want %#v", got, want)
	}
}

func TestOrchestrator_NewDirectoryAddsRecursiveWatch(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	newDir := filepath.Join(project.CanonicalRootPath, "newdir")
	if err := os.MkdirAll(filepath.Join(newDir, "child"), 0o700); err != nil {
		t.Fatalf("mkdir new dir: %v", err)
	}
	watcher := newFakeWatcher()
	orchestrator := newTestOrchestrator(registry, &fakeIngestionRunner{}, watcher)

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	defer orchestrator.Stop(context.Background())
	watcher.events <- WatchEvent{Path: newDir, Op: WatchCreate}

	waitUntil(t, func() bool {
		added := watcher.addedPaths()
		return containsString(added, newDir) && containsString(added, filepath.Join(newDir, "child"))
	})
}

func TestOrchestrator_OverflowAndQueueFullTriggerRescan(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	watcher := newFakeWatcher()
	ingestion := &fakeIngestionRunner{}
	orchestrator := newTestOrchestrator(registry, ingestion, watcher)

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	defer orchestrator.Stop(context.Background())

	watcher.errors <- errFakeOverflow{}
	ingestion.waitRescans(t, 1)

	projectWatcher := &projectWatcher{
		project: project,
		events:  make(chan WatchEvent, 1),
		rescans: make(chan struct{}, 1),
	}
	projectWatcher.events <- WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "queued.go"), Op: WatchWrite}
	orchestrator.handleWatchEvent(projectWatcher, WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "dropped.go"), Op: WatchWrite})
	select {
	case <-projectWatcher.rescans:
	case <-time.After(time.Second):
		t.Fatal("expected queue full to request rescan")
	}
}

func TestOrchestrator_TaskQueueFullDefersSingleRescanWithoutRequeueLoop(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{})
	projectWatcher := &projectWatcher{
		project: project,
		tasks:   make(chan ingestTask, 1),
	}
	projectWatcher.tasks <- ingestTask{relativePath: "busy.go"}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if orchestrator.enqueueTask(ctx, projectWatcher, ingestTask{relativePath: "overflow.go"}) {
		t.Fatal("expected path task enqueue to fail when task queue is full")
	}

	done := make(chan bool, 1)
	go func() {
		done <- orchestrator.enqueueTask(ctx, projectWatcher, ingestTask{rescan: true})
	}()
	select {
	case <-done:
		t.Fatal("expected rescan enqueue to wait for task queue capacity")
	case <-time.After(20 * time.Millisecond):
	}

	<-projectWatcher.tasks
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("expected deferred rescan enqueue to succeed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deferred rescan enqueue")
	}
	task := <-projectWatcher.tasks
	if !task.rescan || task.relativePath != "" {
		t.Fatalf("expected one deferred rescan task, got %#v", task)
	}
}

func TestOrchestratorDefaultsUseBoundedWorkerCounts(t *testing.T) {
	registry := newLiveRegistry(t)
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{})
	if orchestrator.options.WorkerCount != defaultSchedulerGlobalWorkers ||
		orchestrator.options.GlobalWorkerCount != defaultSchedulerGlobalWorkers ||
		orchestrator.options.PerProjectWorkerLimit != defaultSchedulerPerProjectLimit {
		t.Fatalf("expected bounded orchestrator defaults, got %+v", orchestrator.options)
	}
}

func TestOrchestrator_InitialScanAndDisabledProjectFiltering(t *testing.T) {
	registry := newMixedRegistry(t)
	watcher := newFakeWatcher()
	ingestion := &fakeIngestionRunner{}
	orchestrator := NewOrchestrator(registry, ingestion, OrchestratorOptions{
		LiveUpdatesEnabled: true,
		DebounceInterval:   10 * time.Millisecond,
		QueueDepth:         4,
		WorkerCount:        1,
		InitialScanOnStart: true,
	})
	orchestrator.SetWatcherFactory(func() (FileWatcher, error) {
		return watcher, nil
	})

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	defer orchestrator.Stop(context.Background())
	ingestion.waitRescans(t, 1)
	if len(watcher.addedPaths()) == 0 {
		t.Fatal("expected live project watcher")
	}
}

func TestOrchestrator_WatcherFactoryFailureDegradesWithoutStartupFailure(t *testing.T) {
	registry := newLiveRegistry(t)
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{
		LiveUpdatesEnabled: true,
		QueueDepth:         4,
		WorkerCount:        1,
	})
	orchestrator.SetWatcherFactory(func() (FileWatcher, error) {
		return nil, errors.New("watch unavailable")
	})

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator must not fail on watcher factory error: %v", err)
	}
	defer orchestrator.Stop(context.Background())
	state := findWatchState(t, orchestrator.WatchStates(), "live_project")
	if state.Status != WatchStatusDegraded || state.LastErrorCategory != "watcher_create_failed" {
		t.Fatalf("expected degraded watcher state, got %#v", state)
	}
}

func TestOrchestrator_WatcherAddFailureDegradesWithoutStartupFailure(t *testing.T) {
	registry := newLiveRegistry(t)
	watcher := newFakeWatcher()
	watcher.addErr = errors.New("watch add failed")
	orchestrator := newTestOrchestrator(registry, &fakeIngestionRunner{}, watcher)

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator must not fail on add error: %v", err)
	}
	defer orchestrator.Stop(context.Background())
	state := findWatchState(t, orchestrator.WatchStates(), "live_project")
	if state.Status != WatchStatusDegraded || state.LastErrorCategory != "watch_add_failed" || state.FailedDirectoryCount == 0 {
		t.Fatalf("expected degraded add-failure state, got %#v", state)
	}
}

func TestOrchestrator_WatchDirectoryBudgetDegradesWithSkippedCount(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	if err := os.MkdirAll(filepath.Join(project.CanonicalRootPath, "src", "nested"), 0o700); err != nil {
		t.Fatalf("mkdir source dirs: %v", err)
	}
	watcher := newFakeWatcher()
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{
		LiveUpdatesEnabled:       true,
		DebounceInterval:         10 * time.Millisecond,
		QueueDepth:               8,
		WorkerCount:              1,
		MaxWatchedDirectoryCount: 1,
	})
	orchestrator.SetWatcherFactory(func() (FileWatcher, error) {
		return watcher, nil
	})

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	defer orchestrator.Stop(context.Background())
	state := findWatchState(t, orchestrator.WatchStates(), "live_project")
	if state.Status != WatchStatusDegraded || state.LastErrorCategory != "watch_directory_budget_exceeded" || state.SkippedDirectoryCount == 0 {
		t.Fatalf("expected degraded budget state, got %#v", state)
	}
	if len(watcher.addedPaths()) != 1 {
		t.Fatalf("expected watch budget to cap added paths, got %#v", watcher.addedPaths())
	}
}

func TestOrchestrator_WatchStateReportsBackpressureCounters(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	orchestrator := NewOrchestrator(registry, &fakeIngestionRunner{}, OrchestratorOptions{
		LiveUpdatesEnabled: true,
		QueueDepth:         1,
	})
	projectWatcher := &projectWatcher{
		project: project,
		events:  make(chan WatchEvent, 1),
		rescans: make(chan struct{}, 1),
		tasks:   make(chan ingestTask, 1),
	}
	orchestrator.mu.Lock()
	orchestrator.states[project.ID] = WatchState{ProjectID: project.ID, Status: WatchStatusLive, QueueDepth: 1}
	orchestrator.watchers[project.ID] = projectWatcher
	orchestrator.mu.Unlock()

	orchestrator.handleWatchEvent(projectWatcher, WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "one.go"), Op: WatchWrite})
	orchestrator.handleWatchEvent(projectWatcher, WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "two.go"), Op: WatchWrite})

	state := findWatchState(t, orchestrator.WatchStates(), project.ID)
	if state.EventQueueDepth != 1 || state.RescanQueueDepth != 1 {
		t.Fatalf("expected queued event and rescan diagnostics, got %#v", state)
	}
	if state.DroppedEventCount != 1 || state.CoalescedEventCount != 1 {
		t.Fatalf("expected dropped/coalesced counters, got %#v", state)
	}
	if state.OldestEventAgeMillis < 0 {
		t.Fatalf("oldest event age must not be negative: %#v", state)
	}
}

func TestOrchestrator_SlowPathEventLogsDiagnosticWithoutRawPath(t *testing.T) {
	registry := newLiveRegistry(t)
	project, _ := registry.Get("live_project")
	watcher := newFakeWatcher()
	ingestion := &blockingIngestionRunner{release: make(chan struct{})}
	var logs bytes.Buffer
	orchestrator := NewOrchestrator(registry, ingestion, OrchestratorOptions{
		LiveUpdatesEnabled: true,
		DebounceInterval:   10 * time.Millisecond,
		QueueDepth:         8,
		WorkerCount:        1,
		TaskWarnAfter:      10 * time.Millisecond,
		Logger:             slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	orchestrator.SetWatcherFactory(func() (FileWatcher, error) {
		return watcher, nil
	})

	if err := orchestrator.Start(context.Background()); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	watcher.events <- WatchEvent{Path: filepath.Join(project.CanonicalRootPath, "slow.go"), Op: WatchWrite}
	waitUntil(t, func() bool {
		return strings.Contains(logs.String(), "live ingestion task still running")
	})
	if strings.Contains(logs.String(), "slow.go") || strings.Contains(logs.String(), project.CanonicalRootPath) {
		t.Fatalf("slow diagnostic leaked raw path/root: %s", logs.String())
	}
	close(ingestion.release)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := orchestrator.Stop(stopCtx); err != nil {
		t.Fatalf("stop orchestrator: %v", err)
	}
}

type fakeWatcher struct {
	events chan WatchEvent
	errors chan error
	closed chan struct{}
	mu     sync.Mutex
	added  []string
	addErr error
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{
		events: make(chan WatchEvent, 32),
		errors: make(chan error, 32),
		closed: make(chan struct{}),
	}
}

func (watcher *fakeWatcher) Add(path string) error {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.addErr != nil {
		return watcher.addErr
	}
	watcher.added = append(watcher.added, filepath.Clean(path))
	return nil
}

func (watcher *fakeWatcher) Close() error {
	select {
	case <-watcher.closed:
	default:
		close(watcher.closed)
	}
	return nil
}

func (watcher *fakeWatcher) Events() <-chan WatchEvent {
	return watcher.events
}

func (watcher *fakeWatcher) Errors() <-chan error {
	return watcher.errors
}

func (watcher *fakeWatcher) addedPaths() []string {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return append([]string(nil), watcher.added...)
}

type fakeIngestionRunner struct {
	once      sync.Once
	pathsCh   chan string
	rescansCh chan string
}

func (runner *fakeIngestionRunner) IngestProject(_ context.Context, projectID string, trigger Trigger) (Run, error) {
	runner.ensureChannels()
	runner.rescansCh <- projectID + ":" + string(trigger)
	return Run{}, nil
}

func (runner *fakeIngestionRunner) IngestPath(_ context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	runner.ensureChannels()
	runner.pathsCh <- projectID + ":" + relativePath + ":" + string(trigger)
	return Run{}, nil
}

func (runner *fakeIngestionRunner) ensureChannels() {
	runner.once.Do(func() {
		runner.pathsCh = make(chan string, 32)
		runner.rescansCh = make(chan string, 32)
	})
}

func (runner *fakeIngestionRunner) waitPaths(t *testing.T, count int) []string {
	t.Helper()
	runner.ensureChannels()
	paths := make([]string, 0, count)
	for len(paths) < count {
		select {
		case value := <-runner.pathsCh:
			paths = append(paths, value[len("live_project:"):len(value)-len(":live")])
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %d path ingestions, got %#v", count, paths)
		}
	}
	return paths
}

func (runner *fakeIngestionRunner) waitRescans(t *testing.T, count int) {
	t.Helper()
	runner.ensureChannels()
	for i := 0; i < count; i++ {
		select {
		case value := <-runner.rescansCh:
			if value != "live_project:live" {
				t.Fatalf("unexpected rescan %q", value)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for rescan %d", i+1)
		}
	}
}

type errFakeOverflow struct{}

func (errFakeOverflow) Error() string {
	return "fsnotify: queue or buffer overflow"
}

func (errFakeOverflow) Is(target error) bool {
	return target != nil && target.Error() == "fsnotify: queue or buffer overflow"
}

type blockingIngestionRunner struct {
	release chan struct{}
}

func (runner *blockingIngestionRunner) IngestProject(context.Context, string, Trigger) (Run, error) {
	return Run{}, nil
}

func (runner *blockingIngestionRunner) IngestPath(ctx context.Context, _ string, _ string, _ Trigger) (Run, error) {
	select {
	case <-runner.release:
		return Run{Status: RunStatusCompleted}, nil
	case <-ctx.Done():
		return Run{}, ctx.Err()
	}
}

func newTestOrchestrator(registry *projectregistry.Registry, ingestion *fakeIngestionRunner, watcher *fakeWatcher) *Orchestrator {
	orchestrator := NewOrchestrator(registry, ingestion, OrchestratorOptions{
		LiveUpdatesEnabled: true,
		DebounceInterval:   10 * time.Millisecond,
		QueueDepth:         8,
		WorkerCount:        1,
	})
	orchestrator.SetWatcherFactory(func() (FileWatcher, error) {
		return watcher, nil
	})
	return orchestrator
}

func newLiveRegistry(t *testing.T) *projectregistry.Registry {
	t.Helper()
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "live_project",
		DisplayName:    "Live Project",
		RootPath:       t.TempDir(),
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "live_project",
		GraphStorage:   projectregistry.GraphStorageInMemory,
		DigestMode:     projectregistry.DigestModeContentGraph,
		UpdatePolicy:   projectregistry.UpdatePolicyLive,
		FollowSymlinks: false,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		LiveUpdatesEnabled:           true,
		ContentGraphApprovalAccepted: true,
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry
}

func newMixedRegistry(t *testing.T) *projectregistry.Registry {
	t.Helper()
	liveRoot := t.TempDir()
	manualRoot := t.TempDir()
	disabledRoot := t.TempDir()
	registry, err := projectregistry.NewRegistry([]config.Project{
		{
			ID:             "live_project",
			DisplayName:    "Live Project",
			RootPath:       liveRoot,
			Enabled:        true,
			Classification: projectregistry.ClassificationInternal,
			GraphNamespace: "live_project",
			GraphStorage:   projectregistry.GraphStorageInMemory,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyLive,
			FollowSymlinks: false,
		},
		{
			ID:             "manual_project",
			DisplayName:    "Manual Project",
			RootPath:       manualRoot,
			Enabled:        true,
			Classification: projectregistry.ClassificationInternal,
			GraphNamespace: "manual_project",
			GraphStorage:   projectregistry.GraphStorageInMemory,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
			FollowSymlinks: false,
		},
		{
			ID:             "disabled_project",
			DisplayName:    "Disabled Project",
			RootPath:       disabledRoot,
			Enabled:        false,
			Classification: projectregistry.ClassificationInternal,
			GraphNamespace: "disabled_project",
			GraphStorage:   projectregistry.GraphStorageInMemory,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyLive,
			FollowSymlinks: false,
		},
	}, projectregistry.Options{
		ContentGraphEnabled:          true,
		LiveUpdatesEnabled:           true,
		ContentGraphApprovalAccepted: true,
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, expected string) bool {
	expected = filepath.Clean(expected)
	for _, value := range values {
		if filepath.Clean(value) == expected {
			return true
		}
	}
	return false
}

func findWatchState(t *testing.T, states []WatchState, projectID string) WatchState {
	t.Helper()
	for _, state := range states {
		if state.ProjectID == projectID {
			return state
		}
	}
	t.Fatalf("watch state for %q not found in %#v", projectID, states)
	return WatchState{}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
