package projectworkplan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidInput = errors.New("invalid input")

// MaxResumeInstructionsLength is kept for legacy tests and stored-value
// compatibility. New resume instruction validation is intentionally uncapped.
const MaxResumeInstructionsLength = 16 * 1024

var (
	refPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
	emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)
)

type Store interface {
	CreateWorkPlan(context.Context, WorkPlan) (WorkPlan, error)
	GetWorkPlan(context.Context, string, string) (WorkPlan, error)
	ListWorkPlans(context.Context, WorkPlanFilter) ([]WorkPlan, error)
	UpdateWorkPlan(context.Context, WorkPlan) (WorkPlan, error)
	CreateWorkTask(context.Context, WorkTask) (WorkTask, error)
	GetWorkTask(context.Context, string, string) (WorkTask, error)
	ListWorkTasks(context.Context, WorkTaskFilter) ([]WorkTask, error)
	UpdateWorkTask(context.Context, WorkTask) (WorkTask, error)
	CreateAttachment(context.Context, Attachment) (Attachment, error)
	ListAttachments(context.Context, string, string) ([]Attachment, error)
}

type Service struct {
	store               Store
	now                 func() time.Time
	newID               func(string) string
	statusChangeHandler WorkPlanStatusChangeHandler
}

func New(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }, newID: newID}
}

type WorkPlanStatusChange struct {
	ProjectID  string
	PlanID     string
	PlanRef    string
	OldStatus  string
	NewStatus  string
	OwnerAgent string
	ChangedAt  time.Time
}

type WorkPlanStatusChangeHandler interface {
	HandleWorkPlanStatusChanged(context.Context, WorkPlanStatusChange) error
}

func (svc *Service) SetStatusChangeHandler(handler WorkPlanStatusChangeHandler) {
	svc.statusChangeHandler = handler
}

