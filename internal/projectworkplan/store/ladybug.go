package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	model "github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

const (
	labelProject                          = "Project"
	labelWorkPlan                         = "WorkPlan"
	labelWorkTask                         = "WorkTask"
	labelWorkTaskEvidenceAttachment       = "WorkTaskEvidenceAttachment"
	labelWorkTaskContextPackAttachment    = "WorkTaskContextPackAttachment"
	labelWorkTaskClaimAttachment          = "WorkTaskClaimAttachment"
	labelWorkTaskVerifierResultAttachment = "WorkTaskVerifierResultAttachment"
	labelWorkTaskReviewResultAttachment   = "WorkTaskReviewResultAttachment"
	labelWorkTaskKnowledgeAttachment      = "WorkTaskKnowledgeCandidateAttachment"

	relProjectHasWorkPlan               = "PROJECT_HAS_WORK_PLAN"
	relWorkPlanHasWorkTask              = "WORK_PLAN_HAS_WORK_TASK"
	relWorkTaskDependsOn                = "WORK_TASK_DEPENDS_ON"
	relWorkTaskHasEvidenceAttachment    = "WORK_TASK_HAS_EVIDENCE_ATTACHMENT"
	relWorkTaskHasContextPackAttachment = "WORK_TASK_HAS_CONTEXT_PACK_ATTACHMENT"
	relWorkTaskHasClaimAttachment       = "WORK_TASK_HAS_CLAIM_ATTACHMENT"
	relWorkTaskHasVerifierAttachment    = "WORK_TASK_HAS_VERIFIER_RESULT_ATTACHMENT"
	relWorkTaskHasReviewAttachment      = "WORK_TASK_HAS_REVIEW_RESULT_ATTACHMENT"
	relWorkTaskHasKnowledgeAttachment   = "WORK_TASK_HAS_KNOWLEDGE_CANDIDATE_ATTACHMENT"
)

type LadybugStore struct {
	graph ladybug.Graph
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

func (store *LadybugStore) CreateWorkPlan(ctx context.Context, plan model.WorkPlan) (model.WorkPlan, error) {
	plan = cloneWorkPlan(plan)
	if err := store.ensureUniquePlanRef(ctx, plan.ProjectID, plan.PlanRef, plan.ID); err != nil {
		return model.WorkPlan{}, err
	}
	return cloneWorkPlan(plan), store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, workPlanNode(plan)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relProjectHasWorkPlan, nodeRef(labelProject, plan.ProjectID), nodeRef(labelWorkPlan, graphID(plan.ProjectID, plan.ID)), plan.ProjectID, plan.ID, ""))
	})
}

func (store *LadybugStore) GetWorkPlan(ctx context.Context, projectID string, planID string) (model.WorkPlan, error) {
	node, err := store.graph.GetNode(ctx, labelWorkPlan, graphID(projectID, planID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return model.WorkPlan{}, ErrNotFound
	}
	if err != nil {
		return model.WorkPlan{}, err
	}
	plan := nodeToWorkPlan(node)
	if plan.ProjectID != projectID {
		return model.WorkPlan{}, ErrNotFound
	}
	return cloneWorkPlan(plan), nil
}

func (store *LadybugStore) ListWorkPlans(ctx context.Context, filter model.WorkPlanFilter) ([]model.WorkPlan, error) {
	nodeFilter := map[string]string{"project_id": filter.ProjectID}
	if filter.Status != "" {
		nodeFilter["status"] = filter.Status
	}
	if filter.OwnerAgent != "" {
		nodeFilter["owner_agent"] = filter.OwnerAgent
	}
	nodes, err := store.graph.ListNodes(ctx, labelWorkPlan, nodeFilter)
	if err != nil {
		return nil, err
	}
	plans := make([]model.WorkPlan, 0, len(nodes))
	for _, node := range nodes {
		plan := nodeToWorkPlan(node)
		if plan.ProjectID == filter.ProjectID {
			plans = append(plans, cloneWorkPlan(plan))
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].CreatedAt.Equal(plans[j].CreatedAt) {
			return plans[i].ID < plans[j].ID
		}
		return plans[i].CreatedAt.Before(plans[j].CreatedAt)
	})
	return plans, nil
}

