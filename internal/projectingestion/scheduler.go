package projectingestion

import (
	"context"
	"fmt"
	"sync"
)

type SchedulerOptions struct {
	QueueDepth            int
	GlobalWorkerCount     int
	PerProjectWorkerLimit int
	LivePathPriority      bool
}

const (
	defaultSchedulerQueueDepth      = 10000
	defaultSchedulerGlobalWorkers   = 4
	defaultSchedulerPerProjectLimit = 2
)

type SchedulerDiagnostics struct {
	QueueDepth                  int
	LiveQueueDepth              int
	FullScanQueueDepth          int
	ActiveTaskCount             int
	ActiveProjectTaskCount      map[string]int
	ProjectWideTaskCount        map[string]int
	PendingProjectWideTaskCount map[string]int
}

type Scheduler struct {
	runner  ingestionRunner
	options SchedulerOptions

	ctx    context.Context
	cancel context.CancelFunc
	liveCh chan schedulerTask
	fullCh chan schedulerTask
	wg     sync.WaitGroup

	mu                 sync.Mutex
	projectState       map[string]*schedulerProjectState
	active             int
	activeProject      map[string]int
	projectWide        map[string]int
	pendingProjectWide map[string]int
	started            bool
}

type schedulerTask struct {
	projectID          string
	relativePath       string
	trigger            Trigger
	taskType           string
	preparedRun        Run
	pendingProjectWide bool
	done               chan schedulerResult
}

type schedulerProjectState struct {
	active             int
	projectWideActive  bool
	projectWidePending int
	changed            chan struct{}
}

type schedulerResult struct {
	run Run
	err error
}

type asyncProjectRunner interface {
	PrepareProjectRun(context.Context, string, Trigger) (Run, error)
	ExecutePreparedProjectRun(context.Context, Run) (Run, error)
	FailPreparedProjectRun(context.Context, Run, string) (Run, error)
}

type asyncSearchIndexRebuildRunner interface {
	asyncProjectRunner
	ExecutePreparedSearchIndexRebuild(context.Context, Run) (Run, error)
}

func NewScheduler(runner ingestionRunner, options SchedulerOptions) *Scheduler {
	if options.QueueDepth <= 0 {
		options.QueueDepth = defaultSchedulerQueueDepth
	}
	if options.GlobalWorkerCount <= 0 {
		options.GlobalWorkerCount = defaultSchedulerGlobalWorkers
	}
	if options.PerProjectWorkerLimit <= 0 {
		options.PerProjectWorkerLimit = defaultSchedulerPerProjectLimit
	}
	if options.PerProjectWorkerLimit > options.GlobalWorkerCount {
		options.PerProjectWorkerLimit = options.GlobalWorkerCount
	}
	return &Scheduler{
		runner:             runner,
		options:            options,
		liveCh:             make(chan schedulerTask, options.QueueDepth),
		fullCh:             make(chan schedulerTask, options.QueueDepth),
		projectState:       make(map[string]*schedulerProjectState),
		activeProject:      make(map[string]int),
		projectWide:        make(map[string]int),
		pendingProjectWide: make(map[string]int),
	}
}

func (scheduler *Scheduler) Start(ctx context.Context) error {
	if scheduler == nil || scheduler.runner == nil {
		return fmt.Errorf("%w: scheduler dependencies are required", ErrUnsupportedIngest)
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.started {
		return nil
	}
	scheduler.ctx, scheduler.cancel = context.WithCancel(ctx)
	scheduler.started = true
	for i := 0; i < scheduler.options.GlobalWorkerCount; i++ {
		scheduler.wg.Add(1)
		go func() {
			defer scheduler.wg.Done()
			scheduler.workerLoop(scheduler.ctx)
		}()
	}
	return nil
}

func (scheduler *Scheduler) Stop(ctx context.Context) error {
	if scheduler == nil {
		return nil
	}
	scheduler.mu.Lock()
	cancel := scheduler.cancel
	scheduler.cancel = nil
	scheduler.started = false
	scheduler.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		scheduler.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (scheduler *Scheduler) IngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return scheduler.SubmitFullScan(ctx, projectID, trigger)
}

func (scheduler *Scheduler) SubmitIngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return scheduler.SubmitFullScanAsync(ctx, projectID, trigger)
}

