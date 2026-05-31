package projectintegrations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

type SchedulerOptions struct {
	Sleep  func(context.Context, time.Duration) error
	Logger *slog.Logger
}

type Scheduler struct {
	runner    PollRunner
	schedules []ProviderSchedule
	sleep     func(context.Context, time.Duration) error
	logger    *slog.Logger

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	active  map[providerRunKey]struct{}
}

type ProviderSchedule struct {
	ProjectID           string
	Provider            Provider
	InitialFullSync     string
	IncrementalInterval time.Duration
	EmptyPollSleep      time.Duration
	MaxIdleSleep        time.Duration
}

type asyncProviderPollRunner interface {
	PrepareProviderPoll(context.Context, string, Provider, SyncKind) (SyncRun, error)
	ExecutePreparedProviderPoll(context.Context, SyncRun) (PollRunResult, error)
	FailPreparedProviderPoll(context.Context, SyncRun, string) (SyncRun, error)
}

type SchedulerDiagnostics struct {
	Started                bool
	ScheduledProviderCount int
	ActivePollCount        int
	ActivePolls            map[string]int
}

type providerRunKey struct {
	projectID string
	provider  Provider
}

func NewScheduler(projects []config.Project, runner PollRunner, options SchedulerOptions) (*Scheduler, error) {
	if runner == nil {
		return nil, fmt.Errorf("%w: integration runner unavailable", ErrInvalidInput)
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	scheduler := &Scheduler{
		runner:    runner,
		schedules: scheduledProviders(projects),
		sleep:     sleep,
		logger:    options.Logger,
		active:    make(map[providerRunKey]struct{}),
	}
	return scheduler, nil
}

func (scheduler *Scheduler) Start(ctx context.Context) error {
	if scheduler == nil || scheduler.runner == nil {
		return fmt.Errorf("%w: integration scheduler dependencies are required", ErrInvalidInput)
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.started {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	scheduler.ctx = runCtx
	scheduler.cancel = cancel
	scheduler.started = true
	for _, schedule := range scheduler.schedules {
		schedule := schedule
		scheduler.wg.Add(1)
		go func() {
			defer scheduler.wg.Done()
			scheduler.runLoop(runCtx, schedule)
		}()
	}
	scheduler.logInfo("project integration scheduler started", slog.Int("scheduled_provider_count", len(scheduler.schedules)))
	return nil
}

func (scheduler *Scheduler) Stop(ctx context.Context) error {
	if scheduler == nil {
		return nil
	}
	scheduler.mu.Lock()
	cancel := scheduler.cancel
	scheduler.ctx = nil
	scheduler.cancel = nil
	scheduler.started = false
	scheduler.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		scheduler.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		scheduler.logInfo("project integration scheduler stopped")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (scheduler *Scheduler) RunProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (PollRunResult, error) {
	if scheduler == nil || scheduler.runner == nil {
		return PollRunResult{}, fmt.Errorf("%w: integration scheduler dependencies are required", ErrInvalidInput)
	}
	key := providerRunKey{projectID: strings.TrimSpace(projectID), provider: provider}
	if key.projectID == "" || !validProvider(provider) {
		return PollRunResult{}, fmt.Errorf("%w: provider poll target is invalid", ErrInvalidInput)
	}
	if !scheduler.acquire(key) {
		return PollRunResult{}, fmt.Errorf("%w: integration poll already running", ErrInvalidInput)
	}
	defer scheduler.release(key)
	result, err := scheduler.runner.RunProviderPoll(ctx, key.projectID, provider, kind)
	if err != nil {
		return PollRunResult{}, redactedSchedulerError(provider, err)
	}
	return result, nil
}

func (scheduler *Scheduler) SubmitProviderPoll(ctx context.Context, projectID string, provider Provider, kind SyncKind) (SyncRun, error) {
	if scheduler == nil || scheduler.runner == nil {
		return SyncRun{}, fmt.Errorf("%w: integration scheduler dependencies are required", ErrInvalidInput)
	}
	scheduler.mu.Lock()
	started := scheduler.started
	runCtx := scheduler.ctx
	scheduler.mu.Unlock()
	if !started || runCtx == nil {
		return SyncRun{}, fmt.Errorf("%w: integration scheduler is not started", ErrInvalidInput)
	}
	runner, ok := scheduler.runner.(asyncProviderPollRunner)
	if !ok {
		return SyncRun{}, fmt.Errorf("%w: async integration runner is required", ErrInvalidInput)
	}
	key := providerRunKey{projectID: strings.TrimSpace(projectID), provider: provider}
	if key.projectID == "" || !validProvider(provider) {
		return SyncRun{}, fmt.Errorf("%w: provider poll target is invalid", ErrInvalidInput)
	}
	if !scheduler.acquire(key) {
		return SyncRun{}, fmt.Errorf("%w: integration poll already running", ErrInvalidInput)
	}
	run, err := runner.PrepareProviderPoll(ctx, key.projectID, provider, kind)
	if err != nil {
		scheduler.release(key)
		return SyncRun{}, err
	}
	scheduler.wg.Add(1)
	go func() {
		defer scheduler.wg.Done()
		defer scheduler.release(key)
		startedAt := time.Now()
		result, err := runner.ExecutePreparedProviderPoll(runCtx, run)
		if err != nil {
			scheduler.logWarn("project integration poll failed",
				slog.String("project_id", run.ProjectID),
				slog.String("provider", string(run.Provider)),
				slog.String("kind", string(run.Kind)),
				slog.String("run_id", run.ID),
				slog.String("error_category", string(schedulerErrorCategory(err))),
				slog.String("error", redactedSchedulerError(run.Provider, err).Error()),
				slog.Duration("elapsed", time.Since(startedAt)),
			)
			return
		}
		scheduler.logInfo("project integration poll completed",
			slog.String("project_id", run.ProjectID),
			slog.String("provider", string(run.Provider)),
			slog.String("kind", string(run.Kind)),
			slog.String("run_id", result.Run.ID),
			slog.String("status", string(result.Run.Status)),
			slog.Int("items_seen", result.Run.ItemsSeen),
			slog.Int("items_upserted", result.Run.ItemsUpserted),
			slog.Int("items_changed", result.Run.ItemsChanged),
			slog.Int("items_unchanged", result.Run.ItemsUnchanged),
			slog.Int("rich_content_changed", result.Run.RichContentChanged),
			slog.Int("rich_content_unchanged", result.Run.RichContentUnchanged),
			slog.Bool("empty_poll", result.Run.EmptyPoll),
			slog.Duration("idle_sleep", result.Run.IdleSleep),
			slog.Duration("elapsed", time.Since(startedAt)),
		)
	}()
	return run, nil
}

func (scheduler *Scheduler) ScheduledProviders() []ProviderSchedule {
	if scheduler == nil {
		return nil
	}
	schedules := make([]ProviderSchedule, len(scheduler.schedules))
	copy(schedules, scheduler.schedules)
	return schedules
}

func (scheduler *Scheduler) Diagnostics() SchedulerDiagnostics {
	if scheduler == nil {
		return SchedulerDiagnostics{}
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	active := make(map[string]int, len(scheduler.active))
	for key := range scheduler.active {
		active[key.projectID+":"+string(key.provider)]++
	}
	return SchedulerDiagnostics{
		Started:                scheduler.started,
		ScheduledProviderCount: len(scheduler.schedules),
		ActivePollCount:        len(scheduler.active),
		ActivePolls:            active,
	}
}

func (scheduler *Scheduler) runLoop(ctx context.Context, schedule ProviderSchedule) {
	if schedule.InitialFullSync == "on_start" {
		_, _ = scheduler.runScheduledPoll(ctx, schedule, SyncKindInitialFull)
	}
	delay := schedule.IncrementalInterval
	for {
		if err := scheduler.sleep(ctx, delay); err != nil {
			return
		}
		result, err := scheduler.runScheduledPoll(ctx, schedule, SyncKindIncremental)
		delay = nextSchedulerDelay(schedule, result, err)
	}
}

func (scheduler *Scheduler) runScheduledPoll(ctx context.Context, schedule ProviderSchedule, kind SyncKind) (PollRunResult, error) {
	startedAt := time.Now()
	result, err := scheduler.RunProviderPoll(ctx, schedule.ProjectID, schedule.Provider, kind)
	if err != nil {
		scheduler.logWarn("project integration poll failed",
			slog.String("project_id", schedule.ProjectID),
			slog.String("provider", string(schedule.Provider)),
			slog.String("kind", string(kind)),
			slog.String("error_category", string(schedulerErrorCategory(err))),
			slog.String("error", err.Error()),
			slog.Duration("elapsed", time.Since(startedAt)),
		)
		return PollRunResult{}, err
	}
	scheduler.logInfo("project integration poll completed",
		slog.String("project_id", schedule.ProjectID),
		slog.String("provider", string(schedule.Provider)),
		slog.String("kind", string(kind)),
		slog.String("run_id", result.Run.ID),
		slog.String("status", string(result.Run.Status)),
		slog.Int("items_seen", result.Run.ItemsSeen),
		slog.Int("items_upserted", result.Run.ItemsUpserted),
		slog.Int("items_changed", result.Run.ItemsChanged),
		slog.Int("items_unchanged", result.Run.ItemsUnchanged),
		slog.Int("rich_content_changed", result.Run.RichContentChanged),
		slog.Int("rich_content_unchanged", result.Run.RichContentUnchanged),
		slog.Bool("empty_poll", result.Run.EmptyPoll),
		slog.Duration("idle_sleep", result.Run.IdleSleep),
		slog.Duration("elapsed", time.Since(startedAt)),
	)
	return result, nil
}

func (scheduler *Scheduler) acquire(key providerRunKey) bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if _, exists := scheduler.active[key]; exists {
		return false
	}
	scheduler.active[key] = struct{}{}
	return true
}

func (scheduler *Scheduler) release(key providerRunKey) {
	scheduler.mu.Lock()
	delete(scheduler.active, key)
	scheduler.mu.Unlock()
}

func (scheduler *Scheduler) logInfo(message string, attrs ...slog.Attr) {
	if scheduler.logger == nil {
		return
	}
	scheduler.logger.Info(message, attrsToAny(attrs)...)
}

func (scheduler *Scheduler) logWarn(message string, attrs ...slog.Attr) {
	if scheduler.logger == nil {
		return
	}
	scheduler.logger.Warn(message, attrsToAny(attrs)...)
}

func attrsToAny(attrs []slog.Attr) []any {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	return args
}

func scheduledProviders(projects []config.Project) []ProviderSchedule {
	var schedules []ProviderSchedule
	for _, project := range projects {
		projectID := strings.TrimSpace(project.ID)
		if projectID == "" {
			continue
		}
		if project.Integrations.Jira != nil && project.Integrations.Jira.Enabled && project.Integrations.Jira.Polling.IngestionEnabled {
			schedules = append(schedules, providerSchedule(projectID, ProviderJira, project.Integrations.Jira.Polling))
		}
		if project.Integrations.Confluence != nil && project.Integrations.Confluence.Enabled && project.Integrations.Confluence.Polling.IngestionEnabled {
			schedules = append(schedules, providerSchedule(projectID, ProviderConfluence, project.Integrations.Confluence.Polling))
		}
	}
	return schedules
}

func providerSchedule(projectID string, provider Provider, polling config.IntegrationPolling) ProviderSchedule {
	return ProviderSchedule{
		ProjectID:           projectID,
		Provider:            provider,
		InitialFullSync:     strings.TrimSpace(polling.InitialFullSync),
		IncrementalInterval: defaultDuration(polling.IncrementalInterval, time.Minute),
		EmptyPollSleep:      polling.EmptyPollSleep,
		MaxIdleSleep:        polling.MaxIdleSleep,
	}
}

func nextSchedulerDelay(schedule ProviderSchedule, result PollRunResult, err error) time.Duration {
	delay := defaultDuration(schedule.IncrementalInterval, time.Minute)
	if err != nil {
		return idleSchedulerDelay(schedule, delay)
	}
	if !result.Run.EmptyPoll {
		return delay
	}
	return clampSchedulerDelay(schedule, delayForIdleResult(schedule, result, delay))
}

func delayForIdleResult(schedule ProviderSchedule, result PollRunResult, fallback time.Duration) time.Duration {
	idle := result.Run.IdleSleep
	if idle <= 0 {
		idle = schedule.EmptyPollSleep
	}
	return defaultDuration(idle, fallback)
}

func idleSchedulerDelay(schedule ProviderSchedule, fallback time.Duration) time.Duration {
	idle := schedule.EmptyPollSleep
	if idle <= 0 {
		idle = fallback
	}
	return clampSchedulerDelay(schedule, idle)
}

func clampSchedulerDelay(schedule ProviderSchedule, idle time.Duration) time.Duration {
	if schedule.MaxIdleSleep > 0 && idle > schedule.MaxIdleSleep {
		idle = schedule.MaxIdleSleep
	}
	return defaultDuration(idle, defaultDuration(schedule.IncrementalInterval, time.Minute))
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(defaultDuration(duration, time.Minute))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func defaultDuration(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func redactedSchedulerError(provider Provider, err error) error {
	return fmt.Errorf("%w: provider=%s category=%s", ErrProviderRequestFailed, provider, schedulerErrorCategory(err))
}

func schedulerErrorCategory(err error) ErrorCategory {
	var providerErr *ProviderError
	switch {
	case errors.As(err, &providerErr):
		return providerErr.Category
	case errors.Is(err, ErrCredentialUnavailable):
		return ErrorCategoryCredentialUnavailable
	case errors.Is(err, ErrNotFound):
		return ErrorCategoryNotFound
	case errors.Is(err, ErrInvalidInput):
		return ErrorCategoryRequestFailed
	default:
		return ErrorCategoryRequestFailed
	}
}
