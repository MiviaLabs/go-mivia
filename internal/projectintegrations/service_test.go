package projectintegrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

func TestService_ListProvidersIncludesConfiguredJiraAndConfluence(t *testing.T) {
	service := newTestService(t, nil, testIntegrationProject())

	providers, err := service.ListProviders("project-1")
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("expected two providers, got %#v", providers)
	}
	if providers[0].Provider != ProviderJira || !providers[0].Enabled || providers[0].AllowlistKind != "project_keys" || providers[0].AllowlistCount != 2 {
		t.Fatalf("unexpected Jira provider: %#v", providers[0])
	}
	if providers[1].Provider != ProviderConfluence || !providers[1].Enabled || providers[1].AllowlistKind != "space_keys" || providers[1].AllowlistCount != 2 {
		t.Fatalf("unexpected Confluence provider: %#v", providers[1])
	}
}

func TestService_StatusForDisabledConfiguredProvider(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Confluence.Enabled = false
	project.Integrations.Confluence.SpaceKeys = nil
	service := newTestService(t, nil, project)

	status, err := service.Status(context.Background(), "project-1", ProviderConfluence)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Configured || status.Enabled || status.Provider != ProviderConfluence {
		t.Fatalf("unexpected disabled status: %#v", status)
	}
	if status.AllowlistCount != 0 || status.CredentialSource != "file" {
		t.Fatalf("unexpected disabled provider metadata: %#v", status)
	}
}

