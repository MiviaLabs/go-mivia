package store

import (
	"context"
	"sync"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
)

type MemoryStore struct {
	mu           sync.RWMutex
	tasks        map[string]model.Task
	researchRuns map[string]model.ResearchRun
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:        make(map[string]model.Task),
		researchRuns: make(map[string]model.ResearchRun),
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