func (store *LadybugStore) UpdateWorkPlan(ctx context.Context, plan model.WorkPlan) (model.WorkPlan, error) {
	if _, err := store.GetWorkPlan(ctx, plan.ProjectID, plan.ID); err != nil {
		return model.WorkPlan{}, err
	}
	plan = cloneWorkPlan(plan)
	return cloneWorkPlan(plan), store.write(ctx, func(graph ladybug.Graph) error {
		return graph.PutNode(ctx, workPlanNode(plan))
	})
}

func (store *LadybugStore) CreateWorkTask(ctx context.Context, task model.WorkTask) (model.WorkTask, error) {
	if _, err := store.GetWorkPlan(ctx, task.ProjectID, task.PlanID); err != nil {
		return model.WorkTask{}, err
	}
	task = cloneWorkTask(task)
	if err := store.ensureUniqueTaskRef(ctx, task.ProjectID, task.PlanID, task.TaskRef, task.ID); err != nil {
		return model.WorkTask{}, err
	}
	return cloneWorkTask(task), store.write(ctx, func(graph ladybug.Graph) error {
		return store.putTask(ctx, graph, task)
	})
}

func (store *LadybugStore) GetWorkTask(ctx context.Context, projectID string, taskID string) (model.WorkTask, error) {
	node, err := store.graph.GetNode(ctx, labelWorkTask, graphID(projectID, taskID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return model.WorkTask{}, ErrNotFound
	}
	if err != nil {
		return model.WorkTask{}, err
	}
	task := nodeToWorkTask(node)
	if task.ProjectID != projectID {
		return model.WorkTask{}, ErrNotFound
	}
	return cloneWorkTask(task), nil
}

func (store *LadybugStore) ListWorkTasks(ctx context.Context, filter model.WorkTaskFilter) ([]model.WorkTask, error) {
	nodeFilter := map[string]string{"project_id": filter.ProjectID}
	if filter.PlanID != "" {
		nodeFilter["plan_id"] = filter.PlanID
	}
	if filter.Status != "" {
		nodeFilter["status"] = filter.Status
	}
	if filter.OwnerAgent != "" {
		nodeFilter["owner_agent"] = filter.OwnerAgent
	}
	if filter.ClaimedByRunID != "" {
		nodeFilter["claimed_by_run_id"] = filter.ClaimedByRunID
	}
	nodes, err := store.graph.ListNodes(ctx, labelWorkTask, nodeFilter)
	if err != nil {
		return nil, err
	}
	tasks := make([]model.WorkTask, 0, len(nodes))
	for _, node := range nodes {
		task := nodeToWorkTask(node)
		if task.ProjectID == filter.ProjectID {
			tasks = append(tasks, cloneWorkTask(task))
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	return tasks, nil
}

func (store *LadybugStore) UpdateWorkTask(ctx context.Context, task model.WorkTask) (model.WorkTask, error) {
	if _, err := store.GetWorkTask(ctx, task.ProjectID, task.ID); err != nil {
		return model.WorkTask{}, err
	}
	if err := store.ensureUniqueTaskRef(ctx, task.ProjectID, task.PlanID, task.TaskRef, task.ID); err != nil {
		return model.WorkTask{}, err
	}
	task = cloneWorkTask(task)
	return cloneWorkTask(task), store.write(ctx, func(graph ladybug.Graph) error {
		return store.putTask(ctx, graph, task)
	})
}

func (store *LadybugStore) CreateAttachment(ctx context.Context, attachment model.Attachment) (model.Attachment, error) {
	attachment = cloneAttachment(attachment)
	label, relationshipType, refKey, updateTask := attachmentMapping(attachment.Kind, attachment.Ref)
	if label == "" {
		return model.Attachment{}, fmt.Errorf("unknown attachment kind: %s", attachment.Kind)
	}
	return cloneAttachment(attachment), store.attach(ctx, label, relationshipType, attachment.ID, attachment.ProjectID, attachment.PlanID, attachment.TaskID, refKey, attachment.Ref, attachment.AttachedByRunID, attachment.TraceID, attachment.Note, updateTask)
}

func (store *LadybugStore) ListAttachments(ctx context.Context, projectID string, taskID string) ([]model.Attachment, error) {
	if _, err := store.GetWorkTask(ctx, projectID, taskID); err != nil {
		return nil, err
	}
	labels := []string{labelWorkTaskEvidenceAttachment, labelWorkTaskContextPackAttachment, labelWorkTaskClaimAttachment, labelWorkTaskVerifierResultAttachment, labelWorkTaskReviewResultAttachment, labelWorkTaskKnowledgeAttachment}
	out := []model.Attachment{}
	for _, label := range labels {
		nodes, err := store.graph.ListNodes(ctx, label, map[string]string{"project_id": projectID, "task_id": taskID})
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			out = append(out, nodeToAttachment(node))
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

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	if batch, ok := store.graph.(ladybug.BatchGraph); ok {
		return batch.Batch(ctx, fn)
	}
	return fn(store.graph)
}

func (store *LadybugStore) putTask(ctx context.Context, graph ladybug.Graph, task model.WorkTask) error {
	if err := graph.PutNode(ctx, workTaskNode(task)); err != nil {
		return err
	}
	taskRef := nodeRef(labelWorkTask, graphID(task.ProjectID, task.ID))
	if err := graph.PutRelationship(ctx, relationship(relWorkPlanHasWorkTask, nodeRef(labelWorkPlan, graphID(task.ProjectID, task.PlanID)), taskRef, task.ProjectID, task.PlanID, task.ID)); err != nil {
		return err
	}
	for _, dependencyID := range task.DependencyTaskIDs {
		if dependencyID == "" {
			continue
		}
		if err := graph.PutRelationship(ctx, relationship(relWorkTaskDependsOn, taskRef, nodeRef(labelWorkTask, graphID(task.ProjectID, dependencyID)), task.ProjectID, task.PlanID, task.ID)); err != nil {
			return err
		}
	}
	return nil
}

func (store *LadybugStore) attach(ctx context.Context, label string, relationshipType string, attachmentID string, projectID string, planID string, taskID string, refKey string, refValue string, runID string, traceID string, note string, updateTask func(*model.WorkTask)) error {
	task, err := store.GetWorkTask(ctx, projectID, taskID)
	if err != nil {
		return err
	}
	if task.PlanID != planID {
		return ErrNotFound
	}
	updateTask(&task)
	task.UpdatedAt = time.Now().UTC()
	node := ladybug.Node{Label: label, ID: graphID(projectID, attachmentID), Properties: map[string]string{
		"id":                 attachmentID,
		"project_id":         projectID,
		"plan_id":            planID,
		"task_id":            taskID,
		refKey:               refValue,
		"attached_by_run_id": runID,
		"trace_id":           traceID,
		"note":               note,
		"created_at":         formatTime(time.Now().UTC()),
	}}
	return store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, node); err != nil {
			return err
		}
		if err := graph.PutRelationship(ctx, relationship(relationshipType, nodeRef(labelWorkTask, graphID(projectID, taskID)), nodeRef(label, graphID(projectID, attachmentID)), projectID, planID, taskID)); err != nil {
			return err
		}
		return store.putTask(ctx, graph, task)
	})
}

func attachmentMapping(kind string, ref string) (string, string, string, func(*model.WorkTask)) {
	switch kind {
	case "evidence_ref":
		return labelWorkTaskEvidenceAttachment, relWorkTaskHasEvidenceAttachment, "evidence_ref", func(task *model.WorkTask) {
			task.EvidenceRefs = appendUnique(task.EvidenceRefs, ref)
		}
	case "context_pack_ref":
		return labelWorkTaskContextPackAttachment, relWorkTaskHasContextPackAttachment, "context_pack_ref", func(task *model.WorkTask) {
			task.ContextPackRefs = appendUnique(task.ContextPackRefs, ref)
		}
	case "claim_ref":
		return labelWorkTaskClaimAttachment, relWorkTaskHasClaimAttachment, "claim_ref", func(task *model.WorkTask) {
			task.ClaimRefs = appendUnique(task.ClaimRefs, ref)
		}
	case "verifier_result_ref":
		return labelWorkTaskVerifierResultAttachment, relWorkTaskHasVerifierAttachment, "verifier_result_ref", func(task *model.WorkTask) {
			task.VerifierResultRefs = appendUnique(task.VerifierResultRefs, ref)
		}
	case "review_result_ref":
		return labelWorkTaskReviewResultAttachment, relWorkTaskHasReviewAttachment, "review_result_ref", func(task *model.WorkTask) {
			task.ReviewResultRefs = appendUnique(task.ReviewResultRefs, ref)
		}
	case "knowledge_candidate_ref":
		return labelWorkTaskKnowledgeAttachment, relWorkTaskHasKnowledgeAttachment, "knowledge_candidate_ref", func(task *model.WorkTask) {
			task.KnowledgeCandidateRefs = appendUnique(task.KnowledgeCandidateRefs, ref)
		}
	default:
		return "", "", "", nil
	}
}

func (store *LadybugStore) ensureUniquePlanRef(ctx context.Context, projectID string, planRef string, currentID string) error {
	nodes, err := store.graph.ListNodes(ctx, labelWorkPlan, map[string]string{"project_id": projectID, "plan_ref": planRef})
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Properties["id"] != currentID {
			return fmt.Errorf("%w: duplicate work plan ref in project: %s", ErrDuplicate, planRef)
		}
	}
	return nil
}