func TestService_StatusIsRedacted(t *testing.T) {
	project := testIntegrationProject()
	service := newTestService(t, nil, project)

	status, err := service.Status(context.Background(), "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	payload := mustJSON(t, status)
	assertOmits(t, payload,
		"https://tenant.atlassian.net",
		"tenant-cloud-id",
		"ACME",
		"OPS",
		"MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
		"MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
		"/home/mac/secret-email",
		"/home/mac/secret-token",
		"/home/mac/mivialabs/mivialabs-agents-monorepo",
	)
	if !strings.Contains(payload, `"CredentialSource":"env"`) {
		t.Fatalf("expected redacted credential source in payload: %s", payload)
	}
}

func TestService_StatusRedactsCombinedCredentialsFileRef(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Jira.CredentialRefs = config.AtlassianCredentialRefs{
		CredentialsFile: "/home/mac/secret-atlassian-credentials.json",
	}
	service := newTestService(t, nil, project)

	status, err := service.Status(context.Background(), "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	payload := mustJSON(t, status)
	assertOmits(t, payload, "/home/mac/secret-atlassian-credentials.json")
	if !strings.Contains(payload, `"CredentialSource":"file"`) {
		t.Fatalf("expected redacted file credential source in payload: %s", payload)
	}
}

func TestService_PollProviderRunsManualOneShotAndReturnsRedactedMetadata(t *testing.T) {
	runner := &fakePollRunner{
		result: PollRunResult{
			Run: SyncRun{
				ID:            "run-1",
				ProjectID:     "project-1",
				Provider:      ProviderJira,
				Kind:          SyncKindIncremental,
				Status:        SyncRunStatusCompleted,
				ItemsSeen:     1,
				ItemsUpserted: 1,
				StartedAt:     testTime(),
				FinishedAt:    testTime().Add(time.Minute),
			},
			State: SyncState{
				ProjectID:           "project-1",
				Provider:            ProviderJira,
				LastRunID:           "run-1",
				LastSuccessfulRunID: "run-1",
				CursorHash:          "sha256:raw-provider-cursor-token",
				UpdatedAt:           testTime().Add(time.Minute),
			},
		},
	}
	project := testIntegrationProject()
	project.Integrations.Jira.CredentialRefs = config.AtlassianCredentialRefs{
		CredentialsFile: "/home/mac/secret-atlassian-credentials.json",
	}
	service := newTestServiceWithOptions(t, nil, ServiceOptions{Runner: runner}, project)

	status, err := service.PollProvider(context.Background(), "project-1", ProviderJira, SyncKindIncremental)
	if err != nil {
		t.Fatalf("poll provider: %v", err)
	}
	if runner.projectID != "project-1" || runner.provider != ProviderJira || runner.kind != SyncKindIncremental {
		t.Fatalf("runner received unexpected input: %#v", runner)
	}
	if status.Run.Status != SyncRunStatusCompleted || status.Run.ItemsUpserted != 1 || !status.SyncState.CursorHashPresent {
		t.Fatalf("unexpected poll status: %#v", status)
	}
	assertOmits(t, mustJSON(t, status),
		"https://tenant.atlassian.net",
		"tenant-cloud-id",
		"ACME",
		"OPS",
		"/home/mac/secret-atlassian-credentials.json",
		"raw-provider-cursor-token",
		"sha256:",
	)
}

func TestService_SubmitProviderPollReturnsPendingRunMetadata(t *testing.T) {
	runner := &fakePollRunner{
		result: PollRunResult{
			Run: SyncRun{
				ID:        "run-queued",
				ProjectID: "project-1",
				Provider:  ProviderJira,
				Kind:      SyncKindInitialFull,
				Status:    SyncRunStatusPending,
				StartedAt: testTime(),
			},
		},
	}
	service := newTestServiceWithOptions(t, nil, ServiceOptions{Runner: runner}, testIntegrationProject())

	accepted, err := service.SubmitProviderPoll(context.Background(), "project-1", ProviderJira, SyncKindInitialFull)
	if err != nil {
		t.Fatalf("submit provider poll: %v", err)
	}
	if !runner.submitCalled || runner.projectID != "project-1" || runner.provider != ProviderJira || runner.kind != SyncKindInitialFull {
		t.Fatalf("runner received unexpected async input: %#v", runner)
	}
	if !accepted.Accepted || accepted.Run.ID != "run-queued" || accepted.Run.Status != SyncRunStatusPending {
		t.Fatalf("unexpected accepted poll: %#v", accepted)
	}
	assertOmits(t, mustJSON(t, accepted), "tenant.atlassian.net", "ACME", "MIVIA_ATLASSIAN", "/home/mac")
}

func TestService_PollRunStatusReturnsRunByIDWithoutRawCursor(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	service := newTestService(t, store, testIntegrationProject())
	run := SyncRun{
		ID:        "run-queued",
		ProjectID: "project-1",
		Provider:  ProviderJira,
		Kind:      SyncKindInitialFull,
		Status:    SyncRunStatusPending,
		StartedAt: testTime(),
	}
	if err := store.CreateSyncRun(ctx, run); err != nil {
		t.Fatalf("create sync run: %v", err)
	}
	if _, err := store.UpdateSyncState(ctx, SyncStateInput{
		ProjectID:     "project-1",
		Provider:      ProviderJira,
		LastRunID:     "previous-run",
		Cursor:        "raw-provider-cursor-token",
		LastSuccessAt: testTime(),
		UpdatedAt:     testTime(),
	}); err != nil {
		t.Fatalf("update sync state: %v", err)
	}

	status, err := service.PollRunStatus(ctx, "project-1", ProviderJira, "run-queued")
	if err != nil {
		t.Fatalf("poll run status: %v", err)
	}
	if status.Run.ID != "run-queued" || status.Run.Status != SyncRunStatusPending || !status.SyncState.CursorHashPresent {
		t.Fatalf("unexpected run status: %#v", status)
	}
	assertOmits(t, mustJSON(t, status), "raw-provider-cursor-token", "sha256:", "tenant.atlassian.net", "ACME", "/home/mac")
}

func TestService_PollProviderRejectsDisabledProviderBeforeRunner(t *testing.T) {
	project := testIntegrationProject()
	project.Integrations.Jira.Enabled = false
	runner := &fakePollRunner{}
	service := newTestServiceWithOptions(t, nil, ServiceOptions{Runner: runner}, project)

	_, err := service.PollProvider(context.Background(), "project-1", ProviderJira, SyncKindIncremental)
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "provider disabled") {
		t.Fatalf("expected disabled provider error, got %v", err)
	}
	if runner.called {
		t.Fatalf("runner was called for disabled provider")
	}
	assertOmits(t, err.Error(), "tenant.atlassian.net", "ACME", "MIVIA_ATLASSIAN", "/home/mac")
}

func TestService_PollProviderRequiresConfiguredRunner(t *testing.T) {
	service := newTestService(t, nil, testIntegrationProject())

	_, err := service.PollProvider(context.Background(), "project-1", ProviderJira, SyncKindIncremental)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing runner error, got %v", err)
	}
	assertOmits(t, err.Error(), "tenant.atlassian.net", "ACME", "MIVIA_ATLASSIAN", "/home/mac")
}

