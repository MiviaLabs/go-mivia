package store

import (
	"context"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
)

type MemoryStore struct {
	mu          sync.RWMutex
	workflows   map[workflowKey]projectworkflow.WorkflowDefinition
	workflowRef map[workflowRefKey]string
	snapshots   map[snapshotKey]projectworkflow.WorkflowPermissionSnapshot
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workflows:   make(map[workflowKey]projectworkflow.WorkflowDefinition),
		workflowRef: make(map[workflowRefKey]string),
		snapshots:   make(map[snapshotKey]projectworkflow.WorkflowPermissionSnapshot),
	}
}

func (store *MemoryStore) CreateWorkflow(_ context.Context, workflow projectworkflow.WorkflowDefinition) (projectworkflow.WorkflowDefinition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := workflowKey{projectID: workflow.ProjectID, workflowID: workflow.ID}
	if _, ok := store.workflows[key]; ok {
		return projectworkflow.WorkflowDefinition{}, ErrDuplicate
	}
	refKey := workflowRefKey{projectID: workflow.ProjectID, workflowRef: workflow.WorkflowRef}
	if _, ok := store.workflowRef[refKey]; ok {
		return projectworkflow.WorkflowDefinition{}, ErrDuplicate
	}
	workflow = cloneWorkflow(workflow)
	store.workflows[key] = workflow
	store.workflowRef[refKey] = workflow.ID
	return cloneWorkflow(workflow), nil
}

func (store *MemoryStore) GetWorkflow(_ context.Context, projectID, workflowID string) (projectworkflow.WorkflowDefinition, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	workflow, ok := store.workflows[workflowKey{projectID: projectID, workflowID: workflowID}]
	if !ok {
		return projectworkflow.WorkflowDefinition{}, ErrNotFound
	}
	return cloneWorkflow(workflow), nil
}

func (store *MemoryStore) ListWorkflows(_ context.Context, filter WorkflowFilter) ([]projectworkflow.WorkflowDefinition, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectworkflow.WorkflowDefinition, 0)
	for _, workflow := range store.workflows {
		if workflow.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Status != "" && workflow.Status != filter.Status {
			continue
		}
		if filter.WorkflowRef != "" && workflow.WorkflowRef != filter.WorkflowRef {
			continue
		}
		out = append(out, cloneWorkflow(workflow))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].WorkflowRef < out[j].WorkflowRef
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *MemoryStore) UpdateWorkflow(_ context.Context, workflow projectworkflow.WorkflowDefinition) (projectworkflow.WorkflowDefinition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := workflowKey{projectID: workflow.ProjectID, workflowID: workflow.ID}
	existing, ok := store.workflows[key]
	if !ok {
		return projectworkflow.WorkflowDefinition{}, ErrNotFound
	}
	if existing.WorkflowRef != workflow.WorkflowRef {
		newRefKey := workflowRefKey{projectID: workflow.ProjectID, workflowRef: workflow.WorkflowRef}
		if _, ok := store.workflowRef[newRefKey]; ok {
			return projectworkflow.WorkflowDefinition{}, ErrDuplicate
		}
		delete(store.workflowRef, workflowRefKey{projectID: workflow.ProjectID, workflowRef: existing.WorkflowRef})
		store.workflowRef[newRefKey] = workflow.ID
	}
	workflow = cloneWorkflow(workflow)
	store.workflows[key] = workflow
	return cloneWorkflow(workflow), nil
}

func (store *MemoryStore) CreatePermissionSnapshot(_ context.Context, snapshot projectworkflow.WorkflowPermissionSnapshot) (projectworkflow.WorkflowPermissionSnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := snapshotKey{projectID: snapshot.ProjectID, snapshotID: snapshot.ID}
	if _, ok := store.snapshots[key]; ok {
		return projectworkflow.WorkflowPermissionSnapshot{}, ErrDuplicate
	}
	snapshot = clonePermissionSnapshot(snapshot)
	store.snapshots[key] = snapshot
	return clonePermissionSnapshot(snapshot), nil
}

