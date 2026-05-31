package projectingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

var (
	ErrProjectNotFound     = projectregistry.ErrProjectNotFound
	ErrInvalidInput        = projectregistry.ErrInvalidInput
	ErrProjectDisabled     = errors.New("ingestion project disabled")
	ErrUnsupportedIngest   = errors.New("ingestion unsupported")
	ErrIngestionNotFound   = errors.New("ingestion resource not found")
	ErrPathEscapesRoot     = errors.New("path escapes project root")
	ErrPathNotProjectLocal = errors.New("path must be project-relative")
)

const fullScanProgressFlushFiles = 25

type stateStore interface {
	SaveRun(context.Context, Run) error
	GetRun(context.Context, string, string) (Run, error)
	ListLatestRuns(context.Context, string, int) ([]Run, error)
	SaveFileState(context.Context, FileState) error
	ListFileStates(context.Context, string, FileStateFilter) ([]FileState, error)
	ListFileStatesPage(context.Context, string, FileStateFilter, Pagination) ([]FileState, string, error)
	GetFileStateByHash(context.Context, string, string) (FileState, error)
	GetExtractorCache(context.Context, string, string, string, string, string) (ExtractorCacheEntry, error)
	SaveExtractorCache(context.Context, ExtractorCacheEntry) error
	DeleteExtractorCacheForFile(context.Context, string, string) error
}

type stateBatchStore interface {
	SaveFileStatesBatch(context.Context, []FileState) error
}

type activeRunFailer interface {
	FailActiveRuns(context.Context, string, string, time.Time) (int, error)
}

type activeRunLister interface {
	ListActiveRuns(context.Context, string) ([]Run, error)
}

type API interface {
	IngestProject(context.Context, string, Trigger) (Run, error)
	IngestPath(context.Context, string, string, Trigger) (Run, error)
	SubmitIngestProject(context.Context, string, Trigger) (Run, error)
	SubmitRebuildSearchIndex(context.Context, string) (Run, error)
	RebuildSearchIndex(context.Context, string) (Run, error)
	RunMetadata(context.Context, string, string) (RunMetadata, error)
	LatestRunMetadata(context.Context, string) (RunMetadata, error)
	ListFiles(context.Context, string, FileStateFilter, Pagination) (FileList, error)
	GetFile(context.Context, string, string) (FileMetadata, error)
	ListChunks(context.Context, string, string, Pagination, int) (ChunkList, error)
	GetChunk(context.Context, string, string, string, int) (ChunkMetadata, error)
	ListSymbols(context.Context, string, SymbolFilter, Pagination) (SymbolList, error)
	SearchText(context.Context, string, TextSearchOptions) (TextSearchResultList, error)
	SearchFiles(context.Context, string, FileSearchOptions) (FileList, error)
	SearchSymbols(context.Context, string, SymbolFilter, Pagination) (SymbolList, error)
	SearchReferences(context.Context, string, ReferenceSearchOptions) (SymbolReferenceList, error)
	SearchCalls(context.Context, string, ReferenceSearchOptions) (SymbolCallEdgeList, error)
	ListASTQueries(context.Context, string) (ASTQueryCatalog, error)
	SearchAST(context.Context, string, ASTSearchOptions) (ASTSearchResultList, error)
	GetSymbol(context.Context, string, string) (SymbolMetadata, error)
	GetSymbolSource(context.Context, string, string, SymbolSourceOptions) (SymbolSource, error)
	ListSymbolReferences(context.Context, string, string, Pagination) (SymbolReferenceList, error)
	ListSymbolCallers(context.Context, string, string, Pagination) (SymbolCallEdgeList, error)
	ListSymbolCallees(context.Context, string, string, Pagination) (SymbolCallEdgeList, error)
	GetSymbolCallGraph(context.Context, string, string, CallGraphOptions) (SymbolCallGraph, error)
	ListHeadings(context.Context, string, string, Pagination) (HeadingList, error)
	GetFileOutline(context.Context, string, string, FileOutlineOptions) (FileOutline, error)
}

type Service struct {
	registry              *projectregistry.Registry
	graph                 *GraphStore
	state                 stateStore
	extractors            *ExtractorRegistry
	extractorCacheEnabled bool
	fullScanBatchSize     int
	fullScanWorkerCount   int
	fullScanWorkerSlots   chan struct{}
	metricsMu             sync.Mutex
	stageMetrics          map[string]StageDiagnostic
	checkpoint            func(context.Context) error
	now                   func() time.Time
	newID                 func(projectregistry.Project, time.Time) string
}

func NewService(registry *projectregistry.Registry, graph *GraphStore, state stateStore) *Service {
	defaultWorkerCount := runtime.NumCPU()
	if defaultWorkerCount <= 0 {
		defaultWorkerCount = 1
	}
	return &Service{
		registry:              registry,
		graph:                 graph,
		state:                 state,
		extractors:            NewDefaultExtractorRegistry(),
		extractorCacheEnabled: true,
		fullScanBatchSize:     500,
		fullScanWorkerCount:   defaultWorkerCount,
		fullScanWorkerSlots:   make(chan struct{}, defaultWorkerCount),
		stageMetrics:          make(map[string]StageDiagnostic),
		now:                   func() time.Time { return time.Now().UTC() },
		newID:                 defaultRunID,
	}
}

func (svc *Service) Diagnostics() map[string]StageDiagnostic {
	if svc == nil {
		return nil
	}
	svc.metricsMu.Lock()
	defer svc.metricsMu.Unlock()
	metrics := make(map[string]StageDiagnostic, len(svc.stageMetrics))
	for stage, diagnostic := range svc.stageMetrics {
		metrics[stage] = diagnostic
	}
	return metrics
}

func (svc *Service) SetCheckpointFunc(checkpoint func(context.Context) error) {
	svc.checkpoint = checkpoint
}

func (svc *Service) recordStage(stage string, startedAt time.Time, err error) {
	if svc == nil || stage == "" || startedAt.IsZero() {
		return
	}
	duration := time.Since(startedAt)
	now := time.Now().UTC().Unix()
	millis := duration.Milliseconds()
	if duration > 0 && millis == 0 {
		millis = 1
	}
	svc.metricsMu.Lock()
	defer svc.metricsMu.Unlock()
	if svc.stageMetrics == nil {
		svc.stageMetrics = make(map[string]StageDiagnostic)
	}
	diagnostic := svc.stageMetrics[stage]
	diagnostic.Count++
	diagnostic.TotalMillis += millis
	diagnostic.LastMillis = millis
	diagnostic.LastSeenUnix = now
	if millis > diagnostic.MaxMillis {
		diagnostic.MaxMillis = millis
	}
	if err != nil {
		diagnostic.ErrorCount++
		diagnostic.LastErrorUnix = now
		diagnostic.LastError = safeStageError(stage, err)
	}
	svc.stageMetrics[stage] = diagnostic
}

func safeStageError(stage string, err error) string {
	if err == nil {
		return ""
	}
	switch stage {
	case "extract":
		return string(SkipReasonParseError)
	case "read":
		return string(SkipReasonReadError)
	case "chunk":
		return string(SkipReasonChunkError)
	case "walk":
		return "walk_failed"
	default:
		return safeStageCategory(err.Error(), "error")
	}
}

func safeStageCategory(value string, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '_' || r == '-':
			builder.WriteRune(r)
		case r == ' ' || r == ':' || r == '/' || r == '.':
			builder.WriteRune('_')
		}
		if builder.Len() >= 64 {
			break
		}
	}
	out := strings.Trim(builder.String(), "_-")
	if out == "" {
		return fallback
	}
	return out
}

func (svc *Service) withGraphBatch(ctx context.Context, fn func(*Service) (Run, error)) (Run, error) {
	if svc == nil || svc.graph == nil {
		return fn(svc)
	}
	var run Run
	err := svc.graph.WithBatch(ctx, func(graph *GraphStore) error {
		batched := &Service{
			registry:              svc.registry,
			graph:                 graph,
			state:                 svc.state,
			extractors:            svc.extractors,
			extractorCacheEnabled: svc.extractorCacheEnabled,
			fullScanBatchSize:     svc.fullScanBatchSize,
			fullScanWorkerCount:   svc.fullScanWorkerCount,
			fullScanWorkerSlots:   svc.fullScanWorkerSlots,
			stageMetrics:          make(map[string]StageDiagnostic),
			checkpoint:            svc.checkpoint,
			now:                   svc.now,
			newID:                 svc.newID,
		}
		var innerErr error
		run, innerErr = fn(batched)
		batched.metricsMu.Lock()
		for stage, diagnostic := range batched.stageMetrics {
			svc.mergeStageDiagnostic(stage, diagnostic)
		}
		batched.metricsMu.Unlock()
		return innerErr
	})
	return run, err
}

func (svc *Service) mergeStageDiagnostic(stage string, diagnostic StageDiagnostic) {
	if svc == nil || stage == "" || diagnostic.Count == 0 {
		return
	}
	svc.metricsMu.Lock()
	defer svc.metricsMu.Unlock()
	if svc.stageMetrics == nil {
		svc.stageMetrics = make(map[string]StageDiagnostic)
	}
	existing := svc.stageMetrics[stage]
	existing.Count += diagnostic.Count
	existing.TotalMillis += diagnostic.TotalMillis
	existing.LastMillis = diagnostic.LastMillis
	if diagnostic.LastSeenUnix > existing.LastSeenUnix {
		existing.LastSeenUnix = diagnostic.LastSeenUnix
	}
	if diagnostic.MaxMillis > existing.MaxMillis {
		existing.MaxMillis = diagnostic.MaxMillis
	}
	existing.ErrorCount += diagnostic.ErrorCount
	if diagnostic.LastErrorUnix > existing.LastErrorUnix {
		existing.LastErrorUnix = diagnostic.LastErrorUnix
		existing.LastError = diagnostic.LastError
	}
	svc.stageMetrics[stage] = existing
}

func (svc *Service) IngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	project, err := svc.projectForIngestion(projectID, normalizeTrigger(trigger))
	if err != nil {
		return Run{}, err
	}
	run := svc.startRun(project, trigger)
	run.Status = RunStatusRunning
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return svc.executeProjectRun(ctx, project, run)
}

func (svc *Service) SubmitIngestProject(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	return Run{}, fmt.Errorf("%w: manual ingestion submission requires scheduler", ErrUnsupportedIngest)
}

func (svc *Service) SubmitRebuildSearchIndex(ctx context.Context, projectID string) (Run, error) {
	return Run{}, fmt.Errorf("%w: search index repair submission requires scheduler", ErrUnsupportedIngest)
}

