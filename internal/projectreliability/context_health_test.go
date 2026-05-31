package projectreliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestContextHealth_ReadyCompletedContentGraph(t *testing.T) {
	svc := newTestService(ContextProviderFunc{
		latest: RunSummary{
			ID:             "run-1",
			Status:         runStatusCompleted,
			FilesIngested:  2,
			ChunksStored:   4,
			SymbolsStored:  3,
			LastProgressAt: testNow.Add(-time.Hour),
		},
		eligible: 2,
		symbols:  3,
		chunks:   4,
		index:    SearchIndexHealth{Status: "completed"},
	})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthReady {
		t.Fatalf("expected ready, got %#v", health)
	}
	if health.LatestRun == nil || health.LatestRun.ID != "run-1" {
		t.Fatalf("expected latest run summary, got %#v", health.LatestRun)
	}
	if !health.IndexedContentAvailable {
		t.Fatalf("expected indexed content availability, got %#v", health)
	}
}

func TestContextHealth_DegradedSearchWinsOverRunning(t *testing.T) {
	svc := newTestService(ContextProviderFunc{
		latest: RunSummary{
			ID:     "run-1",
			Status: runStatusCompleted,
		},
		active: []RunSummary{{
			ID:     "run-2",
			Status: runStatusRunning,
		}},
		eligible: 1,
		index: SearchIndexHealth{
			Status:   "degraded",
			Degraded: true,
			Reason:   "fts_rebuild_required",
		},
	})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthDegraded || health.StatusReason != "fts_rebuild_required" {
		t.Fatalf("expected degraded search to win, got %#v", health)
	}
	if health.ActiveRunID != "run-2" {
		t.Fatalf("expected active run id, got %q", health.ActiveRunID)
	}
}

func TestContextHealth_ActiveRunsAreSyncing(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   ContextHealthStatus
	}{
		{name: "pending", status: runStatusPending, want: ContextHealthSyncing},
		{name: "running", status: runStatusRunning, want: ContextHealthSyncing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(ContextProviderFunc{
				active: []RunSummary{{
					ID:     "active-run",
					Status: tt.status,
				}},
				eligible: 1,
				index:    SearchIndexHealth{Status: "completed"},
			})

			health, err := svc.ContextHealth(context.Background(), "example")
			if err != nil {
				t.Fatalf("context health: %v", err)
			}
			if health.Status != tt.want {
				t.Fatalf("expected %s, got %#v", tt.want, health)
			}
			if health.StatusReason != "ingestion_active" {
				t.Fatalf("expected ingestion_active reason, got %#v", health)
			}
		})
	}
}

func TestContextHealth_CompletedZeroEligibleIsEmpty(t *testing.T) {
	svc := newTestService(ContextProviderFunc{
		latest: RunSummary{
			ID:             "run-1",
			Status:         runStatusCompleted,
			LastProgressAt: testNow,
		},
		eligible: 0,
		index:    SearchIndexHealth{Status: "completed"},
	})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthEmpty {
		t.Fatalf("expected empty, got %#v", health)
	}
}

func TestContextHealth_DisabledAndInvalidDoNotExposeValidationError(t *testing.T) {
	svc := NewService(
		ProjectProviderFunc(func(context.Context, string) (projectregistry.Project, error) {
			project := testProject()
			project.ValidationStatus = "invalid"
			project.ValidationError = "/private/root failed: token=secret"
			return project, nil
		}),
		ContextProviderFunc{},
		nil,
		Options{Now: func() time.Time { return testNow }},
	)

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthUnavailable || health.StatusReason != "project_validation_failed" {
		t.Fatalf("expected safe validation failure, got %#v", health)
	}
}

func TestContextHealth_SanitizesRawCategories(t *testing.T) {
	svc := newTestService(ContextProviderFunc{
		latest: RunSummary{
			ID:            "run-1",
			Status:        runStatusFailed,
			ErrorCategory: "token=secret /root",
			ReasonCounts: map[string]int{
				"safe_reason":        2,
				"token=secret /root": 1,
			},
		},
		eligible: 1,
		index:    SearchIndexHealth{Status: "completed"},
	})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthDegraded || health.StatusReason != "latest_ingestion_failed" {
		t.Fatalf("expected sanitized failed status, got %#v", health)
	}
	if health.LatestRun.ErrorCategory != "" {
		t.Fatalf("expected unsafe error category to be omitted, got %q", health.LatestRun.ErrorCategory)
	}
	if health.ReasonCounts["other"] != 1 || health.ReasonCounts["safe_reason"] != 2 {
		t.Fatalf("expected sanitized reason counts, got %#v", health.ReasonCounts)
	}
}

