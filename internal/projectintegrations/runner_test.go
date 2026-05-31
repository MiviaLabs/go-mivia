package projectintegrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

const (
	testEmailValue = "FORBIDDEN_EMAIL_MARKER"
	testAPIValue   = "FORBIDDEN_API_MARKER"
)

func TestRunner_RunProviderPollCompletesAndStoresApprovedProviderIdentifiers(t *testing.T) {
	ctx := context.Background()
	recorder, db := newRecordingSQLiteStore(t)
	clock := newStepClock(testTime())
	jira := &fakeJiraPoller{
		result: PollResult{Items: []PollItem{
			{ID: "10001", Key: "ACME-1", Type: "issue", Status: "updated", UpdatedAt: testTime()},
			{ID: "10002", Key: "ACME-2", Type: "issue", Status: "updated", UpdatedAt: testTime().Add(time.Minute)},
		}},
	}
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		JiraClient: jira,
		Now:        clock.Now,
		NewRunID:   fixedRunID("run-completed"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, "")
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.Run.Status != SyncRunStatusCompleted || result.Run.ItemsSeen != 2 || result.Run.ItemsUpserted != 2 || result.Run.ItemsChanged != 2 || result.Run.ItemsUnchanged != 0 || result.Run.EmptyPoll {
		t.Fatalf("unexpected run: %#v", result.Run)
	}
	if result.State.LastFullSyncAt.IsZero() || result.State.EmptyPollCount != 0 || result.State.CurrentIdleSleep != 0 {
		t.Fatalf("unexpected state: %#v", result.State)
	}
	if len(recorder.created) != 1 || recorder.created[0].Status != SyncRunStatusPending {
		t.Fatalf("expected pending create, got %#v", recorder.created)
	}
	assertRunStatusSequence(t, recorder.updated, SyncRunStatusRunning, SyncRunStatusRunning, SyncRunStatusRunning, SyncRunStatusCompleted)
	if recorder.updated[1].ItemsSeen != 1 || recorder.updated[2].ItemsSeen != 2 {
		t.Fatalf("expected incremental running progress, got %#v", recorder.updated)
	}
	if jira.plan.ProjectID != "project-1" || jira.plan.Kind != SyncKindInitialFull || jira.plan.PageSize != 50 || jira.plan.MaxResults != 100 {
		t.Fatalf("planner output was not consumed: %#v", jira.plan)
	}
	if jira.credentials.Email != testEmailValue || jira.credentials.APIToken != testAPIValue {
		t.Fatalf("credentials were not resolved at call time")
	}
	assertTableOmits(t, db, "project_integration_sources", "https://example.atlassian.net", "ACME", "ENG")
	assertTableOmits(t, db, "project_integration_items", "FORBIDDEN_REMOTE_BODY_MARKER", "page body", "comment text", testEmailValue, testAPIValue)
	assertNoSensitiveText(t, fmt.Sprintf("%#v", result), testEmailValue, testAPIValue, "10001", "ACME-1", "FORBIDDEN_REMOTE_BODY_MARKER")
}

func TestRunner_RunProviderPollCountsUnchangedOverlapItems(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	items := []PollItem{
		{ID: "10001", Key: "ACME-1", Type: "issue", Status: "updated", UpdatedAt: testTime()},
		{ID: "10002", Key: "ACME-2", Type: "issue", Status: "updated", UpdatedAt: testTime()},
	}
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		JiraClient: &fakeJiraPoller{result: PollResult{Items: items}},
		Now:        newStepClock(testTime()).Now,
		NewRunID:   fixedRunID("run-first"),
	})
	first, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindInitialFull)
	if err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if first.Run.ItemsChanged != 2 || first.Run.ItemsUnchanged != 0 {
		t.Fatalf("unexpected first counters: %#v", first.Run)
	}

	runner.newRunID = fixedRunID("run-second")
	second, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindIncremental)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if second.Run.ItemsSeen != 2 || second.Run.ItemsUpserted != 0 || second.Run.ItemsChanged != 0 || second.Run.ItemsUnchanged != 2 {
		t.Fatalf("unexpected unchanged counters: %#v", second.Run)
	}
}

func TestRunner_RunProviderPollPersistsApprovedCursor(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		JiraClient: &fakeJiraPoller{result: PollResult{Cursor: "next-page-token-123"}},
		Now:        newStepClock(testTime()).Now,
		NewRunID:   fixedRunID("run-cursor"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindIncremental)
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.State.Cursor != "next-page-token-123" || !strings.HasPrefix(result.State.CursorHash, "sha256:") {
		t.Fatalf("expected raw cursor plus hash in local state, got %#v", result.State)
	}
	assertNoSensitiveText(t, fmt.Sprintf("%#v", result.Run), "next-page-token-123", testEmailValue, testAPIValue)
}

