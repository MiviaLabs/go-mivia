package projectingestion

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

type OrchestratorOptions struct {
	LiveUpdatesEnabled       bool
	DebounceInterval         time.Duration
	QueueDepth               int
	WorkerCount              int
	GlobalWorkerCount        int
	PerProjectWorkerLimit    int
	LivePathPriority         bool
	InitialScanOnStart       bool
	MaxWatchedDirectoryCount int
	TaskWarnAfter            time.Duration
	Logger                   *slog.Logger
}

type ingestionRunner interface {
	IngestProject(context.Context, string, Trigger) (Run, error)
	IngestPath(context.Context, string, string, Trigger) (Run, error)
}

type Orchestrator struct {
	registry       *projectregistry.Registry
	ingestion      ingestionRunner
	scheduler      *Scheduler
	options        OrchestratorOptions
	logger         *slog.Logger
	watcherFactory WatcherFactory

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	states  map[string]WatchState
}

type DiagnosticsSource struct {
	Scheduler    *Scheduler
	Orchestrator *Orchestrator
	Service      *Service
}

func (source DiagnosticsSource) IngestionDiagnostics() DiagnosticsSnapshot {
	var snapshot DiagnosticsSnapshot
	if source.Scheduler != nil {
		snapshot.Scheduler = source.Scheduler.Diagnostics()
	}
	if source.Orchestrator != nil {
		snapshot.Watchers = source.Orchestrator.WatchStates()
	}
	if source.Service != nil {
		snapshot.Stages = source.Service.Diagnostics()
	}
	if snapshot.Stages == nil {
		snapshot.Stages = map[string]StageDiagnostic{}
	}
	return snapshot
}

type WatchState struct {
	ProjectID             string
	Status                string
	WatchedDirectoryCount int
	SkippedDirectoryCount int
	FailedDirectoryCount  int
	QueueDepth            int
	LastErrorCategory     string
	UpdatedAt             time.Time
}

const (
	WatchStatusLive     = "live"
	WatchStatusDegraded = "live_degraded"
	WatchStatusDisabled = "disabled"
)

type projectWatcher struct {
	project  projectregistry.Project
	watcher  FileWatcher
	events   chan WatchEvent
	rescans  chan struct{}
	tasks    chan ingestTask
	stopOnce sync.Once
}

type ingestTask struct {
	rescan       bool
	relativePath string
}

type watchRegistrationStats struct {
	watched       int
	skipped       int
	failed        int
	degraded      bool
	errorCategory string
}

func NewOrchestrator(registry *projectregistry.Registry, ingestion ingestionRunner, options OrchestratorOptions) *Orchestrator {
	if options.DebounceInterval <= 0 {
		options.DebounceInterval = 2 * time.Second
	}
	if options.QueueDepth <= 0 {
		options.QueueDepth = defaultSchedulerQueueDepth
	}
	defaultWorkerCount := runtime.NumCPU()
	if defaultWorkerCount <= 0 {
		defaultWorkerCount = 1
	}
	if options.WorkerCount <= 0 {
		options.WorkerCount = defaultWorkerCount
	}
	if options.GlobalWorkerCount <= 0 {
		options.GlobalWorkerCount = options.WorkerCount
	}
	if options.PerProjectWorkerLimit <= 0 {
		options.PerProjectWorkerLimit = options.GlobalWorkerCount
	}
	if options.TaskWarnAfter <= 0 {
		options.TaskWarnAfter = 30 * time.Second
	}
	scheduler, ok := ingestion.(*Scheduler)
	if !ok {
		scheduler = NewScheduler(ingestion, SchedulerOptions{
			QueueDepth:            options.QueueDepth,
			GlobalWorkerCount:     options.GlobalWorkerCount,
			PerProjectWorkerLimit: options.PerProjectWorkerLimit,
			LivePathPriority:      options.LivePathPriority,
		})
	}
	return &Orchestrator{
		registry:       registry,
		ingestion:      ingestion,
		scheduler:      scheduler,
		options:        options,
		logger:         options.Logger,
		watcherFactory: NewFSNotifyWatcher,
		states:         make(map[string]WatchState),
	}
}

func (orchestrator *Orchestrator) SetWatcherFactory(factory WatcherFactory) {
	orchestrator.watcherFactory = factory
}