func (store *MemoryStore) GetPermissionSnapshot(_ context.Context, projectID, snapshotID string) (projectworkflow.WorkflowPermissionSnapshot, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	snapshot, ok := store.snapshots[snapshotKey{projectID: projectID, snapshotID: snapshotID}]
	if !ok {
		return projectworkflow.WorkflowPermissionSnapshot{}, ErrNotFound
	}
	return clonePermissionSnapshot(snapshot), nil
}

func (store *MemoryStore) ListPermissionSnapshots(_ context.Context, filter PermissionSnapshotFilter) ([]projectworkflow.WorkflowPermissionSnapshot, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectworkflow.WorkflowPermissionSnapshot, 0)
	for _, snapshot := range store.snapshots {
		if snapshot.ProjectID != filter.ProjectID {
			continue
		}
		if filter.WorkflowID != "" && snapshot.WorkflowID != filter.WorkflowID {
			continue
		}
		if filter.AgentID != "" && snapshot.AgentID != filter.AgentID {
			continue
		}
		out = append(out, clonePermissionSnapshot(snapshot))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func cloneWorkflow(workflow projectworkflow.WorkflowDefinition) projectworkflow.WorkflowDefinition {
	workflow.Agents = cloneAgents(workflow.Agents)
	workflow.Steps = cloneSteps(workflow.Steps)
	workflow.ReviewGates = cloneReviewGates(workflow.ReviewGates)
	workflow.PermissionSnapshots = clonePermissionSnapshots(workflow.PermissionSnapshots)
	return workflow
}

func cloneAgents(agents []projectworkflow.WorkflowAgentDefinition) []projectworkflow.WorkflowAgentDefinition {
	out := append([]projectworkflow.WorkflowAgentDefinition(nil), agents...)
	for i := range out {
		out[i].AllowedSkills = append([]string(nil), out[i].AllowedSkills...)
		out[i].AllowedTools = append([]string(nil), out[i].AllowedTools...)
		out[i].AllowedCommands = append([]string(nil), out[i].AllowedCommands...)
		out[i].DeniedCommands = append([]string(nil), out[i].DeniedCommands...)
	}
	return out
}

func cloneSteps(steps []projectworkflow.WorkflowStep) []projectworkflow.WorkflowStep {
	out := append([]projectworkflow.WorkflowStep(nil), steps...)
	for i := range out {
		out[i].DependsOn = append([]string(nil), out[i].DependsOn...)
		out[i].EvidenceNeeded = append([]string(nil), out[i].EvidenceNeeded...)
		out[i].ContextPackRefs = append([]string(nil), out[i].ContextPackRefs...)
		out[i].LikelyFilesAffected = append([]string(nil), out[i].LikelyFilesAffected...)
	}
	return out
}

func cloneReviewGates(gates []projectworkflow.WorkflowReviewGate) []projectworkflow.WorkflowReviewGate {
	out := append([]projectworkflow.WorkflowReviewGate(nil), gates...)
	for i := range out {
		out[i].AppliesTo = append([]string(nil), out[i].AppliesTo...)
		out[i].RequiredArtifacts = append([]string(nil), out[i].RequiredArtifacts...)
		out[i].AllowedActions = append([]string(nil), out[i].AllowedActions...)
	}
	return out
}

func clonePermissionSnapshots(snapshots []projectworkflow.WorkflowPermissionSnapshot) []projectworkflow.WorkflowPermissionSnapshot {
	out := append([]projectworkflow.WorkflowPermissionSnapshot(nil), snapshots...)
	for i := range out {
		out[i] = clonePermissionSnapshot(out[i])
	}
	return out
}

func clonePermissionSnapshot(snapshot projectworkflow.WorkflowPermissionSnapshot) projectworkflow.WorkflowPermissionSnapshot {
	snapshot.AllowedSkills = append([]string(nil), snapshot.AllowedSkills...)
	snapshot.AllowedTools = append([]string(nil), snapshot.AllowedTools...)
	snapshot.AllowedCommands = append([]string(nil), snapshot.AllowedCommands...)
	snapshot.DeniedCommands = append([]string(nil), snapshot.DeniedCommands...)
	return snapshot
}

type workflowKey struct {
	projectID  string
	workflowID string
}

type workflowRefKey struct {
	projectID   string
	workflowRef string
}

type snapshotKey struct {
	projectID  string
	snapshotID string
}
