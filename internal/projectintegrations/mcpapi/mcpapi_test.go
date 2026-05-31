package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations/mcpapi"
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

func TestCallToolStatusIncludesActiveRunMetadata(t *testing.T) {
	store := &fakeStore{
		state: projectintegrations.SyncState{
			ProjectID:           "project-1",
			Provider:            projectintegrations.ProviderJira,
			LastRunID:           "run-completed",
			LastSuccessfulRunID: "run-completed",
			CursorHash:          "sha256:raw-provider-cursor-token",
			UpdatedAt:           testTime().Add(-time.Hour),
		},
		run: projectintegrations.SyncRun{
			ID:        "run-completed",
			ProjectID: "project-1",
			Provider:  projectintegrations.ProviderJira,
			Kind:      projectintegrations.SyncKindIncremental,
			Status:    projectintegrations.SyncRunStatusCompleted,
		},
		activeRun: projectintegrations.SyncRun{
			ID:            "run-active",
			ProjectID:     "project-1",
			Provider:      projectintegrations.ProviderJira,
			Kind:          projectintegrations.SyncKindInitialFull,
			Status:        projectintegrations.SyncRunStatusRunning,
			ItemsSeen:     4,
			ItemsUpserted: 4,
			StartedAt:     testTime(),
		},
	}
	service := newIntegrationService(t, store)

	result, err := mcpapi.CallTool(context.Background(), service, "projects.integrations.status", json.RawMessage(`{"id":"project-1","provider":"jira"}`))
	if err != nil {
		t.Fatalf("integration status: %v", err)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"ID":"run-active"`, `"Kind":"initial_full"`, `"Status":"running"`, `"ItemsSeen":4`, `"LastRunID":"run-completed"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in status response: %s", expected, body)
		}
	}
	assertOmits(t, body, append(forbiddenIntegrationStrings(), "raw-provider-cursor-token", "sha256:")...)
}

func TestCallToolPollQueuesAsyncRunAndReturnsRedactedRunMetadata(t *testing.T) {
	runner := &fakePollRunner{
		result: projectintegrations.PollRunResult{
			Run: projectintegrations.SyncRun{
				ID:        "run-1",
				ProjectID: "project-1",
				Provider:  projectintegrations.ProviderJira,
				Kind:      projectintegrations.SyncKindIncremental,
				Status:    projectintegrations.SyncRunStatusPending,
				StartedAt: testTime(),
			},
		},
	}
	service := newIntegrationServiceWithRunner(t, nil, runner)

	result, err := mcpapi.CallTool(context.Background(), service, "projects.integrations.poll", json.RawMessage(`{"id":"project-1","provider":"jira","kind":"incremental"}`))
	if err != nil {
		t.Fatalf("poll integration: %v", err)
	}
	if runner.projectID != "project-1" || runner.provider != projectintegrations.ProviderJira || runner.kind != projectintegrations.SyncKindIncremental || !runner.submitCalled {
		t.Fatalf("async runner received unexpected input: %#v", runner)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"Provider":"jira"`, `"Accepted":true`, `"Status":"pending"`, `"ID":"run-1"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in poll response: %s", expected, body)
		}
	}
	assertOmits(t, body, append(forbiddenIntegrationStrings(), "raw-provider-cursor-token", "sha256:", "ISSUE-1", "PAGE-1")...)
}

func TestCallToolPollStatusReturnsRunByID(t *testing.T) {
	store := &fakeStore{
		state: projectintegrations.SyncState{
			ProjectID:  "project-1",
			Provider:   projectintegrations.ProviderJira,
			CursorHash: "sha256:raw-provider-cursor-token",
			UpdatedAt:  testTime(),
		},
		run: projectintegrations.SyncRun{
			ID:        "run-1",
			ProjectID: "project-1",
			Provider:  projectintegrations.ProviderJira,
			Kind:      projectintegrations.SyncKindInitialFull,
			Status:    projectintegrations.SyncRunStatusRunning,
			StartedAt: testTime(),
		},
	}
	service := newIntegrationService(t, store)

	result, err := mcpapi.CallTool(context.Background(), service, "projects.integrations.poll_status", json.RawMessage(`{"id":"project-1","provider":"jira","run_id":"run-1"}`))
	if err != nil {
		t.Fatalf("poll status: %v", err)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"Provider":"jira"`, `"Status":"running"`, `"ID":"run-1"`, `"CursorHashPresent":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in poll status response: %s", expected, body)
		}
	}
	assertOmits(t, body, append(forbiddenIntegrationStrings(), "raw-provider-cursor-token", "sha256:")...)
}

