package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
)

const (
	labelProjectWorkflowDefinition         = "ProjectWorkflowDefinition"
	labelProjectWorkflowPermissionSnapshot = "ProjectWorkflowPermissionSnapshot"
)

type LadybugStore struct {
	graph ladybug.Graph
	mu    sync.Mutex
}

func NewLadybugStore(graph ladybug.Graph) *LadybugStore {
	return &LadybugStore{graph: graph}
}

func NewBootstrappedLadybugStore(ctx context.Context, graph ladybug.Graph) (*LadybugStore, error) {
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		return nil, err
	}
	return NewLadybugStore(graph), nil
}

func (store *LadybugStore) CreateWorkflow(ctx context.Context, workflow projectworkflow.WorkflowDefinition) (projectworkflow.WorkflowDefinition, error) {
	workflow = cloneWorkflow(workflow)
	if err := store.ensureUniqueWorkflowRef(ctx, workflow.ProjectID, workflow.WorkflowRef, workflow.ID); err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	if _, err := store.graph.GetNode(ctx, labelProjectWorkflowDefinition, graphID(workflow.ProjectID, workflow.ID)); err == nil {
		return projectworkflow.WorkflowDefinition{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectworkflow.WorkflowDefinition{}, err
	}
	return cloneWorkflow(workflow), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, workflowNode(workflow))
	})
}

func (store *LadybugStore) GetWorkflow(ctx context.Context, projectID, workflowID string) (projectworkflow.WorkflowDefinition, error) {
	node, err := store.graph.GetNode(ctx, labelProjectWorkflowDefinition, graphID(projectID, workflowID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectworkflow.WorkflowDefinition{}, ErrNotFound
	}
	if err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	workflow, err := nodeToWorkflow(node)
	if err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	if workflow.ProjectID != projectID {
		return projectworkflow.WorkflowDefinition{}, ErrNotFound
	}
	return cloneWorkflow(workflow), nil
}

func (store *LadybugStore) ListWorkflows(ctx context.Context, filter WorkflowFilter) ([]projectworkflow.WorkflowDefinition, error) {
	nodeFilter := map[string]string{"project_id": filter.ProjectID}
	if filter.Status != "" {
		nodeFilter["status"] = filter.Status
	}
	if filter.WorkflowRef != "" {
		nodeFilter["workflow_ref"] = filter.WorkflowRef
	}
	nodes, err := store.graph.ListNodes(ctx, labelProjectWorkflowDefinition, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectworkflow.WorkflowDefinition, 0, len(nodes))
	for _, node := range nodes {
		workflow, err := nodeToWorkflow(node)
		if err != nil {
			return nil, err
		}
		if workflow.ProjectID == filter.ProjectID {
			out = append(out, cloneWorkflow(workflow))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].WorkflowRef < out[j].WorkflowRef
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *LadybugStore) UpdateWorkflow(ctx context.Context, workflow projectworkflow.WorkflowDefinition) (projectworkflow.WorkflowDefinition, error) {
	if _, err := store.GetWorkflow(ctx, workflow.ProjectID, workflow.ID); err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	workflow = cloneWorkflow(workflow)
	if err := store.ensureUniqueWorkflowRef(ctx, workflow.ProjectID, workflow.WorkflowRef, workflow.ID); err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	return cloneWorkflow(workflow), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, workflowNode(workflow))
	})
}

func (store *LadybugStore) CreatePermissionSnapshot(ctx context.Context, snapshot projectworkflow.WorkflowPermissionSnapshot) (projectworkflow.WorkflowPermissionSnapshot, error) {
	snapshot = clonePermissionSnapshot(snapshot)
	if _, err := store.graph.GetNode(ctx, labelProjectWorkflowPermissionSnapshot, graphID(snapshot.ProjectID, snapshot.ID)); err == nil {
		return projectworkflow.WorkflowPermissionSnapshot{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectworkflow.WorkflowPermissionSnapshot{}, err
	}
	return clonePermissionSnapshot(snapshot), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, permissionSnapshotNode(snapshot))
	})
}

func (store *LadybugStore) UpdatePermissionSnapshot(ctx context.Context, snapshot projectworkflow.WorkflowPermissionSnapshot) (projectworkflow.WorkflowPermissionSnapshot, error) {
	if _, err := store.GetPermissionSnapshot(ctx, snapshot.ProjectID, snapshot.ID); err != nil {
		return projectworkflow.WorkflowPermissionSnapshot{}, err
	}
	snapshot = clonePermissionSnapshot(snapshot)
	return clonePermissionSnapshot(snapshot), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, permissionSnapshotNode(snapshot))
	})
}

