package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
)

const labelProjectWorkflowChainRun = "ProjectWorkflowChainRun"

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

func (store *LadybugStore) CreateChainRun(ctx context.Context, run projectworkflowchain.ChainRun) (projectworkflowchain.ChainRun, error) {
	run = cloneRun(run)
	if _, err := store.graph.GetNode(ctx, labelProjectWorkflowChainRun, graphID(run.ProjectID, run.ID)); err == nil {
		return projectworkflowchain.ChainRun{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectworkflowchain.ChainRun{}, err
	}
	return cloneRun(run), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, chainRunNode(run))
	})
}

func (store *LadybugStore) GetChainRun(ctx context.Context, projectID, chainRunID string) (projectworkflowchain.ChainRun, error) {
	node, err := store.graph.GetNode(ctx, labelProjectWorkflowChainRun, graphID(projectID, chainRunID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectworkflowchain.ChainRun{}, ErrNotFound
	}
	if err != nil {
		return projectworkflowchain.ChainRun{}, err
	}
	run := nodeToChainRun(node)
	if run.ProjectID != projectID {
		return projectworkflowchain.ChainRun{}, ErrNotFound
	}
	return cloneRun(run), nil
}

func (store *LadybugStore) ListChainRuns(ctx context.Context, filter projectworkflowchain.ChainFilter) ([]projectworkflowchain.ChainRun, error) {
	nodeFilter := map[string]string{}
	if filter.ProjectID != "" {
		nodeFilter["project_id"] = filter.ProjectID
	}
	if filter.ChainRef != "" {
		nodeFilter["chain_ref"] = filter.ChainRef
	}
	if filter.Status != "" {
		nodeFilter["status"] = filter.Status
	}
	nodes, err := store.graph.ListNodes(ctx, labelProjectWorkflowChainRun, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectworkflowchain.ChainRun, 0, len(nodes))
	for _, node := range nodes {
		run := nodeToChainRun(node)
		if filter.ProjectID != "" && run.ProjectID != filter.ProjectID {
			continue
		}
		out = append(out, cloneRun(run))
	}
	sortChainRuns(out)
	return out, nil
}

func (store *LadybugStore) UpdateChainRun(ctx context.Context, run projectworkflowchain.ChainRun) (projectworkflowchain.ChainRun, error) {
	run = cloneRun(run)
	return cloneRun(run), store.write(ctx, func(graph ladybug.Graph) error {
		if _, err := graph.GetNode(ctx, labelProjectWorkflowChainRun, graphID(run.ProjectID, run.ID)); errors.Is(err, ladybug.ErrNodeNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		return graph.PutNode(ctx, chainRunNode(run))
	})
}

func (store *LadybugStore) FindChainRunByWorkPlan(ctx context.Context, projectID, workPlanID string) (projectworkflowchain.ChainRun, error) {
	runs, err := store.ListChainRuns(ctx, projectworkflowchain.ChainFilter{ProjectID: projectID})
	if err != nil {
		return projectworkflowchain.ChainRun{}, err
	}
	for _, run := range runs {
		for _, planID := range run.WorkPlanIDs {
			if planID == workPlanID {
				return cloneRun(run), nil
			}
		}
		for _, stage := range run.StageRuns {
			if stage.WorkPlanID == workPlanID {
				return cloneRun(run), nil
			}
		}
	}
	return projectworkflowchain.ChainRun{}, ErrNotFound
}

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return fn(store.graph)
}

func chainRunNode(run projectworkflowchain.ChainRun) ladybug.Node {
	stageRuns, _ := json.Marshal(run.StageRuns)
	return ladybug.Node{Label: labelProjectWorkflowChainRun, ID: graphID(run.ProjectID, run.ID), Properties: map[string]string{
		"id":                run.ID,
		"project_id":        run.ProjectID,
		"chain_ref":         run.ChainRef,
		"input_ref":         run.InputRef,
		"status":            run.Status,
		"context_refs":      joinList(run.ContextRefs),
		"stage_runs_json":   string(stageRuns),
		"work_plan_ids":     joinList(run.WorkPlanIDs),
		"automation_ids":    joinList(run.AutomationIDs),
		"created_by_run_id": run.CreatedByRunID,
		"trace_id":          run.TraceID,
		"gitops_ready":      strconv.FormatBool(run.GitOpsReady),
		"pull_request_ref":  run.PullRequestRef,
		"next_action":       run.NextAction,
		"created_at":        formatTime(run.CreatedAt),
		"updated_at":        formatTime(run.UpdatedAt),
	}}
}

func nodeToChainRun(node ladybug.Node) projectworkflowchain.ChainRun {
	props := node.Properties
	var stageRuns []projectworkflowchain.StageRun
	_ = json.Unmarshal([]byte(props["stage_runs_json"]), &stageRuns)
	gitOpsReady, _ := strconv.ParseBool(props["gitops_ready"])
	return projectworkflowchain.ChainRun{
		ID:             props["id"],
		ProjectID:      props["project_id"],
		ChainRef:       props["chain_ref"],
		InputRef:       props["input_ref"],
		Status:         props["status"],
		ContextRefs:    splitList(props["context_refs"]),
		StageRuns:      stageRuns,
		WorkPlanIDs:    splitList(props["work_plan_ids"]),
		AutomationIDs:  splitList(props["automation_ids"]),
		CreatedByRunID: props["created_by_run_id"],
		TraceID:        props["trace_id"],
		GitOpsReady:    gitOpsReady,
		PullRequestRef: props["pull_request_ref"],
		NextAction:     props["next_action"],
		CreatedAt:      parseTime(props["created_at"]),
		UpdatedAt:      parseTime(props["updated_at"]),
	}
}

func graphID(projectID string, id string) string {
	return projectID + ":" + id
}

func joinList(values []string) string {
	return strings.Join(values, ",")
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func sortChainRuns(values []projectworkflowchain.ChainRun) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
}
