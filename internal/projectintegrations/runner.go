package projectintegrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
)

type RunnerStore interface {
	UpsertSource(context.Context, SourceMetadataInput) (SourceMetadata, error)
	CreateSyncRun(context.Context, SyncRun) error
	UpdateSyncRun(context.Context, SyncRun) error
	GetSyncState(context.Context, string, Provider) (SyncState, error)
	UpdateSyncState(context.Context, SyncStateInput) (SyncState, error)
	UpsertItem(context.Context, ItemMetadataInput) (ItemMetadata, error)
}

type JiraPoller interface {
	PollJira(context.Context, Credentials, JiraQueryPlan) (PollResult, error)
}

type ConfluencePoller interface {
	PollConfluence(context.Context, Credentials, ConfluenceQueryPlan) (PollResult, error)
}

type Planner struct {
	Jira       func(JiraPlanInput) (JiraQueryPlan, error)
	Confluence func(ConfluencePlanInput) (ConfluenceQueryPlan, error)
}

type RunnerOptions struct {
	Projects           []config.Project
	Store              RunnerStore
	CredentialResolver CredentialResolver
	JiraClient         JiraPoller
	ConfluenceClient   ConfluencePoller
	Planner            Planner
	Now                func() time.Time
	NewRunID           func() string
}

type Runner struct {
	projects           map[string]config.Project
	store              RunnerStore
	credentialResolver CredentialResolver
	jiraClient         JiraPoller
	confluenceClient   ConfluencePoller
	planner            Planner
	now                func() time.Time
	newRunID           func() string
}

type PollItem struct {
	ID        string
	Key       string
	Type      string
	Status    string
	UpdatedAt time.Time
}

type PollResult struct {
	Items  []PollItem
	Cursor string
}

type PollRunResult struct {
	Run   SyncRun
	State SyncState
}

func NewRunner(options RunnerOptions) (*Runner, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: integration store unavailable", ErrInvalidInput)
	}
	projects := make(map[string]config.Project, len(options.Projects))
	for _, project := range options.Projects {
		id := strings.TrimSpace(project.ID)
		if id == "" {
			return nil, fmt.Errorf("%w: project id is empty", ErrInvalidInput)
		}
		if _, exists := projects[id]; exists {
			return nil, fmt.Errorf("%w: duplicate project id", ErrInvalidInput)
		}
		projects[id] = cloneConfigProject(project)
	}
	if options.Planner.Jira == nil {
		options.Planner.Jira = PlanJiraQuery
	}
	if options.Planner.Confluence == nil {
		options.Planner.Confluence = PlanConfluenceQuery
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewRunID == nil {
		options.NewRunID = newRandomRunID
	}
	return &Runner{
		projects:           projects,
		store:              options.Store,
		credentialResolver: options.CredentialResolver,
		jiraClient:         options.JiraClient,
		confluenceClient:   options.ConfluenceClient,
		planner:            options.Planner,
		now:                options.Now,
		newRunID:           options.NewRunID,
	}, nil
}

func (runner *Runner) RunProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (PollRunResult, error) {
	if runner == nil {
		return PollRunResult{}, fmt.Errorf("%w: runner is nil", ErrInvalidInput)
	}
	project, ok := runner.projects[strings.TrimSpace(projectID)]
	if !ok {
		return PollRunResult{}, fmt.Errorf("%w: project", ErrNotFound)
	}
	state, err := runner.store.GetSyncState(ctx, project.ID, provider)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PollRunResult{}, err
	}
	if errors.Is(err, ErrNotFound) {
		state = SyncState{}
	}
	plannedKind, err := planKind(kind, state)
	if err != nil {
		return PollRunResult{}, err
	}
	now := runner.now().UTC()
	run := SyncRun{
		ID:        strings.TrimSpace(runner.newRunID()),
		ProjectID: project.ID,
		Provider:  provider,
		Kind:      plannedKind,
		Status:    SyncRunStatusPending,
		StartedAt: now,
	}
	if run.ID == "" {
		return PollRunResult{}, fmt.Errorf("%w: run id is empty", ErrInvalidInput)
	}
	if err := runner.store.CreateSyncRun(ctx, run); err != nil {
		return PollRunResult{}, err
	}
	run.Status = SyncRunStatusRunning
	if err := runner.store.UpdateSyncRun(ctx, run); err != nil {
		return PollRunResult{}, err
	}
	result, pollErr := runner.pollProvider(ctx, project, provider, plannedKind, state)
	finishedAt := runner.now().UTC()
	if pollErr != nil {
		run.Status = SyncRunStatusFailed
		run.ErrorCategory = string(errorCategory(pollErr))
		run.FinishedAt = finishedAt
		_ = runner.store.UpdateSyncRun(ctx, run)
		return PollRunResult{Run: run}, redactedPollError(provider, run.ErrorCategory)
	}
	run.ItemsSeen = len(result.Items)
	upserted, err := runner.upsertItems(ctx, run, result.Items, finishedAt)
	if err != nil {
		run.Status = SyncRunStatusFailed
		run.ErrorCategory = string(ErrorCategoryRequestFailed)
		run.FinishedAt = finishedAt
		_ = runner.store.UpdateSyncRun(ctx, run)
		return PollRunResult{Run: run}, redactedPollError(provider, run.ErrorCategory)
	}
	run.ItemsUpserted = upserted
	run.EmptyPoll = len(result.Items) == 0
	run.IdleSleep = idleSleepForRun(plannedKind, run.EmptyPoll, state.CurrentIdleSleep, pollingFor(project, provider))
	if run.EmptyPoll {
		run.Status = SyncRunStatusNoOp
	} else {
		run.Status = SyncRunStatusCompleted
	}
	run.FinishedAt = finishedAt
	if err := runner.store.UpdateSyncRun(ctx, run); err != nil {
		return PollRunResult{}, err
	}
	updatedState, err := runner.store.UpdateSyncState(ctx, syncStateInputForRun(state, run, result.Cursor, finishedAt))
	if err != nil {
		return PollRunResult{}, err
	}
	return PollRunResult{Run: run, State: updatedState}, nil
}