func (orchestrator *Orchestrator) Start(ctx context.Context) error {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	if orchestrator.started || !orchestrator.options.LiveUpdatesEnabled {
		if !orchestrator.options.LiveUpdatesEnabled {
			orchestrator.logInfo("live ingestion disabled")
			if orchestrator.registry != nil {
				for _, project := range orchestrator.registry.List() {
					orchestrator.setWatchStateLocked(WatchState{
						ProjectID: project.ID,
						Status:    WatchStatusDisabled,
						UpdatedAt: time.Now().UTC(),
					})
				}
			}
		}
		return nil
	}
	if orchestrator.registry == nil || orchestrator.ingestion == nil {
		return fmt.Errorf("%w: orchestrator dependencies are required", ErrUnsupportedIngest)
	}
	runCtx, cancel := context.WithCancel(ctx)
	if err := orchestrator.scheduler.Start(runCtx); err != nil {
		cancel()
		return err
	}
	startedWatchers := make([]*projectWatcher, 0)
	for _, project := range orchestrator.registry.List() {
		if !project.Enabled || project.DigestMode != projectregistry.DigestModeContentGraph || project.UpdatePolicy != projectregistry.UpdatePolicyLive {
			continue
		}
		watcher, err := orchestrator.watcherFactory()
		if err != nil {
			orchestrator.setWatchStateLocked(WatchState{
				ProjectID:         project.ID,
				Status:            WatchStatusDegraded,
				QueueDepth:        orchestrator.options.QueueDepth,
				LastErrorCategory: "watcher_create_failed",
				UpdatedAt:         time.Now().UTC(),
			})
			orchestrator.logWarn("live ingestion watcher degraded",
				slog.String("project_id", project.ID),
				slog.String("error_category", "watcher_create_failed"),
			)
			continue
		}
		projectWatcher := &projectWatcher{
			project: project,
			watcher: watcher,
			events:  make(chan WatchEvent, orchestrator.options.QueueDepth),
			rescans: make(chan struct{}, 1),
			tasks:   make(chan ingestTask, orchestrator.options.QueueDepth),
		}
		watchStats := orchestrator.addProjectWatches(projectWatcher)
		status := WatchStatusLive
		if watchStats.degraded {
			status = WatchStatusDegraded
		}
		orchestrator.setWatchStateLocked(WatchState{
			ProjectID:             project.ID,
			Status:                status,
			WatchedDirectoryCount: watchStats.watched,
			SkippedDirectoryCount: watchStats.skipped,
			FailedDirectoryCount:  watchStats.failed,
			QueueDepth:            orchestrator.options.QueueDepth,
			LastErrorCategory:     watchStats.errorCategory,
			UpdatedAt:             time.Now().UTC(),
		})
		if watchStats.watched == 0 {
			projectWatcher.close()
			orchestrator.logWarn("live ingestion watcher degraded",
				slog.String("project_id", project.ID),
				slog.String("error_category", watchStats.errorCategory),
				slog.Int("watched_directory_count", watchStats.watched),
				slog.Int("skipped_directory_count", watchStats.skipped),
				slog.Int("failed_directory_count", watchStats.failed),
			)
			continue
		}
		orchestrator.logInfo("live ingestion watcher started",
			slog.String("project_id", project.ID),
			slog.String("watch_status", status),
			slog.Int("watched_directory_count", watchStats.watched),
			slog.Int("skipped_directory_count", watchStats.skipped),
			slog.Int("failed_directory_count", watchStats.failed),
			slog.Int("queue_depth", orchestrator.options.QueueDepth),
			slog.Int("worker_count", orchestrator.options.WorkerCount),
			slog.Int("global_worker_count", orchestrator.options.GlobalWorkerCount),
			slog.Int("per_project_worker_limit", orchestrator.options.PerProjectWorkerLimit),
			slog.Duration("debounce_interval", orchestrator.options.DebounceInterval),
			slog.Bool("initial_scan_on_start", orchestrator.options.InitialScanOnStart),
		)
		startedWatchers = append(startedWatchers, projectWatcher)
		orchestrator.startProjectWatcher(runCtx, projectWatcher)
	}
	orchestrator.cancel = cancel
	orchestrator.started = true
	orchestrator.logInfo("live ingestion orchestrator started", slog.Int("project_count", len(startedWatchers)))
	return nil
}

