package projectintegrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

type Store interface {
	UpsertSource(context.Context, SourceMetadataInput) (SourceMetadata, error)
	ListSources(context.Context, string) ([]SourceMetadata, error)
	GetSyncState(context.Context, string, Provider) (SyncState, error)
	GetSyncRun(context.Context, string, Provider, string) (SyncRun, error)
}

type PollRunner interface {
	RunProviderPoll(context.Context, string, Provider, SyncKind) (PollRunResult, error)
}

type AsyncPollRunner interface {
	SubmitProviderPoll(context.Context, string, Provider, SyncKind) (SyncRun, error)
}

type RichContentReader interface {
	GetRichContentItem(context.Context, string, Provider, string, RichContentReadOptions) (RichContentReadResult, error)
	SearchRichContent(context.Context, string, RichContentSearchOptions) ([]RichContentSearchResult, error)
}

type ServiceOptions struct {
	Runner      PollRunner
	RichContent RichContentReader
}

type Service struct {
	projects    map[string]config.Project
	store       Store
	runner      PollRunner
	richContent RichContentReader
	now         func() time.Time
}

type ProviderSummary struct {
	ProjectID         string
	Provider          Provider
	Configured        bool
	Enabled           bool
	AuthMode          string
	CredentialSource  string
	AllowlistKind     string
	AllowlistCount    int
	IngestionEnabled  bool
	IncrementalPeriod time.Duration
}

type ProviderStatus struct {
	ProjectID            string
	Provider             Provider
	Configured           bool
	Enabled              bool
	AuthMode             string
	CredentialSource     string
	AllowlistKind        string
	AllowlistCount       int
	IngestionEnabled     bool
	InitialFullSync      string
	IncrementalInterval  time.Duration
	EmptyPollSleep       time.Duration
	MaxIdleSleep         time.Duration
	OverlapWindow        time.Duration
	InitialPageSize      int
	IncrementalPageSize  int
	MaxResults           int
	SourcePersisted      bool
	SourceAllowlistCount int
	SyncState            *SyncStateStatus
	LastRun              *SyncRunStatusView
}

type SyncStateStatus struct {
	LastRunID             string
	LastSuccessfulRunID   string
	LastSuccessAt         time.Time
	LastFullSyncAt        time.Time
	LastIncrementalSyncAt time.Time
	LastEmptyPollAt       time.Time
	EmptyPollCount        int
	CurrentIdleSleep      time.Duration
	CursorHashPresent     bool
	UpdatedAt             time.Time
}

type SyncRunStatusView struct {
	ID            string
	Kind          SyncKind
	Status        SyncRunStatus
	ItemsSeen     int
	ItemsUpserted int
	EmptyPoll     bool
	IdleSleep     time.Duration
	ErrorCategory string
	StartedAt     time.Time
	FinishedAt    time.Time
}

type ProviderPollStatus struct {
	ProjectID string
	Provider  Provider
	Run       SyncRunStatusView
	SyncState SyncStateStatus
}

type ProviderPollAccepted struct {
	ProjectID string
	Provider  Provider
	Accepted  bool
	Run       SyncRunStatusView
}

type LocalSearchInput struct {
	ProjectID       string
	Provider        Provider
	Query           string
	MaxResults      int
	MaxSnippetBytes int
	CaseSensitive   bool
}

type LocalReadInput struct {
	ProjectID     string
	Provider      Provider
	ItemIDOrKey   string
	MaxChunkBytes int
}

func NewService(projects []config.Project, store Store) (*Service, error) {
	return NewServiceWithOptions(projects, store, ServiceOptions{})
}

func NewServiceWithOptions(projects []config.Project, store Store, options ServiceOptions) (*Service, error) {
	byID := make(map[string]config.Project, len(projects))
	for _, project := range projects {
		id := strings.TrimSpace(project.ID)
		if id == "" {
			return nil, fmt.Errorf("%w: project id is empty", ErrInvalidInput)
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf("%w: duplicate project id", ErrInvalidInput)
		}
		byID[id] = cloneConfigProject(project)
	}
	return &Service{
		projects:    byID,
		store:       store,
		runner:      options.Runner,
		richContent: options.RichContent,
		now:         time.Now,
	}, nil
}