func (svc *Service) RebuildSearchIndex(ctx context.Context, projectID string) (Run, error) {
	project, err := svc.projectForIngestion(projectID, TriggerManual)
	if err != nil {
		return Run{}, err
	}
	run := svc.startRun(project, TriggerManual)
	run.RunKind = RunKindSearchIndexRebuild
	run.Status = RunStatusRunning
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return svc.executeSearchIndexRebuild(ctx, project, run)
}

func (svc *Service) ExecutePreparedSearchIndexRebuild(ctx context.Context, run Run) (Run, error) {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.ProjectID) == "" {
		return Run{}, ErrInvalidInput
	}
	project, err := svc.projectForIngestion(run.ProjectID, TriggerManual)
	if err != nil {
		return run, err
	}
	run.ProjectID = project.ID
	run.Trigger = TriggerManual
	run.RunKind = RunKindSearchIndexRebuild
	run.Mode = project.DigestMode
	run.Status = RunStatusRunning
	if run.StartedAt.IsZero() {
		run.StartedAt = svc.now().UTC()
	}
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return svc.executeSearchIndexRebuild(ctx, project, run)
}

func (svc *Service) executeSearchIndexRebuild(ctx context.Context, project projectregistry.Project, run Run) (Run, error) {
	search, ok := svc.state.(searchRepairStore)
	if !ok {
		run.Status = RunStatusFailed
		run.ErrorCategory = "search_index_rebuild_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, fmt.Errorf("%w: search index repair requires SQLite search store", ErrUnsupportedIngest)
	}
	states, err := search.ReconcileSearchIndex(ctx, project)
	if err != nil {
		_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_delete_failed")
		run.Status = RunStatusFailed
		run.ErrorCategory = "search_index_delete_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	progress := newFullScanProgress(run)
	for _, state := range states {
		if err := svc.repairSearchIndexFile(ctx, project, progress, state); err != nil {
			_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_rebuild_failed")
			run = progress.currentRun()
			run.Status = RunStatusFailed
			run.ErrorCategory = "search_index_rebuild_failed"
			run.FinishedAt = svc.now().UTC()
			_ = svc.persistRun(ctx, project, run)
			return run, err
		}
	}
	run = progress.currentRun()
	run.Status = RunStatusCompleted
	run.FinishedAt = svc.now().UTC()
	if err := svc.persistRun(ctx, project, run); err != nil {
		_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_rebuild_failed")
		return run, err
	}
	if err := search.ClearSearchIndexDegraded(ctx, project.ID); err != nil {
		return run, err
	}
	if err := svc.checkpointStorage(ctx); err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "checkpoint_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	return run, nil
}

func (svc *Service) repairSearchIndexFile(ctx context.Context, project projectregistry.Project, progress *fullScanProgress, state FileState) error {
	if state.RelativePath == "" || !state.RelativePathSafe {
		return fmt.Errorf("%w: unsafe search index repair state", ErrUnsupportedIngest)
	}
	relative, ok := normalizeProjectRelativePath(state.RelativePath)
	if !ok || relative != state.RelativePath {
		return fmt.Errorf("%w: unsafe search index repair path", ErrPathNotProjectLocal)
	}
	fullPath := filepath.Join(project.CanonicalRootPath, filepath.FromSlash(relative))
	checkedRelative, ok := safeRelativePath(project.CanonicalRootPath, fullPath)
	if !ok || checkedRelative != relative {
		return ErrPathEscapesRoot
	}
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return svc.repairAbsentSearchIndexFile(ctx, project, progress, state)
	}
	if err != nil {
		skipped := svc.skippedState(project, relative, SkipReasonStatError, 0, time.Time{}, true, progress.currentRun().StartedAt)
		return svc.saveSearchIndexRepairResult(ctx, project, progress, fullScanFileResult{state: skipped})
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		skipped := svc.skippedState(project, relative, SkipReasonUnsafePath, info.Size(), info.ModTime().UTC(), true, progress.currentRun().StartedAt)
		return svc.saveSearchIndexRepairResult(ctx, project, progress, fullScanFileResult{state: skipped})
	}
	result := svc.prepareExistingFile(ctx, project, relative, fullPath, info, progress.currentRun())
	return svc.saveSearchIndexRepairResult(ctx, project, progress, result)
}

func (svc *Service) repairAbsentSearchIndexFile(ctx context.Context, project projectregistry.Project, progress *fullScanProgress, state FileState) error {
	run := progress.currentRun()
	state.Status = FileStatusAbsent
	state.Present = false
	state.ContentSHA256 = ""
	state.LastEventAt = run.StartedAt
	state.LastIngestedAt = run.StartedAt
	if err := svc.state.SaveFileState(ctx, state); err != nil {
		return err
	}
	if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
		return err
	}
	if err := svc.graph.putFileState(ctx, project, run, state); err != nil {
		return err
	}
	if search, ok := svc.state.(searchMutationStore); ok {
		if err := search.DeleteSearchFile(ctx, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash)); err != nil {
			_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_delete_failed")
			return err
		}
	}
	progress.record(state, true, 0, 0, false)
	return progress.flush(ctx, svc, project, false)
}

func (svc *Service) saveSearchIndexRepairResult(ctx context.Context, project projectregistry.Project, progress *fullScanProgress, result fullScanFileResult) error {
	if result.err != nil {
		return result.err
	}
	if result.state.Status == FileStatusEligible {
		result.err = svc.saveEligiblePreparedFile(ctx, project, progress.currentRun(), result)
	} else {
		result.err = svc.saveSkipped(ctx, project, progress.currentRun(), result.state, false)
	}
	if result.state.RelativePathHash != "" {
		progress.record(result.state, true, result.chunkCount, result.symbolCount, result.unchanged)
	}
	if result.err != nil {
		return result.err
	}
	return progress.flush(ctx, svc, project, false)
}

func (svc *Service) SetFullScanBatchSize(size int) {
	if size > 0 {
		svc.fullScanBatchSize = size
	}
}

func (svc *Service) SetFullScanWorkerCount(count int) {
	if count > 0 {
		svc.fullScanWorkerCount = count
		svc.fullScanWorkerSlots = make(chan struct{}, count)
	}
}

func (svc *Service) SetFullScanWorkerLimits(globalCount int, perProjectCount int) {
	if perProjectCount <= 0 {
		perProjectCount = svc.effectiveFullScanWorkerCount()
	}
	if globalCount <= 0 {
		globalCount = perProjectCount
	}
	svc.fullScanWorkerCount = perProjectCount
	svc.fullScanWorkerSlots = make(chan struct{}, globalCount)
}

func (svc *Service) SetExtractorCacheEnabled(enabled bool) {
	svc.extractorCacheEnabled = enabled
}

func (svc *Service) PrepareProjectRun(ctx context.Context, projectID string, trigger Trigger) (Run, error) {
	trigger = normalizeTrigger(trigger)
	project, err := svc.projectForIngestion(projectID, trigger)
	if err != nil {
		return Run{}, err
	}
	run := svc.startRun(project, trigger)
	run.RunKind = RunKindDelta
	run.Status = RunStatusPending
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	if err := svc.checkpointStorage(ctx); err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "checkpoint_failed"
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	return run, nil
}

func (svc *Service) checkpointStorage(ctx context.Context) error {
	if svc == nil || svc.checkpoint == nil {
		return nil
	}
	startedAt := time.Now()
	err := svc.checkpoint(ctx)
	svc.recordStage("storage.checkpoint", startedAt, err)
	return err
}

func (svc *Service) ExecutePreparedProjectRun(ctx context.Context, run Run) (Run, error) {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.ProjectID) == "" {
		return Run{}, ErrInvalidInput
	}
	project, err := svc.projectForIngestion(run.ProjectID, normalizeTrigger(run.Trigger))
	if err != nil {
		return run, err
	}
	run.ProjectID = project.ID
	run.Trigger = normalizeTrigger(run.Trigger)
	run.Mode = project.DigestMode
	run.Status = RunStatusRunning
	if run.StartedAt.IsZero() {
		run.StartedAt = svc.now().UTC()
	}
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return svc.executeProjectRun(ctx, project, run)
}

func (svc *Service) FailPreparedProjectRun(ctx context.Context, run Run, errorCategory string) (Run, error) {
	project, err := svc.projectForIngestion(run.ProjectID, normalizeTrigger(run.Trigger))
	if err != nil {
		return run, err
	}
	run.Status = RunStatusFailed
	run.ErrorCategory = strings.TrimSpace(errorCategory)
	if run.ErrorCategory == "" {
		run.ErrorCategory = "ingest_failed"
	}
	run.FinishedAt = svc.now().UTC()
	return run, svc.persistRun(ctx, project, run)
}

func (svc *Service) FailInterruptedRuns(ctx context.Context, errorCategory string) (int, error) {
	if svc == nil || svc.registry == nil || svc.state == nil {
		return 0, fmt.Errorf("%w: ingestion service dependencies are required", ErrUnsupportedIngest)
	}
	errorCategory = strings.TrimSpace(errorCategory)
	if errorCategory == "" {
		errorCategory = "server_restarted"
	}
	failed := 0
	for _, project := range svc.registry.List() {
		if !project.Enabled || project.DigestMode != projectregistry.DigestModeContentGraph {
			continue
		}
		if failer, ok := svc.state.(activeRunFailer); ok {
			finishedAt := svc.now().UTC()
			activeRuns, err := listActiveRuns(ctx, svc.state, project.ID)
			if err != nil {
				return failed, err
			}
			count, err := failer.FailActiveRuns(ctx, project.ID, errorCategory, finishedAt)
			if err != nil {
				return failed, err
			}
			for _, run := range activeRuns {
				run.Status = RunStatusFailed
				run.ErrorCategory = errorCategory
				run.CurrentPhase = "interrupted"
				run.FinishedAt = finishedAt
				run.HeartbeatAt = finishedAt
				run.LastProgressAt = finishedAt
				if err := svc.graph.PutRun(ctx, project, run); err != nil {
					return failed, err
				}
			}
			failed += count
			continue
		}
		runs, err := svc.state.ListLatestRuns(ctx, project.ID, 25)
		if err != nil {
			return failed, err
		}
		for _, run := range runs {
			if run.Status != RunStatusPending && run.Status != RunStatusRunning {
				continue
			}
			run.Status = RunStatusFailed
			run.ErrorCategory = errorCategory
			run.FinishedAt = svc.now().UTC()
			if err := svc.persistRun(ctx, project, run); err != nil {
				return failed, err
			}
			failed++
		}
	}
	return failed, nil
}

