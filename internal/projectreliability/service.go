package projectreliability

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

const (
	defaultStaleAfter   = 24 * time.Hour
	defaultProbeTimeout = 2 * time.Second
)

var ErrRunNotFound = errors.New("context health run not found")

const (
	runStatusPending   = "pending"
	runStatusRunning   = "running"
	runStatusCompleted = "completed"
	runStatusFailed    = "failed"
)

type ProjectProvider interface {
	GetProject(ctx context.Context, projectID string) (projectregistry.Project, error)
}

type ContextProvider interface {
	LatestRun(ctx context.Context, projectID string) (RunSummary, error)
	ActiveRuns(ctx context.Context, projectID string) ([]RunSummary, error)
	EligibleFileCount(ctx context.Context, projectID string) (int, error)
	IndexedSymbolCount(ctx context.Context, projectID string) (int, error)
	IndexedChunkCount(ctx context.Context, projectID string) (int, error)
	SearchIndexHealth(ctx context.Context, projectID string) (SearchIndexHealth, error)
}

type activeSyncProvider interface {
	ActiveSync(projectID string) bool
}

type WorkspaceGitProvider interface {
	GitAvailable(ctx context.Context, projectID string) (bool, error)
}

type Options struct {
	StaleAfter   time.Duration
	ProbeTimeout time.Duration
	Now          func() time.Time
}

type Service struct {
	projects     ProjectProvider
	context      ContextProvider
	workspace    WorkspaceGitProvider
	staleAfter   time.Duration
	probeTimeout time.Duration
	now          func() time.Time
}

type latestRunResult struct {
	run RunSummary
	ok  bool
}

func NewService(projects ProjectProvider, contextProvider ContextProvider, workspace WorkspaceGitProvider, options Options) *Service {
	staleAfter := options.StaleAfter
	if staleAfter == 0 {
		staleAfter = defaultStaleAfter
	}
	probeTimeout := options.ProbeTimeout
	if probeTimeout == 0 {
		probeTimeout = defaultProbeTimeout
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		projects:     projects,
		context:      contextProvider,
		workspace:    workspace,
		staleAfter:   staleAfter,
		probeTimeout: probeTimeout,
		now:          now,
	}
}

func NewServiceFromAPIs(registry *projectregistry.Registry, ingestion projectingestion.API, workspace projectworkspace.API, options Options) *Service {
	return NewService(registryProjectProvider{registry: registry}, ingestionContextProvider{ingestion: ingestion}, workspaceGitProvider{workspace: workspace}, options)
}