func (store *LadybugStore) GetPermissionSnapshot(ctx context.Context, projectID, snapshotID string) (projectworkflow.WorkflowPermissionSnapshot, error) {
	node, err := store.graph.GetNode(ctx, labelProjectWorkflowPermissionSnapshot, graphID(projectID, snapshotID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectworkflow.WorkflowPermissionSnapshot{}, ErrNotFound
	}
	if err != nil {
		return projectworkflow.WorkflowPermissionSnapshot{}, err
	}
	snapshot, err := nodeToPermissionSnapshot(node)
	if err != nil {
		return projectworkflow.WorkflowPermissionSnapshot{}, err
	}
	if snapshot.ProjectID != projectID {
		return projectworkflow.WorkflowPermissionSnapshot{}, ErrNotFound
	}
	return clonePermissionSnapshot(snapshot), nil
}

func (store *LadybugStore) ListPermissionSnapshots(ctx context.Context, filter PermissionSnapshotFilter) ([]projectworkflow.WorkflowPermissionSnapshot, error) {
	nodeFilter := map[string]string{"project_id": filter.ProjectID}
	if filter.WorkflowID != "" {
		nodeFilter["workflow_id"] = filter.WorkflowID
	}
	if filter.AgentID != "" {
		nodeFilter["agent_id"] = filter.AgentID
	}
	nodes, err := store.graph.ListNodes(ctx, labelProjectWorkflowPermissionSnapshot, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectworkflow.WorkflowPermissionSnapshot, 0, len(nodes))
	for _, node := range nodes {
		snapshot, err := nodeToPermissionSnapshot(node)
		if err != nil {
			return nil, err
		}
		if snapshot.ProjectID == filter.ProjectID {
			out = append(out, clonePermissionSnapshot(snapshot))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *LadybugStore) ensureUniqueWorkflowRef(ctx context.Context, projectID, workflowRef, workflowID string) error {
	if workflowRef == "" {
		return nil
	}
	workflows, err := store.ListWorkflows(ctx, WorkflowFilter{ProjectID: projectID, WorkflowRef: workflowRef})
	if err != nil {
		return err
	}
	for _, workflow := range workflows {
		if workflow.ID != workflowID {
			return ErrDuplicate
		}
	}
	return nil
}

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return fn(store.graph)
}

func workflowNode(workflow projectworkflow.WorkflowDefinition) ladybug.Node {
	payload, _ := json.Marshal(workflow)
	return ladybug.Node{Label: labelProjectWorkflowDefinition, ID: graphID(workflow.ProjectID, workflow.ID), Properties: map[string]string{
		"id":           workflow.ID,
		"project_id":   workflow.ProjectID,
		"workflow_ref": workflow.WorkflowRef,
		"status":       workflow.Status,
		"payload":      string(payload),
	}}
}

func permissionSnapshotNode(snapshot projectworkflow.WorkflowPermissionSnapshot) ladybug.Node {
	payload, _ := json.Marshal(snapshot)
	return ladybug.Node{Label: labelProjectWorkflowPermissionSnapshot, ID: graphID(snapshot.ProjectID, snapshot.ID), Properties: map[string]string{
		"id":          snapshot.ID,
		"project_id":  snapshot.ProjectID,
		"workflow_id": snapshot.WorkflowID,
		"agent_id":    snapshot.AgentID,
		"payload":     string(payload),
	}}
}

func nodeToWorkflow(node ladybug.Node) (projectworkflow.WorkflowDefinition, error) {
	var workflow projectworkflow.WorkflowDefinition
	if err := json.Unmarshal([]byte(node.Properties["payload"]), &workflow); err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	return workflow, nil
}

func nodeToPermissionSnapshot(node ladybug.Node) (projectworkflow.WorkflowPermissionSnapshot, error) {
	var snapshot projectworkflow.WorkflowPermissionSnapshot
	if err := json.Unmarshal([]byte(node.Properties["payload"]), &snapshot); err != nil {
		return projectworkflow.WorkflowPermissionSnapshot{}, err
	}
	return snapshot, nil
}

func graphID(projectID string, id string) string {
	return projectID + ":" + id
}