func (svc *Service) CreateWorkPlan(ctx context.Context, input CreateWorkPlanInput) (WorkPlan, error) {
	if svc.store == nil {
		return WorkPlan{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return WorkPlan{}, err
	}
	planRef, err := safeRef(input.PlanRef, "plan_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	userRequestRef, err := safeOptionalRef(input.UserRequestRef, "user_request_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	title, err := safeRequiredText(input.Title, "title", 200)
	if err != nil {
		return WorkPlan{}, err
	}
	goal, err := safeRequiredText(input.GoalSummary, "goal_summary", 500)
	if err != nil {
		return WorkPlan{}, err
	}
	owner, err := safeOptionalRef(input.OwnerAgent, "owner_agent")
	if err != nil {
		return WorkPlan{}, err
	}
	runID, err := safeOptionalRef(input.CreatedByRunID, "created_by_run_id")
	if err != nil {
		return WorkPlan{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return WorkPlan{}, err
	}
	resume, err := safeOptionalText(input.ResumeSummary, "resume_summary", 500)
	if err != nil {
		return WorkPlan{}, err
	}
	isolationMode, err := safeWorkPlanIsolationMode(input.IsolationMode)
	if err != nil {
		return WorkPlan{}, err
	}
	parallelGroupRef, err := safeOptionalRef(input.ParallelGroupRef, "parallel_group_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	workspaceRef, err := safeOptionalRef(input.WorkspaceRef, "workspace_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	gitBaseRef, err := safeOptionalRef(input.GitBaseRef, "git_base_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	gitBranchRef, err := safeOptionalRef(input.GitBranchRef, "git_branch_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	gitWorktreeRef, err := safeOptionalRef(input.GitWorktreeRef, "git_worktree_ref")
	if err != nil {
		return WorkPlan{}, err
	}
	if isolationMode == "" && (gitWorktreeRef != "" || gitBranchRef != "" || workspaceRef != "") {
		isolationMode = WorkPlanIsolationDedicatedWorktree
	}
	now := svc.now()
	plan := WorkPlan{ID: svc.newID("work_plan"), ProjectID: projectID, PlanRef: planRef, UserRequestRef: userRequestRef, Title: title, GoalSummary: goal, Status: WorkPlanStatusPlanned, OwnerAgent: owner, CreatedByRunID: runID, TraceID: traceID, ResumeSummary: resume, IsolationMode: isolationMode, ParallelGroupRef: parallelGroupRef, WorkspaceRef: workspaceRef, GitBaseRef: gitBaseRef, GitBranchRef: gitBranchRef, GitWorktreeRef: gitWorktreeRef, CreatedAt: now, UpdatedAt: now}
	return svc.store.CreateWorkPlan(ctx, plan)
}

func (svc *Service) GetWorkPlan(ctx context.Context, projectID, planID string) (WorkPlan, error) {
	projectID, planID, err := safeProjectObject(projectID, planID, "plan_id")
	if err != nil {
		return WorkPlan{}, err
	}
	return svc.store.GetWorkPlan(ctx, projectID, planID)
}

func (svc *Service) ListWorkPlans(ctx context.Context, filter WorkPlanFilter) ([]WorkPlan, error) {
	projectID, err := safeRef(filter.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter.ProjectID = projectID
	if filter.Status != "" {
		if _, err := safePlanStatus(filter.Status); err != nil {
			return nil, err
		}
	}
	if filter.OwnerAgent != "" {
		if filter.OwnerAgent, err = safeOptionalRef(filter.OwnerAgent, "owner_agent"); err != nil {
			return nil, err
		}
	}
	return svc.store.ListWorkPlans(ctx, filter)
}

func (svc *Service) UpdateWorkPlanStatus(ctx context.Context, input UpdateWorkPlanStatusInput) (WorkPlan, error) {
	projectID, planID, err := safeProjectObject(input.ProjectID, input.PlanID, "plan_id")
	if err != nil {
		return WorkPlan{}, err
	}
	next, err := safePlanStatus(input.Status)
	if err != nil {
		return WorkPlan{}, err
	}
	plan, err := svc.store.GetWorkPlan(ctx, projectID, planID)
	if err != nil {
		return WorkPlan{}, err
	}
	governedCloseoutRecoveryRerun := allowsGovernedCloseoutPlanRecoveryRerun(plan, input, next)
	planningStageCompletion := allowsPlanningReadinessStageCompletion(plan, input, next)
	if err := validatePlanTransition(plan.Status, next); err != nil && !governedCloseoutRecoveryRerun && !planningStageCompletion {
		return WorkPlan{}, err
	}
	if next == WorkPlanStatusDone && !planningStageCompletion {
		if err := svc.ensurePlanHasNoOpenTasks(ctx, projectID, planID); err != nil {
			return WorkPlan{}, err
		}
	}
	if input.ResumeSummary != "" {
		plan.ResumeSummary, err = safeOptionalText(input.ResumeSummary, "resume_summary", 500)
		if err != nil {
			return WorkPlan{}, err
		}
	}
	if input.Outcome != "" {
		plan.Outcome, err = safeOptionalText(input.Outcome, "outcome", 500)
		if err != nil {
			return WorkPlan{}, err
		}
	}
	if input.CurrentTaskID != "" {
		plan.CurrentTaskID, err = safeOptionalRef(input.CurrentTaskID, "current_task_id")
		if err != nil {
			return WorkPlan{}, err
		}
	}
	if input.SafeNextAction != "" {
		if _, err := safeOptionalText(input.SafeNextAction, "safe_next_action", 500); err != nil {
			return WorkPlan{}, err
		}
	}
	if input.RunID != "" {
		if _, err := safeOptionalRef(input.RunID, "run_id"); err != nil {
			return WorkPlan{}, err
		}
	}
	if input.TraceID != "" {
		plan.TraceID, err = safeOptionalRef(input.TraceID, "trace_id")
		if err != nil {
			return WorkPlan{}, err
		}
	}
	oldStatus := plan.Status
	plan.Status = next
	plan.UpdatedAt = svc.now()
	updated, err := svc.store.UpdateWorkPlan(ctx, plan)
	if err != nil {
		return WorkPlan{}, err
	}
	if svc.statusChangeHandler != nil && oldStatus != updated.Status {
		if err := svc.statusChangeHandler.HandleWorkPlanStatusChanged(ctx, WorkPlanStatusChange{
			ProjectID:  updated.ProjectID,
			PlanID:     updated.ID,
			PlanRef:    updated.PlanRef,
			OldStatus:  oldStatus,
			NewStatus:  updated.Status,
			OwnerAgent: updated.OwnerAgent,
			ChangedAt:  updated.UpdatedAt,
		}); err != nil {
			return updated, err
		}
	}
	return updated, nil
}

func (svc *Service) ensurePlanHasNoOpenTasks(ctx context.Context, projectID, planID string) error {
	tasks, err := svc.store.ListWorkTasks(ctx, WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !isTerminalTaskStatus(task.Status) {
			return fmt.Errorf("%w: work plan cannot be marked done while task %s is %s", ErrInvalidInput, task.ID, task.Status)
		}
	}
	return nil
}

func (svc *Service) ResumeWorkPlan(ctx context.Context, input ResumeWorkPlanInput) (WorkPlan, error) {
	return svc.GetWorkPlan(ctx, input.ProjectID, input.PlanID)
}

func (svc *Service) CreateWorkTask(ctx context.Context, input CreateWorkTaskInput) (WorkTask, error) {
	if svc.store == nil {
		return WorkTask{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, planID, err := safeProjectObject(input.ProjectID, input.PlanID, "plan_id")
	if err != nil {
		return WorkTask{}, err
	}
	if _, err := svc.store.GetWorkPlan(ctx, projectID, planID); err != nil {
		return WorkTask{}, err
	}
	taskRef, err := safeRef(input.TaskRef, "task_ref")
	if err != nil {
		return WorkTask{}, err
	}
	title, err := safeRequiredText(input.Title, "title", 200)
	if err != nil {
		return WorkTask{}, err
	}
	description, err := safeOptionalText(input.Description, "description", 1000)
	if err != nil {
		return WorkTask{}, err
	}
	owner, err := safeOptionalRef(input.OwnerAgent, "owner_agent")
	if err != nil {
		return WorkTask{}, err
	}
	runID, err := safeOptionalRef(input.RunID, "run_id")
	if err != nil {
		return WorkTask{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return WorkTask{}, err
	}
	task, err := svc.buildTask(ctx, projectID, planID, taskRef, title, description, owner, runID, traceID, input)
	if err != nil {
		return WorkTask{}, err
	}
	return svc.store.CreateWorkTask(ctx, task)
}

func (svc *Service) UpdateWorkTask(ctx context.Context, task WorkTask) (WorkTask, error) {
	if svc.store == nil {
		return WorkTask{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	if _, _, err := safeProjectObject(task.ProjectID, task.ID, "task_id"); err != nil {
		return WorkTask{}, err
	}
	task.UpdatedAt = svc.now()
	return svc.store.UpdateWorkTask(ctx, task)
}

func (svc *Service) buildTask(ctx context.Context, projectID, planID, taskRef, title, description, owner, runID, traceID string, input CreateWorkTaskInput) (WorkTask, error) {
	evidence, err := safeTextList(input.EvidenceNeeded, "evidence_needed", 200)
	if err != nil {
		return WorkTask{}, err
	}
	contextRefs, err := safeRefList(input.ContextPackRefs, "context_pack_refs")
	if err != nil {
		return WorkTask{}, err
	}
	filesToRead, err := safePathList(input.FilesToRead, "files_to_read")
	if err != nil {
		return WorkTask{}, err
	}
	filesToEdit, err := safePathList(input.FilesToEdit, "files_to_edit")
	if err != nil {
		return WorkTask{}, err
	}
	files, err := safePathList(input.LikelyFilesAffected, "likely_files_affected")
	if err != nil {
		return WorkTask{}, err
	}
	deps, err := safeRefList(input.DependencyTaskIDs, "dependency_task_ids")
	if err != nil {
		return WorkTask{}, err
	}
	verify, err := safeRequiredText(input.VerificationRequirement, "verification_requirement", 500)
	if err != nil {
		return WorkTask{}, err
	}
	gitOpsVerificationMode, err := safeOptionalRef(input.GitOpsVerificationMode, "gitops_verification_mode")
	if err != nil {
		return WorkTask{}, err
	}
	expected, err := safeOptionalText(input.ExpectedOutput, "expected_output", 500)
	if err != nil {
		return WorkTask{}, err
	}
	failure, err := safeOptionalText(input.FailureCriteria, "failure_criteria", 500)
	if err != nil {
		return WorkTask{}, err
	}
	reviewGate, err := safeOptionalText(input.ReviewGate, "review_gate", 500)
	if err != nil {
		return WorkTask{}, err
	}
	resume, err := safeResumeInstructions(input.ResumeInstructions, "resume_instructions")
	if err != nil {
		return WorkTask{}, err
	}
	knowledgeRefs, err := safeRefList(input.KnowledgeCandidateRefs, "knowledge_candidate_refs")
	if err != nil {
		return WorkTask{}, err
	}
	evidenceRefs, err := safeRefList(input.EvidenceRefs, "evidence_refs")
	if err != nil {
		return WorkTask{}, err
	}
	claimRefs, err := safeRefList(input.ClaimRefs, "claim_refs")
	if err != nil {
		return WorkTask{}, err
	}
	verifierRefs, err := safeRefList(input.VerifierResultRefs, "verifier_result_refs")
	if err != nil {
		return WorkTask{}, err
	}
	reviewRefs, err := safeRefList(input.ReviewResultRefs, "review_result_refs")
	if err != nil {
		return WorkTask{}, err
	}
	reviewExemptReason, err := safeOptionalText(input.ReviewExemptReason, "review_exempt_reason", 500)
	if err != nil {
		return WorkTask{}, err
	}
	artifactRefs, err := safeRefList(input.ArtifactRefs, "artifact_refs")
	if err != nil {
		return WorkTask{}, err
	}
	agentRunIDs, err := safeRefList(input.AgentRunIDs, "agent_run_ids")
	if err != nil {
		return WorkTask{}, err
	}
	acceptanceCriteria, err := safeTextList(input.AcceptanceCriteria, "acceptance_criteria", 500)
	if err != nil {
		return WorkTask{}, err
	}
	stopConditions, err := safeTextList(input.StopConditions, "stop_conditions", 500)
	if err != nil {
		return WorkTask{}, err
	}
	verifierLadder, err := safeTextList(input.VerifierLadder, "verifier_ladder", 500)
	if err != nil {
		return WorkTask{}, err
	}
	regressionApplicability, err := safeOptionalText(input.RegressionApplicability, "regression_test_applicability", 500)
	if err != nil {
		return WorkTask{}, err
	}
	downstreamImpactRefs, err := safeRefList(input.DownstreamImpactRefs, "downstream_impact_refs")
	if err != nil {
		return WorkTask{}, err
	}
	outputContract, err := safeOptionalText(input.OutputContract, "output_contract", 500)
	if err != nil {
		return WorkTask{}, err
	}
	quality := input.DecompositionQuality
	if quality == "" {
		quality = assessDecomposition(evidence, contextRefs, files, deps, verify, expected, failure, resume)
	}
	if _, err := safeDecompositionQuality(quality); err != nil {
		return WorkTask{}, err
	}
	now := svc.now()
	status := WorkTaskStatusPlanned
	if quality == DecompositionReady && len(deps) == 0 {
		status = WorkTaskStatusReady
	}
	if input.Status != "" {
		status, err = safeTaskStatus(input.Status)
		if err != nil {
			return WorkTask{}, err
		}
		if isTerminalTaskStatus(status) {
			return WorkTask{}, fmt.Errorf("%w: create task status cannot be terminal", ErrInvalidInput)
		}
		if status == WorkTaskStatusReady && len(deps) > 0 {
			ready, err := svc.dependenciesDone(ctx, projectID, planID, deps)
			if err != nil {
				return WorkTask{}, err
			}
			if !ready {
				return WorkTask{}, fmt.Errorf("%w: create task status ready requires completed dependencies", ErrInvalidInput)
			}
		}
	}
	for _, ref := range optionalRefSlice(runID) {
		agentRunIDs = appendUnique(agentRunIDs, ref)
	}
	return WorkTask{ID: svc.newID("work_task"), ProjectID: projectID, PlanID: planID, TaskRef: taskRef, Title: title, Description: description, Status: status, OwnerAgent: owner, TraceID: traceID, EvidenceNeeded: evidence, ContextPackRefs: contextRefs, FilesToRead: filesToRead, FilesToEdit: filesToEdit, LikelyFilesAffected: files, DependencyTaskIDs: deps, VerificationRequirement: verify, GitOpsVerificationMode: gitOpsVerificationMode, ExpectedOutput: expected, FailureCriteria: failure, ReviewGate: reviewGate, ResumeInstructions: resume, KnowledgeCandidateRefs: knowledgeRefs, EvidenceRefs: evidenceRefs, ClaimRefs: claimRefs, VerifierResultRefs: verifierRefs, ReviewResultRefs: reviewRefs, ReviewExemptReason: reviewExemptReason, ArtifactRefs: artifactRefs, AgentRunIDs: agentRunIDs, DecompositionQuality: quality, AcceptanceCriteria: acceptanceCriteria, StopConditions: stopConditions, VerifierLadder: verifierLadder, RegressionApplicability: regressionApplicability, DownstreamImpactRefs: downstreamImpactRefs, OutputContract: outputContract, CreatedAt: now, UpdatedAt: now}, nil
}

func (svc *Service) dependenciesDone(ctx context.Context, projectID, planID string, deps []string) (bool, error) {
	tasks, err := svc.store.ListWorkTasks(ctx, WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return false, err
	}
	done := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if task.Status == WorkTaskStatusDone {
			done[task.ID] = true
			if ref := strings.TrimSpace(task.TaskRef); ref != "" {
				done[ref] = true
			}
		}
	}
	for _, dep := range deps {
		if !done[strings.TrimSpace(dep)] {
			return false, nil
		}
	}
	return true, nil
}

func (svc *Service) GetWorkTask(ctx context.Context, projectID, taskID string) (WorkTask, error) {
	projectID, taskID, err := safeProjectObject(projectID, taskID, "task_id")
	if err != nil {
		return WorkTask{}, err
	}
	return svc.store.GetWorkTask(ctx, projectID, taskID)
}

func (svc *Service) ListWorkTasks(ctx context.Context, filter WorkTaskFilter) ([]WorkTask, error) {
	return svc.listTasks(ctx, filter)
}

func (svc *Service) ListOpenWorkTasks(ctx context.Context, filter WorkTaskFilter) ([]WorkTask, error) {
	tasks, err := svc.listTasks(ctx, filter)
	if err != nil {
		return nil, err
	}
	openPlanIDs, err := svc.openPlanIDs(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	return filterTasks(tasks, func(task WorkTask) bool {
		return openPlanIDs[task.PlanID] && !isTerminalTaskStatus(task.Status)
	}), nil
}

func (svc *Service) ListMineWorkTasks(ctx context.Context, filter WorkTaskFilter) ([]WorkTask, error) {
	if strings.TrimSpace(filter.OwnerAgent) == "" && strings.TrimSpace(filter.ClaimedByRunID) == "" {
		return nil, fmt.Errorf("%w: owner_agent or claimed_by_run_id is required", ErrInvalidInput)
	}
	return svc.listTasks(ctx, filter)
}

func (svc *Service) ListBlockedWorkTasks(ctx context.Context, filter WorkTaskFilter) ([]WorkTask, error) {
	filter.Status = WorkTaskStatusBlocked
	return svc.listTasks(ctx, filter)
}

func (svc *Service) GetNextWorkTask(ctx context.Context, input GetNextWorkTaskInput) (GetNextWorkTaskResult, error) {
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	planID, err := safeOptionalRef(input.PlanID, "plan_id")
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	ownerAgent, err := safeOptionalRef(input.OwnerAgent, "owner_agent")
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	runID, err := safeOptionalRef(input.RunID, "run_id")
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	if _, err := safeOptionalRef(input.TraceID, "trace_id"); err != nil {
		return GetNextWorkTaskResult{}, err
	}
	tasks, err := svc.listTasks(ctx, WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	openPlanIDs, err := svc.openPlanIDs(ctx, projectID)
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	activeTasks := filterTasks(tasks, func(task WorkTask) bool { return openPlanIDs[task.PlanID] })
	result := summarizeTasks(activeTasks)
	done := map[string]bool{}
	byID := map[string]WorkTask{}
	byDependencyRef := map[string]WorkTask{}
	for _, task := range activeTasks {
		byID[task.ID] = task
		if ref := strings.TrimSpace(task.TaskRef); ref != "" {
			byDependencyRef[ref] = task
		}
		if task.Status == WorkTaskStatusDone {
			done[task.ID] = true
			if ref := strings.TrimSpace(task.TaskRef); ref != "" {
				done[ref] = true
			}
		}
	}
	candidates := make([]WorkTask, 0)
	for _, task := range activeTasks {
		if task.Status != WorkTaskStatusReady || task.DecompositionQuality != DecompositionReady || task.VerificationRequirement == "" {
			continue
		}
		if ownerAgent != "" && task.OwnerAgent != "" && task.OwnerAgent != ownerAgent {
			continue
		}
		if task.ClaimedByRunID != "" && !(input.IncludeClaimedByMe && task.ClaimedByRunID == runID) {
			continue
		}
		allDepsDone := true
		for _, dep := range task.DependencyTaskIDs {
			dep = strings.TrimSpace(dep)
			depTask, ok := byID[dep]
			if !ok {
				depTask, ok = byDependencyRef[dep]
			}
			if !ok || depTask.Status == WorkTaskStatusBlocked || !done[dep] {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			candidates = append(candidates, task)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if ownerAgent != "" && candidates[i].OwnerAgent != candidates[j].OwnerAgent {
			return candidates[i].OwnerAgent == ownerAgent
		}
		if len(candidates[i].DependencyTaskIDs) != len(candidates[j].DependencyTaskIDs) {
			return len(candidates[i].DependencyTaskIDs) < len(candidates[j].DependencyTaskIDs)
		}
		if !candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].TaskRef < candidates[j].TaskRef
	})
	if len(candidates) == 0 {
		result.Reason = "no ready task with complete dependencies and safe decomposition"
		return result, nil
	}
	task := candidates[0]
	plan, err := svc.store.GetWorkPlan(ctx, task.ProjectID, task.PlanID)
	if err != nil {
		return GetNextWorkTaskResult{}, err
	}
	result.Found = true
	result.Task = task
	result.Plan = plan
	result.DependencySummary = dependencySummary(task, byID, byDependencyRef)
	result.EvidenceRefs = append([]string(nil), task.EvidenceRefs...)
	result.ContextPackRefs = append([]string(nil), task.ContextPackRefs...)
	result.ResumeInstructions = task.ResumeInstructions
	result.RequiredVerification = task.VerificationRequirement
	result.SafeReason = "ready decomposition, unblocked dependencies, and verifier requirement present"
	return result, nil
}

func (svc *Service) ClaimWorkTask(ctx context.Context, input WorkTaskActionInput) (WorkTask, error) {
	return svc.transitionTask(ctx, input, WorkTaskStatusClaimed)
}

func summarizeTasks(tasks []WorkTask) GetNextWorkTaskResult {
	var result GetNextWorkTaskResult
	for _, task := range tasks {
		open := task.Status != WorkTaskStatusDone && task.Status != WorkTaskStatusFailed && task.Status != WorkTaskStatusCancelled && task.Status != WorkTaskStatusSuperseded
		if open {
			result.OpenCount++
		}
		if task.Status == WorkTaskStatusBlocked {
			result.BlockedCount++
		}
		if open && (task.Status == WorkTaskStatusClaimed || task.ClaimedByRunID != "") {
			result.ClaimedCount++
		}
	}
	return result
}

func (svc *Service) openPlanIDs(ctx context.Context, projectID string) (map[string]bool, error) {
	plans, err := svc.store.ListWorkPlans(ctx, WorkPlanFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	open := map[string]bool{}
	for _, plan := range plans {
		if !isTerminalPlanStatus(plan.Status) {
			open[plan.ID] = true
		}
	}
	return open, nil
}

func isTerminalPlanStatus(status string) bool {
	return status == WorkPlanStatusDone || status == WorkPlanStatusFailed || status == WorkPlanStatusCancelled || status == WorkPlanStatusSuperseded
}

func isTerminalTaskStatus(status string) bool {
	return status == WorkTaskStatusDone || status == WorkTaskStatusFailed || status == WorkTaskStatusCancelled || status == WorkTaskStatusSuperseded
}

func dependencySummary(task WorkTask, byID map[string]WorkTask, byDependencyRef map[string]WorkTask) []DependencySummary {
	out := make([]DependencySummary, 0, len(task.DependencyTaskIDs))
	for _, depID := range task.DependencyTaskIDs {
		dependencyRef := strings.TrimSpace(depID)
		dep := byID[dependencyRef]
		if dep.ID == "" {
			dep = byDependencyRef[dependencyRef]
		}
		out = append(out, DependencySummary{TaskID: depID, Status: dep.Status, Ready: dep.Status == WorkTaskStatusDone})
	}
	return out
}

func (svc *Service) ReleaseWorkTask(ctx context.Context, input WorkTaskActionInput) (WorkTask, error) {
	return svc.transitionTask(ctx, input, WorkTaskStatusReady)
}

func (svc *Service) StartWorkTask(ctx context.Context, input WorkTaskActionInput) (WorkTask, error) {
	return svc.transitionTask(ctx, input, WorkTaskStatusInProgress)
}

func (svc *Service) CompleteWorkTask(ctx context.Context, input WorkTaskActionInput) (WorkTask, error) {
	return svc.transitionTask(ctx, input, WorkTaskStatusDone)
}

func (svc *Service) FailWorkTask(ctx context.Context, input WorkTaskActionInput) (WorkTask, error) {
	return svc.transitionTask(ctx, input, WorkTaskStatusFailed)
}

func (svc *Service) BlockWorkTask(ctx context.Context, input WorkTaskActionInput) (WorkTask, error) {
	return svc.transitionTask(ctx, input, WorkTaskStatusBlocked)
}

func (svc *Service) UpdateWorkTaskStatus(ctx context.Context, input UpdateWorkTaskStatusInput) (WorkTask, error) {
	next, err := safeTaskStatus(input.Status)
	if err != nil {
		return WorkTask{}, err
	}
	return svc.transitionTask(ctx, input.WorkTaskActionInput, next)
}

func (svc *Service) ExpandWorkTaskScope(ctx context.Context, input ExpandWorkTaskScopeInput) (WorkTask, error) {
	projectID, taskID, err := safeProjectObject(input.ProjectID, input.TaskID, "task_id")
	if err != nil {
		return WorkTask{}, err
	}
	filesToEdit, err := safePathList(input.FilesToEdit, "files_to_edit")
	if err != nil {
		return WorkTask{}, err
	}
	if len(filesToEdit) == 0 {
		return WorkTask{}, fmt.Errorf("%w: files_to_edit is required", ErrInvalidInput)
	}
	resume, err := safeResumeInstructions(input.ResumeInstructions, "resume_instructions")
	if err != nil {
		return WorkTask{}, err
	}
	runID, err := safeOptionalRef(input.RunID, "run_id")
	if err != nil {
		return WorkTask{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return WorkTask{}, err
	}
	task, err := svc.store.GetWorkTask(ctx, projectID, taskID)
	if err != nil {
		return WorkTask{}, err
	}
	for _, path := range filesToEdit {
		task.FilesToEdit = appendUnique(task.FilesToEdit, path)
	}
	if resume != "" {
		task.ResumeInstructions = resume
	}
	if runID != "" {
		task.AgentRunIDs = appendUnique(task.AgentRunIDs, runID)
	}
	if traceID != "" {
		task.TraceID = traceID
	}
	task.UpdatedAt = svc.now()
	return svc.store.UpdateWorkTask(ctx, task)
}

func (svc *Service) transitionTask(ctx context.Context, input WorkTaskActionInput, next string) (WorkTask, error) {
	projectID, taskID, err := safeProjectObject(input.ProjectID, input.TaskID, "task_id")
	if err != nil {
		return WorkTask{}, err
	}
	task, err := svc.store.GetWorkTask(ctx, projectID, taskID)
	if err != nil {
		return WorkTask{}, err
	}
	gitOpsRecoveryRerun := allowsGitOpsRecoveryRerun(task, input, next)
	governedCloseoutRecoveryRerun := allowsGovernedCloseoutTaskRecoveryRerun(task, input, next)
	if next == WorkTaskStatusClaimed {
		if strings.TrimSpace(input.RunID) == "" {
			return WorkTask{}, fmt.Errorf("%w: run_id is required to claim a task", ErrInvalidInput)
		}
		runID, err := safeOptionalRef(input.RunID, "run_id")
		if err != nil {
			return WorkTask{}, err
		}
		if task.ClaimedByRunID != "" && task.ClaimedByRunID != runID {
			return WorkTask{}, fmt.Errorf("%w: task is claimed by another run", ErrInvalidInput)
		}
	}
	if (gitOpsRecoveryRerun || governedCloseoutRecoveryRerun) && task.ClaimedByRunID != "" && strings.TrimSpace(input.RunID) != task.ClaimedByRunID {
		return WorkTask{}, fmt.Errorf("%w: recovery rerun requires current claimed run", ErrInvalidInput)
	}
	if next == WorkTaskStatusReady && releaseIsStale(task.Status) && !gitOpsRecoveryRerun && !governedCloseoutRecoveryRerun {
		return task, nil
	}
	if next == WorkTaskStatusReady && task.Status != WorkTaskStatusReady && task.Status != WorkTaskStatusPlanned && task.Status != WorkTaskStatusClaimed && task.Status != WorkTaskStatusInProgress && task.Status != WorkTaskStatusBlocked && task.Status != WorkTaskStatusNeedsReview && !gitOpsRecoveryRerun && !governedCloseoutRecoveryRerun {
		return WorkTask{}, fmt.Errorf("%w: release requires claimed status", ErrInvalidInput)
	}
	if next == WorkTaskStatusInProgress && task.Status != WorkTaskStatusClaimed {
		return WorkTask{}, fmt.Errorf("%w: start requires claimed status", ErrInvalidInput)
	}
	if next == WorkTaskStatusBlocked && strings.TrimSpace(input.ResumeInstructions) == "" {
		return WorkTask{}, fmt.Errorf("%w: resume_instructions is required", ErrInvalidInput)
	}
	if err := validateTaskTransition(task.Status, next); err != nil && !gitOpsRecoveryRerun && !governedCloseoutRecoveryRerun {
		return WorkTask{}, err
	}
	if next == WorkTaskStatusReady && task.DecompositionQuality != DecompositionReady {
		return WorkTask{}, fmt.Errorf("%w: task decomposition is not ready", ErrInvalidInput)
	}
	if next == WorkTaskStatusReady && len(task.DependencyTaskIDs) > 0 {
		ready, err := svc.dependenciesSatisfiedForReadyTask(ctx, task)
		if err != nil {
			return WorkTask{}, err
		}
		if !ready {
			return WorkTask{}, fmt.Errorf("%w: release requires completed dependencies", ErrInvalidInput)
		}
	}
	if input.OwnerAgent != "" {
		task.OwnerAgent, err = safeOptionalRef(input.OwnerAgent, "owner_agent")
		if err != nil {
			return WorkTask{}, err
		}
	}
	if input.RunID != "" {
		task.ClaimedByRunID, err = safeOptionalRef(input.RunID, "run_id")
		if err != nil {
			return WorkTask{}, err
		}
		task.AgentRunIDs = appendUnique(task.AgentRunIDs, task.ClaimedByRunID)
	}
	if input.TraceID != "" {
		task.TraceID, err = safeOptionalRef(input.TraceID, "trace_id")
		if err != nil {
			return WorkTask{}, err
		}
	}
	if input.Outcome != "" {
		task.Outcome, err = safeOptionalText(input.Outcome, "outcome", 500)
		if err != nil {
			return WorkTask{}, err
		}
	}
	if input.BlockedReason != "" {
		task.BlockedReason, err = safeOptionalText(input.BlockedReason, "blocked_reason", 500)
		if err != nil {
			return WorkTask{}, err
		}
	}
	if input.ResumeInstructions != "" {
		task.ResumeInstructions, err = safeResumeInstructions(input.ResumeInstructions, "resume_instructions")
		if err != nil {
			return WorkTask{}, err
		}
	}
	if len(input.BlockedByTaskIDs) > 0 {
		task.BlockedByTaskIDs, err = safeRefList(input.BlockedByTaskIDs, "blocked_by_task_ids")
		if err != nil {
			return WorkTask{}, err
		}
	}
	if len(input.ContextPackRefs) > 0 {
		contextPackRefs, err := safeRefList(input.ContextPackRefs, "context_pack_refs")
		if err != nil {
			return WorkTask{}, err
		}
		for _, ref := range contextPackRefs {
			task.ContextPackRefs = appendUnique(task.ContextPackRefs, ref)
		}
	}
	if len(input.EvidenceRefs) > 0 {
		evidenceRefs, err := safeRefList(input.EvidenceRefs, "evidence_refs")
		if err != nil {
			return WorkTask{}, err
		}
		for _, ref := range evidenceRefs {
			task.EvidenceRefs = appendUnique(task.EvidenceRefs, ref)
		}
	}
	if len(input.ClaimRefs) > 0 {
		claimRefs, err := safeRefList(input.ClaimRefs, "claim_refs")
		if err != nil {
			return WorkTask{}, err
		}
		for _, ref := range claimRefs {
			task.ClaimRefs = appendUnique(task.ClaimRefs, ref)
		}
	}
	if len(input.KnowledgeRefs) > 0 {
		knowledgeRefs, err := safeRefList(input.KnowledgeRefs, "knowledge_candidate_refs")
		if err != nil {
			return WorkTask{}, err
		}
		for _, ref := range knowledgeRefs {
			task.KnowledgeCandidateRefs = appendUnique(task.KnowledgeCandidateRefs, ref)
		}
	}
	if len(input.VerifierResultRefs) > 0 {
		task.VerifierResultRefs, err = safeRefList(input.VerifierResultRefs, "verifier_result_refs")
		if err != nil {
			return WorkTask{}, err
		}
	}
	if len(input.ReviewResultRefs) > 0 {
		task.ReviewResultRefs, err = safeRefList(input.ReviewResultRefs, "review_result_refs")
		if err != nil {
			return WorkTask{}, err
		}
	}
	if input.ReviewExemptReason != "" {
		task.ReviewExemptReason, err = safeOptionalText(input.ReviewExemptReason, "review_exempt_reason", 300)
		if err != nil {
			return WorkTask{}, err
		}
		task.ReviewResultRefs = nil
	}
	now := svc.now()
	task.Status = next
	task.UpdatedAt = now
	if next != WorkTaskStatusBlocked {
		task.BlockedReason = ""
		task.BlockedByTaskIDs = nil
	}
	switch next {
	case WorkTaskStatusClaimed:
		task.ClaimedAt = now
	case WorkTaskStatusReady:
		task.ClaimedByRunID = ""
		task.ClaimedAt = time.Time{}
	case WorkTaskStatusInProgress:
		task.StartedAt = now
	case WorkTaskStatusDone, WorkTaskStatusFailed:
		task.CompletedAt = now
	}
	if next == WorkTaskStatusBlocked && task.BlockedReason == "" {
		return WorkTask{}, fmt.Errorf("%w: blocked_reason is required", ErrInvalidInput)
	}
	if next == WorkTaskStatusBlocked && task.ResumeInstructions == "" {
		return WorkTask{}, fmt.Errorf("%w: resume_instructions is required", ErrInvalidInput)
	}
	if next == WorkTaskStatusDone && len(task.VerifierResultRefs) == 0 {
		return WorkTask{}, fmt.Errorf("%w: verifier_result_refs are required before done", ErrInvalidInput)
	}
	if next == WorkTaskStatusDone && len(task.ReviewResultRefs) == 0 && strings.TrimSpace(task.ReviewExemptReason) == "" {
		return WorkTask{}, fmt.Errorf("%w: review_result_refs or review_exempt_reason is required before done", ErrInvalidInput)
	}
	if next == WorkTaskStatusDone && len(task.ReviewResultRefs) > 0 {
		if err := svc.validateIndependentReviewRefs(ctx, task); err != nil {
			return WorkTask{}, err
		}
	}
	return svc.store.UpdateWorkTask(ctx, task)
}

func (svc *Service) validateIndependentReviewRefs(ctx context.Context, task WorkTask) error {
	attachments, err := svc.store.ListAttachments(ctx, task.ProjectID, task.ID)
	if err != nil {
		return err
	}
	attachedByRef := make(map[string][]Attachment)
	for _, attachment := range attachments {
		if attachment.Kind == "review_result_ref" {
			attachedByRef[attachment.Ref] = append(attachedByRef[attachment.Ref], attachment)
		}
	}
	for _, ref := range task.ReviewResultRefs {
		reviews := attachedByRef[ref]
		if len(reviews) == 0 {
			return fmt.Errorf("%w: review_result_ref must be attached before completion", ErrInvalidInput)
		}
		if task.ClaimedByRunID == "" {
			continue
		}
		independent := false
		for _, review := range reviews {
			if review.AttachedByRunID != "" && review.AttachedByRunID != task.ClaimedByRunID {
				independent = true
				break
			}
		}
		if !independent {
			return fmt.Errorf("%w: review_result_ref must be attached by an independent run", ErrInvalidInput)
		}
	}
	return nil
}

func (svc *Service) AttachEvidence(ctx context.Context, input AttachInput) (Attachment, error) {
	return svc.attach(ctx, "evidence_ref", input)
}

func (svc *Service) AttachContextPack(ctx context.Context, input AttachInput) (Attachment, error) {
	return svc.attach(ctx, "context_pack_ref", input)
}

func (svc *Service) AttachClaim(ctx context.Context, input AttachInput) (Attachment, error) {
	return svc.attach(ctx, "claim_ref", input)
}

func (svc *Service) AttachVerifierResult(ctx context.Context, input AttachInput) (Attachment, error) {
	return svc.attach(ctx, "verifier_result_ref", input)
}

func (svc *Service) AttachReviewResult(ctx context.Context, input AttachInput) (Attachment, error) {
	return svc.attach(ctx, "review_result_ref", input)
}

func (svc *Service) AttachKnowledgeCandidate(ctx context.Context, input AttachInput) (Attachment, error) {
	return svc.attach(ctx, "knowledge_candidate_ref", input)
}

func (svc *Service) attach(ctx context.Context, kind string, input AttachInput) (Attachment, error) {
	projectID, taskID, err := safeProjectObject(input.ProjectID, input.TaskID, "task_id")
	if err != nil {
		return Attachment{}, err
	}
	task, err := svc.store.GetWorkTask(ctx, projectID, taskID)
	if err != nil {
		return Attachment{}, err
	}
	ref, err := safeRef(input.Ref, kind)
	if err != nil {
		return Attachment{}, err
	}
	runID, err := safeOptionalRef(input.AttachedByRunID, "attached_by_run_id")
	if err != nil {
		return Attachment{}, err
	}
	if kind == "review_result_ref" && runID != "" && task.ClaimedByRunID != "" && runID == task.ClaimedByRunID {
		return Attachment{}, fmt.Errorf("%w: review_result_ref must be attached by an independent run", ErrInvalidInput)
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return Attachment{}, err
	}
	note, err := safeOptionalText(input.Note, "note", 300)
	if err != nil {
		return Attachment{}, err
	}
	attachment := Attachment{ID: svc.newID("work_attachment"), ProjectID: projectID, PlanID: task.PlanID, TaskID: taskID, Kind: kind, Ref: ref, AttachedByRunID: runID, TraceID: traceID, Note: note, CreatedAt: svc.now()}
	created, err := svc.store.CreateAttachment(ctx, attachment)
	if err != nil {
		return Attachment{}, err
	}
	return created, svc.addAttachmentRef(ctx, task, kind, ref)
}

func (svc *Service) addAttachmentRef(ctx context.Context, task WorkTask, kind string, ref string) error {
	switch kind {
	case "evidence_ref":
		task.EvidenceRefs = appendUnique(task.EvidenceRefs, ref)
	case "context_pack_ref":
		task.ContextPackRefs = appendUnique(task.ContextPackRefs, ref)
	case "claim_ref":
		task.ClaimRefs = appendUnique(task.ClaimRefs, ref)
	case "verifier_result_ref":
		task.VerifierResultRefs = appendUnique(task.VerifierResultRefs, ref)
		if task.Status == WorkTaskStatusInProgress {
			if err := validateTaskTransition(task.Status, WorkTaskStatusVerifying); err != nil {
				return err
			}
			task.Status = WorkTaskStatusVerifying
		}
	case "review_result_ref":
		task.ReviewResultRefs = appendUnique(task.ReviewResultRefs, ref)
		if task.Status == WorkTaskStatusInProgress {
			if err := validateTaskTransition(task.Status, WorkTaskStatusNeedsReview); err != nil {
				return err
			}
			task.Status = WorkTaskStatusNeedsReview
		}
	case "knowledge_candidate_ref":
		task.KnowledgeCandidateRefs = appendUnique(task.KnowledgeCandidateRefs, ref)
	}
	task.UpdatedAt = svc.now()
	_, err := svc.store.UpdateWorkTask(ctx, task)
	return err
}

func (svc *Service) listTasks(ctx context.Context, filter WorkTaskFilter) ([]WorkTask, error) {
	projectID, err := safeRef(filter.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter.ProjectID = projectID
	if filter.PlanID != "" {
		if filter.PlanID, err = safeOptionalRef(filter.PlanID, "plan_id"); err != nil {
			return nil, err
		}
	}
	if filter.Status != "" {
		if _, err := safeTaskStatus(filter.Status); err != nil {
			return nil, err
		}
	}
	if filter.OwnerAgent != "" {
		if filter.OwnerAgent, err = safeOptionalRef(filter.OwnerAgent, "owner_agent"); err != nil {
			return nil, err
		}
	}
	if filter.ClaimedByRunID != "" {
		if filter.ClaimedByRunID, err = safeOptionalRef(filter.ClaimedByRunID, "claimed_by_run_id"); err != nil {
			return nil, err
		}
	}
	return svc.store.ListWorkTasks(ctx, filter)
}

func validatePlanTransition(from, to string) error {
	if from == to {
		return nil
	}
	allowed := map[string][]string{
		WorkPlanStatusPlanned:     {WorkPlanStatusActive, WorkPlanStatusCancelled},
		WorkPlanStatusActive:      {WorkPlanStatusBlocked, WorkPlanStatusNeedsReview, WorkPlanStatusDone, WorkPlanStatusFailed, WorkPlanStatusCancelled, WorkPlanStatusSuperseded},
		WorkPlanStatusBlocked:     {WorkPlanStatusActive, WorkPlanStatusCancelled, WorkPlanStatusSuperseded},
		WorkPlanStatusNeedsReview: {WorkPlanStatusActive, WorkPlanStatusDone, WorkPlanStatusFailed, WorkPlanStatusCancelled},
		WorkPlanStatusDone:        {WorkPlanStatusSuperseded},
		WorkPlanStatusFailed:      {WorkPlanStatusSuperseded},
		WorkPlanStatusCancelled:   {WorkPlanStatusSuperseded},
	}
	return validateTransition(from, to, allowed, "work plan")
}

func allowsGitOpsRecoveryRerun(task WorkTask, input WorkTaskActionInput, next string) bool {
	return task.Status == WorkTaskStatusVerifying &&
		next == WorkTaskStatusReady &&
		strings.TrimSpace(input.SafeNextAction) == "gitops_recovery_failed_requeue_implementation" &&
		strings.TrimSpace(input.RunID) != ""
}

func allowsGovernedCloseoutPlanRecoveryRerun(plan WorkPlan, input UpdateWorkPlanStatusInput, next string) bool {
	return plan.Status == WorkPlanStatusFailed &&
		next == WorkPlanStatusActive &&
		strings.TrimSpace(input.SafeNextAction) == "governed_closeout_failed_requeue_implementation" &&
		strings.TrimSpace(input.RunID) != ""
}

func allowsPlanningReadinessStageCompletion(plan WorkPlan, input UpdateWorkPlanStatusInput, next string) bool {
	switch plan.Status {
	case WorkPlanStatusActive, WorkPlanStatusBlocked:
	default:
		return false
	}
	return next == WorkPlanStatusDone &&
		strings.TrimSpace(input.SafeNextAction) == "planning_readiness_review_completed_stage" &&
		strings.TrimSpace(input.RunID) != ""
}

func allowsGovernedCloseoutTaskRecoveryRerun(task WorkTask, input WorkTaskActionInput, next string) bool {
	return task.Status == WorkTaskStatusFailed &&
		next == WorkTaskStatusReady &&
		strings.TrimSpace(input.SafeNextAction) == "governed_closeout_failed_requeue_implementation" &&
		strings.TrimSpace(input.RunID) != ""
}

func validateTaskTransition(from, to string) error {
	if from == to {
		return nil
	}
	allowed := map[string][]string{
		WorkTaskStatusPlanned:     {WorkTaskStatusReady, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusReady:       {WorkTaskStatusClaimed, WorkTaskStatusBlocked, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusClaimed:     {WorkTaskStatusInProgress, WorkTaskStatusReady, WorkTaskStatusBlocked, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusInProgress:  {WorkTaskStatusReady, WorkTaskStatusBlocked, WorkTaskStatusNeedsReview, WorkTaskStatusVerifying, WorkTaskStatusFailed, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusBlocked:     {WorkTaskStatusReady, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusNeedsReview: {WorkTaskStatusReady, WorkTaskStatusVerifying, WorkTaskStatusDone, WorkTaskStatusFailed, WorkTaskStatusBlocked, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusVerifying:   {WorkTaskStatusDone, WorkTaskStatusFailed, WorkTaskStatusBlocked, WorkTaskStatusCancelled, WorkTaskStatusSuperseded},
		WorkTaskStatusDone:        {WorkTaskStatusSuperseded},
		WorkTaskStatusFailed:      {WorkTaskStatusSuperseded},
		WorkTaskStatusCancelled:   {WorkTaskStatusSuperseded},
	}
	return validateTransition(from, to, allowed, "work task")
}

func releaseIsStale(status string) bool {
	switch status {
	case WorkTaskStatusVerifying, WorkTaskStatusDone, WorkTaskStatusFailed, WorkTaskStatusCancelled, WorkTaskStatusSuperseded:
		return true
	default:
		return false
	}
}

func (svc *Service) dependenciesSatisfiedForReadyTask(ctx context.Context, task WorkTask) (bool, error) {
	if !workTaskIsReviewTask(task) {
		return svc.dependenciesDone(ctx, task.ProjectID, task.PlanID, task.DependencyTaskIDs)
	}
	tasks, err := svc.store.ListWorkTasks(ctx, WorkTaskFilter{ProjectID: task.ProjectID, PlanID: task.PlanID, PageSize: 1000})
	if err != nil {
		return false, err
	}
	byID := make(map[string]WorkTask, len(tasks))
	byRef := make(map[string]WorkTask, len(tasks))
	for _, candidate := range tasks {
		byID[candidate.ID] = candidate
		if ref := strings.TrimSpace(candidate.TaskRef); ref != "" {
			byRef[ref] = candidate
		}
	}
	for _, dep := range task.DependencyTaskIDs {
		dep = strings.TrimSpace(dep)
		depTask, ok := byID[dep]
		if !ok {
			depTask, ok = byRef[dep]
		}
		if !ok {
			return false, nil
		}
		switch depTask.Status {
		case WorkTaskStatusNeedsReview, WorkTaskStatusVerifying, WorkTaskStatusDone:
			continue
		default:
			return false, nil
		}
	}
	return true, nil
}

func workTaskIsReviewTask(task WorkTask) bool {
	return strings.HasPrefix(strings.TrimSpace(task.TaskRef), "review-")
}

func validateTransition(from, to string, allowed map[string][]string, label string) error {
	allowedNext := allowed[from]
	for _, candidate := range allowedNext {
		if candidate == to {
			return nil
		}
	}
	if len(allowedNext) == 0 {
		return fmt.Errorf("%w: invalid %s transition %s -> %s; no transitions are allowed from %s", ErrInvalidInput, label, from, to, from)
	}
	return fmt.Errorf("%w: invalid %s transition %s -> %s; allowed from %s: %s", ErrInvalidInput, label, from, to, from, strings.Join(allowedNext, ", "))
}

func assessDecomposition(evidence []string, contextRefs []string, files []string, deps []string, verify string, expected string, failure string, resume string) string {
	if len(evidence) == 0 {
		return DecompositionMissingEvidence
	}
	if len(contextRefs) == 0 && len(files) == 0 {
		return DecompositionMissingContext
	}
	if strings.TrimSpace(verify) == "" {
		return DecompositionMissingVerification
	}
	if strings.TrimSpace(resume) == "" {
		return DecompositionMissingResume
	}
	if strings.TrimSpace(expected) == "" || strings.TrimSpace(failure) == "" {
		return DecompositionDraft
	}
	if len(files) > 8 {
		return DecompositionTooBroad
	}
	_ = deps
	return DecompositionReady
}

func safePlanStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case WorkPlanStatusPlanned, WorkPlanStatusActive, WorkPlanStatusBlocked, WorkPlanStatusNeedsReview, WorkPlanStatusDone, WorkPlanStatusFailed, WorkPlanStatusCancelled, WorkPlanStatusSuperseded:
		return strings.TrimSpace(status), nil
	default:
		return "", fmt.Errorf("%w: invalid work plan status", ErrInvalidInput)
	}
}

func safeWorkPlanIsolationMode(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", WorkPlanIsolationShared, WorkPlanIsolationDedicatedWorktree, WorkPlanIsolationUnavailable:
		return strings.TrimSpace(mode), nil
	default:
		return "", fmt.Errorf("%w: invalid isolation_mode", ErrInvalidInput)
	}
}

func safeTaskStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case WorkTaskStatusPlanned, WorkTaskStatusReady, WorkTaskStatusClaimed, WorkTaskStatusInProgress, WorkTaskStatusBlocked, WorkTaskStatusNeedsReview, WorkTaskStatusVerifying, WorkTaskStatusDone, WorkTaskStatusFailed, WorkTaskStatusCancelled, WorkTaskStatusSuperseded:
		return strings.TrimSpace(status), nil
	default:
		return "", fmt.Errorf("%w: invalid work task status", ErrInvalidInput)
	}
}

func safeDecompositionQuality(quality string) (string, error) {
	switch strings.TrimSpace(quality) {
	case DecompositionDraft, DecompositionReady, DecompositionTooBroad, DecompositionMissingEvidence, DecompositionMissingContext, DecompositionMissingVerification, DecompositionMissingResume:
		return strings.TrimSpace(quality), nil
	default:
		return "", fmt.Errorf("%w: invalid decomposition_quality", ErrInvalidInput)
	}
}

func safeProjectObject(projectID, objectID, name string) (string, string, error) {
	projectID, err := safeRef(projectID, "project_id")
	if err != nil {
		return "", "", err
	}
	objectID, err = safeRef(objectID, name)
	if err != nil {
		return "", "", err
	}
	return projectID, objectID, nil
}

func safeRef(value string, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, name)
	}
	if strings.Contains(value, "\\") || strings.Contains(value, "..") || strings.HasPrefix(value, "/") || filepath.IsAbs(value) || containsUnsafeMarker(value) || containsRootMarker(value) || emailPattern.MatchString(value) || !refPattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, name)
	}
	return value, nil
}

func safeOptionalRef(value string, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeRef(value, name)
}

func safeRequiredText(value string, name string, max int) (string, error) {
	value, err := safeOptionalText(value, name, max)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, name)
	}
	return value, nil
}

func safeOptionalText(value string, name string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidInput, name)
	}
	return safeTextContent(value, name)
}

func safeResumeInstructions(value string, name string) (string, error) {
	value = strings.TrimSpace(value)
	return safeTextContent(value, name)
}

func safeTextContent(value string, name string) (string, error) {
	piiCheckValue := redactSafePathTokens(value)
	if containsUnsafeMarker(value) || emailPattern.MatchString(piiCheckValue) || phonePattern.MatchString(piiCheckValue) {
		return "", fmt.Errorf("%w: %s contains unsafe content", ErrInvalidInput, name)
	}
	return value, nil
}

func redactSafePathTokens(value string) string {
	fields := strings.Fields(value)
	for index, field := range fields {
		trimmed := strings.Trim(field, ".,;:()[]{}<>\"'")
		normalized := filepath.ToSlash(trimmed)
		if strings.Contains(normalized, "/") && safePathToken(normalized) {
			fields[index] = strings.Replace(field, trimmed, "path-ref", 1)
		}
	}
	return strings.Join(fields, " ")
}

func safePathToken(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || strings.HasPrefix(value, "~") || strings.Contains(value, "..") || strings.Contains(value, ":") || filepath.IsAbs(value) || containsRootMarker(value) {
		return false
	}
	if strings.ContainsAny(value, "\x00\r\n\t") {
		return false
	}
	return true
}

func safeTextList(values []string, name string, max int) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		safe, err := safeRequiredText(value, name, max)
		if err != nil {
			return nil, err
		}
		out = append(out, safe)
	}
	return unique(out), nil
}

func safeRefList(values []string, name string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		safe, err := safeRefListValue(value, name)
		if err != nil {
			return nil, err
		}
		out = append(out, safe)
	}
	return unique(out), nil
}

func safeRefListValue(value string, name string) (string, error) {
	if name == "evidence_refs" {
		return safeEvidenceRef(value, name)
	}
	return safeRef(value, name)
}

func safeEvidenceRef(value string, name string) (string, error) {
	if safe, err := safeRef(value, name); err == nil {
		return safe, nil
	}
	value = strings.TrimSpace(value)
	const prefix = "gitops-dirty-path:"
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, name)
	}
	path := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(value, prefix)))
	if len(value) > 2048 || !safePathToken(path) || emailPattern.MatchString(path) || phonePattern.MatchString(path) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, name)
	}
	return prefix + path, nil
}

