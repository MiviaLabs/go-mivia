package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations/mcpapi"
)

func TestCallToolListsConfiguredProvidersWithRedactedMetadata(t *testing.T) {
	service := newIntegrationService(t, nil)

	result, err := mcpapi.CallTool(context.Background(), service, "projects.integrations.list", json.RawMessage(`{"id":"project-1"}`))
	if err != nil {
		t.Fatalf("list integrations: %v", err)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"Provider":"jira"`, `"AllowlistCount":2`, `"CredentialSource":"env"`, `"Provider":"confluence"`, `"CredentialSource":"file"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in redacted list response: %s", expected, body)
		}
	}
	assertOmits(t, body, forbiddenIntegrationStrings()...)
}

func TestCallToolStatusIncludesSyncMetadataWithoutRawCursor(t *testing.T) {
	store := &fakeStore{
		sources: []projectintegrations.SourceMetadata{{
			ProjectID:      "project-1",
			Provider:       projectintegrations.ProviderJira,
			SiteURLHash:    "hash-site-url",
			AllowlistHash:  "hash-allowlist",
			AllowlistCount: 2,
		}},
		state: projectintegrations.SyncState{
			ProjectID:           "project-1",
			Provider:            projectintegrations.ProviderJira,
			LastRunID:           "run-1",
			LastSuccessfulRunID: "run-1",
			EmptyPollCount:      1,
			CurrentIdleSleep:    5 * time.Minute,
			CursorHash:          "sha256:raw-provider-cursor-token",
			UpdatedAt:           testTime(),
		},
		run: projectintegrations.SyncRun{
			ID:            "run-1",
			ProjectID:     "project-1",
			Provider:      projectintegrations.ProviderJira,
			Kind:          projectintegrations.SyncKindIncremental,
			Status:        projectintegrations.SyncRunStatusNoOp,
			ItemsSeen:     0,
			ItemsUpserted: 0,
			EmptyPoll:     true,
			IdleSleep:     5 * time.Minute,
		},
	}
	service := newIntegrationService(t, store)

	result, err := mcpapi.CallTool(context.Background(), service, "projects_integrations_status", json.RawMessage(`{"id":"project-1","provider":"jira"}`))
	if err != nil {
		t.Fatalf("integration status: %v", err)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"Provider":"jira"`, `"CursorHashPresent":true`, `"Status":"no_op"`, `"EmptyPoll":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in status response: %s", expected, body)
		}
	}
	assertOmits(t, body, append(forbiddenIntegrationStrings(), "raw-provider-cursor-token", "sha256:")...)
}

func TestCallToolMissingErrorsAreStableAndRedacted(t *testing.T) {
	service := newIntegrationService(t, nil)

	_, err := mcpapi.CallTool(context.Background(), service, "projects.integrations.status", json.RawMessage(`{"id":"missing","provider":"jira"}`))
	if !errors.Is(err, projectintegrations.ErrNotFound) {
		t.Fatalf("expected missing project error, got %v", err)
	}
	assertOmits(t, err.Error(), forbiddenIntegrationStrings()...)

	_, err = mcpapi.CallTool(context.Background(), service, "projects.integrations.status", json.RawMessage(`{"id":"project-1","provider":"github"}`))
	if !errors.Is(err, projectintegrations.ErrInvalidInput) {
		t.Fatalf("expected invalid provider error, got %v", err)
	}
	assertOmits(t, err.Error(), forbiddenIntegrationStrings()...)
}

func newIntegrationService(t *testing.T, store projectintegrations.Store) *projectintegrations.Service {
	t.Helper()
	service, err := projectintegrations.NewService([]config.Project{testIntegrationProject()}, store)
	if err != nil {
		t.Fatalf("new integration service: %v", err)
	}
	return service
}

func testIntegrationProject() config.Project {
	return config.Project{
		ID:       "project-1",
		RootPath: "/home/mac/mivialabs/mivialabs-agents-monorepo",
		Integrations: config.IntegrationConfig{
			Jira: &config.JiraIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "cloud-id-1",
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
				CloudID:    "cloud-id-1",
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

func forbiddenIntegrationStrings() []string {
	return []string{
		"https://tenant.atlassian.net",
		"cloud-id-1",
		"ACME",
		"OPS",
		"ENG",
		"TEAM",
		"MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
		"MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
		"/home/mac/secret-email",
		"/home/mac/secret-token",
		"/home/mac/mivialabs/mivialabs-agents-monorepo",
		"jira rich description",
		"confluence page body",
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

func assertOmits(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(body, value) {
			t.Fatalf("payload leaked %q: %s", value, body)
		}
	}
}

func testTime() time.Time {
	return time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
}

type fakeStore struct {
	sources []projectintegrations.SourceMetadata
	state   projectintegrations.SyncState
	run     projectintegrations.SyncRun
}

func (store *fakeStore) UpsertSource(context.Context, projectintegrations.SourceMetadataInput) (projectintegrations.SourceMetadata, error) {
	return projectintegrations.SourceMetadata{}, projectintegrations.ErrNotFound
}

func (store *fakeStore) ListSources(context.Context, string) ([]projectintegrations.SourceMetadata, error) {
	return append([]projectintegrations.SourceMetadata(nil), store.sources...), nil
}

func (store *fakeStore) GetSyncState(context.Context, string, projectintegrations.Provider) (projectintegrations.SyncState, error) {
	if store.state.ProjectID == "" {
		return projectintegrations.SyncState{}, projectintegrations.ErrNotFound
	}
	return store.state, nil
}

func (store *fakeStore) GetSyncRun(context.Context, string, projectintegrations.Provider, string) (projectintegrations.SyncRun, error) {
	if store.run.ID == "" {
		return projectintegrations.SyncRun{}, projectintegrations.ErrNotFound
	}
	return store.run, nil
}
