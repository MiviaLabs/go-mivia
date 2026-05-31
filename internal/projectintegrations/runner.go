package projectintegrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

type RunnerStore interface {
	UpsertSource(context.Context, SourceMetadataInput) (SourceMetadata, error)
	CreateSyncRun(context.Context, SyncRun) error
	UpdateSyncRun(context.Context, SyncRun) error
	GetSyncState(context.Context, string, Provider) (SyncState, error)
	UpdateSyncState(context.Context, SyncStateInput) (SyncState, error)
	UpsertItem(context.Context, ItemMetadataInput) (ItemMetadata, error)
}

type RichContentStore interface {
	PutRichContentItem(context.Context, RichContentItem, []RichContentChunk) (RichContentGraphResult, error)
}

type JiraPoller interface {
	PollJira(context.Context, Credentials, JiraQueryPlan) (PollResult, error)
}

type ConfluencePoller interface {
	PollConfluence(context.Context, Credentials, ConfluenceQueryPlan) (PollResult, error)
}

type JiraProgressPoller interface {
	PollJiraWithProgress(context.Context, Credentials, JiraQueryPlan, PollProgressFunc) (PollResult, error)
}

type ConfluenceProgressPoller interface {
	PollConfluenceWithProgress(context.Context, Credentials, ConfluenceQueryPlan, PollProgressFunc) (PollResult, error)
}

type Planner struct {
	Jira       func(JiraPlanInput) (JiraQueryPlan, error)
	Confluence func(ConfluencePlanInput) (ConfluenceQueryPlan, error)
}

type RunnerOptions struct {
	Projects           []config.Project
	Store              RunnerStore
	RichContentStore   RichContentStore
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
	richContentStore   RichContentStore
	credentialResolver CredentialResolver
	jiraClient         JiraPoller
	confluenceClient   ConfluencePoller
	planner            Planner
	now                func() time.Time
	newRunID           func() string
}

type PollItem struct {
	ID              string
	Key             string
	Type            string
	Status          string
	UpdatedAt       time.Time
	ProviderVersion string
	ProviderETag    string
}

type PollProgress struct {
	ItemsSeen int
}

type PollProgressFunc func(context.Context, PollProgress) error

type PollResult struct {
	Items       []PollItem
	RichContent []RichContentPayload
	Cursor      string
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
		richContentStore:   options.RichContentStore,
		credentialResolver: options.CredentialResolver,
		jiraClient:         options.JiraClient,
		confluenceClient:   options.ConfluenceClient,
		planner:            options.Planner,
		now:                options.Now,
		newRunID:           options.NewRunID,
	}, nil
}

func (runner *Runner) RunProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (PollRunResult, error) {
	run, err := runner.PrepareProviderPoll(ctx, projectID, provider, kind)
	if err != nil {
		return PollRunResult{}, err
	}
	return runner.ExecutePreparedProviderPoll(ctx, run)
}

func (runner *Runner) PrepareProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (SyncRun, error) {
	if runner == nil {
		return SyncRun{}, fmt.Errorf("%w: runner is nil", ErrInvalidInput)
	}
	project, ok := runner.projects[strings.TrimSpace(projectID)]
	if !ok {
		return SyncRun{}, fmt.Errorf("%w: project", ErrNotFound)
	}
	state, err := runner.store.GetSyncState(ctx, project.ID, provider)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return SyncRun{}, err
	}
	if errors.Is(err, ErrNotFound) {
		state = SyncState{}
	}
	plannedKind, err := planKind(kind, state)
	if err != nil {
		return SyncRun{}, err
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
		return SyncRun{}, fmt.Errorf("%w: run id is empty", ErrInvalidInput)
	}
	if err := runner.store.CreateSyncRun(ctx, run); err != nil {
		return SyncRun{}, err
	}
	return run, nil
}

