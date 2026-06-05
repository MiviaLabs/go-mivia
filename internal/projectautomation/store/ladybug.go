package store

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

const (
	labelProject                  = "Project"
	labelProjectAutomation        = "ProjectAutomation"
	labelProjectAutomationRun     = "ProjectAutomationRun"
	labelProjectAutomationAttempt = "ProjectAutomationAttempt"
	labelProjectAutomationBatch   = "ProjectAutomationParallelBatch"

	relProjectHasAutomation    = "PROJECT_HAS_AUTOMATION"
	relAutomationHasRun        = "PROJECT_AUTOMATION_HAS_RUN"
	relAutomationRunHasAttempt = "PROJECT_AUTOMATION_RUN_HAS_ATTEMPT"
	relAutomationRunHasBatch   = "PROJECT_AUTOMATION_RUN_HAS_PARALLEL_BATCH"
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

func (store *LadybugStore) CreateAutomation(ctx context.Context, value projectautomation.Automation) (projectautomation.Automation, error) {
	value = cloneAutomation(value)
	if err := store.ensureUniqueAutomationRef(ctx, value.ProjectID, value.AutomationRef, value.ID); err != nil {
		return projectautomation.Automation{}, err
	}
	if _, err := store.graph.GetNode(ctx, labelProjectAutomation, graphID(value.ProjectID, value.ID)); err == nil {
		return projectautomation.Automation{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.Automation{}, err
	}
	return cloneAutomation(value), store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, automationNode(value)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relProjectHasAutomation, nodeRef(labelProject, value.ProjectID), nodeRef(labelProjectAutomation, graphID(value.ProjectID, value.ID)), value.ProjectID, "", "", value.ID))
	})
}

func (store *LadybugStore) GetAutomation(ctx context.Context, projectID, automationID string) (projectautomation.Automation, error) {
	node, err := store.graph.GetNode(ctx, labelProjectAutomation, graphID(projectID, automationID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.Automation{}, ErrNotFound
	}
	if err != nil {
		return projectautomation.Automation{}, err
	}
	value := nodeToAutomation(node)
	if value.ProjectID != projectID {
		return projectautomation.Automation{}, ErrNotFound
	}
	return cloneAutomation(value), nil
}

func (store *LadybugStore) ListAutomations(ctx context.Context, filter projectautomation.AutomationFilter) ([]projectautomation.Automation, error) {
	nodeFilter := map[string]string{"project_id": filter.ProjectID}
	if filter.Status != "" {
		nodeFilter["status"] = filter.Status
	}
	if filter.AgentID != "" {
		nodeFilter["agent_id"] = filter.AgentID
	}
	nodes, err := store.graph.ListNodes(ctx, labelProjectAutomation, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectautomation.Automation, 0, len(nodes))
	for _, node := range nodes {
		value := nodeToAutomation(node)
		if value.ProjectID == filter.ProjectID {
			out = append(out, cloneAutomation(value))
		}
	}
	sortAutomations(out)
	return out, nil
}

func (store *LadybugStore) UpdateAutomation(ctx context.Context, value projectautomation.Automation) (projectautomation.Automation, error) {
	if _, err := store.GetAutomation(ctx, value.ProjectID, value.ID); err != nil {
		return projectautomation.Automation{}, err
	}
	value = cloneAutomation(value)
	if err := store.ensureUniqueAutomationRef(ctx, value.ProjectID, value.AutomationRef, value.ID); err != nil {
		return projectautomation.Automation{}, err
	}
	return cloneAutomation(value), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, automationNode(value))
	})
}

func (store *LadybugStore) CreateRun(ctx context.Context, value projectautomation.AutomationRun) (projectautomation.AutomationRun, error) {
	value = cloneRun(value)
	if _, err := store.graph.GetNode(ctx, labelProjectAutomationRun, graphID(value.ProjectID, value.ID)); err == nil {
		return projectautomation.AutomationRun{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.AutomationRun{}, err
	}
	return cloneRun(value), store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, runNode(value)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relAutomationHasRun, nodeRef(labelProjectAutomation, graphID(value.ProjectID, value.AutomationID)), nodeRef(labelProjectAutomationRun, graphID(value.ProjectID, value.ID)), value.ProjectID, value.PlanID, value.TaskID, value.AutomationID))
	})
}