func safePathList(values []string, name string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.ToSlash(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || strings.Contains(value, "..") || strings.Contains(value, ":") || strings.HasPrefix(value, "~") || filepath.IsAbs(value) {
			return nil, fmt.Errorf("%w: %s contains unsafe path", ErrInvalidInput, name)
		}
		out = append(out, value)
	}
	return unique(out), nil
}

func containsUnsafeMarker(value string) bool {
	lower := redactSafeProhibitionPhrases(strings.ToLower(value))
	markers := []string{
		"begin private key",
		"openai_api_key",
		"anthropic_api_key",
		"ghp_",
		"api_key",
		"password=",
		"secret=",
		"token=",
		"credential=",
		"raw_prompt",
		"raw prompt",
		"raw_completion",
		"raw completion",
		"raw_stderr",
		"raw stderr",
		"provider_payload",
		"provider payload",
		"raw source",
		"source dump",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.HasPrefix(lower, "sk-") || strings.Contains(lower, " sk-") || strings.Contains(lower, "=sk-")
}

func redactSafeProhibitionPhrases(value string) string {
	if strings.Contains(value, "no raw prompt") || strings.Contains(value, "never store") || strings.Contains(value, "must not store") || strings.Contains(value, "do not store") || strings.Contains(value, "must not expose") || strings.Contains(value, "do not expose") || strings.Contains(value, "would expose") || strings.Contains(value, "must not include") || strings.Contains(value, "do not include") || strings.Contains(value, "would include") || strings.Contains(value, "would store") || strings.Contains(value, "prohibited") {
		for _, marker := range []string{
			"raw prompts",
			"raw prompt",
			"raw completions",
			"raw completion",
			"raw_prompt",
			"raw_completion",
			"raw source",
			"source dumps",
			"source dump",
			"raw stderr",
			"raw_stderr",
			"provider payloads",
			"provider payload",
			"provider_payload",
			"credentials",
			"credential",
			"secrets",
			"secret",
			"roots",
			"root",
			"paths",
			"path",
		} {
			value = strings.ReplaceAll(value, marker, "")
		}
	}
	for _, prefix := range []string{"no", "never store", "must not store", "do not store", "would store", "must not expose", "do not expose", "would expose", "must not include", "do not include", "would include"} {
		for _, marker := range []string{"raw prompts", "raw prompt", "raw completions", "raw completion", "raw source", "source dumps", "source dump", "raw stderr", "provider payloads", "provider payload", "credentials", "credential", "secrets", "secret", "roots", "root", "paths", "path"} {
			value = strings.ReplaceAll(value, prefix+" "+marker, "")
		}
	}
	return value
}

func containsRootMarker(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	return strings.Contains(normalized, "/home/") ||
		strings.Contains(normalized, "/users/") ||
		strings.Contains(normalized, "wsl.localhost/") ||
		strings.Contains(normalized, "c:/") ||
		regexp.MustCompile(`^[a-z]:`).MatchString(normalized)
}

func filterTasks(tasks []WorkTask, keep func(WorkTask) bool) []WorkTask {
	out := make([]WorkTask, 0, len(tasks))
	for _, task := range tasks {
		if keep(task) {
			out = append(out, task)
		}
	}
	return out
}

func appendUnique(values []string, next string) []string {
	return unique(append(values, next))
}

func optionalRefSlice(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func newID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}
