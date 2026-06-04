package store

import (
	"context"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

type MemoryStore struct {
	mu          sync.RWMutex
	plans       map[string]projectworkplan.WorkPlan
	planRefs    map[string]string
	tasks       map[string]projectworkplan.WorkTask
	taskRefs    map[string]string
	attachments map[string][]projectworkplan.Attachment
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		plans:       make(map[string]projectworkplan.WorkPlan),
		planRefs:    make(map[string]string),
		tasks:       make(map[string]projectworkplan.WorkTask),
		taskRefs:    make(map[string]string),
		attachments: make(map[string][]projectworkplan.Attachment),
	}
}

func (store *MemoryStore) CreateWorkPlan(_ context.Context, plan projectworkplan.WorkPlan) (projectworkplan.WorkPlan, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.planRefs[planRefKey(plan.ProjectID, plan.PlanRef)]; ok {
		return projectworkplan.WorkPlan{}, ErrDuplicate
	}
	store.plans[planKey(plan.ProjectID, plan.ID)] = plan
	store.planRefs[planRefKey(plan.ProjectID, plan.PlanRef)] = plan.ID
	return plan, nil
}

func (store *MemoryStore) GetWorkPlan(_ context.Context, projectID, planID string) (projectworkplan.WorkPlan, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	plan, ok := store.plans[planKey(projectID, planID)]
	if !ok {
		return projectworkplan.WorkPlan{}, ErrNotFound
	}
	return plan, nil
}

func (store *MemoryStore) ListWorkPlans(_ context.Context, filter projectworkplan.WorkPlanFilter) ([]projectworkplan.WorkPlan, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectworkplan.WorkPlan, 0)
	for _, plan := range store.plans {
		if plan.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Status != "" && plan.Status != filter.Status {
			continue
		}
		if filter.OwnerAgent != "" && plan.OwnerAgent != filter.OwnerAgent {
			continue
		}
		out = append(out, plan)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].PlanRef < out[j].PlanRef
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *MemoryStore) UpdateWorkPlan(_ context.Context, plan projectworkplan.WorkPlan) (projectworkplan.WorkPlan, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := planKey(plan.ProjectID, plan.ID)
	if _, ok := store.plans[key]; !ok {
		return projectworkplan.WorkPlan{}, ErrNotFound
	}
	store.plans[key] = plan
	return plan, nil
}

func (store *MemoryStore) CreateWorkTask(_ context.Context, task projectworkplan.WorkTask) (projectworkplan.WorkTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.plans[planKey(task.ProjectID, task.PlanID)]; !ok {
		return projectworkplan.WorkTask{}, ErrNotFound
	}
	if _, ok := store.taskRefs[taskRefKey(task.ProjectID, task.PlanID, task.TaskRef)]; ok {
		return projectworkplan.WorkTask{}, ErrDuplicate
	}
	task = cloneTask(task)
	store.tasks[taskKey(task.ProjectID, task.ID)] = task
	store.taskRefs[taskRefKey(task.ProjectID, task.PlanID, task.TaskRef)] = task.ID
	return cloneTask(task), nil
}

func (store *MemoryStore) GetWorkTask(_ context.Context, projectID, taskID string) (projectworkplan.WorkTask, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	task, ok := store.tasks[taskKey(projectID, taskID)]
	if !ok {
		return projectworkplan.WorkTask{}, ErrNotFound
	}
	return cloneTask(task), nil
}

func (store *MemoryStore) ListWorkTasks(_ context.Context, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectworkplan.WorkTask, 0)
	for _, task := range store.tasks {
		if task.ProjectID != filter.ProjectID {
			continue
		}
		if filter.PlanID != "" && task.PlanID != filter.PlanID {
			continue
		}
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		if filter.OwnerAgent != "" && task.OwnerAgent != filter.OwnerAgent {
			continue
		}
		if filter.ClaimedByRunID != "" && task.ClaimedByRunID != filter.ClaimedByRunID {
			continue
		}
		out = append(out, cloneTask(task))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].TaskRef < out[j].TaskRef
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *MemoryStore) UpdateWorkTask(_ context.Context, task projectworkplan.WorkTask) (projectworkplan.WorkTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := taskKey(task.ProjectID, task.ID)
	if _, ok := store.tasks[key]; !ok {
		return projectworkplan.WorkTask{}, ErrNotFound
	}
	task = cloneTask(task)
	store.tasks[key] = task
	return cloneTask(task), nil
}

func (store *MemoryStore) CreateAttachment(_ context.Context, attachment projectworkplan.Attachment) (projectworkplan.Attachment, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, ok := store.tasks[taskKey(attachment.ProjectID, attachment.TaskID)]
	if !ok || task.PlanID != attachment.PlanID {
		return projectworkplan.Attachment{}, ErrNotFound
	}
	key := taskKey(attachment.ProjectID, attachment.TaskID)
	store.attachments[key] = append(store.attachments[key], attachment)
	return attachment, nil
}

func (store *MemoryStore) ListAttachments(_ context.Context, projectID, taskID string) ([]projectworkplan.Attachment, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.tasks[taskKey(projectID, taskID)]; !ok {
		return nil, ErrNotFound
	}
	out := append([]projectworkplan.Attachment(nil), store.attachments[taskKey(projectID, taskID)]...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func cloneTask(task projectworkplan.WorkTask) projectworkplan.WorkTask {
	task.EvidenceNeeded = append([]string(nil), task.EvidenceNeeded...)
	task.ContextPackRefs = append([]string(nil), task.ContextPackRefs...)
	task.FilesToRead = append([]string(nil), task.FilesToRead...)
	task.FilesToEdit = append([]string(nil), task.FilesToEdit...)
	task.LikelyFilesAffected = append([]string(nil), task.LikelyFilesAffected...)
	task.DependencyTaskIDs = append([]string(nil), task.DependencyTaskIDs...)
	task.BlockedByTaskIDs = append([]string(nil), task.BlockedByTaskIDs...)
	task.KnowledgeCandidateRefs = append([]string(nil), task.KnowledgeCandidateRefs...)
	task.EvidenceRefs = append([]string(nil), task.EvidenceRefs...)
	task.ClaimRefs = append([]string(nil), task.ClaimRefs...)
	task.VerifierResultRefs = append([]string(nil), task.VerifierResultRefs...)
	task.ReviewResultRefs = append([]string(nil), task.ReviewResultRefs...)
	task.ArtifactRefs = append([]string(nil), task.ArtifactRefs...)
	task.AgentRunIDs = append([]string(nil), task.AgentRunIDs...)
	return task
}

func planKey(projectID, planID string) string {
	return projectID + "\x00" + planID
}

func planRefKey(projectID, planRef string) string {
	return projectID + "\x00" + planRef
}

func taskKey(projectID, taskID string) string {
	return projectID + "\x00" + taskID
}

func taskRefKey(projectID, planID, taskRef string) string {
	return projectID + "\x00" + planID + "\x00" + taskRef
}