func TestCallToolSearchLocalIntegrationContent(t *testing.T) {
	rich := &fakeRichContentReader{
		search: []projectintegrations.RichContentSearchResult{{
			Artifact: projectintegrations.RichContentArtifact{
				ID:        "artifact-1",
				ProjectID: "project-1",
				Provider:  projectintegrations.ProviderJira,
				ItemID:    "10001",
				ItemKey:   "LOCAL-1",
				ItemType:  "Task",
			},
			Chunk: projectintegrations.RichContentChunkView{
				ID:        "chunk-1",
				ProjectID: "project-1",
				Provider:  projectintegrations.ProviderJira,
				FieldName: "summary",
			},
			Snippet: "bounded local result",
		}},
	}
	service := newIntegrationServiceWithRunnerAndRichContent(t, nil, nil, rich)

	result, err := mcpapi.CallTool(context.Background(), service, "projects.integrations.search", json.RawMessage(`{"id":"project-1","provider":"jira","query":"local","max_results":5}`))
	if err != nil {
		t.Fatalf("search integration content: %v", err)
	}
	if rich.searchInput.ProjectID != "project-1" || rich.searchInput.Provider != projectintegrations.ProviderJira || rich.searchInput.Query != "local" || rich.searchInput.MaxResults != 5 {
		t.Fatalf("unexpected search input: %#v", rich.searchInput)
	}
	body := mustJSON(t, result)
	for _, expected := range []string{`"Provider":"jira"`, `"ItemKey":"LOCAL-1"`, `"bounded local result"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected %q in search response: %s", expected, body)
		}
	}
	assertOmits(t, body, forbiddenIntegrationStrings()...)
}

func TestCallToolReadsLocalJiraAndConfluenceContent(t *testing.T) {
	rich := &fakeRichContentReader{
		read: projectintegrations.RichContentReadResult{
			Artifact: projectintegrations.RichContentArtifact{
				ID:        "artifact-1",
				ProjectID: "project-1",
				Provider:  projectintegrations.ProviderJira,
				ItemID:    "10001",
				ItemKey:   "LOCAL-1",
				ItemType:  "Task",
			},
			Chunks: []projectintegrations.RichContentChunkView{{
				ID:        "chunk-1",
				ProjectID: "project-1",
				Provider:  projectintegrations.ProviderJira,
				FieldName: "summary",
				Text:      "bounded local issue text",
			}},
		},
	}
	service := newIntegrationServiceWithRunnerAndRichContent(t, nil, nil, rich)

	result, err := mcpapi.CallTool(context.Background(), service, "projects.jira.issue.get", json.RawMessage(`{"id":"project-1","key":"LOCAL-1","max_chunk_bytes":64}`))
	if err != nil {
		t.Fatalf("read jira content: %v", err)
	}
	if rich.readInput.ProjectID != "project-1" || rich.readInput.Provider != projectintegrations.ProviderJira || rich.readInput.ItemIDOrKey != "LOCAL-1" || rich.readInput.MaxChunkBytes != 64 {
		t.Fatalf("unexpected jira read input: %#v", rich.readInput)
	}
	body := mustJSON(t, result)
	if !strings.Contains(body, "bounded local issue text") || !strings.Contains(body, `"ItemKey":"LOCAL-1"`) {
		t.Fatalf("expected bounded read response, got %s", body)
	}
	assertOmits(t, body, forbiddenIntegrationStrings()...)

	_, err = mcpapi.CallTool(context.Background(), service, "projects.confluence.page.get", json.RawMessage(`{"id":"project-1","page_id":"20001"}`))
	if err != nil {
		t.Fatalf("read confluence content: %v", err)
	}
	if rich.readInput.Provider != projectintegrations.ProviderConfluence || rich.readInput.ItemIDOrKey != "20001" {
		t.Fatalf("unexpected confluence read input: %#v", rich.readInput)
	}
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
	return newIntegrationServiceWithRunner(t, store, nil)
}

func newIntegrationServiceWithRunner(t *testing.T, store projectintegrations.Store, runner projectintegrations.PollRunner) *projectintegrations.Service {
	t.Helper()
	return newIntegrationServiceWithRunnerAndRichContent(t, store, runner, nil)
}

func newIntegrationServiceWithRunnerAndRichContent(t *testing.T, store projectintegrations.Store, runner projectintegrations.PollRunner, rich projectintegrations.RichContentReader) *projectintegrations.Service {
	t.Helper()
	service, err := projectintegrations.NewServiceWithOptions([]config.Project{testIntegrationProject()}, store, projectintegrations.ServiceOptions{Runner: runner, RichContent: rich})
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

type fakePollRunner struct {
	projectID    string
	provider     projectintegrations.Provider
	kind         projectintegrations.SyncKind
	result       projectintegrations.PollRunResult
	err          error
	submitCalled bool
}

func (runner *fakePollRunner) RunProviderPoll(_ context.Context, projectID string, provider projectintegrations.Provider, kind projectintegrations.SyncKind) (projectintegrations.PollRunResult, error) {
	runner.projectID = projectID
	runner.provider = provider
	runner.kind = kind
	if runner.err != nil {
		return projectintegrations.PollRunResult{}, runner.err
	}
	return runner.result, nil
}

func (runner *fakePollRunner) SubmitProviderPoll(_ context.Context, projectID string, provider projectintegrations.Provider, kind projectintegrations.SyncKind) (projectintegrations.SyncRun, error) {
	runner.submitCalled = true
	runner.projectID = projectID
	runner.provider = provider
	runner.kind = kind
	if runner.err != nil {
		return projectintegrations.SyncRun{}, runner.err
	}
	return runner.result.Run, nil
}

type fakeRichContentReader struct {
	searchInput projectintegrations.LocalSearchInput
	readInput   projectintegrations.LocalReadInput
	search      []projectintegrations.RichContentSearchResult
	read        projectintegrations.RichContentReadResult
	err         error
}

func (reader *fakeRichContentReader) SearchRichContent(_ context.Context, projectID string, options projectintegrations.RichContentSearchOptions) ([]projectintegrations.RichContentSearchResult, error) {
	reader.searchInput = projectintegrations.LocalSearchInput{
		ProjectID:       projectID,
		Provider:        options.Provider,
		Query:           options.Query,
		MaxResults:      options.MaxResults,
		MaxSnippetBytes: options.MaxSnippetBytes,
		CaseSensitive:   options.CaseSensitive,
	}
	if reader.err != nil {
		return nil, reader.err
	}
	return reader.search, nil
}

func (reader *fakeRichContentReader) GetRichContentItem(_ context.Context, projectID string, provider projectintegrations.Provider, itemIDOrKey string, options projectintegrations.RichContentReadOptions) (projectintegrations.RichContentReadResult, error) {
	reader.readInput = projectintegrations.LocalReadInput{
		ProjectID:     projectID,
		Provider:      provider,
		ItemIDOrKey:   itemIDOrKey,
		MaxChunkBytes: options.MaxChunkBytes,
	}
	if reader.err != nil {
		return projectintegrations.RichContentReadResult{}, reader.err
	}
	return reader.read, nil
}

type fakeStore struct {
	sources   []projectintegrations.SourceMetadata
	state     projectintegrations.SyncState
	run       projectintegrations.SyncRun
	activeRun projectintegrations.SyncRun
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

func (store *fakeStore) GetActiveSyncRun(context.Context, string, projectintegrations.Provider) (projectintegrations.SyncRun, error) {
	if store.activeRun.ID == "" {
		return projectintegrations.SyncRun{}, projectintegrations.ErrNotFound
	}
	return store.activeRun, nil
}