func (store *LadybugStore) GetRun(ctx context.Context, projectID, runID string) (projectautomation.AutomationRun, error) {
	node, err := store.graph.GetNode(ctx, labelProjectAutomationRun, graphID(projectID, runID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.AutomationRun{}, ErrNotFound
	}
	if err != nil {
		return projectautomation.AutomationRun{}, err
	}
	value := nodeToRun(node)
	if value.ProjectID != projectID {
		return projectautomation.AutomationRun{}, ErrNotFound
	}
	return cloneRun(value), nil
}

func (store *LadybugStore) ListRuns(ctx context.Context, filter projectautomation.RunFilter) ([]projectautomation.AutomationRun, error) {
	nodeFilter := map[string]string{"project_id": filter.ProjectID}
	if filter.AutomationID != "" {
		nodeFilter["automation_id"] = filter.AutomationID
	}
	if filter.PlanID != "" {
		nodeFilter["plan_id"] = filter.PlanID
	}
	if filter.Status != "" {
		nodeFilter["status"] = filter.Status
	}
	if filter.OrchestratorRunID != "" {
		nodeFilter["orchestrator_run_id"] = filter.OrchestratorRunID
	}
	nodes, err := store.graph.ListNodes(ctx, labelProjectAutomationRun, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectautomation.AutomationRun, 0, len(nodes))
	for _, node := range nodes {
		value := nodeToRun(node)
		if value.ProjectID == filter.ProjectID {
			out = append(out, cloneRun(value))
		}
	}
	sortRuns(out)
	return out, nil
}

func (store *LadybugStore) UpdateRun(ctx context.Context, value projectautomation.AutomationRun) (projectautomation.AutomationRun, error) {
	value = cloneRun(value)
	updated := value
	err := store.write(ctx, func(graph ladybug.Graph) error {
		node, err := graph.GetNode(ctx, labelProjectAutomationRun, graphID(value.ProjectID, value.ID))
		if errors.Is(err, ladybug.ErrNodeNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		existing := nodeToRun(node)
		if existing.ProjectID != value.ProjectID {
			return ErrNotFound
		}
		if shouldPreserveExistingRun(existing, value) {
			updated = existing
			return nil
		}
		return graph.PutNode(ctx, runNode(value))
	})
	if err != nil {
		return projectautomation.AutomationRun{}, err
	}
	return cloneRun(updated), nil
}

func (store *LadybugStore) CreateAttempt(ctx context.Context, value projectautomation.AutomationAttempt) (projectautomation.AutomationAttempt, error) {
	value = cloneAttempt(value)
	if _, err := store.graph.GetNode(ctx, labelProjectAutomationAttempt, graphID(value.ProjectID, value.ID)); err == nil {
		return projectautomation.AutomationAttempt{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.AutomationAttempt{}, err
	}
	return cloneAttempt(value), store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, attemptNode(value)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relAutomationRunHasAttempt, nodeRef(labelProjectAutomationRun, graphID(value.ProjectID, value.AutomationRunID)), nodeRef(labelProjectAutomationAttempt, graphID(value.ProjectID, value.ID)), value.ProjectID, "", "", value.AutomationRunID))
	})
}

func (store *LadybugStore) CreateParallelBatch(ctx context.Context, value projectautomation.AutomationParallelBatch) (projectautomation.AutomationParallelBatch, error) {
	value = cloneBatch(value)
	if _, err := store.graph.GetNode(ctx, labelProjectAutomationBatch, graphID(value.ProjectID, value.ID)); err == nil {
		return projectautomation.AutomationParallelBatch{}, ErrDuplicate
	} else if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.AutomationParallelBatch{}, err
	}
	return cloneBatch(value), store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, batchNode(value)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relAutomationRunHasBatch, nodeRef(labelProjectAutomationRun, graphID(value.ProjectID, value.AutomationRunID)), nodeRef(labelProjectAutomationBatch, graphID(value.ProjectID, value.ID)), value.ProjectID, value.PlanID, "", value.AutomationRunID))
	})
}

func (store *LadybugStore) GetParallelBatch(ctx context.Context, projectID, batchID string) (projectautomation.AutomationParallelBatch, error) {
	node, err := store.graph.GetNode(ctx, labelProjectAutomationBatch, graphID(projectID, batchID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectautomation.AutomationParallelBatch{}, ErrNotFound
	}
	if err != nil {
		return projectautomation.AutomationParallelBatch{}, err
	}
	value := nodeToBatch(node)
	if value.ProjectID != projectID {
		return projectautomation.AutomationParallelBatch{}, ErrNotFound
	}
	return cloneBatch(value), nil
}

func (store *LadybugStore) ensureUniqueAutomationRef(ctx context.Context, projectID string, automationRef string, currentID string) error {
	nodes, err := store.graph.ListNodes(ctx, labelProjectAutomation, map[string]string{"project_id": projectID, "automation_ref": automationRef})
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Properties["id"] != currentID {
			return ErrDuplicate
		}
	}
	return nil
}

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if batch, ok := store.graph.(ladybug.BatchGraph); ok {
		return batch.Batch(ctx, fn)
	}
	return fn(store.graph)
}

