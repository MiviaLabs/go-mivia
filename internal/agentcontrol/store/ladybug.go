package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
)

type LadybugStore struct {
	graph ladybug.Graph
}

func NewLadybugStore(graph ladybug.Graph) *LadybugStore {
	return &LadybugStore{graph: graph}
}

func (store *LadybugStore) CreateTask(ctx context.Context, task model.Task) (model.Task, error) {
	err := store.graph.PutNode(ctx, ladybug.Node{
		Label: "Task",
		ID:    task.ID,
		Properties: map[string]string{
			"id":         task.ID,
			"title":      task.Title,
			"status":     task.Status,
			"created_at": task.CreatedAt.Format(time.RFC3339Nano),
			"updated_at": task.UpdatedAt.Format(time.RFC3339Nano),
		},
	})
	return task, err
}

func (store *LadybugStore) GetTask(ctx context.Context, id string) (model.Task, error) {
	node, err := store.graph.GetNode(ctx, "Task", id)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return model.Task{}, ErrNotFound
	}
	if err != nil {
		return model.Task{}, err
	}
	return taskFromNode(node)
}

func (store *LadybugStore) UpdateTask(ctx context.Context, task model.Task) (model.Task, error) {
	if _, err := store.GetTask(ctx, task.ID); err != nil {
		return model.Task{}, err
	}
	return store.CreateTask(ctx, task)
}

func (store *LadybugStore) CreateResearchRun(ctx context.Context, run model.ResearchRun) (model.ResearchRun, error) {
	err := store.graph.PutNode(ctx, ladybug.Node{
		Label: "ResearchRun",
		ID:    run.ID,
		Properties: map[string]string{
			"id":           run.ID,
			"task_id":      run.TaskID,
			"goal_summary": run.GoalSummary,
			"status":       run.Status,
			"created_at":   run.CreatedAt.Format(time.RFC3339Nano),
			"updated_at":   run.UpdatedAt.Format(time.RFC3339Nano),
		},
	})
	return run, err
}

func (store *LadybugStore) GetResearchRun(ctx context.Context, id string) (model.ResearchRun, error) {
	node, err := store.graph.GetNode(ctx, "ResearchRun", id)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return model.ResearchRun{}, ErrNotFound
	}
	if err != nil {
		return model.ResearchRun{}, err
	}
	return researchRunFromNode(node)
}

func (store *LadybugStore) CreateAgentRun(ctx context.Context, run model.AgentRun) (model.AgentRun, error) {
	return store.putAgentRun(ctx, run)
}

func (store *LadybugStore) AppendAgentStep(ctx context.Context, runID string, step model.AgentStep) (model.AgentRun, error) {
	run, err := store.GetAgentRun(ctx, runID)
	if err != nil {
		return model.AgentRun{}, err
	}
	run.Steps = append(run.Steps, step)
	return store.putAgentRun(ctx, run)
}

func (store *LadybugStore) PromoteAgentArtifact(ctx context.Context, runID string, promotion model.AgentPromotion) (model.AgentRun, error) {
	run, err := store.GetAgentRun(ctx, runID)
	if err != nil {
		return model.AgentRun{}, err
	}
	run.Promotions = appendAgentPromotion(run.Promotions, promotion)
	return store.putAgentRun(ctx, run)
}

func (store *LadybugStore) CompleteAgentRun(ctx context.Context, run model.AgentRun) (model.AgentRun, error) {
	if _, err := store.GetAgentRun(ctx, run.ID); err != nil {
		return model.AgentRun{}, err
	}
	return store.putAgentRun(ctx, run)
}

func (store *LadybugStore) GetAgentRun(ctx context.Context, id string) (model.AgentRun, error) {
	node, err := store.graph.GetNode(ctx, "AgentRun", id)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return model.AgentRun{}, ErrNotFound
	}
	if err != nil {
		return model.AgentRun{}, err
	}
	return agentRunFromNode(node)
}