func (scheduler *Scheduler) SubmitRebuildSearchIndex(ctx context.Context, projectID string) (Run, error) {
	if scheduler == nil {
		return Run{}, fmt.Errorf("%w: scheduler is required", ErrUnsupportedIngest)
	}
	scheduler.mu.Lock()
	started := scheduler.started
	scheduler.mu.Unlock()
	if !started {
		return Run{}, fmt.Errorf("%w: scheduler is not started", ErrUnsupportedIngest)
	}
	runner, ok := scheduler.runner.(asyncSearchIndexRebuildRunner)
	if !ok {
		return Run{}, fmt.Errorf("%w: async search index repair runner is required", ErrUnsupportedIngest)
	}
	run, err := runner.PrepareProjectRun(ctx, projectID, TriggerManual)
	if err != nil {
		return run, err
	}
	task := schedulerTask{
		projectID:   run.ProjectID,
		trigger:     TriggerManual,
		taskType:    "search_index_rebuild",
		preparedRun: run,
	}
	if err := scheduler.enqueueTask(ctx, scheduler.fullCh, &task, false); err == nil {
		return run, nil
	} else if ctx.Err() != nil {
		_, _ = runner.FailPreparedProjectRun(context.Background(), run, "enqueue_canceled")
		return run, ctx.Err()
	} else {
		failed, failErr := runner.FailPreparedProjectRun(ctx, run, "queue_full")
		if failErr != nil {
			return failed, failErr
		}
		return failed, fmt.Errorf("%w: ingestion queue is full", ErrUnsupportedIngest)
	}
}

func (scheduler *Scheduler) RebuildSearchIndex(ctx context.Context, projectID string) (Run, error) {
	return scheduler.submit(ctx, scheduler.fullCh, schedulerTask{
		projectID: projectID,
		trigger:   TriggerManual,
		taskType:  "search_index_rebuild",
		done:      make(chan schedulerResult, 1),
	})
}

func (scheduler *Scheduler) IngestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	return scheduler.SubmitPath(ctx, projectID, relativePath, trigger)
}