func listActiveRuns(ctx context.Context, state stateStore, projectID string) ([]Run, error) {
	if lister, ok := state.(activeRunLister); ok {
		return lister.ListActiveRuns(ctx, projectID)
	}
	runs, err := state.ListLatestRuns(ctx, projectID, 25)
	if err != nil {
		return nil, err
	}
	active := make([]Run, 0, len(runs))
	for _, run := range runs {
		if run.Status == RunStatusPending || run.Status == RunStatusRunning {
			active = append(active, run)
		}
	}
	return active, nil
}

func (svc *Service) executeProjectRun(ctx context.Context, project projectregistry.Project, run Run) (Run, error) {
	runStartedAt := time.Now()
	defer func() { svc.recordStage("runtime.full_scan", runStartedAt, nil) }()
	run.CurrentPhase = "walking"
	run.HeartbeatAt = svc.now().UTC()
	run.LastProgressAt = run.HeartbeatAt
	_ = svc.persistRun(ctx, project, run)
	progress := newFullScanProgress(run)
	workerCount := svc.effectiveFullScanWorkerCount()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan fullScanFileJob, workerCount)
	results := make(chan fullScanFileResult, workerCount)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if err := svc.acquireFullScanWorker(runCtx); err != nil {
					select {
					case results <- fullScanFileResult{err: err}:
					case <-runCtx.Done():
					}
					return
				}
				result := svc.prepareExistingFile(runCtx, project, job.relative, job.fullPath, job.info, progress.currentRun())
				svc.releaseFullScanWorker()
				select {
				case results <- result:
				case <-runCtx.Done():
					return
				}
				if result.err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	var resultErr error
	var resultMu sync.Mutex
	resultsDone := make(chan struct{})
	go func() {
		defer close(resultsDone)
		batch := make([]fullScanFileResult, 0, svc.fullScanBatchSize)
		flushBatch := func(force bool) error {
			if len(batch) == 0 {
				return nil
			}
			flushSize := svc.fullScanBatchSize
			if flushSize <= 0 {
				flushSize = fullScanProgressFlushFiles
			}
			if !force && len(batch) < flushSize {
				return nil
			}
			if err := svc.saveFullScanPreparedBatch(ctx, project, progress.currentRun(), batch); err != nil {
				return err
			}
			batch = batch[:0]
			return progress.flush(ctx, svc, project, true)
		}
		for result := range results {
			if result.err != nil {
				resultMu.Lock()
				if resultErr == nil {
					resultErr = result.err
				}
				resultMu.Unlock()
				cancel()
				continue
			}
			batch = append(batch, result)
			if result.state.RelativePathHash != "" {
				progress.record(result.state, true, result.chunkCount, result.symbolCount, result.unchanged)
				if err := progress.flush(ctx, svc, project, false); err != nil {
					resultMu.Lock()
					if resultErr == nil {
						resultErr = err
					}
					resultMu.Unlock()
					cancel()
					continue
				}
			}
			if err := flushBatch(false); err != nil {
				resultMu.Lock()
				if resultErr == nil {
					resultErr = err
				}
				resultMu.Unlock()
				cancel()
			}
		}
		if err := flushBatch(true); err != nil {
			resultMu.Lock()
			if resultErr == nil {
				resultErr = err
			}
			resultMu.Unlock()
			cancel()
		}
	}()

	root := project.CanonicalRootPath
	walkStartedAt := time.Now()
	walkErr := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if filePath == root {
				return fmt.Errorf("walk failed for project root")
			}
			relative, ok := safeRelativePath(root, filePath)
			if !ok {
				return ErrPathEscapesRoot
			}
			state := svc.skippedState(project, relative, SkipReasonStatError, 0, time.Time{}, true, run.StartedAt)
			if err := svc.saveSkipped(ctx, project, progress.currentRun(), state, false); err != nil {
				return err
			}
			progress.record(state, false, 0, 0, false)
			return progress.flush(ctx, svc, project, false)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == root {
			return nil
		}
		relative, ok := safeRelativePath(root, filePath)
		if !ok {
			return ErrPathEscapesRoot
		}
		if entry.Type()&os.ModeSymlink != 0 {
			state := svc.skippedState(project, relative, SkipReasonUnsafePath, 0, time.Time{}, true, run.StartedAt)
			err := svc.saveSkipped(ctx, project, progress.currentRun(), state, entry.IsDir())
			progress.record(state, false, 0, 0, false)
			if flushErr := progress.flush(ctx, svc, project, false); flushErr != nil {
				return flushErr
			}
			return err
		}
		if entry.IsDir() {
			if projectregistry.ProjectExcludesRelativePath(project, relative) {
				return filepath.SkipDir
			}
			if !projectregistry.ProjectMayIncludeRelativePath(project, relative) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			state := svc.skippedState(project, relative, SkipReasonStatError, 0, time.Time{}, true, run.StartedAt)
			if err := svc.saveSkipped(ctx, project, progress.currentRun(), state, false); err != nil {
				return err
			}
			progress.record(state, false, 0, 0, false)
			return progress.flush(ctx, svc, project, false)
		}
		if !info.Mode().IsRegular() {
			state := svc.skippedState(project, relative, SkipReasonUnsafePath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
			if err := svc.saveSkipped(ctx, project, progress.currentRun(), state, false); err != nil {
				return err
			}
			progress.record(state, false, 0, 0, false)
			return progress.flush(ctx, svc, project, false)
		}
		if !projectregistry.ProjectIncludesRelativePath(project, relative) {
			state := svc.skippedState(project, relative, SkipReasonDeniedPath, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
			if err := svc.saveSkipped(ctx, project, progress.currentRun(), state, false); err != nil {
				return err
			}
			progress.record(state, false, 0, 0, false)
			return progress.flush(ctx, svc, project, false)
		}
		select {
		case jobs <- fullScanFileJob{relative: relative, fullPath: filePath, info: info}:
			return nil
		case <-runCtx.Done():
			return runCtx.Err()
		}
	})
	svc.recordStage("walk", walkStartedAt, walkErr)
	close(jobs)
	<-resultsDone

	resultMu.Lock()
	if resultErr != nil && walkErr == nil {
		walkErr = resultErr
	}
	resultMu.Unlock()

	run = progress.currentRun()
	if walkErr != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "walk_failed"
		run.CurrentPhase = "failed"
		run.FinishedAt = svc.now().UTC()
		run.HeartbeatAt = run.FinishedAt
		_ = svc.persistRun(ctx, project, run)
		return run, walkErr
	}
	run.CurrentPhase = "tombstoning"
	run.HeartbeatAt = svc.now().UTC()
	_ = svc.persistRun(ctx, project, run)
	seen := progress.seenSnapshot()
	tombstoneStartedAt := time.Now()
	err := svc.tombstoneMissingFiles(ctx, project, run, seen)
	svc.recordStage("storage.tombstone", tombstoneStartedAt, err)
	if err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "tombstone_failed"
		run.CurrentPhase = "failed"
		run.FinishedAt = svc.now().UTC()
		run.HeartbeatAt = run.FinishedAt
		_ = svc.persistRun(ctx, project, run)
		return run, err
	}
	run.Status = RunStatusCompleted
	run.CurrentPhase = "completed"
	run.FinishedAt = svc.now().UTC()
	run.HeartbeatAt = run.FinishedAt
	run.LastProgressAt = run.FinishedAt
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	return run, nil
}

func (svc *Service) effectiveFullScanWorkerCount() int {
	if svc.fullScanWorkerCount > 0 {
		return svc.fullScanWorkerCount
	}
	return 1
}

func (svc *Service) acquireFullScanWorker(ctx context.Context) error {
	if svc == nil || svc.fullScanWorkerSlots == nil {
		return nil
	}
	startedAt := time.Now()
	select {
	case svc.fullScanWorkerSlots <- struct{}{}:
		svc.recordStage("scheduler.full_scan_worker_wait", startedAt, nil)
		return nil
	case <-ctx.Done():
		svc.recordStage("scheduler.full_scan_worker_wait", startedAt, ctx.Err())
		return ctx.Err()
	}
}

func (svc *Service) releaseFullScanWorker() {
	if svc == nil || svc.fullScanWorkerSlots == nil {
		return
	}
	select {
	case <-svc.fullScanWorkerSlots:
	default:
	}
}

type fullScanFileJob struct {
	relative string
	fullPath string
	info     fs.FileInfo
}

type fullScanFileResult struct {
	state       FileState
	chunks      []Chunk
	symbols     []Symbol
	references  []Reference
	calls       []Call
	headings    []Heading
	chunkCount  int
	symbolCount int
	unchanged   bool
	err         error
}

type fullScanProgress struct {
	mu                sync.Mutex
	run               Run
	seen              map[string]struct{}
	changesSinceFlush int
}

func newFullScanProgress(run Run) *fullScanProgress {
	return &fullScanProgress{
		run:  run,
		seen: make(map[string]struct{}),
	}
}

func (progress *fullScanProgress) currentRun() Run {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	return progress.run
}

func (progress *fullScanProgress) seenSnapshot() map[string]struct{} {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	seen := make(map[string]struct{}, len(progress.seen))
	for hash := range progress.seen {
		seen[hash] = struct{}{}
	}
	return seen
}

func (progress *fullScanProgress) record(state FileState, countSeen bool, chunkCount int, symbolCount int, unchanged bool) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if state.RelativePathHash != "" {
		progress.seen[state.RelativePathHash] = struct{}{}
	}
	if countSeen {
		progress.run.FilesSeen++
	}
	if state.Status == FileStatusEligible {
		if unchanged {
			progress.run.FilesUnchanged++
		} else {
			progress.run.FilesIngested++
			progress.run.ChunksStored += chunkCount
			progress.run.SymbolsStored += symbolCount
		}
	} else {
		progress.run.FilesSkipped++
	}
	recordRunReason(&progress.run, state.SkippedReason)
	now := time.Now().UTC()
	progress.run.HeartbeatAt = now
	progress.run.LastProgressAt = now
	progress.run.CurrentPhase = "processing"
	progress.changesSinceFlush++
}

func (progress *fullScanProgress) flush(ctx context.Context, svc *Service, project projectregistry.Project, force bool) error {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if !force && progress.changesSinceFlush < fullScanProgressFlushFiles {
		return nil
	}
	progress.changesSinceFlush = 0
	return svc.persistRun(ctx, project, progress.run)
}

func (svc *Service) IngestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	return svc.withGraphBatch(ctx, func(svc *Service) (Run, error) {
		return svc.ingestPath(ctx, projectID, relativePath, trigger)
	})
}