func (store *LadybugStore) ensureUniqueTaskRef(ctx context.Context, projectID string, planID string, taskRef string, currentID string) error {
	nodes, err := store.graph.ListNodes(ctx, labelWorkTask, map[string]string{"project_id": projectID, "plan_id": planID, "task_ref": taskRef})
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Properties["id"] != currentID {
			return fmt.Errorf("%w: duplicate work task ref in plan: %s", ErrDuplicate, taskRef)
		}
	}
	return nil
}

func workPlanNode(plan model.WorkPlan) ladybug.Node {
	return ladybug.Node{Label: labelWorkPlan, ID: graphID(plan.ProjectID, plan.ID), Properties: map[string]string{
		"id":                 plan.ID,
		"project_id":         plan.ProjectID,
		"plan_ref":           plan.PlanRef,
		"user_request_ref":   plan.UserRequestRef,
		"title":              plan.Title,
		"goal_summary":       plan.GoalSummary,
		"status":             plan.Status,
		"owner_agent":        plan.OwnerAgent,
		"created_by_run_id":  plan.CreatedByRunID,
		"trace_id":           plan.TraceID,
		"current_task_id":    plan.CurrentTaskID,
		"resume_summary":     plan.ResumeSummary,
		"outcome":            plan.Outcome,
		"isolation_mode":     plan.IsolationMode,
		"parallel_group_ref": plan.ParallelGroupRef,
		"workspace_ref":      plan.WorkspaceRef,
		"git_base_ref":       plan.GitBaseRef,
		"git_branch_ref":     plan.GitBranchRef,
		"git_worktree_ref":   plan.GitWorktreeRef,
		"created_at":         formatTime(plan.CreatedAt),
		"updated_at":         formatTime(plan.UpdatedAt),
	}}
}