func (orchestrator *Orchestrator) WatchStates() []WatchState {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	states := make([]WatchState, 0, len(orchestrator.states))
	for _, state := range orchestrator.states {
		states = append(states, state)
	}
	return states
}

func (orchestrator *Orchestrator) Stop(ctx context.Context) error {
	orchestrator.mu.Lock()
	if !orchestrator.started {
		orchestrator.mu.Unlock()
		return nil
	}
	cancel := orchestrator.cancel
	orchestrator.cancel = nil
	orchestrator.started = false
	orchestrator.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	_ = orchestrator.scheduler.Stop(ctx)
	orchestrator.logInfo("live ingestion orchestrator stopping")
	done := make(chan struct{})
	go func() {
		orchestrator.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		orchestrator.logInfo("live ingestion orchestrator stopped")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (orchestrator *Orchestrator) startProjectWatcher(ctx context.Context, projectWatcher *projectWatcher) {
	orchestrator.wg.Add(2 + orchestrator.options.WorkerCount)
	go func() {
		defer orchestrator.wg.Done()
		orchestrator.watchLoop(ctx, projectWatcher)
	}()
	go func() {
		defer orchestrator.wg.Done()
		orchestrator.debounceLoop(ctx, projectWatcher)
	}()
	for i := 0; i < orchestrator.options.WorkerCount; i++ {
		go func() {
			defer orchestrator.wg.Done()
			orchestrator.workerLoop(ctx, projectWatcher)
		}()
	}
	if orchestrator.options.InitialScanOnStart {
		orchestrator.logInfo("live ingestion initial scan queued", slog.String("project_id", projectWatcher.project.ID))
		orchestrator.enqueueRescan(projectWatcher)
	}
}

func (orchestrator *Orchestrator) watchLoop(ctx context.Context, projectWatcher *projectWatcher) {
	defer projectWatcher.close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-projectWatcher.watcher.Events():
			if !ok {
				return
			}
			orchestrator.handleWatchEvent(projectWatcher, event)
		case err, ok := <-projectWatcher.watcher.Errors():
			if !ok {
				return
			}
			if isWatcherOverflow(err) {
				orchestrator.logWarn("live ingestion watcher overflow; rescan queued", slog.String("project_id", projectWatcher.project.ID))
				orchestrator.enqueueRescan(projectWatcher)
			}
		}
	}
}

func (orchestrator *Orchestrator) handleWatchEvent(projectWatcher *projectWatcher, event WatchEvent) {
	if event.Op&WatchCreate != 0 && isDirectoryPath(event.Path) {
		stats := orchestrator.addWatchesUnder(projectWatcher, event.Path)
		if stats.watched > 0 {
			orchestrator.logInfo("live ingestion watched new directories",
				slog.String("project_id", projectWatcher.project.ID),
				slog.String("watch_status", watchStatusForStats(stats)),
				slog.Int("watched_directory_count", stats.watched),
				slog.Int("skipped_directory_count", stats.skipped),
				slog.Int("failed_directory_count", stats.failed),
			)
		}
		if stats.degraded {
			orchestrator.updateWatchState(projectWatcher.project.ID, stats)
		}
	}
	select {
	case projectWatcher.events <- event:
	default:
		orchestrator.logWarn("live ingestion event queue full; rescan queued", slog.String("project_id", projectWatcher.project.ID))
		orchestrator.enqueueRescan(projectWatcher)
	}
}

func (orchestrator *Orchestrator) debounceLoop(ctx context.Context, projectWatcher *projectWatcher) {
	pending := make(map[string]struct{})
	timer := time.NewTimer(orchestrator.options.DebounceInterval)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-projectWatcher.rescans:
			orchestrator.enqueueTask(ctx, projectWatcher, ingestTask{rescan: true})
		case event := <-projectWatcher.events:
			if relative, ok := orchestrator.relativeEventPath(projectWatcher.project, event.Path); ok {
				pending[relative] = struct{}{}
				resetTimer(timer, orchestrator.options.DebounceInterval)
			}
		case <-timer.C:
			overflowed := false
			for relative := range pending {
				if !orchestrator.enqueueTask(ctx, projectWatcher, ingestTask{relativePath: relative}) {
					overflowed = true
					break
				}
			}
			for relative := range pending {
				delete(pending, relative)
			}
			if overflowed {
				orchestrator.enqueueTask(ctx, projectWatcher, ingestTask{rescan: true})
			}
		}
	}
}