func (service *Service) ListProviders(projectID string) ([]ProviderSummary, error) {
	project, err := service.project(projectID)
	if err != nil {
		return nil, err
	}
	var providers []ProviderSummary
	if project.Integrations.Jira != nil {
		providers = append(providers, providerSummary(project.ID, ProviderJira, jiraStatusConfig(*project.Integrations.Jira)))
	}
	if project.Integrations.Confluence != nil {
		providers = append(providers, providerSummary(project.ID, ProviderConfluence, confluenceStatusConfig(*project.Integrations.Confluence)))
	}
	return providers, nil
}

func (service *Service) Status(ctx context.Context, projectID string, provider Provider) (ProviderStatus, error) {
	project, err := service.project(projectID)
	if err != nil {
		return ProviderStatus{}, err
	}
	statusCfg, err := providerStatusConfig(project, provider)
	if err != nil {
		return ProviderStatus{}, err
	}
	status := ProviderStatus{
		ProjectID:           project.ID,
		Provider:            provider,
		Configured:          true,
		Enabled:             statusCfg.enabled,
		AuthMode:            statusCfg.authMode,
		CredentialSource:    statusCfg.credentialSource,
		AllowlistKind:       statusCfg.allowlistKind,
		AllowlistCount:      len(statusCfg.allowlist),
		IngestionEnabled:    statusCfg.polling.IngestionEnabled,
		InitialFullSync:     statusCfg.polling.InitialFullSync,
		IncrementalInterval: statusCfg.polling.IncrementalInterval,
		EmptyPollSleep:      statusCfg.polling.EmptyPollSleep,
		MaxIdleSleep:        statusCfg.polling.MaxIdleSleep,
		OverlapWindow:       statusCfg.polling.OverlapWindow,
		InitialPageSize:     statusCfg.polling.InitialPageSize,
		IncrementalPageSize: statusCfg.polling.IncrementalPageSize,
		MaxResults:          statusCfg.maxResults,
	}
	if service.store == nil {
		return status, nil
	}
	if err := status.addStoredMetadata(ctx, service.store); err != nil {
		return ProviderStatus{}, err
	}
	return status, nil
}