func automationNode(value projectautomation.Automation) ladybug.Node {
	return ladybug.Node{Label: labelProjectAutomation, ID: graphID(value.ProjectID, value.ID), Properties: map[string]string{
		"id":                       value.ID,
		"project_id":               value.ProjectID,
		"automation_ref":           value.AutomationRef,
		"title":                    value.Title,
		"purpose":                  value.Purpose,
		"status":                   value.Status,
		"agent_id":                 value.AgentID,
		"plan_id":                  value.PlanID,
		"allowed_task_refs":        joinList(value.AllowedTaskRefs),
		"required_review_task_ids": joinList(value.RequiredReviewTaskIDs),
		"trigger_kind":             value.TriggerKind,
		"source_kind":              value.SourceKind,
		"schedule_policy":          value.SchedulePolicy,
		"permission_ref":           value.PermissionRef,
		"created_by_run_id":        value.CreatedByRunID,
		"trace_id":                 value.TraceID,
		"created_at":               formatTime(value.CreatedAt),
		"updated_at":               formatTime(value.UpdatedAt),
	}}
}

func runNode(value projectautomation.AutomationRun) ladybug.Node {
	return ladybug.Node{Label: labelProjectAutomationRun, ID: graphID(value.ProjectID, value.ID), Properties: map[string]string{
		"id":                  value.ID,
		"project_id":          value.ProjectID,
		"automation_id":       value.AutomationID,
		"agent_id":            value.AgentID,
		"plan_id":             value.PlanID,
		"task_id":             value.TaskID,
		"work_task_status":    value.WorkTaskStatus,
		"status":              value.Status,
		"runner_kind":         value.RunnerKind,
		"agent_run_id":        value.AgentRunID,
		"trace_id":            value.TraceID,
		"attempt_count":       strconv.Itoa(value.AttemptCount),
		"orchestrator_run_id": value.OrchestratorRunID,
		"parent_run_id":       value.ParentRunID,
		"worker_run_ids":      joinList(value.WorkerRunIDs),
		"parallel_group_id":   value.ParallelGroupID,
		"failure_category":    value.FailureCategory,
		"safe_summary":        value.SafeSummary,
		"started_at":          formatTime(value.StartedAt),
		"finished_at":         formatTime(value.FinishedAt),
		"created_at":          formatTime(value.CreatedAt),
		"updated_at":          formatTime(value.UpdatedAt),
	}}
}

func attemptNode(value projectautomation.AutomationAttempt) ladybug.Node {
	return ladybug.Node{Label: labelProjectAutomationAttempt, ID: graphID(value.ProjectID, value.ID), Properties: map[string]string{
		"id":                   value.ID,
		"project_id":           value.ProjectID,
		"automation_run_id":    value.AutomationRunID,
		"attempt_number":       strconv.Itoa(value.AttemptNumber),
		"runner_kind":          value.RunnerKind,
		"command_ref":          value.CommandRef,
		"input_summary_hash":   value.InputSummaryHash,
		"output_summary_hash":  value.OutputSummaryHash,
		"status":               value.Status,
		"failure_category":     value.FailureCategory,
		"duration_ms":          strconv.FormatInt(value.DurationMS, 10),
		"verifier_result_refs": joinList(value.VerifierResultRefs),
		"evidence_refs":        joinList(value.EvidenceRefs),
		"claim_refs":           joinList(value.ClaimRefs),
		"knowledge_refs":       joinList(value.KnowledgeRefs),
		"created_at":           formatTime(value.CreatedAt),
		"finished_at":          formatTime(value.FinishedAt),
	}}
}

func batchNode(value projectautomation.AutomationParallelBatch) ladybug.Node {
	return ladybug.Node{Label: labelProjectAutomationBatch, ID: graphID(value.ProjectID, value.ID), Properties: map[string]string{
		"id":                  value.ID,
		"project_id":          value.ProjectID,
		"automation_run_id":   value.AutomationRunID,
		"orchestrator_run_id": value.OrchestratorRunID,
		"plan_id":             value.PlanID,
		"task_ids":            joinList(value.TaskIDs),
		"status":              value.Status,
		"safety_reason":       value.SafetyReason,
		"conflict_summary":    value.ConflictSummary,
		"created_at":          formatTime(value.CreatedAt),
		"updated_at":          formatTime(value.UpdatedAt),
	}}
}