func (orchestrator *Orchestrator) workerLoop(ctx context.Context, projectWatcher *projectWatcher) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-projectWatcher.tasks:
			if task.rescan {
				startedAt := time.Now()
				orchestrator.logInfo("live ingestion rescan started", slog.String("project_id", projectWatcher.project.ID))
				done := orchestrator.monitorLiveTask(ctx, projectWatcher.project.ID, "", "full_scan", startedAt)
				run, err := orchestrator.scheduler.SubmitFullScan(ctx, projectWatcher.project.ID, TriggerLive)
				close(done)
				if err != nil {
					orchestrator.logWarn("live ingestion rescan failed",
						slog.String("project_id", projectWatcher.project.ID),
						slog.String("error_category", "ingest_failed"),
						slog.String("error", err.Error()),
						slog.Duration("elapsed", time.Since(startedAt)),
					)
					continue
				}
				orchestrator.logInfo("live ingestion rescan completed",
					slog.String("project_id", projectWatcher.project.ID),
					slog.String("run_id", run.ID),
					slog.String("status", string(run.Status)),
					slog.Int("files_seen", run.FilesSeen),
					slog.Int("files_ingested", run.FilesIngested),
					slog.Int("files_skipped", run.FilesSkipped),
					slog.Int("chunks_stored", run.ChunksStored),
					slog.Int("symbols_stored", run.SymbolsStored),
					slog.Duration("elapsed", time.Since(startedAt)),
				)
				continue
			}
			pathHash := shortHash(task.relativePath)
			startedAt := time.Now()
			orchestrator.logInfo("live ingestion path event started",
				slog.String("project_id", projectWatcher.project.ID),
				slog.String("relative_path_hash", pathHash),
			)
			done := orchestrator.monitorLiveTask(ctx, projectWatcher.project.ID, pathHash, "path", startedAt)
			run, err := orchestrator.scheduler.SubmitPath(ctx, projectWatcher.project.ID, task.relativePath, TriggerLive)
			close(done)
			if err != nil {
				orchestrator.logWarn("live ingestion path event failed",
					slog.String("project_id", projectWatcher.project.ID),
					slog.String("relative_path_hash", pathHash),
					slog.String("error_category", "ingest_failed"),
					slog.String("error", err.Error()),
					slog.Duration("elapsed", time.Since(startedAt)),
				)
				continue
			}
			orchestrator.logInfo("live ingestion path event completed",
				slog.String("project_id", projectWatcher.project.ID),
				slog.String("relative_path_hash", pathHash),
				slog.String("run_id", run.ID),
				slog.String("status", string(run.Status)),
				slog.Int("files_ingested", run.FilesIngested),
				slog.Int("files_skipped", run.FilesSkipped),
				slog.Duration("elapsed", time.Since(startedAt)),
			)
		}
	}
}

func (orchestrator *Orchestrator) enqueueTask(ctx context.Context, projectWatcher *projectWatcher, task ingestTask) bool {
	select {
	case projectWatcher.tasks <- task:
		return true
	case <-ctx.Done():
		return false
	default:
		if !task.rescan {
			orchestrator.logWarn("live ingestion task queue full; path events coalesced to rescan", slog.String("project_id", projectWatcher.project.ID))
			return false
		}
		orchestrator.logWarn("live ingestion task queue full; rescan deferred", slog.String("project_id", projectWatcher.project.ID))
		select {
		case projectWatcher.tasks <- task:
			return true
		case <-ctx.Done():
			return false
		}
	}
}

func (orchestrator *Orchestrator) monitorLiveTask(ctx context.Context, projectID string, relativePathHash string, taskType string, startedAt time.Time) chan struct{} {
	done := make(chan struct{})
	go func() {
		timer := time.NewTimer(orchestrator.options.TaskWarnAfter)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-done:
		case <-timer.C:
			attrs := []slog.Attr{
				slog.String("project_id", projectID),
				slog.String("task_type", taskType),
				slog.String("error_category", "live_task_slow"),
				slog.Duration("elapsed", time.Since(startedAt)),
			}
			if relativePathHash != "" {
				attrs = append(attrs, slog.String("relative_path_hash", relativePathHash))
			}
			orchestrator.logWarn("live ingestion task still running", attrs...)
		}
	}()
	return done
}

func (orchestrator *Orchestrator) enqueueRescan(projectWatcher *projectWatcher) {
	select {
	case projectWatcher.rescans <- struct{}{}:
	default:
	}
}

