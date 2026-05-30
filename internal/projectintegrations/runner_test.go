package projectintegrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
)

const (
	testEmailValue = "FORBIDDEN_EMAIL_MARKER"
	testAPIValue   = "FORBIDDEN_API_MARKER"
)

func TestRunner_RunProviderPollCompletesAndStoresMetadataOnly(t *testing.T) {
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
	if result.Run.Status != SyncRunStatusCompleted || result.Run.ItemsSeen != 2 || result.Run.ItemsUpserted != 2 || result.Run.EmptyPoll {
		t.Fatalf("unexpected run: %#v", result.Run)
	}
	if result.State.LastFullSyncAt.IsZero() || result.State.EmptyPollCount != 0 || result.State.CurrentIdleSleep != 0 {
		t.Fatalf("unexpected state: %#v", result.State)
	}
	if len(recorder.created) != 1 || recorder.created[0].Status != SyncRunStatusPending {
		t.Fatalf("expected pending create, got %#v", recorder.created)
	}
	assertRunStatusSequence(t, recorder.updated, SyncRunStatusRunning, SyncRunStatusCompleted)
	if jira.plan.ProjectID != "project-1" || jira.plan.Kind != SyncKindInitialFull || jira.plan.PageSize != 50 || jira.plan.MaxResults != 100 {
		t.Fatalf("planner output was not consumed: %#v", jira.plan)
	}
	if jira.credentials.Email != testEmailValue || jira.credentials.APIToken != testAPIValue {
		t.Fatalf("credentials were not resolved at call time")
	}
	assertTableOmits(t, db, "project_integration_sources", "https://example.atlassian.net", "ACME", "ENG")
	assertTableOmits(t, db, "project_integration_items", "10001", "10002", "ACME-1", "ACME-2", "FORBIDDEN_REMOTE_BODY_MARKER", "page body", "comment text")
	assertNoSensitiveText(t, fmt.Sprintf("%#v", result), testEmailValue, testAPIValue, "10001", "ACME-1", "FORBIDDEN_REMOTE_BODY_MARKER")
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