func (svc *Service) ContextHealth(ctx context.Context, projectID string) (ContextHealth, error) {
	project, err := svc.projects.GetProject(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return ContextHealth{}, err
	}
	checkedAt := svc.now().UTC()
	health := ContextHealth{
		ProjectID:        project.ID,
		Enabled:          project.Enabled,
		DigestMode:       project.DigestMode,
		UpdatePolicy:     project.UpdatePolicy,
		WorkspaceMode:    project.WorkspaceMode,
		GraphStorage:     project.GraphStorage,
		ValidationStatus: project.ValidationStatus,
		CheckedAt:        checkedAt,
	}

	if !project.Enabled {
		health.Status = ContextHealthDisabled
		health.StatusReason = "project_disabled"
		return health, nil
	}
	if project.ValidationStatus != projectregistry.ValidationStatusValid {
		health.Status = ContextHealthUnavailable
		health.StatusReason = "project_validation_failed"
		return health, nil
	}
	if project.DigestMode != projectregistry.DigestModeContentGraph {
		health.Status = ContextHealthDisabled
		health.StatusReason = "content_graph_disabled"
		return health, nil
	}

	if svc.context == nil {
		health.Status = ContextHealthUnavailable
		health.StatusReason = "context_provider_unavailable"
		return health, nil
	}
	activeSync := false
	if provider, ok := svc.context.(activeSyncProvider); ok {
		activeSync = provider.ActiveSync(project.ID)
	}

	var probeIssue string
	var probeTimedOut bool
	var latest RunSummary
	var hasLatest bool
	var activeRuns []RunSummary
	latestResult, err := contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) (latestRunResult, error) {
		latest, hasLatest, err := svc.latestRun(probeCtx, project.ID)
		return latestRunResult{run: latest, ok: hasLatest}, err
	})
	latest = latestResult.run
	hasLatest = latestResult.ok
	if err != nil {
		probeIssue = firstNonEmpty(probeIssue, "latest_run_unknown")
		probeTimedOut = probeTimedOut || errors.Is(err, context.DeadlineExceeded)
	} else if hasLatest {
		health.LatestRun = &latest
		health.ReasonCounts = copyPositiveCounts(latest.ReasonCounts)
	}

	activeRuns, err = contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) ([]RunSummary, error) {
		return svc.context.ActiveRuns(probeCtx, project.ID)
	})
	if err != nil {
		probeIssue = firstNonEmpty(probeIssue, "active_runs_unknown")
		probeTimedOut = probeTimedOut || errors.Is(err, context.DeadlineExceeded)
	}
	if len(activeRuns) > 0 {
		health.ActiveRunID = activeRuns[0].ID
	}

	health.EligibleFileCount, err = contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) (int, error) {
		return svc.context.EligibleFileCount(probeCtx, project.ID)
	})
	if err != nil {
		probeIssue = firstNonEmpty(probeIssue, "eligible_file_count_unknown")
		probeTimedOut = probeTimedOut || errors.Is(err, context.DeadlineExceeded)
	}
	health.IndexedSymbolCount, err = contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) (int, error) {
		return svc.context.IndexedSymbolCount(probeCtx, project.ID)
	})
	if err != nil {
		probeIssue = firstNonEmpty(probeIssue, "indexed_symbol_count_unknown")
		probeTimedOut = probeTimedOut || errors.Is(err, context.DeadlineExceeded)
	}
	health.IndexedChunkCount, err = contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) (int, error) {
		return svc.context.IndexedChunkCount(probeCtx, project.ID)
	})
	if err != nil {
		probeIssue = firstNonEmpty(probeIssue, "indexed_chunk_count_unknown")
		probeTimedOut = probeTimedOut || errors.Is(err, context.DeadlineExceeded)
	}
	health.IndexedContentAvailable = health.EligibleFileCount > 0 || health.IndexedSymbolCount > 0 || health.IndexedChunkCount > 0

	searchIndex, err := contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) (SearchIndexHealth, error) {
		return svc.context.SearchIndexHealth(probeCtx, project.ID)
	})
	if err != nil {
		probeIssue = firstNonEmpty(probeIssue, "search_index_unknown")
		probeTimedOut = probeTimedOut || errors.Is(err, context.DeadlineExceeded)
		searchIndex = SearchIndexHealth{Status: "unknown", Degraded: true, Reason: "search_index_unknown"}
	}
	health.SearchIndex = SearchIndexHealth{
		Status:   safeCategory(searchIndex.Status, "unknown"),
		Degraded: searchIndex.Degraded,
		Reason:   safeCategory(searchIndex.Reason, ""),
	}

	if svc.workspace != nil {
		health.WorkspaceGitAvailable, _ = contextHealthProbe(ctx, svc.probeTimeout, func(probeCtx context.Context) (bool, error) {
			return svc.workspace.GitAvailable(probeCtx, project.ID)
		})
	}

	health.Status, health.StatusReason = classifyHealth(project, latest, hasLatest, activeRuns, health, checkedAt, svc.staleAfter)
	if activeSync && health.Status != ContextHealthDegraded {
		health.Status = ContextHealthSyncing
		health.StatusReason = "ingestion_active"
	}
	if probeIssue != "" && health.Status != ContextHealthDegraded {
		if activeSync {
			health.Status = ContextHealthSyncing
			health.StatusReason = "ingestion_active"
		} else if probeTimedOut {
			health.Status = ContextHealthSyncing
			health.StatusReason = probeIssue
		} else {
			health.Status = ContextHealthDegraded
			health.StatusReason = probeIssue
		}
	}
	return health, nil
}

func contextHealthProbe[T any](parent context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	if err := parent.Err(); err != nil {
		var zero T
		return zero, err
	}
	if timeout <= 0 {
		return fn(parent)
	}
	probeCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := fn(probeCtx)
		done <- result{value: value, err: err}
	}()
	select {
	case result := <-done:
		return result.value, result.err
	case <-probeCtx.Done():
		var zero T
		return zero, probeCtx.Err()
	}
}