func (orchestrator *Orchestrator) addProjectWatches(projectWatcher *projectWatcher) watchRegistrationStats {
	root := projectWatcher.project.CanonicalRootPath
	if root == "" {
		root = projectWatcher.project.RootPath
	}
	return orchestrator.addWatchesUnder(projectWatcher, root)
}

func (orchestrator *Orchestrator) addWatchesUnder(projectWatcher *projectWatcher, root string) watchRegistrationStats {
	project := projectWatcher.project
	walkRoot := filepath.Clean(root)
	stats := watchRegistrationStats{}
	err := filepath.WalkDir(walkRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			stats.failed++
			stats.degraded = true
			stats.errorCategory = "watch_walk_failed"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		relative, ok := safeRelativePath(project.CanonicalRootPath, current)
		if !ok {
			stats.failed++
			stats.degraded = true
			stats.errorCategory = "watch_path_escape"
			return filepath.SkipDir
		}
		if relative != "" && projectregistry.ProjectExcludesRelativePath(project, relative) {
			return filepath.SkipDir
		}
		if !projectregistry.ProjectMayIncludeRelativePath(project, relative) {
			return filepath.SkipDir
		}
		if orchestrator.options.MaxWatchedDirectoryCount > 0 && stats.watched >= orchestrator.options.MaxWatchedDirectoryCount {
			stats.skipped++
			stats.degraded = true
			stats.errorCategory = "watch_directory_budget_exceeded"
			return filepath.SkipDir
		}
		if err := projectWatcher.watcher.Add(current); err != nil {
			stats.failed++
			stats.degraded = true
			stats.errorCategory = "watch_add_failed"
			return filepath.SkipDir
		}
		stats.watched++
		return nil
	})
	if err != nil {
		stats.failed++
		stats.degraded = true
		if stats.errorCategory == "" {
			stats.errorCategory = "watch_walk_failed"
		}
	}
	if stats.degraded && stats.errorCategory == "" {
		stats.errorCategory = "watch_degraded"
	}
	return stats
}

func (orchestrator *Orchestrator) relativeEventPath(project projectregistry.Project, eventPath string) (string, bool) {
	relative, ok := safeRelativePath(project.CanonicalRootPath, eventPath)
	if !ok || relative == "" {
		return "", false
	}
	if projectregistry.ProjectExcludesRelativePath(project, relative) {
		return "", false
	}
	if !projectregistry.ProjectIncludesRelativePath(project, relative) {
		return "", false
	}
	return relative, true
}

func (orchestrator *Orchestrator) updateWatchState(projectID string, stats watchRegistrationStats) {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	state := orchestrator.states[projectID]
	state.ProjectID = projectID
	state.Status = watchStatusForStats(stats)
	state.WatchedDirectoryCount += stats.watched
	state.SkippedDirectoryCount += stats.skipped
	state.FailedDirectoryCount += stats.failed
	state.QueueDepth = orchestrator.options.QueueDepth
	state.LastErrorCategory = stats.errorCategory
	state.UpdatedAt = time.Now().UTC()
	orchestrator.setWatchStateLocked(state)
}

func (orchestrator *Orchestrator) setWatchStateLocked(state WatchState) {
	if orchestrator.states == nil {
		orchestrator.states = make(map[string]WatchState)
	}
	orchestrator.states[state.ProjectID] = state
}

func watchStatusForStats(stats watchRegistrationStats) string {
	if stats.degraded {
		return WatchStatusDegraded
	}
	return WatchStatusLive
}

func closeProjectWatchers(watchers []*projectWatcher) {
	for _, watcher := range watchers {
		watcher.close()
	}
}

func (watcher *projectWatcher) close() {
	watcher.stopOnce.Do(func() {
		_ = watcher.watcher.Close()
	})
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func (orchestrator *Orchestrator) logInfo(message string, attrs ...slog.Attr) {
	if orchestrator.logger == nil {
		return
	}
	orchestrator.logger.Info(message, attrsToAny(attrs)...)
}

func (orchestrator *Orchestrator) logWarn(message string, attrs ...slog.Attr) {
	if orchestrator.logger == nil {
		return
	}
	orchestrator.logger.Warn(message, attrsToAny(attrs)...)
}

func attrsToAny(attrs []slog.Attr) []any {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	return args
}