func TestContextHealth_StaleLiveProject(t *testing.T) {
	svc := newTestService(ContextProviderFunc{
		latest: RunSummary{
			ID:             "run-1",
			Status:         runStatusCompleted,
			LastProgressAt: testNow.Add(-25 * time.Hour),
		},
		eligible: 1,
		index:    SearchIndexHealth{Status: "completed"},
	})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthStale {
		t.Fatalf("expected stale, got %#v", health)
	}
}

var testNow = time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

func newTestService(contextProvider ContextProvider) *Service {
	return NewService(
		ProjectProviderFunc(func(context.Context, string) (projectregistry.Project, error) {
			return testProject(), nil
		}),
		contextProvider,
		WorkspaceGitProviderFunc(func(context.Context, string) (bool, error) {
			return true, nil
		}),
		Options{Now: func() time.Time { return testNow }},
	)
}

func testProject() projectregistry.Project {
	return projectregistry.Project{
		ID:               "example",
		Enabled:          true,
		DigestMode:       projectregistry.DigestModeContentGraph,
		UpdatePolicy:     projectregistry.UpdatePolicyLive,
		WorkspaceMode:    projectregistry.WorkspaceModeReadOnly,
		GraphStorage:     projectregistry.GraphStoragePersistent,
		ValidationStatus: projectregistry.ValidationStatusValid,
	}
}

type ProjectProviderFunc func(context.Context, string) (projectregistry.Project, error)

func (fn ProjectProviderFunc) GetProject(ctx context.Context, projectID string) (projectregistry.Project, error) {
	return fn(ctx, projectID)
}

type ContextProviderFunc struct {
	latest      RunSummary
	latestErr   error
	active      []RunSummary
	activeErr   error
	eligible    int
	eligibleErr error
	symbols     int
	symbolsErr  error
	chunks      int
	chunksErr   error
	index       SearchIndexHealth
	indexErr    error
}

func (provider ContextProviderFunc) LatestRun(context.Context, string) (RunSummary, error) {
	if provider.latestErr != nil {
		return RunSummary{}, provider.latestErr
	}
	if provider.latest.ID == "" {
		return RunSummary{}, ErrRunNotFound
	}
	return provider.latest, nil
}

func (provider ContextProviderFunc) ActiveRuns(context.Context, string) ([]RunSummary, error) {
	if provider.activeErr != nil {
		return nil, provider.activeErr
	}
	return provider.active, nil
}

func (provider ContextProviderFunc) EligibleFileCount(context.Context, string) (int, error) {
	return provider.eligible, provider.eligibleErr
}

func (provider ContextProviderFunc) IndexedSymbolCount(context.Context, string) (int, error) {
	return provider.symbols, provider.symbolsErr
}

func (provider ContextProviderFunc) IndexedChunkCount(context.Context, string) (int, error) {
	return provider.chunks, provider.chunksErr
}

func (provider ContextProviderFunc) SearchIndexHealth(context.Context, string) (SearchIndexHealth, error) {
	return provider.index, provider.indexErr
}

type WorkspaceGitProviderFunc func(context.Context, string) (bool, error)

func (fn WorkspaceGitProviderFunc) GitAvailable(ctx context.Context, projectID string) (bool, error) {
	return fn(ctx, projectID)
}

func TestContextHealth_DegradesProviderErrors(t *testing.T) {
	wantErr := errors.New("store unavailable")
	svc := newTestService(ContextProviderFunc{eligibleErr: wantErr})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health should degrade instead of returning provider error: %v", err)
	}
	if health.Status != ContextHealthDegraded || health.StatusReason != "eligible_file_count_unknown" {
		t.Fatalf("expected degraded provider error, got %#v", health)
	}
}

func TestContextHealth_BoundsSlowProvider(t *testing.T) {
	svc := NewService(
		ProjectProviderFunc(func(context.Context, string) (projectregistry.Project, error) {
			return testProject(), nil
		}),
		slowContextProvider{},
		nil,
		Options{
			Now:          func() time.Time { return testNow },
			ProbeTimeout: 10 * time.Millisecond,
		},
	)

	start := time.Now()
	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health should degrade instead of timing out: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("context health was not bounded: %s", elapsed)
	}
	if health.Status != ContextHealthSyncing || health.StatusReason != "latest_run_unknown" {
		t.Fatalf("expected syncing timeout, got %#v", health)
	}
}