func (runner *Runner) pollProvider(ctx context.Context, project config.Project, provider Provider, kind SyncKind, state SyncState) (PollResult, error) {
	switch provider {
	case ProviderJira:
		if project.Integrations.Jira == nil || runner.jiraClient == nil {
			return PollResult{}, ErrInvalidInput
		}
		cfg := *project.Integrations.Jira
		if _, err := runner.store.UpsertSource(ctx, sourceInput(project.ID, ProviderJira, jiraStatusConfig(cfg), runner.now().UTC())); err != nil {
			return PollResult{}, err
		}
		plan, err := runner.planner.Jira(JiraPlanInput{ProjectID: project.ID, Config: cfg, State: state, Kind: kind})
		if err != nil {
			return PollResult{}, err
		}
		credentials, err := runner.credentialResolver.ResolveAtlassian(cfg.CredentialRefs)
		if err != nil {
			return PollResult{}, err
		}
		return runner.jiraClient.PollJira(ctx, credentials, plan)
	case ProviderConfluence:
		if project.Integrations.Confluence == nil || runner.confluenceClient == nil {
			return PollResult{}, ErrInvalidInput
		}
		cfg := *project.Integrations.Confluence
		if _, err := runner.store.UpsertSource(ctx, sourceInput(project.ID, ProviderConfluence, confluenceStatusConfig(cfg), runner.now().UTC())); err != nil {
			return PollResult{}, err
		}
		plan, err := runner.planner.Confluence(ConfluencePlanInput{ProjectID: project.ID, Config: cfg, State: state, Kind: kind})
		if err != nil {
			return PollResult{}, err
		}
		credentials, err := runner.credentialResolver.ResolveAtlassian(cfg.CredentialRefs)
		if err != nil {
			return PollResult{}, err
		}
		return runner.confluenceClient.PollConfluence(ctx, credentials, plan)
	default:
		return PollResult{}, ErrInvalidInput
	}
}

func (runner *Runner) upsertItems(ctx context.Context, run SyncRun, items []PollItem, seenAt time.Time) (int, error) {
	upserted := 0
	for _, item := range items {
		if _, err := runner.store.UpsertItem(ctx, ItemMetadataInput{
			ProjectID:     run.ProjectID,
			Provider:      run.Provider,
			ItemID:        item.ID,
			ItemKey:       item.Key,
			ItemType:      item.Type,
			ItemStatus:    item.Status,
			ItemUpdatedAt: item.UpdatedAt,
			FirstSeenAt:   seenAt,
			LastSeenAt:    seenAt,
			LastRunID:     run.ID,
		}); err != nil {
			return upserted, err
		}
		upserted++
	}
	return upserted, nil
}

func syncStateInputForRun(previous SyncState, run SyncRun, cursor string, now time.Time) SyncStateInput {
	input := SyncStateInput{
		ProjectID:             run.ProjectID,
		Provider:              run.Provider,
		LastRunID:             run.ID,
		LastSuccessfulRunID:   run.ID,
		LastSuccessAt:         now,
		LastFullSyncAt:        previous.LastFullSyncAt,
		LastIncrementalSyncAt: previous.LastIncrementalSyncAt,
		LastEmptyPollAt:       previous.LastEmptyPollAt,
		EmptyPollCount:        previous.EmptyPollCount,
		CurrentIdleSleep:      0,
		Cursor:                cursor,
		UpdatedAt:             now,
	}
	if strings.TrimSpace(input.Cursor) == "" {
		input.Cursor = previous.Cursor
	}
	if run.Kind == SyncKindInitialFull {
		input.LastFullSyncAt = now
	} else {
		input.LastIncrementalSyncAt = now
	}
	if run.EmptyPoll {
		input.LastEmptyPollAt = now
		input.EmptyPollCount++
		input.CurrentIdleSleep = run.IdleSleep
	} else {
		input.EmptyPollCount = 0
	}
	return input
}

func idleSleepForRun(kind SyncKind, empty bool, current time.Duration, polling config.IntegrationPolling) time.Duration {
	if kind != SyncKindIncremental || !empty {
		return 0
	}
	next := polling.EmptyPollSleep
	if current > 0 {
		next = current + polling.EmptyPollSleep
	}
	if polling.MaxIdleSleep > 0 && next > polling.MaxIdleSleep {
		return polling.MaxIdleSleep
	}
	return next
}

func pollingFor(project config.Project, provider Provider) config.IntegrationPolling {
	switch provider {
	case ProviderJira:
		if project.Integrations.Jira != nil {
			return project.Integrations.Jira.Polling
		}
	case ProviderConfluence:
		if project.Integrations.Confluence != nil {
			return project.Integrations.Confluence.Polling
		}
	}
	return config.IntegrationPolling{}
}

func errorCategory(err error) ErrorCategory {
	var providerErr *ProviderError
	switch {
	case errors.As(err, &providerErr):
		return providerErr.Category
	case errors.Is(err, ErrCredentialUnavailable):
		return ErrorCategoryCredentialUnavailable
	default:
		return ErrorCategoryRequestFailed
	}
}

func redactedPollError(provider Provider, category string) error {
	return fmt.Errorf("%w: provider=%s category=%s", ErrProviderRequestFailed, provider, category)
}

func newRandomRunID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("integration-run-%d", time.Now().UTC().UnixNano())
	}
	return "integration-run-" + hex.EncodeToString(buf[:])
}