func (runner *Runner) ExecutePreparedProviderPoll(ctx context.Context, run SyncRun) (PollRunResult, error) {
	if runner == nil {
		return PollRunResult{}, fmt.Errorf("%w: runner is nil", ErrInvalidInput)
	}
	run.ID = strings.TrimSpace(run.ID)
	run.ProjectID = strings.TrimSpace(run.ProjectID)
	if run.ID == "" || run.ProjectID == "" || !validProvider(run.Provider) {
		return PollRunResult{}, fmt.Errorf("%w: prepared run is invalid", ErrInvalidInput)
	}
	project, ok := runner.projects[run.ProjectID]
	if !ok {
		return PollRunResult{}, fmt.Errorf("%w: project", ErrNotFound)
	}
	state, err := runner.store.GetSyncState(ctx, project.ID, run.Provider)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PollRunResult{}, err
	}
	if errors.Is(err, ErrNotFound) {
		state = SyncState{}
	}
	if run.Kind == "" {
		plannedKind, err := planKind(run.Kind, state)
		if err != nil {
			return PollRunResult{}, err
		}
		run.Kind = plannedKind
	}
	run.Status = SyncRunStatusRunning
	if err := runner.store.UpdateSyncRun(ctx, run); err != nil {
		return PollRunResult{}, err
	}
	progress := func(ctx context.Context, progress PollProgress) error {
		if progress.ItemsSeen <= run.ItemsSeen {
			return nil
		}
		run.ItemsSeen = progress.ItemsSeen
		return runner.store.UpdateSyncRun(ctx, run)
	}
	result, pollErr := runner.pollProvider(ctx, project, run.Provider, run.Kind, state, progress)
	finishedAt := runner.now().UTC()
	if pollErr != nil {
		run.Status = SyncRunStatusFailed
		run.ErrorCategory = string(errorCategory(pollErr))
		run.FinishedAt = finishedAt
		_ = runner.store.UpdateSyncRun(ctx, run)
		return PollRunResult{Run: run}, redactedPollError(run.Provider, run.ErrorCategory)
	}
	run.ItemsSeen = len(result.Items)
	itemsChanged, itemsUnchanged, err := runner.upsertItems(ctx, &run, result.Items, finishedAt)
	if err != nil {
		run.Status = SyncRunStatusFailed
		run.ErrorCategory = string(ErrorCategoryRequestFailed)
		run.FinishedAt = finishedAt
		_ = runner.store.UpdateSyncRun(ctx, run)
		return PollRunResult{Run: run}, redactedPollError(run.Provider, run.ErrorCategory)
	}
	run.ItemsChanged = itemsChanged
	run.ItemsUnchanged = itemsUnchanged
	run.ItemsUpserted = itemsChanged
	if err := runner.putRichContent(ctx, &run, result.RichContent); err != nil {
		run.Status = SyncRunStatusFailed
		run.ErrorCategory = string(ErrorCategoryRequestFailed)
		run.FinishedAt = finishedAt
		_ = runner.store.UpdateSyncRun(ctx, run)
		return PollRunResult{Run: run}, redactedPollError(run.Provider, run.ErrorCategory)
	}
	run.EmptyPoll = len(result.Items) == 0
	run.IdleSleep = idleSleepForRun(run.Kind, run.EmptyPoll, state.CurrentIdleSleep, pollingFor(project, run.Provider))
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

func (runner *Runner) FailPreparedProviderPoll(ctx context.Context, run SyncRun, errorCategory string) (SyncRun, error) {
	if runner == nil {
		return SyncRun{}, fmt.Errorf("%w: runner is nil", ErrInvalidInput)
	}
	run.ID = strings.TrimSpace(run.ID)
	run.ProjectID = strings.TrimSpace(run.ProjectID)
	if run.ID == "" || run.ProjectID == "" || !validProvider(run.Provider) {
		return SyncRun{}, fmt.Errorf("%w: prepared run is invalid", ErrInvalidInput)
	}
	run.Status = SyncRunStatusFailed
	run.ErrorCategory = strings.TrimSpace(errorCategory)
	if run.ErrorCategory == "" {
		run.ErrorCategory = string(ErrorCategoryRequestFailed)
	}
	run.FinishedAt = runner.now().UTC()
	if err := runner.store.UpdateSyncRun(ctx, run); err != nil {
		return run, err
	}
	return run, nil
}

func (runner *Runner) putRichContent(ctx context.Context, run *SyncRun, payloads []RichContentPayload) error {
	if runner.richContentStore == nil || len(payloads) == 0 {
		return nil
	}
	for _, payload := range payloads {
		result, err := runner.richContentStore.PutRichContentItem(ctx, payload.Item, payload.Chunks)
		if err != nil {
			return err
		}
		if result.Changed {
			run.RichContentChanged++
		} else {
			run.RichContentUnchanged++
		}
		if err := runner.store.UpdateSyncRun(ctx, *run); err != nil {
			return err
		}
	}
	return nil
}

func (runner *Runner) pollProvider(ctx context.Context, project config.Project, provider Provider, kind SyncKind, state SyncState, progress PollProgressFunc) (PollResult, error) {
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
		if progressClient, ok := runner.jiraClient.(JiraProgressPoller); ok {
			return progressClient.PollJiraWithProgress(ctx, credentials, plan, progress)
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
		if progressClient, ok := runner.confluenceClient.(ConfluenceProgressPoller); ok {
			return progressClient.PollConfluenceWithProgress(ctx, credentials, plan, progress)
		}
		return runner.confluenceClient.PollConfluence(ctx, credentials, plan)
	default:
		return PollResult{}, ErrInvalidInput
	}
}

func (runner *Runner) upsertItems(ctx context.Context, run *SyncRun, items []PollItem, seenAt time.Time) (int, int, error) {
	changed := 0
	unchanged := 0
	for index, item := range items {
		metadata, err := runner.store.UpsertItem(ctx, ItemMetadataInput{
			ProjectID:       run.ProjectID,
			Provider:        run.Provider,
			ItemID:          item.ID,
			ItemKey:         item.Key,
			ItemType:        item.Type,
			ItemStatus:      item.Status,
			ItemUpdatedAt:   item.UpdatedAt,
			ProviderVersion: item.ProviderVersion,
			ProviderETag:    item.ProviderETag,
			FirstSeenAt:     seenAt,
			LastSeenAt:      seenAt,
			LastRunID:       run.ID,
		})
		if err != nil {
			return changed, unchanged, err
		}
		if metadata.Changed {
			changed++
		} else {
			unchanged++
		}
		run.ItemsSeen = index + 1
		run.ItemsChanged = changed
		run.ItemsUnchanged = unchanged
		run.ItemsUpserted = changed
		if err := runner.store.UpdateSyncRun(ctx, *run); err != nil {
			return changed, unchanged, err
		}
	}
	return changed, unchanged, nil
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