func TestRunner_RunProviderPollWritesRichContentToGraphStore(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	richStore := &fakeRichContentStore{}
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		RichContentStore: richStore,
		JiraClient: &fakeJiraPoller{result: PollResult{
			Items: []PollItem{{ID: "10001", Key: "ACME-1", Type: "Task", Status: "Open", UpdatedAt: testTime()}},
			RichContent: []RichContentPayload{{
				Item: RichContentItem{
					ProjectID: "project-1",
					Provider:  ProviderJira,
					ItemID:    "10001",
					ItemKey:   "ACME-1",
					ItemType:  "Task",
					Fields:    []RichContentField{{Name: "summary", Text: "Safe summary"}},
				},
				Chunks: []RichContentChunk{{FieldName: "summary", Text: "Safe summary"}},
			}},
		}},
		Now:      newStepClock(testTime()).Now,
		NewRunID: fixedRunID("run-rich-content"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindInitialFull)
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.Run.Status != SyncRunStatusCompleted || len(richStore.items) != 1 {
		t.Fatalf("expected completed run with rich content write, run=%#v writes=%#v", result.Run, richStore.items)
	}
	if result.Run.RichContentChanged != 1 || result.Run.RichContentUnchanged != 0 {
		t.Fatalf("unexpected rich content counters: %#v", result.Run)
	}
	if richStore.items[0].ItemID != "10001" || richStore.chunks[0][0].Text != "Safe summary" {
		t.Fatalf("unexpected rich content write: items=%#v chunks=%#v", richStore.items, richStore.chunks)
	}
	assertNoSensitiveText(t, fmt.Sprintf("%#v", result), "Safe summary", testEmailValue, testAPIValue)
}

func TestRunner_RunProviderPollRichContentGraphErrorsAreRedactedAndDoNotAdvanceState(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		RichContentStore: &fakeRichContentStore{err: fmt.Errorf("graph failed FORBIDDEN_REMOTE_BODY_MARKER ACME-1 %s", testAPIValue)},
		JiraClient: &fakeJiraPoller{result: PollResult{
			Items: []PollItem{{ID: "10001", Key: "ACME-1", Type: "Task", Status: "Open", UpdatedAt: testTime()}},
			RichContent: []RichContentPayload{{
				Item:   RichContentItem{ProjectID: "project-1", Provider: ProviderJira, ItemID: "10001"},
				Chunks: []RichContentChunk{{FieldName: "summary", Text: "FORBIDDEN_REMOTE_BODY_MARKER"}},
			}},
		}},
		Now:      newStepClock(testTime()).Now,
		NewRunID: fixedRunID("run-rich-content-failed"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindInitialFull)
	if err == nil {
		t.Fatal("expected rich content graph write error")
	}
	if result.Run.Status != SyncRunStatusFailed || result.Run.ErrorCategory != string(ErrorCategoryRequestFailed) {
		t.Fatalf("unexpected failed run: %#v", result.Run)
	}
	if _, stateErr := recorder.GetSyncState(ctx, "project-1", ProviderJira); !errors.Is(stateErr, ErrNotFound) {
		t.Fatalf("sync state should not advance after graph failure, got %v", stateErr)
	}
	assertNoSensitiveText(t, err.Error(), "FORBIDDEN_REMOTE_BODY_MARKER", "ACME-1", testAPIValue)
	assertRunStatusSequence(t, recorder.updated, SyncRunStatusRunning, SyncRunStatusRunning, SyncRunStatusFailed)
}