func TestContextHealth_ActiveSyncStillReportsGraphInventory(t *testing.T) {
	svc := newTestService(activeSyncContextProvider{ContextProviderFunc: ContextProviderFunc{
		latest: RunSummary{
			ID:             "run-1",
			Status:         runStatusCompleted,
			LastProgressAt: testNow.Add(-time.Hour),
		},
		eligible: 42,
		symbols:  100,
		chunks:   60,
		index:    SearchIndexHealth{Status: "completed"},
	}})

	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Status != ContextHealthSyncing || health.StatusReason != "ingestion_active" {
		t.Fatalf("expected active sync status, got %#v", health)
	}
	if !health.IndexedContentAvailable || health.EligibleFileCount != 42 || health.IndexedSymbolCount != 100 || health.IndexedChunkCount != 60 {
		t.Fatalf("expected active sync to keep graph inventory, got %#v", health)
	}
	if health.LatestRun == nil || health.LatestRun.ID != "run-1" {
		t.Fatalf("expected active sync to keep latest run context, got %#v", health)
	}
}

func TestContextHealth_ActiveSyncBoundsBlockingProvider(t *testing.T) {
	block := make(chan struct{})
	svc := NewService(
		ProjectProviderFunc(func(context.Context, string) (projectregistry.Project, error) {
			return testProject(), nil
		}),
		activeSyncBlockingContextProvider{blockingContextProvider: blockingContextProvider{blockLatest: block}},
		nil,
		Options{
			Now:          func() time.Time { return testNow },
			ProbeTimeout: 10 * time.Millisecond,
		},
	)

	start := time.Now()
	health, err := svc.ContextHealth(context.Background(), "example")
	if err != nil {
		t.Fatalf("context health should report active sync instead of calling blocking provider: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("context health was not bounded: %s", elapsed)
	}
	if health.Status != ContextHealthSyncing || health.StatusReason != "ingestion_active" {
		t.Fatalf("expected active sync, got %#v", health)
	}
	close(block)
}

func TestProjectHasActiveSyncTreatsGlobalIngestionAsIndexingWindow(t *testing.T) {
	provider := schedulerDiagnosticsProviderFunc(func() projectingestion.SchedulerDiagnostics {
		return projectingestion.SchedulerDiagnostics{
			ActiveTaskCount:             1,
			ActiveProjectTaskCount:      map[string]int{"other": 1},
			ProjectWideTaskCount:        map[string]int{},
			PendingProjectWideTaskCount: map[string]int{},
		}
	})
	if !projectHasActiveSync(provider, "example") {
		t.Fatalf("expected global active ingestion to count as active sync window")
	}
}

type slowContextProvider struct{}

type activeSyncContextProvider struct {
	ContextProviderFunc
}

func (activeSyncContextProvider) ActiveSync(string) bool {
	return true
}

func (slowContextProvider) LatestRun(ctx context.Context, _ string) (RunSummary, error) {
	<-ctx.Done()
	return RunSummary{}, ctx.Err()
}

func (slowContextProvider) ActiveRuns(context.Context, string) ([]RunSummary, error) {
	return nil, nil
}

func (slowContextProvider) EligibleFileCount(context.Context, string) (int, error) {
	return 0, nil
}

func (slowContextProvider) IndexedSymbolCount(context.Context, string) (int, error) {
	return 0, nil
}

func (slowContextProvider) IndexedChunkCount(context.Context, string) (int, error) {
	return 0, nil
}

func (slowContextProvider) SearchIndexHealth(context.Context, string) (SearchIndexHealth, error) {
	return SearchIndexHealth{Status: "unknown"}, nil
}

type blockingContextProvider struct {
	blockLatest <-chan struct{}
}

func (provider blockingContextProvider) LatestRun(context.Context, string) (RunSummary, error) {
	<-provider.blockLatest
	return RunSummary{}, ErrRunNotFound
}

func (blockingContextProvider) ActiveRuns(context.Context, string) ([]RunSummary, error) {
	return nil, nil
}

func (blockingContextProvider) EligibleFileCount(context.Context, string) (int, error) {
	return 0, nil
}

func (blockingContextProvider) IndexedSymbolCount(context.Context, string) (int, error) {
	return 0, nil
}

func (blockingContextProvider) IndexedChunkCount(context.Context, string) (int, error) {
	return 0, nil
}

func (blockingContextProvider) SearchIndexHealth(context.Context, string) (SearchIndexHealth, error) {
	return SearchIndexHealth{Status: "unknown"}, nil
}

type activeSyncBlockingContextProvider struct {
	blockingContextProvider
}

func (activeSyncBlockingContextProvider) ActiveSync(string) bool {
	return true
}

type schedulerDiagnosticsProviderFunc func() projectingestion.SchedulerDiagnostics

func (fn schedulerDiagnosticsProviderFunc) Diagnostics() projectingestion.SchedulerDiagnostics {
	return fn()
}
