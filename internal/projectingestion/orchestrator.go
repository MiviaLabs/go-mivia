package projectingestion

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

type OrchestratorOptions struct {
	LiveUpdatesEnabled bool
	DebounceInterval   time.Duration
	QueueDepth         int
	WorkerCount        int
	InitialScanOnStart bool
	Logger             *slog.Logger
}

type ingestionRunner interface {
	IngestProject(context.Context, string, Trigger) (Run, error)
	IngestPath(context.Context, string, string, Trigger) (Run, error)
}

type Orchestrator struct {
	registry       *projectregistry.Registry
	ingestion      ingestionRunner
	options        OrchestratorOptions
	logger         *slog.Logger
	watcherFactory WatcherFactory

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
}

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

func NewOrchestrator(registry *projectregistry.Registry, ingestion ingestionRunner, options OrchestratorOptions) *Orchestrator {
	if options.DebounceInterval <= 0 {
		options.DebounceInterval = 2 * time.Second
	}
	if options.QueueDepth <= 0 {
		options.QueueDepth = 128
	}
	if options.WorkerCount <= 0 {
		options.WorkerCount = 1
	}
	return &Orchestrator{
		registry:       registry,
		ingestion:      ingestion,
		options:        options,
		logger:         options.Logger,
		watcherFactory: NewFSNotifyWatcher,
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
		}
		return nil
	}
	if orchestrator.registry == nil || orchestrator.ingestion == nil {
		return fmt.Errorf("%w: orchestrator dependencies are required", ErrUnsupportedIngest)
	}
	runCtx, cancel := context.WithCancel(ctx)
	startedWatchers := make([]*projectWatcher, 0)
	for _, project := range orchestrator.registry.List() {
		if !project.Enabled || project.DigestMode != projectregistry.DigestModeContentGraph || project.UpdatePolicy != projectregistry.UpdatePolicyLive {
			continue
		}
		watcher, err := orchestrator.watcherFactory()
		if err != nil {
			cancel()
			closeProjectWatchers(startedWatchers)
			return err
		}
		projectWatcher := &projectWatcher{
			project: project,
			watcher: watcher,
			events:  make(chan WatchEvent, orchestrator.options.QueueDepth),
			rescans: make(chan struct{}, 1),
			tasks:   make(chan ingestTask, orchestrator.options.QueueDepth),
		}
		watchedDirectoryCount, err := orchestrator.addProjectWatches(projectWatcher)
		if err != nil {
			cancel()
			projectWatcher.close()
			closeProjectWatchers(startedWatchers)
			return err
		}
		orchestrator.logInfo("live ingestion watcher started",
			slog.String("project_id", project.ID),
			slog.Int("watched_directory_count", watchedDirectoryCount),
			slog.Int("queue_depth", orchestrator.options.QueueDepth),
			slog.Int("worker_count", orchestrator.options.WorkerCount),
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
		if added, err := orchestrator.addWatchesUnder(projectWatcher, event.Path); err == nil && added > 0 {
			orchestrator.logInfo("live ingestion watched new directories",
				slog.String("project_id", projectWatcher.project.ID),
				slog.Int("watched_directory_count", added),
			)
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
			for relative := range pending {
				orchestrator.enqueueTask(ctx, projectWatcher, ingestTask{relativePath: relative})
				delete(pending, relative)
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
				orchestrator.logInfo("live ingestion rescan started", slog.String("project_id", projectWatcher.project.ID))
				run, err := orchestrator.ingestion.IngestProject(ctx, projectWatcher.project.ID, TriggerLive)
				if err != nil {
					orchestrator.logWarn("live ingestion rescan failed",
						slog.String("project_id", projectWatcher.project.ID),
						slog.String("error_category", "ingest_failed"),
						slog.String("error", err.Error()),
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
				)
				continue
			}
			pathHash := shortHash(task.relativePath)
			orchestrator.logInfo("live ingestion path event started",
				slog.String("project_id", projectWatcher.project.ID),
				slog.String("relative_path_hash", pathHash),
			)
			run, err := orchestrator.ingestion.IngestPath(ctx, projectWatcher.project.ID, task.relativePath, TriggerLive)
			if err != nil {
				orchestrator.logWarn("live ingestion path event failed",
					slog.String("project_id", projectWatcher.project.ID),
					slog.String("relative_path_hash", pathHash),
					slog.String("error_category", "ingest_failed"),
					slog.String("error", err.Error()),
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
			)
		}
	}
}

func (orchestrator *Orchestrator) enqueueTask(ctx context.Context, projectWatcher *projectWatcher, task ingestTask) {
	select {
	case projectWatcher.tasks <- task:
	case <-ctx.Done():
	default:
		orchestrator.enqueueRescan(projectWatcher)
	}
}

func (orchestrator *Orchestrator) enqueueRescan(projectWatcher *projectWatcher) {
	select {
	case projectWatcher.rescans <- struct{}{}:
	default:
	}
}

func (orchestrator *Orchestrator) addProjectWatches(projectWatcher *projectWatcher) (int, error) {
	root := projectWatcher.project.CanonicalRootPath
	if root == "" {
		root = projectWatcher.project.RootPath
	}
	return orchestrator.addWatchesUnder(projectWatcher, root)
}

func (orchestrator *Orchestrator) addWatchesUnder(projectWatcher *projectWatcher, root string) (int, error) {
	project := projectWatcher.project
	walkRoot := filepath.Clean(root)
	watched := 0
	err := filepath.WalkDir(walkRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("watch walk failed")
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
			return ErrPathEscapesRoot
		}
		if relative != "" && projectregistry.ProjectExcludesRelativePath(project, relative) {
			return filepath.SkipDir
		}
		if !projectregistry.ProjectMayIncludeRelativePath(project, relative) {
			return filepath.SkipDir
		}
		if err := projectWatcher.watcher.Add(current); err != nil {
			return err
		}
		watched++
		return nil
	})
	return watched, err
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