func (store *LadybugStore) putAgentRun(ctx context.Context, run model.AgentRun) (model.AgentRun, error) {
	properties, err := agentRunProperties(run)
	if err != nil {
		return model.AgentRun{}, err
	}
	err = store.graph.PutNode(ctx, ladybug.Node{
		Label:      "AgentRun",
		ID:         run.ID,
		Properties: properties,
	})
	return run, err
}

func taskFromNode(node ladybug.Node) (model.Task, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, node.Properties["created_at"])
	if err != nil {
		return model.Task{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, node.Properties["updated_at"])
	if err != nil {
		return model.Task{}, err
	}
	return model.Task{
		ID:        node.Properties["id"],
		Title:     node.Properties["title"],
		Status:    node.Properties["status"],
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func researchRunFromNode(node ladybug.Node) (model.ResearchRun, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, node.Properties["created_at"])
	if err != nil {
		return model.ResearchRun{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, node.Properties["updated_at"])
	if err != nil {
		return model.ResearchRun{}, err
	}
	return model.ResearchRun{
		ID:          node.Properties["id"],
		TaskID:      node.Properties["task_id"],
		GoalSummary: node.Properties["goal_summary"],
		Status:      node.Properties["status"],
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

func agentRunProperties(run model.AgentRun) (map[string]string, error) {
	changedFiles, err := marshalJSON(run.ChangedFiles)
	if err != nil {
		return nil, err
	}
	verifiers, err := marshalJSON(run.Verifiers)
	if err != nil {
		return nil, err
	}
	artifacts, err := marshalJSON(run.Artifacts)
	if err != nil {
		return nil, err
	}
	promotions, err := marshalJSON(run.Promotions)
	if err != nil {
		return nil, err
	}
	steps, err := marshalJSON(run.Steps)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"id":               run.ID,
		"project_id":       run.ProjectID,
		"task_id":          run.TaskID,
		"status":           run.Status,
		"started_at":       run.StartedAt.Format(time.RFC3339Nano),
		"completed_at":     formatOptionalTime(run.CompletedAt),
		"failure_category": run.FailureCategory,
		"summary":          run.Summary,
		"changed_files":    changedFiles,
		"verifiers":        verifiers,
		"artifacts":        artifacts,
		"promotions":       promotions,
		"steps":            steps,
	}, nil
}

func agentRunFromNode(node ladybug.Node) (model.AgentRun, error) {
	startedAt, err := time.Parse(time.RFC3339Nano, node.Properties["started_at"])
	if err != nil {
		return model.AgentRun{}, err
	}
	completedAt, err := parseOptionalTime(node.Properties["completed_at"])
	if err != nil {
		return model.AgentRun{}, err
	}
	var changedFiles []string
	if err := unmarshalJSON(node.Properties["changed_files"], &changedFiles); err != nil {
		return model.AgentRun{}, err
	}
	var verifiers []model.AgentVerifier
	if err := unmarshalJSON(node.Properties["verifiers"], &verifiers); err != nil {
		return model.AgentRun{}, err
	}
	var artifacts []model.AgentArtifact
	if err := unmarshalJSON(node.Properties["artifacts"], &artifacts); err != nil {
		return model.AgentRun{}, err
	}
	var promotions []model.AgentPromotion
	if err := unmarshalJSON(node.Properties["promotions"], &promotions); err != nil {
		return model.AgentRun{}, err
	}
	var steps []model.AgentStep
	if err := unmarshalJSON(node.Properties["steps"], &steps); err != nil {
		return model.AgentRun{}, err
	}
	return model.AgentRun{
		ID:              node.Properties["id"],
		ProjectID:       node.Properties["project_id"],
		TaskID:          node.Properties["task_id"],
		Status:          node.Properties["status"],
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		FailureCategory: node.Properties["failure_category"],
		Summary:         node.Properties["summary"],
		ChangedFiles:    changedFiles,
		Verifiers:       verifiers,
		Artifacts:       artifacts,
		Promotions:      promotions,
		Steps:           steps,
	}, nil
}

func marshalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func unmarshalJSON(raw string, dst any) error {
	if raw == "" {
		raw = "null"
	}
	return json.Unmarshal([]byte(raw), dst)
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func parseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, raw)
}
