package store

import (
	"context"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

type MemoryStore struct {
	mu         sync.Mutex
	automation map[string]projectautomation.Automation
	runs       map[string]projectautomation.AutomationRun
	attempts   map[string]projectautomation.AutomationAttempt
	batches    map[string]projectautomation.AutomationParallelBatch
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		automation: make(map[string]projectautomation.Automation),
		runs:       make(map[string]projectautomation.AutomationRun),
		attempts:   make(map[string]projectautomation.AutomationAttempt),
		batches:    make(map[string]projectautomation.AutomationParallelBatch),
	}
}

func (store *MemoryStore) CreateAutomation(_ context.Context, value projectautomation.Automation) (projectautomation.Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.automation[value.ID]; ok {
		return projectautomation.Automation{}, ErrDuplicate
	}
	for _, existing := range store.automation {
		if existing.ProjectID == value.ProjectID && existing.AutomationRef == value.AutomationRef {
			return projectautomation.Automation{}, ErrDuplicate
		}
	}
	store.automation[value.ID] = cloneAutomation(value)
	return cloneAutomation(value), nil
}

func (store *MemoryStore) GetAutomation(_ context.Context, projectID, automationID string) (projectautomation.Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.automation[automationID]
	if !ok || value.ProjectID != projectID {
		return projectautomation.Automation{}, ErrNotFound
	}
	return cloneAutomation(value), nil
}

func (store *MemoryStore) ListAutomations(_ context.Context, filter projectautomation.AutomationFilter) ([]projectautomation.Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []projectautomation.Automation
	for _, value := range store.automation {
		if value.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Status != "" && value.Status != filter.Status {
			continue
		}
		if filter.AgentID != "" && value.AgentID != filter.AgentID {
			continue
		}
		out = append(out, cloneAutomation(value))
	}
	return out, nil
}

func (store *MemoryStore) UpdateAutomation(_ context.Context, value projectautomation.Automation) (projectautomation.Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	existing, ok := store.automation[value.ID]
	if !ok || existing.ProjectID != value.ProjectID {
		return projectautomation.Automation{}, ErrNotFound
	}
	store.automation[value.ID] = cloneAutomation(value)
	return cloneAutomation(value), nil
}

func (store *MemoryStore) CreateRun(_ context.Context, value projectautomation.AutomationRun) (projectautomation.AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.runs[value.ID]; ok {
		return projectautomation.AutomationRun{}, ErrDuplicate
	}
	store.runs[value.ID] = cloneRun(value)
	return cloneRun(value), nil
}

func (store *MemoryStore) GetRun(_ context.Context, projectID, runID string) (projectautomation.AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.runs[runID]
	if !ok || value.ProjectID != projectID {
		return projectautomation.AutomationRun{}, ErrNotFound
	}
	return cloneRun(value), nil
}

func (store *MemoryStore) ListRuns(_ context.Context, filter projectautomation.RunFilter) ([]projectautomation.AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []projectautomation.AutomationRun
	for _, value := range store.runs {
		if value.ProjectID != filter.ProjectID {
			continue
		}
		if filter.AutomationID != "" && value.AutomationID != filter.AutomationID {
			continue
		}
		if filter.PlanID != "" && value.PlanID != filter.PlanID {
			continue
		}
		if filter.Status != "" && value.Status != filter.Status {
			continue
		}
		if filter.OrchestratorRunID != "" && value.OrchestratorRunID != filter.OrchestratorRunID {
			continue
		}
		out = append(out, cloneRun(value))
	}
	return out, nil
}

func (store *MemoryStore) UpdateRun(_ context.Context, value projectautomation.AutomationRun) (projectautomation.AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	existing, ok := store.runs[value.ID]
	if !ok || existing.ProjectID != value.ProjectID {
		return projectautomation.AutomationRun{}, ErrNotFound
	}
	if shouldPreserveExistingRun(existing, value) {
		return cloneRun(existing), nil
	}
	store.runs[value.ID] = cloneRun(value)
	return cloneRun(value), nil
}

func (store *MemoryStore) CreateAttempt(_ context.Context, value projectautomation.AutomationAttempt) (projectautomation.AutomationAttempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.attempts[value.ID]; ok {
		return projectautomation.AutomationAttempt{}, ErrDuplicate
	}
	store.attempts[value.ID] = cloneAttempt(value)
	return cloneAttempt(value), nil
}

func (store *MemoryStore) CreateParallelBatch(_ context.Context, value projectautomation.AutomationParallelBatch) (projectautomation.AutomationParallelBatch, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.batches[value.ID]; ok {
		return projectautomation.AutomationParallelBatch{}, ErrDuplicate
	}
	store.batches[value.ID] = cloneBatch(value)
	return cloneBatch(value), nil
}

func (store *MemoryStore) GetParallelBatch(_ context.Context, projectID, batchID string) (projectautomation.AutomationParallelBatch, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.batches[batchID]
	if !ok || value.ProjectID != projectID {
		return projectautomation.AutomationParallelBatch{}, ErrNotFound
	}
	return cloneBatch(value), nil
}

func cloneAutomation(value projectautomation.Automation) projectautomation.Automation {
	value.AllowedTaskRefs = append([]string(nil), value.AllowedTaskRefs...)
	return value
}

func cloneRun(value projectautomation.AutomationRun) projectautomation.AutomationRun {
	value.WorkerRunIDs = append([]string(nil), value.WorkerRunIDs...)
	return value
}

func cloneAttempt(value projectautomation.AutomationAttempt) projectautomation.AutomationAttempt {
	value.VerifierResultRefs = append([]string(nil), value.VerifierResultRefs...)
	value.EvidenceRefs = append([]string(nil), value.EvidenceRefs...)
	value.ClaimRefs = append([]string(nil), value.ClaimRefs...)
	value.KnowledgeRefs = append([]string(nil), value.KnowledgeRefs...)
	return value
}

func cloneBatch(value projectautomation.AutomationParallelBatch) projectautomation.AutomationParallelBatch {
	value.TaskIDs = append([]string(nil), value.TaskIDs...)
	return value
}
