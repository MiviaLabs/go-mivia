package store

import (
	"context"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
)

type MemoryStore struct {
	mu     sync.RWMutex
	runs   map[runKey]projectworkflowchain.ChainRun
	planID map[planKey]runKey
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runs: map[runKey]projectworkflowchain.ChainRun{}, planID: map[planKey]runKey{}}
}

func (store *MemoryStore) CreateChainRun(_ context.Context, run projectworkflowchain.ChainRun) (projectworkflowchain.ChainRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := runKey{projectID: run.ProjectID, runID: run.ID}
	if _, ok := store.runs[key]; ok {
		return projectworkflowchain.ChainRun{}, ErrDuplicate
	}
	run = cloneRun(run)
	store.runs[key] = run
	store.indexPlansLocked(key, run)
	return cloneRun(run), nil
}

func (store *MemoryStore) GetChainRun(_ context.Context, projectID, chainRunID string) (projectworkflowchain.ChainRun, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	run, ok := store.runs[runKey{projectID: projectID, runID: chainRunID}]
	if !ok {
		return projectworkflowchain.ChainRun{}, ErrNotFound
	}
	return cloneRun(run), nil
}

func (store *MemoryStore) ListChainRuns(_ context.Context, filter projectworkflowchain.ChainFilter) ([]projectworkflowchain.ChainRun, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectworkflowchain.ChainRun, 0)
	for _, run := range store.runs {
		if filter.ProjectID != "" && run.ProjectID != filter.ProjectID {
			continue
		}
		if filter.ChainRef != "" && run.ChainRef != filter.ChainRef {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		out = append(out, cloneRun(run))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *MemoryStore) UpdateChainRun(_ context.Context, run projectworkflowchain.ChainRun) (projectworkflowchain.ChainRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := runKey{projectID: run.ProjectID, runID: run.ID}
	if _, ok := store.runs[key]; !ok {
		return projectworkflowchain.ChainRun{}, ErrNotFound
	}
	for plan, existing := range store.planID {
		if existing == key {
			delete(store.planID, plan)
		}
	}
	run = cloneRun(run)
	store.runs[key] = run
	store.indexPlansLocked(key, run)
	return cloneRun(run), nil
}

func (store *MemoryStore) FindChainRunByWorkPlan(_ context.Context, projectID, workPlanID string) (projectworkflowchain.ChainRun, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	key, ok := store.planID[planKey{projectID: projectID, planID: workPlanID}]
	if !ok {
		return projectworkflowchain.ChainRun{}, ErrNotFound
	}
	run, ok := store.runs[key]
	if !ok {
		return projectworkflowchain.ChainRun{}, ErrNotFound
	}
	return cloneRun(run), nil
}

func (store *MemoryStore) indexPlansLocked(key runKey, run projectworkflowchain.ChainRun) {
	for _, planID := range run.WorkPlanIDs {
		store.planID[planKey{projectID: run.ProjectID, planID: planID}] = key
	}
	for _, stage := range run.StageRuns {
		if stage.WorkPlanID != "" {
			store.planID[planKey{projectID: run.ProjectID, planID: stage.WorkPlanID}] = key
		}
	}
}

func cloneRun(run projectworkflowchain.ChainRun) projectworkflowchain.ChainRun {
	run.ContextRefs = append([]string(nil), run.ContextRefs...)
	run.WorkPlanIDs = append([]string(nil), run.WorkPlanIDs...)
	run.AutomationIDs = append([]string(nil), run.AutomationIDs...)
	run.StageRuns = append([]projectworkflowchain.StageRun(nil), run.StageRuns...)
	for i := range run.StageRuns {
		run.StageRuns[i].WorkTaskIDs = append([]string(nil), run.StageRuns[i].WorkTaskIDs...)
		run.StageRuns[i].AutomationIDs = append([]string(nil), run.StageRuns[i].AutomationIDs...)
	}
	return run
}

type runKey struct {
	projectID string
	runID     string
}

type planKey struct {
	projectID string
	planID    string
}
