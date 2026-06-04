package projectautomation

import (
	"context"
	"sort"
	"sync"
	"time"
)

const defaultExecutorPollInterval = 5 * time.Second

type Executor struct {
	service *Service
	options ExecutorOptions

	mu       sync.Mutex
	started  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	inFlight map[string]struct{}

	globalSem  chan struct{}
	projectSem map[string]chan struct{}
	agentSem   map[string]chan struct{}
}

func NewExecutor(service *Service, options ExecutorOptions) *Executor {
	if options.PollInterval <= 0 {
		options.PollInterval = defaultExecutorPollInterval
	}
	if options.GlobalWorkerCount <= 0 {
		options.GlobalWorkerCount = 1
	}
	if options.PerProjectWorkerLimit <= 0 || options.PerProjectWorkerLimit > options.GlobalWorkerCount {
		options.PerProjectWorkerLimit = options.GlobalWorkerCount
	}
	if options.PerAgentWorkerLimit <= 0 || options.PerAgentWorkerLimit > options.PerProjectWorkerLimit {
		options.PerAgentWorkerLimit = options.PerProjectWorkerLimit
	}
	return &Executor{
		service:    service,
		options:    options,
		inFlight:   make(map[string]struct{}),
		globalSem:  make(chan struct{}, options.GlobalWorkerCount),
		projectSem: make(map[string]chan struct{}),
		agentSem:   make(map[string]chan struct{}),
	}
}

func (executor *Executor) Start(ctx context.Context) error {
	if executor == nil || executor.service == nil || !executor.shouldRun() {
		return nil
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.started {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	executor.cancel = cancel
	executor.started = true
	executor.wg.Add(1)
	go executor.loop(runCtx)
	return nil
}

func (executor *Executor) Stop(ctx context.Context) error {
	if executor == nil {
		return nil
	}
	executor.mu.Lock()
	cancel := executor.cancel
	executor.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		executor.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		executor.mu.Lock()
		executor.started = false
		executor.cancel = nil
		executor.mu.Unlock()
		return nil
	}
}

func (executor *Executor) shouldRun() bool {
	return executor.options.Enabled &&
		executor.options.RunnerEnabled
}

func (executor *Executor) loop(ctx context.Context) {
	defer executor.wg.Done()
	executor.pollOnce(ctx)
	ticker := time.NewTicker(executor.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			executor.pollOnce(ctx)
		}
	}
}

func (executor *Executor) pollOnce(ctx context.Context) {
	if executor == nil || executor.service == nil || !executor.shouldRun() {
		return
	}
	for _, projectID := range executor.options.ProjectIDs {
		if ctx.Err() != nil {
			return
		}
		executor.submitAutomaticRuns(ctx, projectID)
		if !executor.runsQueuedWork() {
			continue
		}
		runs, err := executor.service.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusQueued})
		if err != nil {
			continue
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
		for _, run := range runs {
			if ctx.Err() != nil {
				return
			}
			if !executor.startRun(ctx, run) {
				return
			}
		}
	}
}

func (executor *Executor) runsQueuedWork() bool {
	return executor.options.RunnerExecution == RunnerExecutionInProcess ||
		executor.options.RunnerExecution == RunnerExecutionManaged
}

func (executor *Executor) submitAutomaticRuns(ctx context.Context, projectID string) {
	automations, err := executor.service.ListAutomations(ctx, AutomationFilter{ProjectID: projectID, Status: AutomationStatusEnabled})
	if err != nil {
		return
	}
	for _, automation := range automations {
		if ctx.Err() != nil {
			return
		}
		if automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || !executor.service.hasReadyAutomaticTask(ctx, automation) || executor.service.hasAnyRun(ctx, automation) {
			continue
		}
		_, _ = executor.service.SubmitRun(ctx, SubmitRunInput{
			ProjectID:         automation.ProjectID,
			AutomationID:      automation.ID,
			PlanID:            automation.PlanID,
			OwnerAgent:        automation.AgentID,
			RunnerKind:        RunnerKindCodexCLI,
			OrchestratorRunID: "automatic:" + automation.ID,
			SafeNextAction:    "execute ready automatic workflow task",
		})
	}
}

func (executor *Executor) startRun(ctx context.Context, run AutomationRun) bool {
	key := run.ProjectID + "/" + run.ID
	executor.mu.Lock()
	if _, ok := executor.inFlight[key]; ok {
		executor.mu.Unlock()
		return true
	}
	projectSem := executor.projectSemaphoreLocked(run.ProjectID)
	agentSem := executor.agentSemaphoreLocked(run.AgentID)
	executor.mu.Unlock()

	if !tryAcquire(ctx, executor.globalSem) {
		return false
	}
	if !tryAcquire(ctx, projectSem) {
		release(executor.globalSem)
		return true
	}
	if !tryAcquire(ctx, agentSem) {
		release(projectSem)
		release(executor.globalSem)
		return true
	}

	executor.mu.Lock()
	if _, ok := executor.inFlight[key]; ok {
		executor.mu.Unlock()
		release(agentSem)
		release(projectSem)
		release(executor.globalSem)
		return true
	}
	executor.inFlight[key] = struct{}{}
	executor.wg.Add(1)
	executor.mu.Unlock()

	go func() {
		defer executor.wg.Done()
		defer release(agentSem)
		defer release(projectSem)
		defer release(executor.globalSem)
		defer func() {
			executor.mu.Lock()
			delete(executor.inFlight, key)
			executor.mu.Unlock()
		}()
		_, _ = executor.service.ExecuteQueuedRun(ctx, run.ProjectID, run.ID)
	}()
	return true
}

func (executor *Executor) projectSemaphoreLocked(projectID string) chan struct{} {
	if semaphore, ok := executor.projectSem[projectID]; ok {
		return semaphore
	}
	semaphore := make(chan struct{}, executor.options.PerProjectWorkerLimit)
	executor.projectSem[projectID] = semaphore
	return semaphore
}

func (executor *Executor) agentSemaphoreLocked(agentID string) chan struct{} {
	if semaphore, ok := executor.agentSem[agentID]; ok {
		return semaphore
	}
	semaphore := make(chan struct{}, executor.options.PerAgentWorkerLimit)
	executor.agentSem[agentID] = semaphore
	return semaphore
}

func tryAcquire(ctx context.Context, semaphore chan struct{}) bool {
	select {
	case semaphore <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func release(semaphore chan struct{}) {
	select {
	case <-semaphore:
	default:
	}
}
