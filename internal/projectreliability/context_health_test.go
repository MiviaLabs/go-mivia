package projectreliability

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestContextHealth_ActiveRunsAreNotBroken(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   ContextHealthStatus
	}{
		{name: "pending", status: runStatusPending, want: ContextHealthWarmingUp},
		{name: "running", status: runStatusRunning, want: ContextHealthRunning},
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

func newTestService(contextProvider ContextProviderFunc) *Service {
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

func TestContextHealth_PropagatesProviderErrors(t *testing.T) {
	wantErr := errors.New("store unavailable")
	svc := newTestService(ContextProviderFunc{eligibleErr: wantErr})

	_, err := svc.ContextHealth(context.Background(), "example")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected provider error, got %v", err)
	}
}