func workTaskNode(task model.WorkTask) ladybug.Node {
	return ladybug.Node{Label: labelWorkTask, ID: graphID(task.ProjectID, task.ID), Properties: map[string]string{
		"id":                       task.ID,
		"project_id":               task.ProjectID,
		"plan_id":                  task.PlanID,
		"task_ref":                 task.TaskRef,
		"title":                    task.Title,
		"description":              task.Description,
		"status":                   task.Status,
		"owner_agent":              task.OwnerAgent,
		"claimed_by_run_id":        task.ClaimedByRunID,
		"trace_id":                 task.TraceID,
		"evidence_needed":          joinList(task.EvidenceNeeded),
		"context_pack_refs":        joinList(task.ContextPackRefs),
		"files_to_read":            joinList(task.FilesToRead),
		"files_to_edit":            joinList(task.FilesToEdit),
		"likely_files_affected":    joinList(task.LikelyFilesAffected),
		"dependency_task_ids":      joinList(task.DependencyTaskIDs),
		"verification_requirement": task.VerificationRequirement,
		"gitops_verification_mode": task.GitOpsVerificationMode,
		"expected_output":          task.ExpectedOutput,
		"failure_criteria":         task.FailureCriteria,
		"review_gate":              task.ReviewGate,
		"outcome":                  task.Outcome,
		"resume_instructions":      task.ResumeInstructions,
		"blocked_reason":           task.BlockedReason,
		"blocked_by_task_ids":      joinList(task.BlockedByTaskIDs),
		"knowledge_candidate_refs": joinList(task.KnowledgeCandidateRefs),
		"evidence_refs":            joinList(task.EvidenceRefs),
		"claim_refs":               joinList(task.ClaimRefs),
		"verifier_result_refs":     joinList(task.VerifierResultRefs),
		"review_result_refs":       joinList(task.ReviewResultRefs),
		"review_exempt_reason":     task.ReviewExemptReason,
		"artifact_refs":            joinList(task.ArtifactRefs),
		"agent_run_ids":            joinList(task.AgentRunIDs),
		"decomposition_quality":    task.DecompositionQuality,
		"acceptance_criteria":      joinList(task.AcceptanceCriteria),
		"stop_conditions":          joinList(task.StopConditions),
		"verifier_ladder":          joinList(task.VerifierLadder),
		"regression_applicability": task.RegressionApplicability,
		"downstream_impact_refs":   joinList(task.DownstreamImpactRefs),
		"output_contract":          task.OutputContract,
		"created_at":               formatTime(task.CreatedAt),
		"updated_at":               formatTime(task.UpdatedAt),
		"claimed_at":               formatTime(task.ClaimedAt),
		"started_at":               formatTime(task.StartedAt),
		"completed_at":             formatTime(task.CompletedAt),
	}}
}

