package projectintegrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
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