func (svc *Service) latestRun(ctx context.Context, projectID string) (RunSummary, bool, error) {
	latest, err := svc.context.LatestRun(ctx, projectID)
	if errors.Is(err, ErrRunNotFound) {
		return RunSummary{}, false, nil
	}
	if err != nil {
		return RunSummary{}, false, err
	}
	return sanitizeRunSummary(latest), true, nil
}

func classifyHealth(project projectregistry.Project, latest RunSummary, hasLatest bool, activeRuns []RunSummary, health ContextHealth, checkedAt time.Time, staleAfter time.Duration) (ContextHealthStatus, string) {
	if health.SearchIndex.Degraded {
		reason := health.SearchIndex.Reason
		if reason == "" {
			reason = "search_index_degraded"
		}
		return ContextHealthDegraded, reason
	}
	if len(activeRuns) > 0 {
		switch activeRuns[0].Status {
		case runStatusRunning:
			return ContextHealthSyncing, "ingestion_active"
		case runStatusPending:
			return ContextHealthSyncing, "ingestion_active"
		}
	}
	if hasLatest && latest.Status == runStatusFailed {
		return ContextHealthDegraded, safeCategory(latest.ErrorCategory, "latest_ingestion_failed")
	}
	latestHasFileWarnings := hasLatest && latest.Status == runStatusCompleted && latest.ErrorCategory == "file_errors" && health.IndexedContentAvailable
	if hasLatest && latest.ErrorCategory != "" {
		if !latestHasFileWarnings {
			return ContextHealthDegraded, safeCategory(latest.ErrorCategory, "latest_ingestion_degraded")
		}
	}
	if hasLatest && latest.Status == runStatusCompleted && health.EligibleFileCount == 0 {
		return ContextHealthEmpty, ""
	}
	if hasLatest && latest.Status == runStatusCompleted && staleAfter > 0 {
		progressAt := latest.LastProgressAt
		if progressAt.IsZero() {
			progressAt = latest.FinishedAt
		}
		if !progressAt.IsZero() && checkedAt.Sub(progressAt) > staleAfter {
			return ContextHealthStale, "latest_ingestion_stale"
		}
	}
	if hasLatest && latest.Status == runStatusCompleted {
		if latestHasFileWarnings {
			return ContextHealthReady, "file_warnings"
		}
		return ContextHealthReady, ""
	}
	if project.UpdatePolicy == projectregistry.UpdatePolicyLive {
		return ContextHealthWarmingUp, "awaiting_live_ingestion"
	}
	return ContextHealthUnavailable, "no_ingestion_run"
}

type schedulerDiagnosticsProvider interface {
	Diagnostics() projectingestion.SchedulerDiagnostics
}

func projectHasActiveSync(provider any, projectID string) bool {
	diagnosticsProvider, ok := provider.(schedulerDiagnosticsProvider)
	if !ok {
		return false
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return false
	}
	diagnostics := diagnosticsProvider.Diagnostics()
	return diagnostics.ActiveProjectTaskCount[projectID] > 0 ||
		diagnostics.ProjectWideTaskCount[projectID] > 0 ||
		diagnostics.PendingProjectWideTaskCount[projectID] > 0 ||
		diagnostics.ActiveTaskCount > 0
}

func sanitizeRunSummary(run RunSummary) RunSummary {
	return RunSummary{
		ID:             run.ID,
		Status:         safeCategory(run.Status, ""),
		Trigger:        safeCategory(run.Trigger, ""),
		RunKind:        safeCategory(run.RunKind, ""),
		Mode:           safeCategory(run.Mode, ""),
		FilesSeen:      run.FilesSeen,
		FilesIngested:  run.FilesIngested,
		FilesSkipped:   run.FilesSkipped,
		FilesUnchanged: run.FilesUnchanged,
		ChunksStored:   run.ChunksStored,
		SymbolsStored:  run.SymbolsStored,
		ErrorCategory:  safeCategory(run.ErrorCategory, ""),
		ReasonCounts:   copyPositiveCounts(run.ReasonCounts),
		CurrentPhase:   safeCategory(run.CurrentPhase, ""),
		StartedAt:      run.StartedAt,
		FinishedAt:     run.FinishedAt,
		HeartbeatAt:    run.HeartbeatAt,
		LastProgressAt: run.LastProgressAt,
	}
}