func TestService_UpsertConfiguredSourcesStoresHashesAndCountsOnly(t *testing.T) {
	ctx := context.Background()
	store, db := newTestSQLiteStore(t)
	service := newTestService(t, store, testIntegrationProject())
	service.now = func() time.Time { return testTime() }

	sources, err := service.UpsertConfiguredSources(ctx, "project-1")
	if err != nil {
		t.Fatalf("upsert sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected two sources, got %#v", sources)
	}
	for _, source := range sources {
		if source.SiteURLHash == "" || source.AllowlistHash == "" {
			t.Fatalf("expected hashed source metadata, got %#v", source)
		}
		if source.Provider == ProviderJira && source.AllowlistCount != 2 {
			t.Fatalf("unexpected Jira source: %#v", source)
		}
		if source.Provider == ProviderConfluence && source.AllowlistCount != 2 {
			t.Fatalf("unexpected Confluence source: %#v", source)
		}
	}
	assertTableOmits(t, db, "project_integration_sources",
		"https://tenant.atlassian.net",
		"tenant-cloud-id",
		"ACME",
		"OPS",
		"ENG",
		"TEAM",
		"MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
		"MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
		"/home/mac/secret-email",
		"/home/mac/secret-token",
	)
}

func TestService_StatusIncludesSyncStateAndLastRunWithoutRawCursor(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	service := newTestService(t, store, testIntegrationProject())
	if _, err := service.UpsertConfiguredSources(ctx, "project-1"); err != nil {
		t.Fatalf("upsert sources: %v", err)
	}
	run := SyncRun{
		ID:            "run-1",
		ProjectID:     "project-1",
		Provider:      ProviderJira,
		Kind:          SyncKindIncremental,
		Status:        SyncRunStatusNoOp,
		ItemsSeen:     0,
		ItemsUpserted: 0,
		EmptyPoll:     true,
		IdleSleep:     5 * time.Minute,
		StartedAt:     testTime(),
		FinishedAt:    testTime().Add(time.Minute),
	}
	if err := store.CreateSyncRun(ctx, run); err != nil {
		t.Fatalf("create sync run: %v", err)
	}
	if _, err := store.UpdateSyncState(ctx, SyncStateInput{
		ProjectID:             "project-1",
		Provider:              ProviderJira,
		LastRunID:             "run-1",
		LastSuccessfulRunID:   "run-1",
		LastSuccessAt:         testTime().Add(time.Minute),
		LastIncrementalSyncAt: testTime().Add(time.Minute),
		LastEmptyPollAt:       testTime().Add(time.Minute),
		EmptyPollCount:        1,
		CurrentIdleSleep:      5 * time.Minute,
		Cursor:                "raw-provider-cursor-token",
		UpdatedAt:             testTime().Add(time.Minute),
	}); err != nil {
		t.Fatalf("update sync state: %v", err)
	}

	status, err := service.Status(ctx, "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.SyncState == nil || !status.SyncState.CursorHashPresent || status.SyncState.EmptyPollCount != 1 {
		t.Fatalf("unexpected sync state: %#v", status.SyncState)
	}
	if status.LastRun == nil || status.LastRun.Status != SyncRunStatusNoOp || !status.LastRun.EmptyPoll {
		t.Fatalf("unexpected last run: %#v", status.LastRun)
	}
	assertOmits(t, mustJSON(t, status), "raw-provider-cursor-token", "sha256:")
}

func TestService_StatusPrefersActiveRunOverLastCompletedState(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	service := newTestService(t, store, testIntegrationProject())
	completed := SyncRun{
		ID:            "run-completed",
		ProjectID:     "project-1",
		Provider:      ProviderJira,
		Kind:          SyncKindIncremental,
		Status:        SyncRunStatusCompleted,
		ItemsSeen:     10,
		ItemsUpserted: 10,
		StartedAt:     testTime().Add(-2 * time.Hour),
		FinishedAt:    testTime().Add(-time.Hour),
	}
	if err := store.CreateSyncRun(ctx, completed); err != nil {
		t.Fatalf("create completed run: %v", err)
	}
	if _, err := store.UpdateSyncState(ctx, SyncStateInput{
		ProjectID:           "project-1",
		Provider:            ProviderJira,
		LastRunID:           completed.ID,
		LastSuccessfulRunID: completed.ID,
		LastSuccessAt:       completed.FinishedAt,
		Cursor:              "raw-provider-cursor-token",
		UpdatedAt:           completed.FinishedAt,
	}); err != nil {
		t.Fatalf("update sync state: %v", err)
	}
	active := SyncRun{
		ID:            "run-active",
		ProjectID:     "project-1",
		Provider:      ProviderJira,
		Kind:          SyncKindInitialFull,
		Status:        SyncRunStatusRunning,
		ItemsSeen:     3,
		ItemsUpserted: 3,
		StartedAt:     testTime(),
	}
	if err := store.CreateSyncRun(ctx, active); err != nil {
		t.Fatalf("create active run: %v", err)
	}

	status, err := service.Status(ctx, "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.LastRun == nil || status.LastRun.ID != active.ID || status.LastRun.Status != SyncRunStatusRunning || status.LastRun.ItemsSeen != 3 {
		t.Fatalf("expected active run status, got %#v", status.LastRun)
	}
	if status.SyncState == nil || status.SyncState.LastRunID != completed.ID || !status.SyncState.CursorHashPresent {
		t.Fatalf("expected prior completed sync state to remain visible, got %#v", status.SyncState)
	}
	assertOmits(t, mustJSON(t, status), "raw-provider-cursor-token", "sha256:", "tenant.atlassian.net", "ACME", "/home/mac")
}