func nodeToAutomation(node ladybug.Node) projectautomation.Automation {
	props := node.Properties
	return projectautomation.Automation{
		ID:                    props["id"],
		ProjectID:             props["project_id"],
		AutomationRef:         props["automation_ref"],
		Title:                 props["title"],
		Purpose:               props["purpose"],
		Status:                props["status"],
		AgentID:               props["agent_id"],
		PlanID:                props["plan_id"],
		AllowedTaskRefs:       splitList(props["allowed_task_refs"]),
		RequiredReviewTaskIDs: splitList(props["required_review_task_ids"]),
		TriggerKind:           props["trigger_kind"],
		SourceKind:            props["source_kind"],
		SchedulePolicy:        props["schedule_policy"],
		PermissionRef:         props["permission_ref"],
		CreatedByRunID:        props["created_by_run_id"],
		TraceID:               props["trace_id"],
		CreatedAt:             parseTime(props["created_at"]),
		UpdatedAt:             parseTime(props["updated_at"]),
	}
}

func nodeToRun(node ladybug.Node) projectautomation.AutomationRun {
	props := node.Properties
	return projectautomation.AutomationRun{
		ID:                props["id"],
		ProjectID:         props["project_id"],
		AutomationID:      props["automation_id"],
		AgentID:           props["agent_id"],
		PlanID:            props["plan_id"],
		TaskID:            props["task_id"],
		WorkTaskStatus:    props["work_task_status"],
		Status:            props["status"],
		RunnerKind:        props["runner_kind"],
		AgentRunID:        props["agent_run_id"],
		TraceID:           props["trace_id"],
		AttemptCount:      parseInt(props["attempt_count"]),
		OrchestratorRunID: props["orchestrator_run_id"],
		ParentRunID:       props["parent_run_id"],
		WorkerRunIDs:      splitList(props["worker_run_ids"]),
		ParallelGroupID:   props["parallel_group_id"],
		FailureCategory:   props["failure_category"],
		SafeSummary:       props["safe_summary"],
		StartedAt:         parseTime(props["started_at"]),
		FinishedAt:        parseTime(props["finished_at"]),
		CreatedAt:         parseTime(props["created_at"]),
		UpdatedAt:         parseTime(props["updated_at"]),
	}
}

func nodeToAttempt(node ladybug.Node) projectautomation.AutomationAttempt {
	props := node.Properties
	return projectautomation.AutomationAttempt{
		ID:                 props["id"],
		ProjectID:          props["project_id"],
		AutomationRunID:    props["automation_run_id"],
		AttemptNumber:      parseInt(props["attempt_number"]),
		RunnerKind:         props["runner_kind"],
		CommandRef:         props["command_ref"],
		InputSummaryHash:   props["input_summary_hash"],
		OutputSummaryHash:  props["output_summary_hash"],
		Status:             props["status"],
		FailureCategory:    props["failure_category"],
		DurationMS:         parseInt64(props["duration_ms"]),
		VerifierResultRefs: splitList(props["verifier_result_refs"]),
		EvidenceRefs:       splitList(props["evidence_refs"]),
		ClaimRefs:          splitList(props["claim_refs"]),
		KnowledgeRefs:      splitList(props["knowledge_refs"]),
		CreatedAt:          parseTime(props["created_at"]),
		FinishedAt:         parseTime(props["finished_at"]),
	}
}

func nodeToBatch(node ladybug.Node) projectautomation.AutomationParallelBatch {
	props := node.Properties
	return projectautomation.AutomationParallelBatch{
		ID:                props["id"],
		ProjectID:         props["project_id"],
		AutomationRunID:   props["automation_run_id"],
		OrchestratorRunID: props["orchestrator_run_id"],
		PlanID:            props["plan_id"],
		TaskIDs:           splitList(props["task_ids"]),
		Status:            props["status"],
		SafetyReason:      props["safety_reason"],
		ConflictSummary:   props["conflict_summary"],
		CreatedAt:         parseTime(props["created_at"]),
		UpdatedAt:         parseTime(props["updated_at"]),
	}
}

func relationship(relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string, planID string, taskID string, automationID string) ladybug.Relationship {
	return ladybug.Relationship{Type: relationshipType, From: from, To: to, Properties: map[string]string{"project_id": projectID, "plan_id": planID, "task_id": taskID, "automation_id": automationID}}
}

func nodeRef(label string, id string) ladybug.NodeRef {
	return ladybug.NodeRef{Label: label, ID: id}
}

func graphID(projectID string, id string) string {
	return projectID + ":" + id
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

func joinList(values []string) string {
	return strings.Join(values, ",")
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func sortAutomations(values []projectautomation.Automation) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
}

func sortRuns(values []projectautomation.AutomationRun) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
}
