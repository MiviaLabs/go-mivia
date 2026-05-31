package store

import (
	"context"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
)

type MemoryStore struct {
	mu           sync.RWMutex
	tasks        map[string]model.Task
	researchRuns map[string]model.ResearchRun
	agentRuns    map[string]model.AgentRun
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:        make(map[string]model.Task),
		researchRuns: make(map[string]model.ResearchRun),
		agentRuns:    make(map[string]model.AgentRun),
	}
}

func (store *MemoryStore) CreateTask(_ context.Context, task model.Task) (model.Task, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tasks[task.ID] = task
	return task, nil
}

func (store *MemoryStore) GetTask(_ context.Context, id string) (model.Task, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	task, ok := store.tasks[id]
	if !ok {
		return model.Task{}, ErrNotFound
	}
	return task, nil
}

func (store *MemoryStore) UpdateTask(_ context.Context, task model.Task) (model.Task, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.tasks[task.ID]; !ok {
		return model.Task{}, ErrNotFound
	}
	store.tasks[task.ID] = task
	return task, nil
}

func (store *MemoryStore) CreateResearchRun(_ context.Context, run model.ResearchRun) (model.ResearchRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.researchRuns[run.ID] = run
	return run, nil
}

func (store *MemoryStore) GetResearchRun(_ context.Context, id string) (model.ResearchRun, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	run, ok := store.researchRuns[id]
	if !ok {
		return model.ResearchRun{}, ErrNotFound
	}
	return run, nil
}

func (store *MemoryStore) CreateAgentRun(_ context.Context, run model.AgentRun) (model.AgentRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.agentRuns[run.ID] = cloneAgentRun(run)
	return cloneAgentRun(run), nil
}

func (store *MemoryStore) AppendAgentStep(_ context.Context, runID string, step model.AgentStep) (model.AgentRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.agentRuns[runID]
	if !ok {
		return model.AgentRun{}, ErrNotFound
	}
	run.Steps = append(run.Steps, cloneAgentStep(step))
	store.agentRuns[runID] = run
	return cloneAgentRun(run), nil
}

func (store *MemoryStore) PromoteAgentArtifact(_ context.Context, runID string, promotion model.AgentPromotion) (model.AgentRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.agentRuns[runID]
	if !ok {
		return model.AgentRun{}, ErrNotFound
	}
	run.Promotions = appendAgentPromotion(run.Promotions, promotion)
	store.agentRuns[runID] = run
	return cloneAgentRun(run), nil
}

func (store *MemoryStore) CompleteAgentRun(_ context.Context, run model.AgentRun) (model.AgentRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.agentRuns[run.ID]; !ok {
		return model.AgentRun{}, ErrNotFound
	}
	store.agentRuns[run.ID] = cloneAgentRun(run)
	return cloneAgentRun(run), nil
}

func (store *MemoryStore) GetAgentRun(_ context.Context, id string) (model.AgentRun, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	run, ok := store.agentRuns[id]
	if !ok {
		return model.AgentRun{}, ErrNotFound
	}
	return cloneAgentRun(run), nil
}

func cloneAgentRun(run model.AgentRun) model.AgentRun {
	run.ChangedFiles = append([]string(nil), run.ChangedFiles...)
	run.Verifiers = cloneAgentVerifiers(run.Verifiers)
	run.Artifacts = cloneAgentArtifacts(run.Artifacts)
	run.Promotions = cloneAgentPromotions(run.Promotions)
	run.Steps = append([]model.AgentStep(nil), run.Steps...)
	for i := range run.Steps {
		run.Steps[i] = cloneAgentStep(run.Steps[i])
	}
	return run
}

func cloneAgentStep(step model.AgentStep) model.AgentStep {
	step.ChangedFiles = append([]string(nil), step.ChangedFiles...)
	step.Verifiers = cloneAgentVerifiers(step.Verifiers)
	step.Artifacts = cloneAgentArtifacts(step.Artifacts)
	return step
}

func cloneAgentVerifiers(verifiers []model.AgentVerifier) []model.AgentVerifier {
	out := append([]model.AgentVerifier(nil), verifiers...)
	for i := range out {
		out[i].Args = append([]string(nil), out[i].Args...)
	}
	return out
}

func cloneAgentArtifacts(artifacts []model.AgentArtifact) []model.AgentArtifact {
	return append([]model.AgentArtifact(nil), artifacts...)
}

func cloneAgentPromotions(promotions []model.AgentPromotion) []model.AgentPromotion {
	return append([]model.AgentPromotion(nil), promotions...)
}

func appendAgentPromotion(promotions []model.AgentPromotion, promotion model.AgentPromotion) []model.AgentPromotion {
	out := append([]model.AgentPromotion(nil), promotions...)
	return append(out, promotion)
}