func (svc *Service) ingestPath(ctx context.Context, projectID string, relativePath string, trigger Trigger) (Run, error) {
	trigger = normalizeTrigger(trigger)
	project, err := svc.projectForIngestion(projectID, trigger)
	if err != nil {
		return Run{}, err
	}
	relative, ok := normalizeProjectRelativePath(relativePath)
	if !ok {
		return Run{}, ErrPathNotProjectLocal
	}
	fullPath := filepath.Join(project.CanonicalRootPath, filepath.FromSlash(relative))
	checkedRelative, ok := safeRelativePath(project.CanonicalRootPath, fullPath)
	if !ok || checkedRelative != relative {
		return Run{}, ErrPathEscapesRoot
	}

	run := svc.startRun(project, trigger)
	if err := svc.persistRun(ctx, project, run); err != nil {
		return run, err
	}
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		state := FileState{
			ProjectID:        project.ID,
			RelativePathHash: hashValue(relative),
			RelativePath:     relative,
			RelativePathSafe: true,
			Status:           FileStatusAbsent,
			Present:          false,
			LastEventAt:      run.StartedAt,
			LastIngestedAt:   run.StartedAt,
		}
		if err := svc.state.SaveFileState(ctx, state); err != nil {
			return run, err
		}
		if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
			return run, err
		}
		if err := svc.graph.PutFileState(ctx, project, run, state); err != nil {
			return run, err
		}
		if search, ok := svc.state.(searchMutationStore); ok {
			if err := search.DeleteSearchFile(ctx, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash)); err != nil {
				_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_delete_failed")
				return run, err
			}
		}
		run.Status = RunStatusCompleted
		run.FinishedAt = svc.now().UTC()
		run.HeartbeatAt = run.FinishedAt
		run.LastProgressAt = run.FinishedAt
		run.CurrentPhase = "completed"
		return run, svc.persistRun(ctx, project, run)
	}
	if err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "stat_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, fmt.Errorf("stat failed for relative path %q", relative)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() || !projectregistry.ProjectIncludesRelativePath(project, relative) {
		reason := SkipReasonUnsafePath
		if !projectregistry.ProjectIncludesRelativePath(project, relative) {
			reason = SkipReasonDeniedPath
		}
		state := svc.skippedState(project, relative, reason, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		run.FilesSkipped = 1
		recordRunReason(&run, state.SkippedReason)
		if err := svc.saveSkipped(ctx, project, run, state, false); err != nil {
			return run, err
		}
		run.Status = RunStatusCompleted
		run.FinishedAt = svc.now().UTC()
		run.HeartbeatAt = run.FinishedAt
		run.LastProgressAt = run.FinishedAt
		run.CurrentPhase = "completed"
		return run, svc.persistRun(ctx, project, run)
	}
	result := svc.prepareExistingFile(ctx, project, relative, fullPath, info, run)
	if result.err != nil {
		run.Status = RunStatusFailed
		run.ErrorCategory = "ingest_failed"
		run.FinishedAt = svc.now().UTC()
		_ = svc.persistRun(ctx, project, run)
		return run, result.err
	}
	if result.unchanged {
		if err := svc.saveUnchangedPreparedFile(ctx, project, run, result); err != nil {
			return run, err
		}
	} else if result.state.Status == FileStatusEligible {
		if err := svc.saveEligiblePreparedFile(ctx, project, run, result); err != nil {
			return run, err
		}
	} else if err := svc.saveSkipped(ctx, project, run, result.state, false); err != nil {
		return run, err
	}
	if result.state.Status == FileStatusEligible {
		run.FilesSeen = 1
		if result.unchanged {
			run.FilesUnchanged = 1
		} else {
			run.FilesIngested = 1
			run.ChunksStored = len(result.chunks)
			run.SymbolsStored = len(result.symbols)
		}
	} else {
		run.FilesSeen = 1
		run.FilesSkipped = 1
		recordRunReason(&run, result.state.SkippedReason)
	}
	run.Status = RunStatusCompleted
	run.FinishedAt = svc.now().UTC()
	run.HeartbeatAt = run.FinishedAt
	run.LastProgressAt = run.FinishedAt
	run.CurrentPhase = "completed"
	return run, svc.persistRun(ctx, project, run)
}

func (svc *Service) GetRun(ctx context.Context, projectID string, runID string) (Run, error) {
	return svc.state.GetRun(ctx, strings.TrimSpace(projectID), strings.TrimSpace(runID))
}

func (svc *Service) RunMetadata(ctx context.Context, projectID string, runID string) (RunMetadata, error) {
	run, err := svc.GetRun(ctx, projectID, runID)
	if err != nil {
		return RunMetadata{}, err
	}
	return MetadataForRun(run), nil
}

func (svc *Service) LatestRunMetadata(ctx context.Context, projectID string) (RunMetadata, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return RunMetadata{}, err
	}
	runs, err := svc.state.ListLatestRuns(ctx, project.ID, 25)
	if err != nil {
		return RunMetadata{}, err
	}
	if len(runs) == 0 {
		return RunMetadata{}, ErrRunNotFound
	}
	for _, run := range runs {
		if isZeroDeltaHeartbeat(run) {
			continue
		}
		return MetadataForRun(run), nil
	}
	return MetadataForRun(runs[0]), nil
}

func isZeroDeltaHeartbeat(run Run) bool {
	return run.RunKind == RunKindDelta &&
		run.Status == RunStatusCompleted &&
		run.FilesSeen == 0 &&
		run.FilesIngested == 0 &&
		run.FilesSkipped == 0 &&
		run.FilesUnchanged == 0 &&
		run.ChunksStored == 0 &&
		run.SymbolsStored == 0
}

func (svc *Service) ListFileStates(ctx context.Context, projectID string, filter FileStateFilter) ([]FileState, error) {
	return svc.state.ListFileStates(ctx, strings.TrimSpace(projectID), filter)
}

func (svc *Service) ListFiles(ctx context.Context, projectID string, filter FileStateFilter, pagination Pagination) (FileList, error) {
	if filter.Extension != "" {
		normalized, err := NormalizeFileExtension(filter.Extension)
		if err != nil {
			return FileList{}, err
		}
		filter.Extension = normalized
	}
	if filter.PathPrefix != "" {
		normalized, err := NormalizePathPrefix(filter.PathPrefix)
		if err != nil {
			return FileList{}, err
		}
		filter.PathPrefix = normalized
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileList{}, err
	}
	states, nextToken, err := svc.state.ListFileStatesPage(ctx, project.ID, filter, pagination)
	if err != nil {
		return FileList{}, err
	}
	files := make([]FileMetadata, 0, len(states))
	for _, state := range states {
		files = append(files, MetadataForFileState(project, state))
	}
	return FileList{Files: files, NextPageToken: nextToken}, nil
}

func NormalizePathPrefix(raw string) (string, error) {
	prefix := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if prefix == "" {
		return "", nil
	}
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "\x00") {
		return "", ErrInvalidInput
	}
	cleaned := path.Clean(prefix)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidInput
	}
	if strings.HasSuffix(prefix, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned, nil
}