func nodeToWorkPlan(node ladybug.Node) model.WorkPlan {
	props := node.Properties
	return model.WorkPlan{
		ID:               props["id"],
		ProjectID:        props["project_id"],
		PlanRef:          props["plan_ref"],
		UserRequestRef:   props["user_request_ref"],
		Title:            props["title"],
		GoalSummary:      props["goal_summary"],
		Status:           props["status"],
		OwnerAgent:       props["owner_agent"],
		CreatedByRunID:   props["created_by_run_id"],
		TraceID:          props["trace_id"],
		CurrentTaskID:    props["current_task_id"],
		ResumeSummary:    props["resume_summary"],
		Outcome:          props["outcome"],
		IsolationMode:    props["isolation_mode"],
		ParallelGroupRef: props["parallel_group_ref"],
		WorkspaceRef:     props["workspace_ref"],
		GitBaseRef:       props["git_base_ref"],
		GitBranchRef:     props["git_branch_ref"],
		GitWorktreeRef:   props["git_worktree_ref"],
		CreatedAt:        parseTime(props["created_at"]),
		UpdatedAt:        parseTime(props["updated_at"]),
	}
}

func nodeToWorkTask(node ladybug.Node) model.WorkTask {
	props := node.Properties
	return model.WorkTask{
		ID:                      props["id"],
		ProjectID:               props["project_id"],
		PlanID:                  props["plan_id"],
		TaskRef:                 props["task_ref"],
		Title:                   props["title"],
		Description:             props["description"],
		Status:                  props["status"],
		OwnerAgent:              props["owner_agent"],
		ClaimedByRunID:          props["claimed_by_run_id"],
		TraceID:                 props["trace_id"],
		EvidenceNeeded:          splitList(props["evidence_needed"]),
		ContextPackRefs:         splitList(props["context_pack_refs"]),
		FilesToRead:             splitList(props["files_to_read"]),
		FilesToEdit:             splitList(props["files_to_edit"]),
		LikelyFilesAffected:     splitList(props["likely_files_affected"]),
		DependencyTaskIDs:       splitList(props["dependency_task_ids"]),
		VerificationRequirement: props["verification_requirement"],
		GitOpsVerificationMode:  props["gitops_verification_mode"],
		ExpectedOutput:          props["expected_output"],
		FailureCriteria:         props["failure_criteria"],
		ReviewGate:              props["review_gate"],
		Outcome:                 props["outcome"],
		ResumeInstructions:      props["resume_instructions"],
		BlockedReason:           props["blocked_reason"],
		BlockedByTaskIDs:        splitList(props["blocked_by_task_ids"]),
		KnowledgeCandidateRefs:  splitList(props["knowledge_candidate_refs"]),
		EvidenceRefs:            splitList(props["evidence_refs"]),
		ClaimRefs:               splitList(props["claim_refs"]),
		VerifierResultRefs:      splitList(props["verifier_result_refs"]),
		ReviewResultRefs:        splitList(props["review_result_refs"]),
		ReviewExemptReason:      props["review_exempt_reason"],
		ArtifactRefs:            splitList(props["artifact_refs"]),
		AgentRunIDs:             splitList(props["agent_run_ids"]),
		DecompositionQuality:    props["decomposition_quality"],
		AcceptanceCriteria:      splitList(props["acceptance_criteria"]),
		StopConditions:          splitList(props["stop_conditions"]),
		VerifierLadder:          splitList(props["verifier_ladder"]),
		RegressionApplicability: props["regression_applicability"],
		DownstreamImpactRefs:    splitList(props["downstream_impact_refs"]),
		OutputContract:          props["output_contract"],
		CreatedAt:               parseTime(props["created_at"]),
		UpdatedAt:               parseTime(props["updated_at"]),
		ClaimedAt:               parseTime(props["claimed_at"]),
		StartedAt:               parseTime(props["started_at"]),
		CompletedAt:             parseTime(props["completed_at"]),
	}
}