func TestRunner_RunProviderPollNoOpIncrementalPersistsIdleSleep(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	if _, err := recorder.UpdateSyncState(ctx, SyncStateInput{
		ProjectID:           "project-1",
		Provider:            ProviderJira,
		LastSuccessfulRunID: "run-before",
		LastSuccessAt:       testTime().Add(-time.Hour),
		LastFullSyncAt:      testTime().Add(-time.Hour),
		UpdatedAt:           testTime().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		JiraClient: &fakeJiraPoller{result: PollResult{}},
		Now:        newStepClock(testTime()).Now,
		NewRunID:   fixedRunID("run-noop"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindIncremental)
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.Run.Status != SyncRunStatusNoOp || !result.Run.EmptyPoll || result.Run.IdleSleep != 5*time.Minute {
		t.Fatalf("unexpected no-op run: %#v", result.Run)
	}
	if result.State.EmptyPollCount != 1 || result.State.CurrentIdleSleep != 5*time.Minute || result.State.LastEmptyPollAt.IsZero() {
		t.Fatalf("unexpected no-op state: %#v", result.State)
	}
	assertRunStatusSequence(t, recorder.updated, SyncRunStatusRunning, SyncRunStatusNoOp)
}

func TestRunner_RunProviderPollUpdatesRunningProgressWhilePersistingItems(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		JiraClient: &fakeJiraPoller{result: PollResult{Items: []PollItem{
			{ID: "10001", Key: "ACME-1", Type: "Task", Status: "Open", UpdatedAt: testTime()},
			{ID: "10002", Key: "ACME-2", Type: "Task", Status: "Open", UpdatedAt: testTime()},
			{ID: "10003", Key: "ACME-3", Type: "Task", Status: "Open", UpdatedAt: testTime()},
		}}},
		Now:      newStepClock(testTime()).Now,
		NewRunID: fixedRunID("run-progress"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindInitialFull)
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.Run.ItemsSeen != 3 || result.Run.ItemsUpserted != 3 {
		t.Fatalf("unexpected final progress: %#v", result.Run)
	}
	assertRunStatusSequence(t, recorder.updated, SyncRunStatusRunning, SyncRunStatusRunning, SyncRunStatusRunning, SyncRunStatusRunning, SyncRunStatusCompleted)
	for i, run := range recorder.updated[1:4] {
		expected := i + 1
		if run.ItemsSeen != expected || run.ItemsUpserted != expected || run.Status != SyncRunStatusRunning {
			t.Fatalf("update %d: expected running progress %d, got %#v", i+1, expected, run)
		}
	}
}

func TestRunner_RunProviderPollClampsEmptyPollIdleSleep(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	if _, err := recorder.UpdateSyncState(ctx, SyncStateInput{
		ProjectID:           "project-1",
		Provider:            ProviderJira,
		LastSuccessfulRunID: "run-before",
		LastSuccessAt:       testTime().Add(-time.Hour),
		LastFullSyncAt:      testTime().Add(-time.Hour),
		EmptyPollCount:      2,
		CurrentIdleSleep:    8 * time.Minute,
		UpdatedAt:           testTime().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	project := runnerTestProject()
	project.Integrations.Jira.Polling.EmptyPollSleep = 5 * time.Minute
	project.Integrations.Jira.Polling.MaxIdleSleep = 10 * time.Minute
	runner := newTestRunner(t, recorder, project, RunnerOptions{
		JiraClient: &fakeJiraPoller{result: PollResult{}},
		Now:        newStepClock(testTime()).Now,
		NewRunID:   fixedRunID("run-clamped"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindIncremental)
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.Run.IdleSleep != 10*time.Minute || result.State.CurrentIdleSleep != 10*time.Minute {
		t.Fatalf("expected clamped idle sleep, got run=%s state=%s", result.Run.IdleSleep, result.State.CurrentIdleSleep)
	}
}

func TestRunner_RunProviderPollFailsWithRedactedError(t *testing.T) {
	ctx := context.Background()
	recorder, db := newRecordingSQLiteStore(t)
	leaky := fmt.Errorf("provider failed with %s %s FORBIDDEN_REMOTE_BODY_MARKER ACME-1 FORBIDDEN_FILE_REF", testEmailValue, testAPIValue)
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		JiraClient: &fakeJiraPoller{err: leaky},
		Now:        newStepClock(testTime()).Now,
		NewRunID:   fixedRunID("run-failed"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindIncremental)
	if err == nil {
		t.Fatal("expected poll error")
	}
	if result.Run.Status != SyncRunStatusFailed || result.Run.ErrorCategory != string(ErrorCategoryRequestFailed) {
		t.Fatalf("unexpected failed run: %#v", result.Run)
	}
	assertNoSensitiveText(t, err.Error(), testEmailValue, testAPIValue, "FORBIDDEN_REMOTE_BODY_MARKER", "ACME-1", "FORBIDDEN_FILE_REF")
	assertRunStatusSequence(t, recorder.updated, SyncRunStatusRunning, SyncRunStatusFailed)
	assertTableOmits(t, db, "project_integration_sync_runs", testEmailValue, testAPIValue, "FORBIDDEN_REMOTE_BODY_MARKER", "ACME-1", "FORBIDDEN_FILE_REF")
}

func TestRunner_RunProviderPollCredentialErrorsAreRedacted(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	project := runnerTestProject()
	project.Integrations.Jira.CredentialRefs.APITokenEnv = "FORBIDDEN_API_ENV_NAME"
	runner := newTestRunner(t, recorder, project, RunnerOptions{
		JiraClient: &fakeJiraPoller{},
		CredentialResolver: CredentialResolver{LookupEnv: func(name string) (string, bool) {
			if name == "JIRA_EMAIL_ENV" {
				return testEmailValue, true
			}
			return "", false
		}},
		Now:      newStepClock(testTime()).Now,
		NewRunID: fixedRunID("run-credential-failed"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderJira, SyncKindIncremental)
	if err == nil {
		t.Fatal("expected credential error")
	}
	if result.Run.Status != SyncRunStatusFailed || result.Run.ErrorCategory != string(ErrorCategoryCredentialUnavailable) {
		t.Fatalf("unexpected failed run: %#v", result.Run)
	}
	assertNoSensitiveText(t, err.Error(), "FORBIDDEN_API_ENV_NAME", "JIRA_EMAIL_ENV", testEmailValue, testAPIValue)
}

func TestRunner_RunProviderPollConfluenceUsesPlannerWithoutRichDefaults(t *testing.T) {
	ctx := context.Background()
	recorder, _ := newRecordingSQLiteStore(t)
	confluence := &fakeConfluencePoller{}
	runner := newTestRunner(t, recorder, runnerTestProject(), RunnerOptions{
		ConfluenceClient: confluence,
		Now:              newStepClock(testTime()).Now,
		NewRunID:         fixedRunID("run-confluence"),
	})

	result, err := runner.RunProviderPoll(ctx, "project-1", ProviderConfluence, SyncKindInitialFull)
	if err != nil {
		t.Fatalf("run poll: %v", err)
	}
	if result.Run.Status != SyncRunStatusNoOp || confluence.plan.Provider != ProviderConfluence || len(confluence.plan.SpaceKeys) != 2 {
		t.Fatalf("unexpected confluence run/plan: run=%#v plan=%#v", result.Run, confluence.plan)
	}
	if confluence.plan.IncludeBody || confluence.plan.IncludeComments || confluence.plan.IncludeLabels || confluence.plan.IncludeProperties {
		t.Fatalf("rich content flags should remain disabled by default: %#v", confluence.plan)
	}
}

type recordingSQLiteStore struct {
	*SQLiteStore
	created []SyncRun
	updated []SyncRun
}

func newRecordingSQLiteStore(t *testing.T) (*recordingSQLiteStore, *sql.DB) {
	t.Helper()
	store, db := newTestSQLiteStore(t)
	return &recordingSQLiteStore{SQLiteStore: store}, db
}

func (store *recordingSQLiteStore) CreateSyncRun(ctx context.Context, run SyncRun) error {
	store.created = append(store.created, run)
	return store.SQLiteStore.CreateSyncRun(ctx, run)
}

func (store *recordingSQLiteStore) UpdateSyncRun(ctx context.Context, run SyncRun) error {
	store.updated = append(store.updated, run)
	return store.SQLiteStore.UpdateSyncRun(ctx, run)
}

type fakeJiraPoller struct {
	credentials Credentials
	plan        JiraQueryPlan
	result      PollResult
	err         error
}

func (poller *fakeJiraPoller) PollJira(_ context.Context, credentials Credentials, plan JiraQueryPlan) (PollResult, error) {
	poller.credentials = credentials
	poller.plan = plan
	if poller.err != nil {
		return PollResult{}, poller.err
	}
	return poller.result, nil
}

type fakeConfluencePoller struct {
	credentials Credentials
	plan        ConfluenceQueryPlan
	result      PollResult
	err         error
}

func (poller *fakeConfluencePoller) PollConfluence(_ context.Context, credentials Credentials, plan ConfluenceQueryPlan) (PollResult, error) {
	poller.credentials = credentials
	poller.plan = plan
	if poller.err != nil {
		return PollResult{}, poller.err
	}
	return poller.result, nil
}

type fakeRichContentStore struct {
	items  []RichContentItem
	chunks [][]RichContentChunk
	err    error
}

func (store *fakeRichContentStore) PutRichContentItem(_ context.Context, item RichContentItem, chunks []RichContentChunk) (RichContentGraphResult, error) {
	if store.err != nil {
		return RichContentGraphResult{}, store.err
	}
	store.items = append(store.items, item)
	store.chunks = append(store.chunks, append([]RichContentChunk(nil), chunks...))
	return RichContentGraphResult{ArtifactID: "artifact-" + item.ItemID, ChunksWritten: len(chunks), ContentSHA256: "sha256:test", Changed: true}, nil
}

type stepClock struct {
	next time.Time
}

func newStepClock(start time.Time) *stepClock {
	return &stepClock{next: start}
}

func (clock *stepClock) Now() time.Time {
	now := clock.next
	clock.next = clock.next.Add(time.Minute)
	return now
}

func fixedRunID(id string) func() string {
	return func() string { return id }
}

func newTestRunner(t *testing.T, store RunnerStore, project config.Project, overrides RunnerOptions) *Runner {
	t.Helper()
	resolver := overrides.CredentialResolver
	if resolver.LookupEnv == nil && resolver.ReadFile == nil {
		resolver = CredentialResolver{LookupEnv: func(name string) (string, bool) {
			switch name {
			case "JIRA_EMAIL_ENV", "CONFLUENCE_EMAIL_ENV":
				return testEmailValue, true
			case "JIRA_API_ENV", "CONFLUENCE_API_ENV":
				return testAPIValue, true
			default:
				return "", false
			}
		}}
	}
	options := RunnerOptions{
		Projects:           []config.Project{project},
		Store:              store,
		RichContentStore:   overrides.RichContentStore,
		CredentialResolver: resolver,
		JiraClient:         overrides.JiraClient,
		ConfluenceClient:   overrides.ConfluenceClient,
		Planner:            overrides.Planner,
		Now:                overrides.Now,
		NewRunID:           overrides.NewRunID,
	}
	if options.Now == nil {
		options.Now = newStepClock(testTime()).Now
	}
	if options.NewRunID == nil {
		options.NewRunID = fixedRunID("run-test")
	}
	runner, err := NewRunner(options)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return runner
}

func runnerTestProject() config.Project {
	return config.Project{
		ID: "project-1",
		Integrations: config.IntegrationConfig{
			Jira: &config.JiraIntegration{
				Enabled:  true,
				SiteURL:  "https://example.atlassian.net",
				CloudID:  "cloud-1",
				AuthMode: "basic",
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailEnv:    "JIRA_EMAIL_ENV",
					APITokenEnv: "JIRA_API_ENV",
				},
				MaxResults:     100,
				ProjectKeys:    []string{"ACME", "ENG"},
				DefaultFields:  []string{"summary", "updated"},
				AllowedFields:  []string{"description", "comment"},
				JQLExtraFilter: "statusCategory != Done",
				Polling: config.IntegrationPolling{
					IngestionEnabled:    false,
					InitialFullSync:     "manual",
					IncrementalInterval: time.Minute,
					EmptyPollSleep:      5 * time.Minute,
					MaxIdleSleep:        10 * time.Minute,
					OverlapWindow:       2 * time.Minute,
					InitialPageSize:     50,
					IncrementalPageSize: 25,
				},
			},
			Confluence: &config.ConfluenceIntegration{
				Enabled:  true,
				SiteURL:  "https://example.atlassian.net",
				CloudID:  "cloud-1",
				AuthMode: "basic",
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailEnv:    "CONFLUENCE_EMAIL_ENV",
					APITokenEnv: "CONFLUENCE_API_ENV",
				},
				MaxResults: 100,
				SpaceKeys:  []string{"docs", "eng"},
				Polling: config.IntegrationPolling{
					IngestionEnabled:    false,
					InitialFullSync:     "manual",
					IncrementalInterval: time.Minute,
					EmptyPollSleep:      5 * time.Minute,
					MaxIdleSleep:        10 * time.Minute,
					OverlapWindow:       2 * time.Minute,
					InitialPageSize:     50,
					IncrementalPageSize: 25,
				},
			},
		},
	}
}

func assertRunStatusSequence(t *testing.T, runs []SyncRun, statuses ...SyncRunStatus) {
	t.Helper()
	if len(runs) != len(statuses) {
		t.Fatalf("expected %d run updates, got %#v", len(statuses), runs)
	}
	for i, status := range statuses {
		if runs[i].Status != status {
			t.Fatalf("update %d: expected %s, got %#v", i, status, runs[i])
		}
	}
}

func assertNoSensitiveText(t *testing.T, text string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(text, value) {
			t.Fatalf("text leaked %q: %s", value, text)
		}
	}
}

func TestRunner_NewRejectsInvalidInputs(t *testing.T) {
	if _, err := NewRunner(RunnerOptions{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}