func NormalizeFileExtension(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.ContainsAny(raw, `/\`) {
		return "", ErrInvalidInput
	}
	if strings.ContainsFunc(raw, unicode.IsSpace) {
		return "", ErrInvalidInput
	}
	extension := strings.ToLower(raw)
	if !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	if extension == "." {
		return "", ErrInvalidInput
	}
	for _, value := range extension[1:] {
		if !isExtensionChar(value) {
			return "", ErrInvalidInput
		}
	}
	return extension, nil
}

func isExtensionChar(value rune) bool {
	return utf8.RuneLen(value) == 1 && (value >= 'a' && value <= 'z' || value >= '0' && value <= '9')
}

func (svc *Service) GetFile(ctx context.Context, projectID string, fileID string) (FileMetadata, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileMetadata{}, err
	}
	if !validOpaqueID(fileID) {
		return FileMetadata{}, ErrInvalidInput
	}
	prefix := project.GraphNamespace + ":"
	if !strings.HasPrefix(fileID, prefix) {
		return FileMetadata{}, ErrIngestionNotFound
	}
	state, err := svc.state.GetFileStateByHash(ctx, project.ID, strings.TrimPrefix(fileID, prefix))
	if err != nil {
		return FileMetadata{}, err
	}
	return MetadataForFileState(project, state), nil
}

func (svc *Service) ListChunks(ctx context.Context, projectID string, fileID string, pagination Pagination, maxChunkBytes int) (ChunkList, error) {
	if maxChunkBytes < 0 {
		return ChunkList{}, ErrInvalidInput
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return ChunkList{}, err
	}
	return svc.graph.ListChunks(ctx, project, fileID, pagination, effectiveMaxChunkBytes(project, maxChunkBytes))
}

func (svc *Service) GetChunk(ctx context.Context, projectID string, fileID string, chunkID string, maxChunkBytes int) (ChunkMetadata, error) {
	if maxChunkBytes < 0 {
		return ChunkMetadata{}, ErrInvalidInput
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return ChunkMetadata{}, err
	}
	return svc.graph.GetChunk(ctx, project, fileID, chunkID, effectiveMaxChunkBytes(project, maxChunkBytes))
}

func (svc *Service) ListSymbols(ctx context.Context, projectID string, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	normalized, err := NormalizeSymbolFilter(filter)
	if err != nil {
		return SymbolList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolList{}, err
	}
	return svc.graph.ListSymbols(ctx, project, normalized, pagination)
}

func (svc *Service) SearchText(ctx context.Context, projectID string, options TextSearchOptions) (TextSearchResultList, error) {
	normalized, err := NormalizeTextSearchOptions(options)
	if err != nil {
		return TextSearchResultList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return TextSearchResultList{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	results, err := svc.searchBackendForIndex(index).SearchText(ctx, project, normalized)
	if err != nil {
		return TextSearchResultList{}, err
	}
	results.Index = &index
	return results, nil
}

func (svc *Service) SearchFiles(ctx context.Context, projectID string, options FileSearchOptions) (FileList, error) {
	normalized, err := NormalizeFileSearchOptions(options)
	if err != nil {
		return FileList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileList{}, err
	}
	if search, ok := svc.state.(searchQueryStore); ok {
		files, err := search.SearchFiles(ctx, project, normalized)
		if err != nil {
			return FileList{}, err
		}
		index := svc.searchIndexMetadata(ctx, project)
		files.Index = &index
		return files, nil
	}
	present := true
	states, err := svc.state.ListFileStates(ctx, project.ID, FileStateFilter{
		Status:     FileStatusEligible,
		Extension:  normalized.Extension,
		PathPrefix: normalized.PathPrefix,
		Present:    &present,
	})
	if err != nil {
		return FileList{}, err
	}
	files := make([]FileMetadata, 0, len(states))
	for _, state := range states {
		if !state.RelativePathSafe {
			continue
		}
		if normalized.PathContains != "" && !containsWithCaseOption(state.RelativePath, normalized.PathContains, normalized.CaseSensitive) {
			continue
		}
		files = append(files, MetadataForFileState(project, state))
	}
	sortFileMetadata(files)
	window, nextToken, err := paginate(files, Pagination{PageSize: normalized.PageSize, PageToken: normalized.PageToken})
	if err != nil {
		return FileList{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	return FileList{Files: window, NextPageToken: nextToken, Index: &index}, nil
}

func (svc *Service) SearchSymbols(ctx context.Context, projectID string, filter SymbolFilter, pagination Pagination) (SymbolList, error) {
	normalized, err := NormalizeSymbolFilter(filter)
	if err != nil {
		return SymbolList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolList{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	symbols, err := svc.searchBackendForIndex(index).SearchSymbols(ctx, project, normalized, pagination)
	if err != nil {
		return SymbolList{}, err
	}
	symbols.Index = &index
	return symbols, nil
}

func (svc *Service) SearchReferences(ctx context.Context, projectID string, options ReferenceSearchOptions) (SymbolReferenceList, error) {
	normalized, err := NormalizeReferenceSearchOptions(options)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	refs, err := svc.searchBackendForIndex(index).SearchReferences(ctx, project, normalized)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	refs.Index = &index
	return refs, nil
}

func (svc *Service) SearchCalls(ctx context.Context, projectID string, options ReferenceSearchOptions) (SymbolCallEdgeList, error) {
	normalized, err := NormalizeReferenceSearchOptions(options)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	calls, err := svc.searchBackendForIndex(index).SearchCalls(ctx, project, normalized)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	calls.Index = &index
	return calls, nil
}

func (svc *Service) SearchAST(ctx context.Context, projectID string, options ASTSearchOptions) (ASTSearchResultList, error) {
	normalized, err := NormalizeASTSearchOptions(options)
	if err != nil {
		return ASTSearchResultList{}, err
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return ASTSearchResultList{}, err
	}
	results, err := searchAST(ctx, svc, project, normalized)
	if err != nil {
		return ASTSearchResultList{}, err
	}
	entry, ok := astSearchCatalogEntry(normalized.Language, normalized.Query)
	if !ok {
		return ASTSearchResultList{}, ErrInvalidInput
	}
	coverage, err := astSearchCoverage(ctx, svc, project, entry, normalized)
	if err != nil {
		return ASTSearchResultList{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	results.Coverage = &coverage
	results.Index = &index
	return results, nil
}

func (svc *Service) ListASTQueries(ctx context.Context, projectID string) (ASTQueryCatalog, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return ASTQueryCatalog{}, err
	}
	coverage, err := astSearchCatalogCoverage(ctx, svc, project)
	if err != nil {
		return ASTQueryCatalog{}, err
	}
	index := svc.searchIndexMetadata(ctx, project)
	return ASTQueryCatalog{
		Queries:  astSearchCatalogMetadata(),
		Coverage: coverage,
		Index:    &index,
	}, nil
}

func (svc *Service) ListHeadings(ctx context.Context, projectID string, fileID string, pagination Pagination) (HeadingList, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return HeadingList{}, err
	}
	return svc.graph.ListHeadings(ctx, project, strings.TrimSpace(fileID), pagination)
}

func (svc *Service) GetFileOutline(ctx context.Context, projectID string, fileID string, options FileOutlineOptions) (FileOutline, error) {
	if options.MaxChunkBytes < 0 {
		return FileOutline{}, ErrInvalidInput
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return FileOutline{}, err
	}
	if !validOpaqueID(fileID) {
		return FileOutline{}, ErrInvalidInput
	}
	normalized, err := NormalizeSymbolFilter(options.SymbolFilter)
	if err != nil {
		return FileOutline{}, err
	}
	options.SymbolFilter = normalized
	prefix := project.GraphNamespace + ":"
	if !strings.HasPrefix(fileID, prefix) {
		return FileOutline{}, ErrIngestionNotFound
	}
	return svc.graph.GetFileOutline(ctx, project, strings.TrimSpace(fileID), options)
}

func NormalizeSymbolFilter(filter SymbolFilter) (SymbolFilter, error) {
	filter.NamePrefix = strings.TrimSpace(filter.NamePrefix)
	filter.NameContains = strings.TrimSpace(filter.NameContains)
	filter.FileID = strings.TrimSpace(filter.FileID)
	filter.Package = strings.TrimSpace(filter.Package)
	filter.Receiver = strings.TrimSpace(filter.Receiver)
	if filter.Extension != "" {
		extension, err := NormalizeFileExtension(filter.Extension)
		if err != nil {
			return SymbolFilter{}, err
		}
		filter.Extension = extension
	}
	if filter.Kind != "" && !validSymbolKind(filter.Kind) {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.NamePrefix != "" && strings.Contains(filter.NamePrefix, "\x00") {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.NameContains != "" && strings.Contains(filter.NameContains, "\x00") {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.FileID != "" && !validOpaqueID(filter.FileID) {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.Package != "" && strings.ContainsAny(filter.Package, "\x00/\\") {
		return SymbolFilter{}, ErrInvalidInput
	}
	if filter.Receiver != "" && strings.ContainsAny(filter.Receiver, "\x00/\\") {
		return SymbolFilter{}, ErrInvalidInput
	}
	return filter, nil
}

func NormalizeTextSearchOptions(options TextSearchOptions) (TextSearchOptions, error) {
	options.Query = strings.TrimSpace(options.Query)
	options.Mode = strings.TrimSpace(options.Mode)
	if options.Mode == "" {
		options.Mode = "literal"
	}
	if options.Mode != "literal" || options.Query == "" || strings.Contains(options.Query, "\x00") || len(options.Query) > MaxSearchQueryBytes {
		return TextSearchOptions{}, ErrInvalidInput
	}
	if options.Extension != "" {
		extension, err := NormalizeFileExtension(options.Extension)
		if err != nil {
			return TextSearchOptions{}, err
		}
		options.Extension = extension
	}
	if options.PathPrefix != "" {
		prefix, err := NormalizePathPrefix(options.PathPrefix)
		if err != nil {
			return TextSearchOptions{}, err
		}
		options.PathPrefix = prefix
	}
	if options.MaxSnippetBytes < 0 || options.MaxMatches < 0 {
		return TextSearchOptions{}, ErrInvalidInput
	}
	if options.MaxSnippetBytes == 0 {
		options.MaxSnippetBytes = DefaultMaxSnippetBytes
	}
	if options.MaxSnippetBytes > MaxSnippetBytes {
		options.MaxSnippetBytes = MaxSnippetBytes
	}
	if options.MaxMatches > MaxPageSize {
		options.MaxMatches = MaxPageSize
	}
	if _, _, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken}); err != nil {
		return TextSearchOptions{}, err
	}
	return options, nil
}

func NormalizeFileSearchOptions(options FileSearchOptions) (FileSearchOptions, error) {
	options.PathContains = strings.TrimSpace(strings.ReplaceAll(options.PathContains, "\\", "/"))
	if strings.Contains(options.PathContains, "\x00") || strings.HasPrefix(options.PathContains, "/") || strings.Contains(options.PathContains, "..") {
		return FileSearchOptions{}, ErrInvalidInput
	}
	if options.Extension != "" {
		extension, err := NormalizeFileExtension(options.Extension)
		if err != nil {
			return FileSearchOptions{}, err
		}
		options.Extension = extension
	}
	if options.PathPrefix != "" {
		prefix, err := NormalizePathPrefix(options.PathPrefix)
		if err != nil {
			return FileSearchOptions{}, err
		}
		options.PathPrefix = prefix
	}
	if _, _, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken}); err != nil {
		return FileSearchOptions{}, err
	}
	return options, nil
}

func NormalizeReferenceSearchOptions(options ReferenceSearchOptions) (ReferenceSearchOptions, error) {
	options.NameContains = strings.TrimSpace(options.NameContains)
	options.TargetNameContains = strings.TrimSpace(options.TargetNameContains)
	options.CallerNameContains = strings.TrimSpace(options.CallerNameContains)
	options.CalleeNameContains = strings.TrimSpace(options.CalleeNameContains)
	options.EnclosingContains = strings.TrimSpace(options.EnclosingContains)
	options.ResolutionStatus = strings.TrimSpace(options.ResolutionStatus)
	options.Confidence = strings.TrimSpace(options.Confidence)
	for _, value := range []string{
		options.NameContains,
		options.TargetNameContains,
		options.CallerNameContains,
		options.CalleeNameContains,
		options.EnclosingContains,
		options.ResolutionStatus,
		options.Confidence,
	} {
		if strings.Contains(value, "\x00") {
			return ReferenceSearchOptions{}, ErrInvalidInput
		}
	}
	if options.Extension != "" {
		extension, err := NormalizeFileExtension(options.Extension)
		if err != nil {
			return ReferenceSearchOptions{}, err
		}
		options.Extension = extension
	}
	if options.PathPrefix != "" {
		prefix, err := NormalizePathPrefix(options.PathPrefix)
		if err != nil {
			return ReferenceSearchOptions{}, err
		}
		options.PathPrefix = prefix
	}
	if _, _, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken}); err != nil {
		return ReferenceSearchOptions{}, err
	}
	return options, nil
}

func NormalizeASTSearchOptions(options ASTSearchOptions) (ASTSearchOptions, error) {
	options.Language = strings.ToLower(strings.TrimSpace(options.Language))
	options.Query = strings.TrimSpace(options.Query)
	options.PathPrefix = strings.TrimSpace(options.PathPrefix)
	if options.Language == "" || options.Query == "" || strings.Contains(options.Query, "\x00") || len(options.Query) > MaxASTQueryBytes {
		return ASTSearchOptions{}, ErrInvalidInput
	}
	if !astSearchLanguageSupported(options.Language) {
		return ASTSearchOptions{}, ErrInvalidInput
	}
	if !validASTQueryID(options.Query) {
		return ASTSearchOptions{}, ErrInvalidInput
	}
	if options.Extension != "" {
		extension, err := NormalizeFileExtension(options.Extension)
		if err != nil {
			return ASTSearchOptions{}, err
		}
		options.Extension = extension
	}
	if options.PathPrefix != "" {
		prefix, err := NormalizePathPrefix(options.PathPrefix)
		if err != nil {
			return ASTSearchOptions{}, err
		}
		options.PathPrefix = prefix
	}
	if len(options.Captures) > 16 {
		return ASTSearchOptions{}, ErrInvalidInput
	}
	for i, capture := range options.Captures {
		capture = strings.TrimSpace(capture)
		if capture == "" || strings.ContainsAny(capture, "\x00/\\") || len(capture) > MaxASTQueryBytes || !validASTCaptureName(capture) {
			return ASTSearchOptions{}, ErrInvalidInput
		}
		options.Captures[i] = capture
	}
	if options.MaxSnippetBytes < 0 || options.MaxMatches < 0 {
		return ASTSearchOptions{}, ErrInvalidInput
	}
	if options.MaxSnippetBytes == 0 {
		options.MaxSnippetBytes = DefaultMaxSnippetBytes
	}
	if options.MaxSnippetBytes > MaxSnippetBytes {
		options.MaxSnippetBytes = MaxSnippetBytes
	}
	if options.MaxMatches == 0 || options.MaxMatches > MaxPageSize {
		options.MaxMatches = MaxPageSize
	}
	if _, _, err := paginationWindow(Pagination{PageSize: options.PageSize, PageToken: options.PageToken}); err != nil {
		return ASTSearchOptions{}, err
	}
	return options, nil
}

func (svc *Service) searchIndexMetadata(ctx context.Context, project projectregistry.Project) SearchIndexMetadata {
	runs, err := svc.state.ListLatestRuns(ctx, project.ID, 1)
	metadata := SearchIndexMetadata{IndexStatus: "unknown"}
	if err != nil || len(runs) == 0 {
		return svc.withSearchIndexHealth(ctx, project, metadata)
	}
	metadata = SearchIndexMetadata{IndexStatus: string(runs[0].Status), IngestionRunID: runs[0].ID}
	return svc.withSearchIndexHealth(ctx, project, metadata)
}

func (svc *Service) withSearchIndexHealth(ctx context.Context, project projectregistry.Project, metadata SearchIndexMetadata) SearchIndexMetadata {
	search, ok := svc.state.(searchQueryStore)
	if !ok {
		return metadata
	}
	health, err := search.SearchIndexHealth(ctx, project)
	if err != nil || !health.Degraded {
		return metadata
	}
	metadata.Degraded = true
	metadata.DegradedReason = health.Reason
	return metadata
}

func (svc *Service) searchBackend() searchQueryStore {
	if search, ok := svc.state.(searchQueryStore); ok {
		return search
	}
	return graphSearchAdapter{graph: svc.graph}
}

func (svc *Service) searchBackendForIndex(index SearchIndexMetadata) searchQueryStore {
	if index.Degraded && index.DegradedReason == "search_index_drift" && svc.graph != nil {
		return graphSearchAdapter{graph: svc.graph}
	}
	return svc.searchBackend()
}

func containsWithCaseOption(value string, query string, caseSensitive bool) bool {
	if query == "" {
		return true
	}
	if caseSensitive {
		return strings.Contains(value, query)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func sortFileMetadata(files []FileMetadata) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].RelativePath == files[j].RelativePath {
			return files[i].ID < files[j].ID
		}
		return files[i].RelativePath < files[j].RelativePath
	})
}

func validSymbolKind(kind SymbolKind) bool {
	switch kind {
	case SymbolKindPackage, SymbolKindImport, SymbolKindFunction, SymbolKindMethod, SymbolKindType,
		SymbolKindClass, SymbolKindExport, SymbolKindStage, SymbolKindTarget, SymbolKindPath,
		SymbolKindKey, SymbolKindMigration:
		return true
	default:
		return false
	}
}

func (svc *Service) GetSymbol(ctx context.Context, projectID string, symbolID string) (SymbolMetadata, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolMetadata{}, err
	}
	if !validOpaqueID(symbolID) || !strings.HasPrefix(strings.TrimSpace(symbolID), project.GraphNamespace+":") {
		return SymbolMetadata{}, ErrIngestionNotFound
	}
	return svc.graph.GetSymbol(ctx, project, symbolID)
}

func (svc *Service) GetSymbolSource(ctx context.Context, projectID string, symbolID string, options SymbolSourceOptions) (SymbolSource, error) {
	if options.MaxSourceBytes < 0 {
		return SymbolSource{}, ErrInvalidInput
	}
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolSource{}, err
	}
	if !validOpaqueID(symbolID) || !strings.HasPrefix(strings.TrimSpace(symbolID), project.GraphNamespace+":") {
		return SymbolSource{}, ErrIngestionNotFound
	}
	return svc.graph.GetSymbolSource(ctx, project, strings.TrimSpace(symbolID), effectiveMaxSourceBytes(project, options.MaxSourceBytes))
}

func (svc *Service) ListSymbolReferences(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolReferenceList, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolReferenceList{}, err
	}
	if !validOpaqueID(symbolID) || !strings.HasPrefix(strings.TrimSpace(symbolID), project.GraphNamespace+":") {
		return SymbolReferenceList{}, ErrIngestionNotFound
	}
	return svc.graph.ListSymbolReferences(ctx, project, strings.TrimSpace(symbolID), pagination)
}

func (svc *Service) ListSymbolCallers(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolCallEdgeList, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	if !validOpaqueID(symbolID) || !strings.HasPrefix(strings.TrimSpace(symbolID), project.GraphNamespace+":") {
		return SymbolCallEdgeList{}, ErrIngestionNotFound
	}
	return svc.graph.ListSymbolCallers(ctx, project, strings.TrimSpace(symbolID), pagination)
}

func (svc *Service) ListSymbolCallees(ctx context.Context, projectID string, symbolID string, pagination Pagination) (SymbolCallEdgeList, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolCallEdgeList{}, err
	}
	if !validOpaqueID(symbolID) || !strings.HasPrefix(strings.TrimSpace(symbolID), project.GraphNamespace+":") {
		return SymbolCallEdgeList{}, ErrIngestionNotFound
	}
	return svc.graph.ListSymbolCallees(ctx, project, strings.TrimSpace(symbolID), pagination)
}

func (svc *Service) GetSymbolCallGraph(ctx context.Context, projectID string, symbolID string, options CallGraphOptions) (SymbolCallGraph, error) {
	project, err := svc.projectForQuery(projectID)
	if err != nil {
		return SymbolCallGraph{}, err
	}
	if !validOpaqueID(symbolID) || !strings.HasPrefix(strings.TrimSpace(symbolID), project.GraphNamespace+":") {
		return SymbolCallGraph{}, ErrIngestionNotFound
	}
	normalized, err := normalizeCallGraphOptions(options)
	if err != nil {
		return SymbolCallGraph{}, err
	}
	return svc.graph.GetSymbolCallGraph(ctx, project, strings.TrimSpace(symbolID), normalized)
}

func normalizeCallGraphOptions(options CallGraphOptions) (CallGraphOptions, error) {
	options.Direction = strings.TrimSpace(options.Direction)
	if options.Direction == "" {
		options.Direction = "callees"
	}
	switch options.Direction {
	case "callers", "callees", "both":
	default:
		return CallGraphOptions{}, ErrInvalidInput
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = 1
	}
	if options.MaxNodes == 0 {
		options.MaxNodes = 25
	}
	if options.MaxDepth < 1 || options.MaxDepth > MaxCallGraphDepth || options.MaxNodes < 1 || options.MaxNodes > MaxCallGraphNodes {
		return CallGraphOptions{}, ErrInvalidInput
	}
	return options, nil
}

func (svc *Service) projectForIngestion(projectID string, trigger Trigger) (projectregistry.Project, error) {
	if svc == nil || svc.registry == nil || svc.graph == nil || svc.state == nil {
		return projectregistry.Project{}, fmt.Errorf("%w: service dependencies are required", ErrUnsupportedIngest)
	}
	project, ok := svc.registry.Get(strings.TrimSpace(projectID))
	if !ok {
		return projectregistry.Project{}, ErrProjectNotFound
	}
	if !project.Enabled {
		return projectregistry.Project{}, ErrProjectDisabled
	}
	if project.DigestMode != projectregistry.DigestModeContentGraph {
		return projectregistry.Project{}, fmt.Errorf("%w: digest_mode must be %q", ErrUnsupportedIngest, projectregistry.DigestModeContentGraph)
	}
	switch trigger {
	case TriggerManual:
	case TriggerLive:
		if project.UpdatePolicy != projectregistry.UpdatePolicyLive {
			return projectregistry.Project{}, fmt.Errorf("%w: update_policy must be %q for live ingestion", ErrUnsupportedIngest, projectregistry.UpdatePolicyLive)
		}
	default:
		return projectregistry.Project{}, ErrInvalidInput
	}
	root := project.CanonicalRootPath
	if root == "" {
		root = project.RootPath
	}
	cleanRoot, canonicalRoot, err := validateCanonicalRoot(root)
	if err != nil {
		return projectregistry.Project{}, fmt.Errorf("%w: invalid root path", ErrUnsupportedIngest)
	}
	if project.CanonicalRootPath != "" && project.CanonicalRootPath != canonicalRoot {
		return projectregistry.Project{}, fmt.Errorf("%w: canonical root path mismatch", ErrUnsupportedIngest)
	}
	project.RootPath = cleanRoot
	project.CanonicalRootPath = canonicalRoot
	return project, nil
}

func (svc *Service) projectForQuery(projectID string) (projectregistry.Project, error) {
	if svc == nil || svc.registry == nil || svc.graph == nil || svc.state == nil {
		return projectregistry.Project{}, fmt.Errorf("%w: service dependencies are required", ErrUnsupportedIngest)
	}
	project, ok := svc.registry.Get(strings.TrimSpace(projectID))
	if !ok {
		return projectregistry.Project{}, ErrProjectNotFound
	}
	if !project.Enabled {
		return projectregistry.Project{}, ErrProjectDisabled
	}
	if project.DigestMode != projectregistry.DigestModeContentGraph {
		return projectregistry.Project{}, fmt.Errorf("%w: digest_mode must be %q", ErrUnsupportedIngest, projectregistry.DigestModeContentGraph)
	}
	return project, nil
}

func (svc *Service) startRun(project projectregistry.Project, trigger Trigger) Run {
	trigger = normalizeTrigger(trigger)
	startedAt := svc.now().UTC()
	return Run{
		ID:             svc.newID(project, startedAt),
		ProjectID:      project.ID,
		Trigger:        trigger,
		RunKind:        RunKindFullScan,
		Mode:           project.DigestMode,
		Status:         RunStatusRunning,
		StartedAt:      startedAt,
		HeartbeatAt:    startedAt,
		LastProgressAt: startedAt,
		CurrentPhase:   "starting",
	}
}

func normalizeTrigger(trigger Trigger) Trigger {
	if trigger == "" {
		return TriggerManual
	}
	return trigger
}

func (svc *Service) ingestExistingFile(ctx context.Context, project projectregistry.Project, relative string, fullPath string, info fs.FileInfo, run Run) (FileState, []Chunk, []Symbol, []Heading, error) {
	result := svc.prepareExistingFile(ctx, project, relative, fullPath, info, run)
	if result.err != nil {
		return result.state, nil, nil, nil, result.err
	}
	if result.unchanged {
		return result.state, nil, nil, nil, svc.saveUnchangedPreparedFile(ctx, project, run, result)
	}
	if result.state.Status == FileStatusEligible {
		if err := svc.saveEligiblePreparedFile(ctx, project, run, result); err != nil {
			return FileState{}, nil, nil, nil, err
		}
		return result.state, result.chunks, result.symbols, result.headings, nil
	}
	return result.state, nil, nil, nil, svc.saveSkipped(ctx, project, run, result.state, false)
}

func (svc *Service) prepareExistingFile(ctx context.Context, project projectregistry.Project, relative string, fullPath string, info fs.FileInfo, run Run) fullScanFileResult {
	startedAt := time.Now()
	defer func() { svc.recordStage("prepare", startedAt, nil) }()
	options := SafetyOptions{
		MaxFileBytes:          project.MaxFileBytes,
		MaxChunkBytes:         project.MaxChunkBytes,
		SensitiveMarkerPolicy: project.SensitiveMarkerPolicy,
	}
	if options.MaxFileBytes > 0 && info.Size() > options.MaxFileBytes {
		state := svc.skippedState(project, relative, SkipReasonFileTooLarge, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		return fullScanFileResult{state: state}
	}
	extractor := svc.extractors.ExtractorFor(relative)
	unchangedStartedAt := time.Now()
	unchangedState, unchanged, err := svc.metadataUnchangedFile(ctx, project, extractor, relative, info, run)
	svc.recordStage("fast_unchanged_check", unchangedStartedAt, err)
	if err != nil {
		return fullScanFileResult{err: err}
	}
	if unchanged {
		return fullScanFileResult{state: unchangedState, unchanged: true}
	}
	readStartedAt := time.Now()
	content, err := os.ReadFile(fullPath)
	svc.recordStage("read", readStartedAt, err)
	if err != nil {
		state := svc.skippedState(project, relative, SkipReasonReadError, info.Size(), info.ModTime().UTC(), true, run.StartedAt)
		return fullScanFileResult{state: state}
	}
	chunkStartedAt := time.Now()
	chunkSet, safety, err := BuildChunks(relative, content, options)
	svc.recordStage("chunk", chunkStartedAt, err)
	if err != nil {
		state := svc.skippedState(project, relative, SkipReasonChunkError, int64(len(content)), info.ModTime().UTC(), true, run.StartedAt)
		return fullScanFileResult{state: state}
	}
	if !safety.Eligible {
		state := fileStateFromSafety(project, relative, safety, "", info.ModTime().UTC(), run.StartedAt)
		return fullScanFileResult{state: state}
	}

	state := fileStateFromSafety(project, relative, safety, chunkSet.ContentSHA256, info.ModTime().UTC(), run.StartedAt)
	contentUnchangedStartedAt := time.Now()
	unchanged, err = svc.fileVersionUnchanged(ctx, project, extractor, state)
	svc.recordStage("fast_unchanged_check", contentUnchangedStartedAt, err)
	if err != nil {
		return fullScanFileResult{state: state, err: err}
	} else if unchanged {
		return fullScanFileResult{state: state, unchanged: true}
	}

	extractStartedAt := time.Now()
	result, err := svc.extractEligible(ctx, project, relative, hashValue(relative), chunkSet.ContentSHA256, extractor, content, run.StartedAt)
	svc.recordStage("extract", extractStartedAt, err)
	if err != nil {
		state := svc.skippedState(project, relative, SkipReasonParseError, int64(len(content)), info.ModTime().UTC(), true, run.StartedAt)
		return fullScanFileResult{state: state}
	}
	return fullScanFileResult{
		state:       state,
		chunks:      chunkSet.Chunks,
		symbols:     result.Symbols,
		references:  result.References,
		calls:       result.Calls,
		headings:    result.Headings,
		chunkCount:  len(chunkSet.Chunks),
		symbolCount: len(result.Symbols),
	}
}

func (svc *Service) metadataUnchangedFile(ctx context.Context, project projectregistry.Project, extractor Extractor, relative string, info fs.FileInfo, run Run) (FileState, bool, error) {
	previous, err := svc.state.GetFileStateByHash(ctx, project.ID, hashValue(relative))
	if errors.Is(err, ErrIngestionNotFound) {
		return FileState{}, false, nil
	}
	if err != nil {
		return FileState{}, false, err
	}
	if previous.Status != FileStatusEligible || !previous.Present || !previous.RelativePathSafe || previous.RelativePath != relative || previous.ContentSHA256 == "" {
		return FileState{}, false, nil
	}
	if previous.SizeBytes != info.Size() || !previous.ModifiedAt.Equal(info.ModTime().UTC()) {
		return FileState{}, false, nil
	}
	state := previous
	state.LastEventAt = run.StartedAt
	state.LastIngestedAt = run.StartedAt
	if extractor != nil && svc.extractorCacheEnabled {
		if _, err := svc.state.GetExtractorCache(ctx, project.ID, state.RelativePathHash, state.ContentSHA256, extractor.Name(), extractor.Version()); err != nil {
			if errors.Is(err, ErrExtractorCacheMiss) {
				return FileState{}, false, nil
			}
			return FileState{}, false, err
		}
	} else if extractor != nil {
		return FileState{}, false, nil
	}
	graphOK, err := svc.graph.HasFileVersion(ctx, project, state)
	if err != nil || !graphOK {
		return FileState{}, false, err
	}
	if search, ok := svc.state.(searchMutationStore); ok {
		searchOK, err := search.HasSearchFileVersion(ctx, project, state)
		if err != nil || !searchOK {
			return FileState{}, false, err
		}
	}
	return state, true, nil
}

func (svc *Service) fileVersionUnchanged(ctx context.Context, project projectregistry.Project, extractor Extractor, state FileState) (bool, error) {
	if state.Status != FileStatusEligible || !state.Present || !state.RelativePathSafe || state.ContentSHA256 == "" {
		return false, nil
	}
	previous, err := svc.state.GetFileStateByHash(ctx, project.ID, state.RelativePathHash)
	if errors.Is(err, ErrIngestionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if previous.Status != FileStatusEligible || !previous.Present || !previous.RelativePathSafe || previous.ContentSHA256 != state.ContentSHA256 {
		return false, nil
	}
	if extractor != nil && svc.extractorCacheEnabled {
		if _, err := svc.state.GetExtractorCache(ctx, project.ID, state.RelativePathHash, state.ContentSHA256, extractor.Name(), extractor.Version()); err != nil {
			if errors.Is(err, ErrExtractorCacheMiss) {
				return false, nil
			}
			return false, err
		}
	} else if extractor != nil {
		return false, nil
	}
	graphOK, err := svc.graph.HasFileVersion(ctx, project, state)
	if err != nil || !graphOK {
		return false, err
	}
	if search, ok := svc.state.(searchMutationStore); ok {
		searchOK, err := search.HasSearchFileVersion(ctx, project, state)
		if err != nil || !searchOK {
			return false, err
		}
	}
	return true, nil
}

func (svc *Service) saveUnchangedPreparedFile(ctx context.Context, project projectregistry.Project, run Run, result fullScanFileResult) error {
	stateStartedAt := time.Now()
	if err := svc.state.SaveFileState(ctx, result.state); err != nil {
		svc.recordStage("storage.state_write", stateStartedAt, err)
		return err
	}
	svc.recordStage("storage.state_write", stateStartedAt, nil)
	graphStartedAt := time.Now()
	if err := svc.graph.PutUnchangedFile(ctx, project, run, result.state); err != nil {
		svc.recordStage("storage.graph_write", graphStartedAt, err)
		return err
	}
	svc.recordStage("storage.graph_write", graphStartedAt, nil)
	if search, ok := svc.state.(searchMutationStore); ok {
		searchStartedAt := time.Now()
		if err := search.UpdateSearchFileMetadata(ctx, project, result.state); err != nil {
			_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_write_failed")
			svc.recordStage("storage.search_write", searchStartedAt, err)
			return err
		}
		svc.recordStage("storage.search_write", searchStartedAt, nil)
	}
	return nil
}

func (svc *Service) saveEligiblePreparedFile(ctx context.Context, project projectregistry.Project, run Run, result fullScanFileResult) error {
	stateStartedAt := time.Now()
	if err := svc.state.SaveFileState(ctx, result.state); err != nil {
		svc.recordStage("storage.state_write", stateStartedAt, err)
		return err
	}
	svc.recordStage("storage.state_write", stateStartedAt, nil)
	graphStartedAt := time.Now()
	if err := svc.graph.PutEligibleFile(ctx, project, run, result.state, result.chunks, result.symbols, result.references, result.calls, result.headings); err != nil {
		svc.recordStage("storage.graph_write", graphStartedAt, err)
		return err
	}
	svc.recordStage("storage.graph_write", graphStartedAt, nil)
	if search, ok := svc.state.(searchMutationStore); ok {
		searchStartedAt := time.Now()
		if err := search.UpsertSearchFile(ctx, project, result.state, result.chunks, result.symbols, result.references, result.calls); err != nil {
			_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_write_failed")
			svc.recordStage("storage.search_write", searchStartedAt, err)
			return err
		}
		svc.recordStage("storage.search_write", searchStartedAt, nil)
	}
	return nil
}

func (svc *Service) saveFullScanPreparedBatch(ctx context.Context, project projectregistry.Project, run Run, results []fullScanFileResult) error {
	if len(results) == 0 {
		return nil
	}
	states := make([]FileState, 0, len(results))
	for _, result := range results {
		states = append(states, result.state)
	}
	stateStartedAt := time.Now()
	if batchStore, ok := svc.state.(stateBatchStore); ok {
		if err := batchStore.SaveFileStatesBatch(ctx, states); err != nil {
			svc.recordStage("storage.state_write", stateStartedAt, err)
			return err
		}
	} else {
		for _, state := range states {
			if err := svc.state.SaveFileState(ctx, state); err != nil {
				svc.recordStage("storage.state_write", stateStartedAt, err)
				return err
			}
		}
	}
	for _, state := range states {
		if state.Status != FileStatusEligible {
			if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
				svc.recordStage("storage.state_write", stateStartedAt, err)
				return err
			}
		}
	}
	svc.recordStage("storage.state_write", stateStartedAt, nil)

	graphStartedAt := time.Now()
	if err := svc.graph.PutPreparedFilesBatch(ctx, project, run, results); err != nil {
		svc.recordStage("storage.graph_write", graphStartedAt, err)
		return err
	}
	svc.recordStage("storage.graph_write", graphStartedAt, nil)

	if search, ok := svc.state.(searchBatchMutationStore); ok {
		searchStartedAt := time.Now()
		if err := search.ApplySearchFileBatch(ctx, project, results); err != nil {
			if marker, ok := svc.state.(searchMutationStore); ok {
				_ = marker.MarkSearchIndexDegraded(ctx, project.ID, "search_index_write_failed")
			}
			svc.recordStage("storage.search_write", searchStartedAt, err)
			return err
		}
		svc.recordStage("storage.search_write", searchStartedAt, nil)
	} else if search, ok := svc.state.(searchMutationStore); ok {
		searchStartedAt := time.Now()
		for _, result := range results {
			var err error
			switch {
			case result.unchanged:
				err = search.UpdateSearchFileMetadata(ctx, project, result.state)
			case result.state.Status == FileStatusEligible:
				err = search.UpsertSearchFile(ctx, project, result.state, result.chunks, result.symbols, result.references, result.calls)
			default:
				err = search.DeleteSearchFile(ctx, project.ID, repoFileID(project.GraphNamespace, result.state.RelativePathHash))
			}
			if err != nil {
				_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_write_failed")
				svc.recordStage("storage.search_write", searchStartedAt, err)
				return err
			}
		}
		svc.recordStage("storage.search_write", searchStartedAt, nil)
	}
	return nil
}

func (svc *Service) extractEligible(ctx context.Context, project projectregistry.Project, relative string, relativePathHash string, contentSHA256 string, extractor Extractor, content []byte, eventAt time.Time) (ExtractorResult, error) {
	if extractor == nil {
		return ExtractorResult{}, nil
	}
	if svc.extractorCacheEnabled && contentSHA256 != "" {
		entry, err := svc.state.GetExtractorCache(ctx, project.ID, relativePathHash, contentSHA256, extractor.Name(), extractor.Version())
		if err == nil {
			return ExtractorResult{
				ExtractorName:    entry.ExtractorName,
				ExtractorVersion: entry.ExtractorVersion,
				Symbols:          entry.Symbols,
				Headings:         entry.Headings,
				References:       entry.References,
				Calls:            entry.Calls,
			}, nil
		}
		if err != nil && !errors.Is(err, ErrExtractorCacheMiss) {
			return ExtractorResult{}, err
		}
	}
	result, err := extractor.Parse(ctx, relative, content)
	if err != nil {
		return ExtractorResult{}, err
	}
	result.ExtractorName = extractor.Name()
	result.ExtractorVersion = extractor.Version()
	if svc.extractorCacheEnabled && contentSHA256 != "" {
		if err := svc.state.SaveExtractorCache(ctx, ExtractorCacheEntry{
			ProjectID:        project.ID,
			RelativePathHash: relativePathHash,
			ContentSHA256:    contentSHA256,
			ExtractorName:    result.ExtractorName,
			ExtractorVersion: result.ExtractorVersion,
			Symbols:          result.Symbols,
			Headings:         result.Headings,
			References:       result.References,
			Calls:            result.Calls,
			CreatedAt:        eventAt,
			UpdatedAt:        eventAt,
		}); err != nil {
			return ExtractorResult{}, err
		}
	}
	return result, nil
}

func (svc *Service) saveSkipped(ctx context.Context, project projectregistry.Project, run Run, state FileState, skipDir bool) error {
	stateStartedAt := time.Now()
	if err := svc.state.SaveFileState(ctx, state); err != nil {
		svc.recordStage("storage.state_write", stateStartedAt, err)
		return err
	}
	if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
		svc.recordStage("storage.state_write", stateStartedAt, err)
		return err
	}
	svc.recordStage("storage.state_write", stateStartedAt, nil)
	graphStartedAt := time.Now()
	if err := svc.graph.PutSkippedFile(ctx, project, run, state); err != nil {
		svc.recordStage("storage.graph_write", graphStartedAt, err)
		return err
	}
	svc.recordStage("storage.graph_write", graphStartedAt, nil)
	if search, ok := svc.state.(searchMutationStore); ok {
		searchStartedAt := time.Now()
		if err := search.DeleteSearchFile(ctx, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash)); err != nil {
			_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_delete_failed")
			svc.recordStage("storage.search_write", searchStartedAt, err)
			return err
		}
		svc.recordStage("storage.search_write", searchStartedAt, nil)
	}
	if skipDir {
		return filepath.SkipDir
	}
	return nil
}

func (svc *Service) tombstoneMissingFiles(ctx context.Context, project projectregistry.Project, run Run, seen map[string]struct{}) error {
	present := true
	pageToken := ""
	pageSize := svc.fullScanBatchSize
	if pageSize <= 0 || pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	for {
		states, nextToken, err := svc.state.ListFileStatesPage(ctx, project.ID, FileStateFilter{Present: &present}, Pagination{
			PageSize:  pageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return err
		}
		for _, state := range states {
			if _, ok := seen[state.RelativePathHash]; ok {
				continue
			}
			state.Status = FileStatusAbsent
			state.Present = false
			state.ContentSHA256 = ""
			state.LastEventAt = run.StartedAt
			state.LastIngestedAt = run.StartedAt
			if err := svc.state.SaveFileState(ctx, state); err != nil {
				return err
			}
			if err := svc.state.DeleteExtractorCacheForFile(ctx, project.ID, state.RelativePathHash); err != nil {
				return err
			}
			if err := svc.graph.putFileState(ctx, project, run, state); err != nil {
				return err
			}
			if search, ok := svc.state.(searchMutationStore); ok {
				if err := search.DeleteSearchFile(ctx, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash)); err != nil {
					_ = search.MarkSearchIndexDegraded(ctx, project.ID, "search_index_delete_failed")
					return err
				}
			}
		}
		if nextToken == "" {
			break
		}
		pageToken = nextToken
	}
	return nil
}

func (svc *Service) persistRun(ctx context.Context, project projectregistry.Project, run Run) error {
	if err := svc.state.SaveRun(ctx, run); err != nil {
		return err
	}
	return svc.graph.PutRun(ctx, project, run)
}

func (svc *Service) skippedState(project projectregistry.Project, relative string, reason SkipReason, size int64, modifiedAt time.Time, present bool, eventAt time.Time) FileState {
	state := FileState{
		ProjectID:        project.ID,
		RelativePathHash: hashValue(relative),
		RelativePath:     relative,
		RelativePathSafe: true,
		Status:           FileStatusSkipped,
		Present:          present,
		SizeBytes:        size,
		ModifiedAt:       modifiedAt,
		LastEventAt:      eventAt,
		LastIngestedAt:   eventAt,
		SkippedReason:    reason,
	}
	if reason == SkipReasonDeniedPath || reason == SkipReasonSensitiveContent {
		state.RelativePath = ""
		state.RelativePathSafe = false
	}
	return state
}

func fileStateFromSafety(project projectregistry.Project, originalRelative string, safety SafetyResult, contentSHA256 string, modifiedAt time.Time, eventAt time.Time) FileState {
	relative := safety.RelativePath
	if relative == "" {
		relative = originalRelative
	}
	status := FileStatusSkipped
	if safety.Eligible {
		status = FileStatusEligible
	}
	state := FileState{
		ProjectID:        project.ID,
		RelativePathHash: hashValue(relative),
		RelativePath:     relative,
		RelativePathSafe: safety.RelativePathSafe,
		Status:           status,
		Present:          true,
		ContentSHA256:    contentSHA256,
		SizeBytes:        safety.SizeBytes,
		ModifiedAt:       modifiedAt,
		LastEventAt:      eventAt,
		LastIngestedAt:   eventAt,
		SkippedReason:    safety.Reason,
	}
	if safety.Reason == SkipReasonDeniedPath || safety.Reason == SkipReasonSensitiveContent {
		state.RelativePath = ""
		state.RelativePathSafe = false
	}
	if status != FileStatusEligible {
		state.ContentSHA256 = ""
	}
	return state
}

func parseEligible(relative string, content []byte) ([]Symbol, []Heading, error) {
	result, err := NewDefaultExtractorRegistry().Extract(context.Background(), relative, content)
	return result.Symbols, result.Headings, err
}

func recordRunReason(run *Run, reason SkipReason) {
	if run == nil || reason == SkipReasonNone {
		return
	}
	if run.ReasonCounts == nil {
		run.ReasonCounts = make(map[string]int)
	}
	run.ReasonCounts[string(reason)]++
	if isFileErrorReason(reason) {
		run.ErrorCategory = "file_errors"
	}
}

func isFileErrorReason(reason SkipReason) bool {
	switch reason {
	case SkipReasonStatError, SkipReasonReadError, SkipReasonChunkError, SkipReasonParseError:
		return true
	default:
		return false
	}
}

func validateCanonicalRoot(root string) (string, string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("root path must be absolute")
	}
	cleanRoot := filepath.Clean(root)
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("root path must be a directory")
	}
	canonicalRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", "", err
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	if canonicalRoot != cleanRoot {
		return "", "", fmt.Errorf("root path must not resolve through a symlink")
	}
	return cleanRoot, canonicalRoot, nil
}

func safeRelativePath(root string, filePath string) (string, bool) {
	relative, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", false
	}
	if relative == "." {
		return "", true
	}
	if relative == ".." || strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func normalizeProjectRelativePath(relative string) (string, bool) {
	relative = strings.TrimSpace(relative)
	if relative == "" || strings.ContainsRune(relative, '\x00') || strings.Contains(relative, "\\") {
		return "", false
	}
	if strings.HasPrefix(relative, "/") || strings.HasPrefix(relative, "//") || filepath.IsAbs(relative) {
		return "", false
	}
	cleaned := path.Clean(relative)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func defaultRunID(project projectregistry.Project, startedAt time.Time) string {
	return "ingest_" + shortHash(project.ID+"\x00"+project.GraphNamespace+"\x00"+startedAt.UTC().Format(time.RFC3339Nano))
}

func hashValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