func (scheduler *Scheduler) RunMetadata(ctx context.Context, projectID string, runID string) (RunMetadata, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return RunMetadata{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.RunMetadata(ctx, projectID, runID)
}

func (scheduler *Scheduler) LatestRunMetadata(ctx context.Context, projectID string) (RunMetadata, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return RunMetadata{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.LatestRunMetadata(ctx, projectID)
}

func (scheduler *Scheduler) ListFiles(ctx context.Context, projectID string, filter FileStateFilter, pagination Pagination) (FileList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return FileList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListFiles(ctx, projectID, filter, pagination)
}

func (scheduler *Scheduler) GetFile(ctx context.Context, projectID string, fileID string) (FileMetadata, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return FileMetadata{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.GetFile(ctx, projectID, fileID)
}

func (scheduler *Scheduler) ListChunks(ctx context.Context, projectID string, fileID string, pagination Pagination, maxChunkBytes int) (ChunkList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return ChunkList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListChunks(ctx, projectID, fileID, pagination, maxChunkBytes)
}

func (scheduler *Scheduler) GetChunk(ctx context.Context, projectID string, fileID string, chunkID string, maxChunkBytes int) (ChunkMetadata, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return ChunkMetadata{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.GetChunk(ctx, projectID, fileID, chunkID, maxChunkBytes)
}

func (scheduler *Scheduler) ListSymbols(ctx context.Context, projectID string, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListSymbols(ctx, projectID, filter, pagination)
}

func (scheduler *Scheduler) SearchText(ctx context.Context, projectID string, options TextSearchOptions) (TextSearchResultList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return TextSearchResultList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchText(ctx, projectID, options)
}

func (scheduler *Scheduler) SearchFiles(ctx context.Context, projectID string, options FileSearchOptions) (FileList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return FileList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchFiles(ctx, projectID, options)
}

func (scheduler *Scheduler) SearchIndexHealth(ctx context.Context, projectID string) (SearchIndexHealth, error) {
	api, ok := scheduler.runner.(interface {
		SearchIndexHealth(context.Context, string) (SearchIndexHealth, error)
	})
	if !ok {
		return SearchIndexHealth{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchIndexHealth(ctx, projectID)
}

func (scheduler *Scheduler) ContextSearchIndexHealth(ctx context.Context, projectID string) (SearchIndexHealth, error) {
	api, ok := scheduler.runner.(interface {
		ContextSearchIndexHealth(context.Context, string) (SearchIndexHealth, error)
	})
	if !ok {
		return SearchIndexHealth{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ContextSearchIndexHealth(ctx, projectID)
}

func (scheduler *Scheduler) SearchSymbols(ctx context.Context, projectID string, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchSymbols(ctx, projectID, filter, pagination)
}

func (scheduler *Scheduler) SearchReferences(ctx context.Context, projectID string, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolReferenceList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchReferences(ctx, projectID, options)
}

func (scheduler *Scheduler) SearchCalls(ctx context.Context, projectID string, options ReferenceSearchOptions) (SymbolCallEdgeList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolCallEdgeList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchCalls(ctx, projectID, options)
}

func (scheduler *Scheduler) ListASTQueries(ctx context.Context, projectID string) (ASTQueryCatalog, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return ASTQueryCatalog{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListASTQueries(ctx, projectID)
}

func (scheduler *Scheduler) SearchAST(ctx context.Context, projectID string, options ASTSearchOptions) (ASTSearchResultList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return ASTSearchResultList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.SearchAST(ctx, projectID, options)
}

func (scheduler *Scheduler) GetSymbol(ctx context.Context, projectID string, symbolID string) (SymbolMetadata, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolMetadata{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.GetSymbol(ctx, projectID, symbolID)
}

func (scheduler *Scheduler) GetSymbolSource(ctx context.Context, projectID string, symbolID string, options SymbolSourceOptions) (SymbolSource, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolSource{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.GetSymbolSource(ctx, projectID, symbolID, options)
}

func (scheduler *Scheduler) ListSymbolReferences(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolReferenceList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolReferenceList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListSymbolReferences(ctx, projectID, symbolID, pagination)
}

func (scheduler *Scheduler) ListSymbolCallers(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolCallEdgeList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolCallEdgeList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListSymbolCallers(ctx, projectID, symbolID, pagination)
}

func (scheduler *Scheduler) ListSymbolCallees(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolCallEdgeList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolCallEdgeList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListSymbolCallees(ctx, projectID, symbolID, pagination)
}

func (scheduler *Scheduler) ListSymbolImplementers(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolImplementationList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolImplementationList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListSymbolImplementers(ctx, projectID, symbolID, pagination)
}

func (scheduler *Scheduler) GetSymbolCallGraph(ctx context.Context, projectID string, symbolID string, options CallGraphOptions) (SymbolCallGraph, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return SymbolCallGraph{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.GetSymbolCallGraph(ctx, projectID, symbolID, options)
}

func (scheduler *Scheduler) ListHeadings(ctx context.Context, projectID string, fileID string, pagination Pagination) (HeadingList, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return HeadingList{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.ListHeadings(ctx, projectID, fileID, pagination)
}

func (scheduler *Scheduler) GetFileOutline(ctx context.Context, projectID string, fileID string, options FileOutlineOptions) (FileOutline, error) {
	api, ok := scheduler.runner.(API)
	if !ok {
		return FileOutline{}, fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
	}
	return api.GetFileOutline(ctx, projectID, fileID, options)
}

func (scheduler *Scheduler) SubmitFullScan(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return scheduler.submit(ctx, scheduler.fullCh, schedulerTask{
		projectID: projectID,
		trigger:   trigger,
		taskType:  "full_scan",
		done:      make(chan schedulerResult, 1),
	})
}

func (scheduler *Scheduler) SubmitFullScanAsync(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	if scheduler == nil {
		return Run{}, fmt.Errorf("%w: scheduler is required", ErrUnsupportedIngest)
	}
	scheduler.mu.Lock()
	started := scheduler.started
	scheduler.mu.Unlock()
	if !started {
		return Run{}, fmt.Errorf("%w: scheduler is not started", ErrUnsupportedIngest)
	}
	runner, ok := scheduler.runner.(asyncProjectRunner)
	if !ok {
		return Run{}, fmt.Errorf("%w: async ingestion runner is required", ErrUnsupportedIngest)
	}
	run, err := runner.PrepareProjectRun(ctx, projectID, trigger)
	if err != nil {
		return run, err
	}
	task := schedulerTask{
		projectID:   run.ProjectID,
		trigger:     run.Trigger,
		taskType:    "full_scan",
		preparedRun: run,
	}
	if err := scheduler.enqueueTask(ctx, scheduler.fullCh, &task, false); err == nil {
		return run, nil
	} else if ctx.Err() != nil {
		_, _ = runner.FailPreparedProjectRun(context.Background(), run, "enqueue_canceled")
		return run, ctx.Err()
	} else {
		failed, failErr := runner.FailPreparedProjectRun(ctx, run, "queue_full")
		if failErr != nil {
			return failed, failErr
		}
		return failed, fmt.Errorf("%w: ingestion queue is full", ErrUnsupportedIngest)
	}
}

func (scheduler *Scheduler) SubmitPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	return scheduler.submit(ctx, scheduler.liveCh, schedulerTask{
		projectID:    projectID,
		relativePath: relativePath,
		trigger:      trigger,
		taskType:     "path",
		done:         make(chan schedulerResult, 1),
	})
}

func (scheduler *Scheduler) submit(ctx context.Context, queue chan schedulerTask, task schedulerTask) (Run, error) {
	if scheduler == nil {
		return Run{}, fmt.Errorf("%w: scheduler is required", ErrUnsupportedIngest)
	}
	if err := scheduler.enqueueTask(ctx, queue, &task, true); err != nil {
		if ctx.Err() != nil {
			return Run{}, ctx.Err()
		}
		return Run{}, err
	}
	select {
	case result := <-task.done:
		return result.run, result.err
	case <-ctx.Done():
		return Run{}, ctx.Err()
	}
}

func (scheduler *Scheduler) enqueueTask(ctx context.Context, queue chan schedulerTask, task *schedulerTask, block bool) error {
	if task.projectWide() {
		scheduler.registerPendingProjectWide(task)
	}
	if block {
		select {
		case queue <- *task:
			return nil
		case <-ctx.Done():
			scheduler.unregisterPendingProjectWide(task)
			return ctx.Err()
		}
	}
	select {
	case queue <- *task:
		return nil
	case <-ctx.Done():
		scheduler.unregisterPendingProjectWide(task)
		return ctx.Err()
	default:
		scheduler.unregisterPendingProjectWide(task)
		return fmt.Errorf("%w: ingestion queue is full", ErrUnsupportedIngest)
	}
}

func (scheduler *Scheduler) workerLoop(ctx context.Context) {
	for {
		task, ok := scheduler.nextTask(ctx)
		if !ok {
			return
		}
		scheduler.runTask(ctx, task)
	}
}

func (scheduler *Scheduler) nextTask(ctx context.Context) (schedulerTask, bool) {
	if scheduler.hasPendingProjectWide() {
		select {
		case task := <-scheduler.fullCh:
			return task, true
		default:
		}
	}
	if scheduler.options.LivePathPriority {
		select {
		case task := <-scheduler.liveCh:
			return task, true
		default:
		}
	}
	select {
	case <-ctx.Done():
		return schedulerTask{}, false
	case task := <-scheduler.liveCh:
		return task, true
	case task := <-scheduler.fullCh:
		return task, true
	}
}

func (scheduler *Scheduler) runTask(ctx context.Context, task schedulerTask) {
	if !scheduler.acquireProject(ctx, task) {
		scheduler.unregisterPendingProjectWide(&task)
		result := schedulerResult{err: ctx.Err()}
		if task.preparedRun.ID != "" {
			if failed, err := scheduler.failPreparedRun(context.Background(), task, "execution_canceled"); err != nil {
				result.err = err
			} else {
				result.run = failed
			}
		}
		if task.done != nil {
			task.done <- result
		}
		return
	}
	defer scheduler.releaseProject(task)

	var result schedulerResult
	if task.taskType == "path" {
		result.run, result.err = scheduler.runner.IngestPath(ctx, task.projectID, task.relativePath, task.trigger)
	} else if task.taskType == "search_index_rebuild" {
		if task.preparedRun.ID != "" {
			runner, ok := scheduler.runner.(asyncSearchIndexRebuildRunner)
			if !ok {
				result.err = fmt.Errorf("%w: async search index repair runner is required", ErrUnsupportedIngest)
				result.run, _ = scheduler.failPreparedRun(ctx, task, "runner_unsupported")
			} else {
				result.run, result.err = runner.ExecutePreparedSearchIndexRebuild(ctx, task.preparedRun)
			}
		} else {
			runner, ok := scheduler.runner.(API)
			if !ok {
				result.err = fmt.Errorf("%w: ingestion query API is required", ErrUnsupportedIngest)
			} else {
				result.run, result.err = runner.RebuildSearchIndex(ctx, task.projectID)
			}
		}
	} else if task.preparedRun.ID != "" {
		runner, ok := scheduler.runner.(asyncProjectRunner)
		if !ok {
			result.err = fmt.Errorf("%w: async ingestion runner is required", ErrUnsupportedIngest)
			result.run, _ = scheduler.failPreparedRun(ctx, task, "runner_unsupported")
		} else {
			result.run, result.err = runner.ExecutePreparedProjectRun(ctx, task.preparedRun)
		}
	} else {
		result.run, result.err = scheduler.runner.IngestProject(ctx, task.projectID, task.trigger)
	}
	if task.done != nil {
		task.done <- result
	}
}

func (scheduler *Scheduler) acquireProject(ctx context.Context, task schedulerTask) bool {
	for {
		scheduler.mu.Lock()
		state := scheduler.projectStateLocked(task.projectID)
		if scheduler.canAcquireProjectLocked(state, task) {
			if task.pendingProjectWide {
				scheduler.decrementPendingProjectWideLocked(state, task.projectID)
				task.pendingProjectWide = false
			}
			state.active++
			scheduler.active++
			scheduler.activeProject[task.projectID]++
			if task.projectWide() {
				state.projectWideActive = true
				scheduler.projectWide[task.projectID]++
			}
			scheduler.mu.Unlock()
			return true
		}
		changed := state.changed
		scheduler.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

func (scheduler *Scheduler) releaseProject(task schedulerTask) {
	scheduler.mu.Lock()
	state := scheduler.projectStateLocked(task.projectID)
	if state.active > 0 {
		state.active--
	}
	if task.projectWide() {
		state.projectWideActive = false
		scheduler.projectWide[task.projectID]--
		if scheduler.projectWide[task.projectID] <= 0 {
			delete(scheduler.projectWide, task.projectID)
		}
	}
	scheduler.active--
	if scheduler.active < 0 {
		scheduler.active = 0
	}
	scheduler.activeProject[task.projectID]--
	if scheduler.activeProject[task.projectID] <= 0 {
		delete(scheduler.activeProject, task.projectID)
	}
	close(state.changed)
	state.changed = make(chan struct{})
	if state.active == 0 && !state.projectWideActive && state.projectWidePending == 0 {
		delete(scheduler.projectState, task.projectID)
	}
	scheduler.mu.Unlock()
}

func (scheduler *Scheduler) projectStateLocked(projectID string) *schedulerProjectState {
	state, ok := scheduler.projectState[projectID]
	if !ok {
		state = &schedulerProjectState{changed: make(chan struct{})}
		scheduler.projectState[projectID] = state
	}
	return state
}

func (scheduler *Scheduler) canAcquireProjectLocked(state *schedulerProjectState, task schedulerTask) bool {
	if task.projectWide() {
		return state.active == 0 && !state.projectWideActive
	}
	if state.projectWideActive || state.projectWidePending > 0 {
		return false
	}
	return state.active < scheduler.options.PerProjectWorkerLimit && !state.projectWideActive
}

func (schedulerTask schedulerTask) projectWide() bool {
	return schedulerTask.taskType == "full_scan" || schedulerTask.taskType == "search_index_rebuild"
}

func (scheduler *Scheduler) registerPendingProjectWide(task *schedulerTask) {
	if scheduler == nil || task == nil || !task.projectWide() || task.pendingProjectWide {
		return
	}
	scheduler.mu.Lock()
	state := scheduler.projectStateLocked(task.projectID)
	state.projectWidePending++
	scheduler.pendingProjectWide[task.projectID]++
	task.pendingProjectWide = true
	close(state.changed)
	state.changed = make(chan struct{})
	scheduler.mu.Unlock()
}

func (scheduler *Scheduler) unregisterPendingProjectWide(task *schedulerTask) {
	if scheduler == nil || task == nil || !task.pendingProjectWide {
		return
	}
	scheduler.mu.Lock()
	state := scheduler.projectStateLocked(task.projectID)
	scheduler.unregisterPendingProjectWideLocked(state, task.projectID)
	task.pendingProjectWide = false
	scheduler.mu.Unlock()
}

func (scheduler *Scheduler) unregisterPendingProjectWideLocked(state *schedulerProjectState, projectID string) {
	scheduler.decrementPendingProjectWideLocked(state, projectID)
	if state.active == 0 && !state.projectWideActive && state.projectWidePending == 0 {
		delete(scheduler.projectState, projectID)
	}
}

func (scheduler *Scheduler) decrementPendingProjectWideLocked(state *schedulerProjectState, projectID string) {
	if state.projectWidePending > 0 {
		state.projectWidePending--
	}
	if scheduler.pendingProjectWide[projectID] > 0 {
		scheduler.pendingProjectWide[projectID]--
	}
	if scheduler.pendingProjectWide[projectID] <= 0 {
		delete(scheduler.pendingProjectWide, projectID)
	}
	close(state.changed)
	state.changed = make(chan struct{})
}

func (scheduler *Scheduler) hasPendingProjectWide() bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return len(scheduler.pendingProjectWide) > 0
}

func (scheduler *Scheduler) failPreparedRun(ctx context.Context, task schedulerTask, category string) (Run, error) {
	runner, ok := scheduler.runner.(asyncProjectRunner)
	if !ok || task.preparedRun.ID == "" {
		return task.preparedRun, nil
	}
	return runner.FailPreparedProjectRun(ctx, task.preparedRun, category)
}

func (scheduler *Scheduler) Diagnostics() SchedulerDiagnostics {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	activeProject := make(map[string]int, len(scheduler.activeProject))
	for projectID, count := range scheduler.activeProject {
		activeProject[projectID] = count
	}
	projectWide := make(map[string]int, len(scheduler.projectWide))
	for projectID, count := range scheduler.projectWide {
		projectWide[projectID] = count
	}
	pendingProjectWide := make(map[string]int, len(scheduler.pendingProjectWide))
	for projectID, count := range scheduler.pendingProjectWide {
		pendingProjectWide[projectID] = count
	}
	return SchedulerDiagnostics{
		QueueDepth:                  len(scheduler.liveCh) + len(scheduler.fullCh),
		LiveQueueDepth:              len(scheduler.liveCh),
		FullScanQueueDepth:          len(scheduler.fullCh),
		ActiveTaskCount:             scheduler.active,
		ActiveProjectTaskCount:      activeProject,
		ProjectWideTaskCount:        projectWide,
		PendingProjectWideTaskCount: pendingProjectWide,
	}
}