func copyPositiveCounts(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for reason, count := range in {
		if count > 0 {
			out[safeCategory(reason, "other")] = count
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func safeCategory(value string, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	if len(value) > 80 {
		return fallback
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fallback
	}
	return value
}

type registryProjectProvider struct {
	registry *projectregistry.Registry
}

func (provider registryProjectProvider) GetProject(_ context.Context, projectID string) (projectregistry.Project, error) {
	if provider.registry == nil {
		return projectregistry.Project{}, projectregistry.ErrProjectNotFound
	}
	project, ok := provider.registry.Get(projectID)
	if !ok {
		return projectregistry.Project{}, projectregistry.ErrProjectNotFound
	}
	return project, nil
}

type ingestionContextProvider struct {
	ingestion projectingestion.API
}

func (provider ingestionContextProvider) ActiveSync(projectID string) bool {
	return projectHasActiveSync(provider.ingestion, projectID)
}

func (provider ingestionContextProvider) LatestRun(ctx context.Context, projectID string) (RunSummary, error) {
	if provider.ingestion == nil {
		return RunSummary{}, ErrRunNotFound
	}
	run, err := provider.ingestion.LatestRunMetadata(ctx, projectID)
	if errors.Is(err, projectingestion.ErrRunNotFound) {
		return RunSummary{}, ErrRunNotFound
	}
	if err != nil {
		return RunSummary{}, err
	}
	return runSummaryFromIngestion(run), nil
}

func (provider ingestionContextProvider) ActiveRuns(ctx context.Context, projectID string) ([]RunSummary, error) {
	if provider.ingestion == nil {
		return nil, nil
	}
	if activeProvider, ok := provider.ingestion.(interface {
		ActiveRunMetadata(context.Context, string) ([]projectingestion.RunMetadata, error)
	}); ok {
		runs, err := activeProvider.ActiveRunMetadata(ctx, projectID)
		if err != nil {
			return nil, err
		}
		summaries := make([]RunSummary, 0, len(runs))
		for _, run := range runs {
			summaries = append(summaries, sanitizeRunSummary(runSummaryFromIngestion(run)))
		}
		return summaries, nil
	}
	latest, err := provider.LatestRun(ctx, projectID)
	if errors.Is(err, ErrRunNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	switch latest.Status {
	case runStatusPending, runStatusRunning:
		return []RunSummary{latest}, nil
	default:
		return nil, nil
	}
}

func (provider ingestionContextProvider) EligibleFileCount(ctx context.Context, projectID string) (int, error) {
	if provider.ingestion == nil {
		return 0, nil
	}
	if counter, ok := provider.ingestion.(interface {
		EligibleFileCount(context.Context, string) (int, error)
	}); ok {
		return counter.EligibleFileCount(ctx, projectID)
	}
	return countFiles(ctx, provider.ingestion, projectID, projectingestion.FileStateFilter{Status: projectingestion.FileStatusEligible})
}

func (provider ingestionContextProvider) IndexedSymbolCount(ctx context.Context, projectID string) (int, error) {
	if provider.ingestion == nil {
		return 0, nil
	}
	if counter, ok := provider.ingestion.(interface {
		IndexedSymbolCount(context.Context, string) (int, error)
	}); ok {
		return counter.IndexedSymbolCount(ctx, projectID)
	}
	total := 0
	pageToken := ""
	for {
		result, err := provider.ingestion.ListSymbols(ctx, projectID, projectingestion.SymbolFilter{}, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize, PageToken: pageToken})
		if err != nil {
			return 0, err
		}
		total += len(result.Symbols)
		if result.NextPageToken == "" {
			return total, nil
		}
		pageToken = result.NextPageToken
	}
}

func (provider ingestionContextProvider) IndexedChunkCount(ctx context.Context, projectID string) (int, error) {
	if provider.ingestion == nil {
		return 0, nil
	}
	if counter, ok := provider.ingestion.(interface {
		IndexedChunkCount(context.Context, string) (int, error)
	}); ok {
		return counter.IndexedChunkCount(ctx, projectID)
	}
	latest, err := provider.LatestRun(ctx, projectID)
	if errors.Is(err, ErrRunNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.ChunksStored, nil
}

func (provider ingestionContextProvider) SearchIndexHealth(ctx context.Context, projectID string) (SearchIndexHealth, error) {
	if provider.ingestion == nil {
		return SearchIndexHealth{Status: "unknown"}, nil
	}
	if healthProvider, ok := provider.ingestion.(interface {
		ContextSearchIndexHealth(context.Context, string) (projectingestion.SearchIndexHealth, error)
	}); ok {
		health, err := healthProvider.ContextSearchIndexHealth(ctx, projectID)
		if err != nil {
			return SearchIndexHealth{}, err
		}
		status := "ok"
		if health.Degraded {
			status = "degraded"
		}
		return SearchIndexHealth{
			Status:   status,
			Degraded: health.Degraded,
			Reason:   health.Reason,
		}, nil
	}
	if healthProvider, ok := provider.ingestion.(interface {
		SearchIndexHealth(context.Context, string) (projectingestion.SearchIndexHealth, error)
	}); ok {
		health, err := healthProvider.SearchIndexHealth(ctx, projectID)
		if err != nil {
			return SearchIndexHealth{}, err
		}
		status := "ok"
		if health.Degraded {
			status = "degraded"
		}
		return SearchIndexHealth{
			Status:   status,
			Degraded: health.Degraded,
			Reason:   health.Reason,
		}, nil
	}
	result, err := provider.ingestion.SearchFiles(ctx, projectID, projectingestion.FileSearchOptions{PageSize: 1})
	if err != nil {
		return SearchIndexHealth{}, err
	}
	if result.Index == nil {
		return SearchIndexHealth{Status: "unknown"}, nil
	}
	return SearchIndexHealth{
		Status:   result.Index.IndexStatus,
		Degraded: result.Index.Degraded,
		Reason:   result.Index.DegradedReason,
	}, nil
}

func countFiles(ctx context.Context, ingestion projectingestion.API, projectID string, filter projectingestion.FileStateFilter) (int, error) {
	total := 0
	pageToken := ""
	for {
		result, err := ingestion.ListFiles(ctx, projectID, filter, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize, PageToken: pageToken})
		if err != nil {
			return 0, err
		}
		total += len(result.Files)
		if result.NextPageToken == "" {
			return total, nil
		}
		pageToken = result.NextPageToken
	}
}

func runSummaryFromIngestion(run projectingestion.RunMetadata) RunSummary {
	return RunSummary{
		ID:             run.ID,
		Status:         run.Status,
		Trigger:        run.Trigger,
		RunKind:        run.RunKind,
		Mode:           run.Mode,
		FilesSeen:      run.FilesSeen,
		FilesIngested:  run.FilesIngested,
		FilesSkipped:   run.FilesSkipped,
		FilesUnchanged: run.FilesUnchanged,
		ChunksStored:   run.ChunksStored,
		SymbolsStored:  run.SymbolsStored,
		ErrorCategory:  run.ErrorCategory,
		ReasonCounts:   run.ReasonCounts,
		CurrentPhase:   run.CurrentPhase,
		StartedAt:      run.StartedAt,
		FinishedAt:     run.FinishedAt,
		HeartbeatAt:    run.HeartbeatAt,
		LastProgressAt: run.LastProgressAt,
	}
}

type workspaceGitProvider struct {
	workspace projectworkspace.API
}

func (provider workspaceGitProvider) GitAvailable(ctx context.Context, projectID string) (bool, error) {
	if provider.workspace == nil {
		return false, nil
	}
	if checker, ok := provider.workspace.(interface {
		GitAvailable(context.Context, string) (bool, error)
	}); ok {
		return checker.GitAvailable(ctx, projectID)
	}
	_, err := provider.workspace.GitStatus(ctx, projectID, projectworkspace.GitStatusOptions{PageSize: 1})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, projectworkspace.ErrGitUnavailable) ||
		errors.Is(err, projectworkspace.ErrWorkspaceDisabled) ||
		errors.Is(err, projectworkspace.ErrProjectNotFound) {
		return false, nil
	}
	return false, err
}