func nodeToAttachment(node ladybug.Node) model.Attachment {
	props := node.Properties
	kind, ref := attachmentKindAndRef(props)
	return model.Attachment{
		ID:              props["id"],
		ProjectID:       props["project_id"],
		PlanID:          props["plan_id"],
		TaskID:          props["task_id"],
		Kind:            kind,
		Ref:             ref,
		AttachedByRunID: props["attached_by_run_id"],
		TraceID:         props["trace_id"],
		Note:            props["note"],
		CreatedAt:       parseTime(props["created_at"]),
	}
}

func attachmentKindAndRef(props map[string]string) (string, string) {
	for _, key := range []string{"evidence_ref", "context_pack_ref", "claim_ref", "verifier_result_ref", "review_result_ref", "knowledge_candidate_ref"} {
		if props[key] != "" {
			return key, props[key]
		}
	}
	return "", ""
}

func relationship(relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string, planID string, taskID string) ladybug.Relationship {
	return ladybug.Relationship{Type: relationshipType, From: from, To: to, Properties: map[string]string{"project_id": projectID, "plan_id": planID, "task_id": taskID}}
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
	if len(values) == 0 {
		return ""
	}
	data, err := json.Marshal(values)
	if err != nil {
		return strings.Join(values, ",")
	}
	return string(data)
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	if strings.HasPrefix(strings.TrimSpace(value), "[") {
		var values []string
		if err := json.Unmarshal([]byte(value), &values); err == nil {
			return values
		}
	}
	return strings.Split(value, ",")
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return cloneStrings(values)
	}
	for _, existing := range values {
		if existing == value {
			return cloneStrings(values)
		}
	}
	out := cloneStrings(values)
	out = append(out, value)
	return out
}

func cloneWorkPlan(plan model.WorkPlan) model.WorkPlan {
	return plan
}

func cloneWorkTask(task model.WorkTask) model.WorkTask {
	task.EvidenceNeeded = cloneStrings(task.EvidenceNeeded)
	task.ContextPackRefs = cloneStrings(task.ContextPackRefs)
	task.FilesToRead = cloneStrings(task.FilesToRead)
	task.FilesToEdit = cloneStrings(task.FilesToEdit)
	task.LikelyFilesAffected = cloneStrings(task.LikelyFilesAffected)
	task.DependencyTaskIDs = cloneStrings(task.DependencyTaskIDs)
	task.BlockedByTaskIDs = cloneStrings(task.BlockedByTaskIDs)
	task.KnowledgeCandidateRefs = cloneStrings(task.KnowledgeCandidateRefs)
	task.EvidenceRefs = cloneStrings(task.EvidenceRefs)
	task.ClaimRefs = cloneStrings(task.ClaimRefs)
	task.VerifierResultRefs = cloneStrings(task.VerifierResultRefs)
	task.ReviewResultRefs = cloneStrings(task.ReviewResultRefs)
	task.ArtifactRefs = cloneStrings(task.ArtifactRefs)
	task.AgentRunIDs = cloneStrings(task.AgentRunIDs)
	task.AcceptanceCriteria = cloneStrings(task.AcceptanceCriteria)
	task.StopConditions = cloneStrings(task.StopConditions)
	task.VerifierLadder = cloneStrings(task.VerifierLadder)
	task.DownstreamImpactRefs = cloneStrings(task.DownstreamImpactRefs)
	return task
}

func cloneAttachment(attachment model.Attachment) model.Attachment {
	return attachment
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