func TestService_MissingProjectAndProviderErrorsAreStableAndRedacted(t *testing.T) {
	service := newTestService(t, nil, testIntegrationProject())

	_, err := service.Status(context.Background(), "missing-project", ProviderJira)
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected missing project error, got %v", err)
	}
	assertOmits(t, err.Error(), "tenant.atlassian.net", "ACME", "MIVIA_ATLASSIAN", "/home/mac")

	_, err = service.Status(context.Background(), "project-1", Provider("github"))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid provider error, got %v", err)
	}
	assertOmits(t, err.Error(), "tenant.atlassian.net", "ACME", "MIVIA_ATLASSIAN", "/home/mac")

	project := testIntegrationProject()
	project.Integrations.Confluence = nil
	service = newTestService(t, nil, project)
	_, err = service.Status(context.Background(), "project-1", ProviderConfluence)
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("expected missing provider error, got %v", err)
	}
	assertOmits(t, err.Error(), "tenant.atlassian.net", "ACME", "MIVIA_ATLASSIAN", "/home/mac")
}

func newTestService(t *testing.T, store Store, projects ...config.Project) *Service {
	t.Helper()
	service, err := NewService(projects, store)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service
}

func newTestServiceWithOptions(t *testing.T, store Store, options ServiceOptions, projects ...config.Project) *Service {
	t.Helper()
	service, err := NewServiceWithOptions(projects, store, options)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service
}

type fakePollRunner struct {
	called       bool
	submitCalled bool
	projectID    string
	provider     Provider
	kind         SyncKind
	result       PollRunResult
	err          error
}

func (runner *fakePollRunner) RunProviderPoll(_ context.Context, projectID string, provider Provider, kind SyncKind) (PollRunResult, error) {
	runner.called = true
	runner.projectID = projectID
	runner.provider = provider
	runner.kind = kind
	if runner.err != nil {
		return PollRunResult{}, runner.err
	}
	return runner.result, nil
}

func (runner *fakePollRunner) SubmitProviderPoll(_ context.Context, projectID string, provider Provider, kind SyncKind) (SyncRun, error) {
	runner.submitCalled = true
	runner.projectID = projectID
	runner.provider = provider
	runner.kind = kind
	if runner.err != nil {
		return SyncRun{}, runner.err
	}
	return runner.result.Run, nil
}

func testIntegrationProject() config.Project {
	return config.Project{
		ID:          "project-1",
		DisplayName: "Project 1",
		RootPath:    "/home/mac/mivialabs/mivialabs-agents-monorepo",
		Integrations: config.IntegrationConfig{
			Jira: &config.JiraIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "tenant-cloud-id",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailEnv:    "MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
					APITokenEnv: "MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
				},
				Polling: config.IntegrationPolling{
					IngestionEnabled:    false,
					InitialFullSync:     "manual",
					IncrementalInterval: time.Minute,
					EmptyPollSleep:      10 * time.Minute,
					MaxIdleSleep:        30 * time.Minute,
					OverlapWindow:       2 * time.Minute,
					InitialPageSize:     50,
					IncrementalPageSize: 25,
				},
				ProjectKeys: []string{"ACME", "OPS"},
			},
			Confluence: &config.ConfluenceIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "tenant-cloud-id",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailFile:    "/home/mac/secret-email",
					APITokenFile: "/home/mac/secret-token",
				},
				Polling: config.IntegrationPolling{
					IngestionEnabled:    false,
					InitialFullSync:     "manual",
					IncrementalInterval: time.Minute,
					EmptyPollSleep:      10 * time.Minute,
					MaxIdleSleep:        30 * time.Minute,
					OverlapWindow:       2 * time.Minute,
					InitialPageSize:     50,
					IncrementalPageSize: 25,
				},
				SpaceKeys: []string{"ENG", "TEAM"},
			},
		},
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(payload)
}

func assertOmits(t *testing.T, value string, forbidden ...string) {
	t.Helper()
	for _, raw := range forbidden {
		if raw != "" && strings.Contains(value, raw) {
			t.Fatalf("value leaked %q: %s", raw, value)
		}
	}
}
