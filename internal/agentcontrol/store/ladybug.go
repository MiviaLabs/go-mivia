package store

import (
	"context"
	"errors"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
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