func (service *Service) UpsertConfiguredSources(ctx context.Context, projectID string) ([]SourceMetadata, error) {
	if service.store == nil {
		return nil, fmt.Errorf("%w: integration store unavailable", ErrNotFound)
	}
	project, err := service.project(projectID)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	var sources []SourceMetadata
	if project.Integrations.Jira != nil {
		source, err := service.store.UpsertSource(ctx, sourceInput(project.ID, ProviderJira, jiraStatusConfig(*project.Integrations.Jira), now))
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if project.Integrations.Confluence != nil {
		source, err := service.store.UpsertSource(ctx, sourceInput(project.ID, ProviderConfluence, confluenceStatusConfig(*project.Integrations.Confluence), now))
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func (service *Service) PollProvider(ctx context.Context, projectID string, provider Provider, kind SyncKind) (ProviderPollStatus, error) {
	if service == nil {
		return ProviderPollStatus{}, fmt.Errorf("%w: service is nil", ErrInvalidInput)
	}
	if service.runner == nil {
		return ProviderPollStatus{}, fmt.Errorf("%w: integration runner unavailable", ErrNotFound)
	}
	project, err := service.project(projectID)
	if err != nil {
		return ProviderPollStatus{}, err
	}
	statusCfg, err := providerStatusConfig(project, provider)
	if err != nil {
		return ProviderPollStatus{}, err
	}
	if !statusCfg.enabled {
		return ProviderPollStatus{}, fmt.Errorf("%w: provider disabled", ErrInvalidInput)
	}
	result, err := service.runner.RunProviderPoll(ctx, project.ID, provider, kind)
	if err != nil {
		return ProviderPollStatus{}, err
	}
	return ProviderPollStatus{
		ProjectID: project.ID,
		Provider:  provider,
		Run:       syncRunStatusView(result.Run),
		SyncState: syncStateStatus(result.State),
	}, nil
}

func (service *Service) SubmitProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (ProviderPollAccepted, error) {
	if service == nil {
		return ProviderPollAccepted{}, fmt.Errorf("%w: service is nil", ErrInvalidInput)
	}
	if service.runner == nil {
		return ProviderPollAccepted{}, fmt.Errorf("%w: integration runner unavailable", ErrNotFound)
	}
	asyncRunner, ok := service.runner.(AsyncPollRunner)
	if !ok {
		return ProviderPollAccepted{}, fmt.Errorf("%w: async integration runner unavailable", ErrNotFound)
	}
	project, err := service.project(projectID)
	if err != nil {
		return ProviderPollAccepted{}, err
	}
	statusCfg, err := providerStatusConfig(project, provider)
	if err != nil {
		return ProviderPollAccepted{}, err
	}
	if !statusCfg.enabled {
		return ProviderPollAccepted{}, fmt.Errorf("%w: provider disabled", ErrInvalidInput)
	}
	run, err := asyncRunner.SubmitProviderPoll(ctx, project.ID, provider, kind)
	if err != nil {
		return ProviderPollAccepted{}, err
	}
	return ProviderPollAccepted{
		ProjectID: project.ID,
		Provider:  provider,
		Accepted:  true,
		Run:       syncRunStatusView(run),
	}, nil
}

func (service *Service) PollRunStatus(ctx context.Context, projectID string, provider Provider, runID string) (ProviderPollStatus, error) {
	if service == nil {
		return ProviderPollStatus{}, fmt.Errorf("%w: service is nil", ErrInvalidInput)
	}
	if service.store == nil {
		return ProviderPollStatus{}, fmt.Errorf("%w: integration store unavailable", ErrNotFound)
	}
	project, err := service.project(projectID)
	if err != nil {
		return ProviderPollStatus{}, err
	}
	if _, err := providerStatusConfig(project, provider); err != nil {
		return ProviderPollStatus{}, err
	}
	run, err := service.store.GetSyncRun(ctx, project.ID, provider, strings.TrimSpace(runID))
	if err != nil {
		return ProviderPollStatus{}, err
	}
	status := ProviderPollStatus{
		ProjectID: project.ID,
		Provider:  provider,
		Run:       syncRunStatusView(run),
	}
	state, err := service.store.GetSyncState(ctx, project.ID, provider)
	if err == nil {
		status.SyncState = syncStateStatus(state)
	} else if !errors.Is(err, ErrNotFound) {
		return ProviderPollStatus{}, err
	}
	return status, nil
}

func (service *Service) SearchLocalContent(ctx context.Context, input LocalSearchInput) ([]RichContentSearchResult, error) {
	if service == nil {
		return nil, fmt.Errorf("%w: service is nil", ErrInvalidInput)
	}
	if service.richContent == nil {
		return nil, fmt.Errorf("%w: integration rich content unavailable", ErrNotFound)
	}
	project, err := service.project(input.ProjectID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Query) == "" {
		return nil, ErrInvalidInput
	}
	if input.Provider != "" {
		if _, err := providerStatusConfig(project, input.Provider); err != nil {
			return nil, err
		}
	}
	return service.richContent.SearchRichContent(ctx, project.ID, RichContentSearchOptions{
		Provider:        input.Provider,
		Query:           input.Query,
		MaxResults:      input.MaxResults,
		MaxSnippetBytes: input.MaxSnippetBytes,
		CaseSensitive:   input.CaseSensitive,
	})
}

func (service *Service) ReadLocalContent(ctx context.Context, input LocalReadInput) (RichContentReadResult, error) {
	if service == nil {
		return RichContentReadResult{}, fmt.Errorf("%w: service is nil", ErrInvalidInput)
	}
	if service.richContent == nil {
		return RichContentReadResult{}, fmt.Errorf("%w: integration rich content unavailable", ErrNotFound)
	}
	project, err := service.project(input.ProjectID)
	if err != nil {
		return RichContentReadResult{}, err
	}
	if strings.TrimSpace(input.ItemIDOrKey) == "" {
		return RichContentReadResult{}, ErrInvalidInput
	}
	if _, err := providerStatusConfig(project, input.Provider); err != nil {
		return RichContentReadResult{}, err
	}
	return service.richContent.GetRichContentItem(ctx, project.ID, input.Provider, input.ItemIDOrKey, RichContentReadOptions{MaxChunkBytes: input.MaxChunkBytes})
}

func (service *Service) project(projectID string) (config.Project, error) {
	if service == nil {
		return config.Project{}, fmt.Errorf("%w: service is nil", ErrInvalidInput)
	}
	project, ok := service.projects[strings.TrimSpace(projectID)]
	if !ok {
		return config.Project{}, fmt.Errorf("%w: project", ErrNotFound)
	}
	return cloneConfigProject(project), nil
}

func (status *ProviderStatus) addStoredMetadata(ctx context.Context, store Store) error {
	sources, err := store.ListSources(ctx, status.ProjectID)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.Provider == status.Provider {
			status.SourcePersisted = true
			status.SourceAllowlistCount = source.AllowlistCount
			break
		}
	}
	state, err := store.GetSyncState(ctx, status.ProjectID, status.Provider)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	stateStatus := syncStateStatus(state)
	status.SyncState = &stateStatus
	if state.LastRunID == "" {
		return nil
	}
	run, err := store.GetSyncRun(ctx, status.ProjectID, status.Provider, state.LastRunID)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	runStatus := syncRunStatusView(run)
	status.LastRun = &runStatus
	return nil
}

func syncStateStatus(state SyncState) SyncStateStatus {
	return SyncStateStatus{
		LastRunID:             state.LastRunID,
		LastSuccessfulRunID:   state.LastSuccessfulRunID,
		LastSuccessAt:         state.LastSuccessAt,
		LastFullSyncAt:        state.LastFullSyncAt,
		LastIncrementalSyncAt: state.LastIncrementalSyncAt,
		LastEmptyPollAt:       state.LastEmptyPollAt,
		EmptyPollCount:        state.EmptyPollCount,
		CurrentIdleSleep:      state.CurrentIdleSleep,
		CursorHashPresent:     state.CursorHash != "",
		UpdatedAt:             state.UpdatedAt,
	}
}

func syncRunStatusView(run SyncRun) SyncRunStatusView {
	return SyncRunStatusView{
		ID:            run.ID,
		Kind:          run.Kind,
		Status:        run.Status,
		ItemsSeen:     run.ItemsSeen,
		ItemsUpserted: run.ItemsUpserted,
		EmptyPoll:     run.EmptyPoll,
		IdleSleep:     run.IdleSleep,
		ErrorCategory: run.ErrorCategory,
		StartedAt:     run.StartedAt,
		FinishedAt:    run.FinishedAt,
	}
}

type statusConfig struct {
	enabled          bool
	siteURL          string
	cloudID          string
	authMode         string
	credentialSource string
	allowlistKind    string
	allowlist        []string
	polling          config.IntegrationPolling
	maxResults       int
}

func providerStatusConfig(project config.Project, provider Provider) (statusConfig, error) {
	switch provider {
	case ProviderJira:
		if project.Integrations.Jira == nil {
			return statusConfig{}, fmt.Errorf("%w: provider", ErrNotFound)
		}
		return jiraStatusConfig(*project.Integrations.Jira), nil
	case ProviderConfluence:
		if project.Integrations.Confluence == nil {
			return statusConfig{}, fmt.Errorf("%w: provider", ErrNotFound)
		}
		return confluenceStatusConfig(*project.Integrations.Confluence), nil
	default:
		return statusConfig{}, ErrInvalidInput
	}
}

func jiraStatusConfig(cfg config.JiraIntegration) statusConfig {
	return statusConfig{
		enabled:          cfg.Enabled,
		siteURL:          cfg.SiteURL,
		cloudID:          cfg.CloudID,
		authMode:         strings.TrimSpace(cfg.AuthMode),
		credentialSource: credentialSource(cfg.CredentialRefs),
		allowlistKind:    "project_keys",
		allowlist:        append([]string(nil), cfg.ProjectKeys...),
		polling:          cfg.Polling,
		maxResults:       cfg.MaxResults,
	}
}

func confluenceStatusConfig(cfg config.ConfluenceIntegration) statusConfig {
	return statusConfig{
		enabled:          cfg.Enabled,
		siteURL:          cfg.SiteURL,
		cloudID:          cfg.CloudID,
		authMode:         strings.TrimSpace(cfg.AuthMode),
		credentialSource: credentialSource(cfg.CredentialRefs),
		allowlistKind:    "space_keys",
		allowlist:        append([]string(nil), cfg.SpaceKeys...),
		polling:          cfg.Polling,
		maxResults:       cfg.MaxResults,
	}
}

func providerSummary(projectID string, provider Provider, cfg statusConfig) ProviderSummary {
	return ProviderSummary{
		ProjectID:         projectID,
		Provider:          provider,
		Configured:        true,
		Enabled:           cfg.enabled,
		AuthMode:          cfg.authMode,
		CredentialSource:  cfg.credentialSource,
		AllowlistKind:     cfg.allowlistKind,
		AllowlistCount:    len(cfg.allowlist),
		IngestionEnabled:  cfg.polling.IngestionEnabled,
		IncrementalPeriod: cfg.polling.IncrementalInterval,
	}
}

func sourceInput(projectID string, provider Provider, cfg statusConfig, updatedAt time.Time) SourceMetadataInput {
	return SourceMetadataInput{
		ProjectID:           projectID,
		Provider:            provider,
		SiteURL:             cfg.siteURL,
		CloudID:             cfg.cloudID,
		Allowlist:           cfg.allowlist,
		AuthMode:            cfg.authMode,
		IngestionEnabled:    cfg.polling.IngestionEnabled,
		InitialFullSync:     cfg.polling.InitialFullSync,
		IncrementalInterval: cfg.polling.IncrementalInterval,
		EmptyPollSleep:      cfg.polling.EmptyPollSleep,
		MaxIdleSleep:        cfg.polling.MaxIdleSleep,
		OverlapWindow:       cfg.polling.OverlapWindow,
		InitialPageSize:     cfg.polling.InitialPageSize,
		IncrementalPageSize: cfg.polling.IncrementalPageSize,
		MaxResults:          cfg.maxResults,
		UpdatedAt:           updatedAt,
	}
}

func credentialSource(refs config.AtlassianCredentialRefs) string {
	credentialsFile := strings.TrimSpace(refs.CredentialsFile) != ""
	emailEnv := strings.TrimSpace(refs.EmailEnv) != ""
	emailFile := strings.TrimSpace(refs.EmailFile) != ""
	tokenEnv := strings.TrimSpace(refs.APITokenEnv) != ""
	tokenFile := strings.TrimSpace(refs.APITokenFile) != ""
	switch {
	case credentialsFile && !emailEnv && !emailFile && !tokenEnv && !tokenFile:
		return "file"
	case emailEnv && tokenEnv && !emailFile && !tokenFile:
		return "env"
	case emailFile && tokenFile && !emailEnv && !tokenEnv:
		return "file"
	case (emailEnv || emailFile) && (tokenEnv || tokenFile):
		return "mixed"
	default:
		return "none"
	}
}

func cloneConfigProject(project config.Project) config.Project {
	project.Include = append([]string(nil), project.Include...)
	project.Exclude = append([]string(nil), project.Exclude...)
	if project.Integrations.Jira != nil {
		jira := *project.Integrations.Jira
		jira.ProjectKeys = append([]string(nil), jira.ProjectKeys...)
		jira.DefaultFields = append([]string(nil), jira.DefaultFields...)
		jira.AllowedFields = append([]string(nil), jira.AllowedFields...)
		project.Integrations.Jira = &jira
	}
	if project.Integrations.Confluence != nil {
		confluence := *project.Integrations.Confluence
		confluence.SpaceKeys = append([]string(nil), confluence.SpaceKeys...)
		confluence.RootPageIDs = append([]string(nil), confluence.RootPageIDs...)
		project.Integrations.Confluence = &confluence
	}
	return project
}
