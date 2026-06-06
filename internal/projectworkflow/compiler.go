package projectworkflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

type plannedTask struct {
	step WorkflowStep
	id   string
}

// CompileWorkflow compiles a stored, enabled workflow into governed execution metadata.
func (svc *Service) CompileWorkflow(ctx context.Context, input WorkflowCompileInput) (WorkflowCompileResult, error) {
	if svc.store == nil {
		return WorkflowCompileResult{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRequiredWorkflowRef(input.ProjectID, "project_id")
	if err != nil {
		return WorkflowCompileResult{}, err
	}
	workflowID, err := safeRequiredWorkflowRef(input.WorkflowID, "workflow_id")
	if err != nil {
		return WorkflowCompileResult{}, err
	}
	runID, err := safeOptionalWorkflowRef(input.CreatedByRunID, "created_by_run_id")
	if err != nil {
		return WorkflowCompileResult{}, err
	}
	traceID, err := safeOptionalWorkflowRef(input.TraceID, "trace_id")
	if err != nil {
		return WorkflowCompileResult{}, err
	}
	userRequestRef, err := safeOptionalWorkflowRef(input.UserRequestRef, "user_request_ref")
	if err != nil {
		return WorkflowCompileResult{}, err
	}
	titleOverride, err := safeOptionalCompileText(input.TitleOverride, "title_override", 200)
	if err != nil {
		return WorkflowCompileResult{}, err
	}

	workflow, err := svc.store.GetWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return WorkflowCompileResult{}, err
	}
	issues := ValidateWorkflow(workflow)
	if workflow.Status != WorkflowStatusEnabled {
		issues = append(issues, WorkflowValidationIssue{Code: "workflow_not_enabled", Severity: workflowIssueError, FieldPath: "status", Message: "workflow must be enabled before compilation"})
	}
	if hasErrorIssues(issues) {
		return WorkflowCompileResult{WorkflowID: workflow.ID, ValidationIssues: issues, DryRun: input.DryRun, CompiledAt: svc.now()}, fmt.Errorf("%w: workflow validation failed", ErrInvalidInput)
	}
	if !input.DryRun && (svc.workPlans == nil || svc.automations == nil) {
		return WorkflowCompileResult{}, fmt.Errorf("%w: compiler dependencies are required", ErrInvalidInput)
	}

	graph, err := svc.planCompileGraph(workflow)
	if err != nil {
		return WorkflowCompileResult{WorkflowID: workflow.ID, DryRun: input.DryRun, CompiledAt: svc.now()}, err
	}

	if input.DryRun {
		return svc.dryRunCompileResult(workflow, graph), nil
	}

	title := firstNonEmpty(titleOverride, workflow.Title)
	planRef := compilePlanRef(workflow.WorkflowRef, runID, svc.newID)
	isolation := compileIsolationRefs(workflow, planRef, userRequestRef, runID)
	plan, err := svc.workPlans.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:        workflow.ProjectID,
		PlanRef:          planRef,
		UserRequestRef:   userRequestRef,
		Title:            title,
		GoalSummary:      workflow.Purpose,
		OwnerAgent:       "workflow-compiler",
		CreatedByRunID:   firstNonEmpty(runID, workflow.CreatedByRunID),
		TraceID:          firstNonEmpty(traceID, workflow.TraceID),
		ResumeSummary:    "Compiled from workflow " + workflow.WorkflowRef + "; execute ready Work Tasks through governed automation.",
		IsolationMode:    projectworkplan.WorkPlanIsolationDedicatedWorktree,
		ParallelGroupRef: isolation.parallelGroupRef,
		WorkspaceRef:     isolation.workspaceRef,
		GitBaseRef:       isolation.gitBaseRef,
		GitBranchRef:     isolation.gitBranchRef,
		GitWorktreeRef:   isolation.gitWorktreeRef,
	})
	if err != nil {
		return WorkflowCompileResult{}, err
	}

	result := WorkflowCompileResult{WorkflowID: workflow.ID, WorkPlanID: plan.ID, DryRun: false, CompiledAt: svc.now()}
	snapshots, err := svc.ensureCompileSnapshots(ctx, workflow)
	if err != nil {
		return result, err
	}
	snapshotByAgent := map[string]WorkflowPermissionSnapshot{}
	for _, snapshot := range snapshots {
		snapshotByAgent[snapshot.AgentID] = snapshot
		result.PermissionSnapshotIDs = append(result.PermissionSnapshotIDs, snapshot.ID)
	}

	taskByStep := map[string]projectworkplan.WorkTask{}
	for _, item := range graph.tasks {
		created, err := svc.workPlans.CreateWorkTask(ctx, svc.compileTaskInput(workflow, plan.ID, item.step, graph.gatesByStep[item.step.ID], taskByStep, runID, traceID))
		if err != nil {
			return result, fmt.Errorf("create compiled work task %s: %w", item.step.ID, err)
		}
		taskByStep[item.step.ID] = created
		result.WorkTaskIDs = append(result.WorkTaskIDs, created.ID)
	}
	for _, task := range taskByStep {
		for _, gate := range graph.gatesByStep[task.TaskRef] {
			reviewer, err := svc.workPlans.CreateWorkTask(ctx, svc.reviewTaskInput(workflow, plan.ID, task, gate, runID, traceID))
			if err != nil {
				return result, fmt.Errorf("create compiled review task %s/%s: %w", task.TaskRef, gate.ID, err)
			}
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, reviewer.ID)
		}
	}
	reviewTaskIDsByAutomationStep := map[string][]string{}
	for _, step := range graph.automationSteps {
		for _, gate := range graph.gatesByStep[step.ID] {
			reviewer, err := svc.workPlans.CreateWorkTask(ctx, svc.reviewAutomationTaskInput(workflow, plan.ID, step, gate, taskByStep, runID, traceID))
			if err != nil {
				return result, fmt.Errorf("create compiled automation review task %s/%s: %w", step.ID, gate.ID, err)
			}
			reviewTaskIDsByAutomationStep[step.ID] = append(reviewTaskIDsByAutomationStep[step.ID], reviewer.ID)
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, reviewer.ID)
		}
	}
	for _, step := range graph.automationSteps {
		snapshot := snapshotByAgent[step.Agent]
		automation, err := svc.automations.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
			ProjectID:             workflow.ProjectID,
			AutomationRef:         compileAutomationRef(plan.PlanRef, step.ID),
			Title:                 step.Title,
			Purpose:               firstNonEmpty(step.Description, "Run workflow automation step "+step.ID),
			Status:                firstNonEmpty(step.AutomationStatus, projectautomation.AutomationStatusDraft),
			AgentID:               step.Agent,
			PlanID:                plan.ID,
			AllowedTaskRefs:       allowedTaskRefs(step, taskByStep),
			RequiredReviewTaskIDs: reviewTaskIDsByAutomationStep[step.ID],
			TriggerKind:           firstNonEmpty(step.TriggerKind, projectautomation.TriggerKindManual),
			SchedulePolicy:        step.SchedulePolicy,
			PermissionRef:         "permission_snapshot:" + snapshot.ID,
			SourceKind:            projectautomation.AutomationSourceWorkflow,
			CreatedByRunID:        firstNonEmpty(runID, workflow.CreatedByRunID),
			TraceID:               firstNonEmpty(traceID, workflow.TraceID),
		})
		if err != nil {
			return result, fmt.Errorf("%w: create compiled automation %s: %v", ErrInvalidInput, step.ID, err)
		}
		result.AutomationIDs = append(result.AutomationIDs, automation.ID)
	}
	return result, nil
}

type compileGraph struct {
	tasks           []plannedTask
	automationSteps []WorkflowStep
	gatesByStep     map[string][]WorkflowReviewGate
}

func (svc *Service) planCompileGraph(workflow WorkflowDefinition) (compileGraph, error) {
	graph := compileGraph{gatesByStep: requiredGatesByStep(workflow.ReviewGates)}
	stepsByID := map[string]WorkflowStep{}
	for _, step := range workflow.Steps {
		stepsByID[step.ID] = step
	}
	for _, step := range workflow.Steps {
		if step.Kind == WorkflowStepKindAutomation || step.Kind == WorkflowStepKindAutomationBatch {
			if len(graph.gatesByStep[step.ID]) == 0 {
				return graph, fmt.Errorf("%w: automation step %s is missing a required review gate", ErrInvalidInput, step.ID)
			}
			for _, gate := range graph.gatesByStep[step.ID] {
				if gate.IndependentFromOwner && gate.ReviewerAgent == step.Agent {
					return graph, fmt.Errorf("%w: review gate %s requires independent reviewer", ErrInvalidInput, gate.ID)
				}
			}
			if !hasTaskProducingDependency(step, stepsByID) {
				return graph, fmt.Errorf("%w: automation step %s must depend on at least one work task step", ErrInvalidInput, step.ID)
			}
			graph.automationSteps = append(graph.automationSteps, step)
		}
	}
	visited := map[string]bool{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(stepID string) error {
		if visited[stepID] {
			return nil
		}
		if visiting[stepID] {
			return fmt.Errorf("%w: workflow task dependency cycle", ErrInvalidInput)
		}
		step, ok := stepsByID[stepID]
		if !ok || step.Kind != WorkflowStepKindWorkTask {
			return nil
		}
		visiting[stepID] = true
		for _, dep := range step.DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[stepID] = false
		visited[stepID] = true
		for _, gate := range graph.gatesByStep[step.ID] {
			if gate.IndependentFromOwner && gate.ReviewerAgent == step.Agent {
				return fmt.Errorf("%w: review gate %s requires independent reviewer", ErrInvalidInput, gate.ID)
			}
		}
		graph.tasks = append(graph.tasks, plannedTask{step: step, id: svc.newID("work_task")})
		return nil
	}
	for _, step := range workflow.Steps {
		if err := visit(step.ID); err != nil {
			return graph, err
		}
	}
	return graph, nil
}

func (svc *Service) dryRunCompileResult(workflow WorkflowDefinition, graph compileGraph) WorkflowCompileResult {
	result := WorkflowCompileResult{WorkflowID: workflow.ID, WorkPlanID: svc.newID("work_plan"), DryRun: true, CompiledAt: svc.now()}
	for _, task := range graph.tasks {
		result.WorkTaskIDs = append(result.WorkTaskIDs, task.id)
		for range graph.gatesByStep[task.step.ID] {
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, svc.newID("work_task"))
		}
	}
	for _, step := range graph.automationSteps {
		for range graph.gatesByStep[step.ID] {
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, svc.newID("work_task"))
		}
	}
	for range graph.automationSteps {
		result.AutomationIDs = append(result.AutomationIDs, svc.newID("automation"))
	}
	for _, agent := range workflow.Agents {
		result.PermissionSnapshotIDs = append(result.PermissionSnapshotIDs, "permission_snapshot:"+agent.ID)
	}
	return result
}

func (svc *Service) ensureCompileSnapshots(ctx context.Context, workflow WorkflowDefinition) ([]WorkflowPermissionSnapshot, error) {
	existing, err := svc.store.ListPermissionSnapshots(ctx, PermissionSnapshotFilter{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID})
	if err != nil {
		return nil, err
	}
	byAgent := map[string]WorkflowPermissionSnapshot{}
	for _, snapshot := range existing {
		byAgent[snapshot.AgentID] = snapshot
	}
	for _, agent := range workflow.Agents {
		if byAgent[agent.ID].ID != "" {
			continue
		}
		snapshot, err := svc.permissionSnapshotForAgent(workflow, agent)
		if err != nil {
			return nil, err
		}
		created, err := svc.store.CreatePermissionSnapshot(ctx, snapshot)
		if err != nil {
			return nil, err
		}
		byAgent[agent.ID] = created
	}
	out := make([]WorkflowPermissionSnapshot, 0, len(workflow.Agents))
	for _, agent := range workflow.Agents {
		out = append(out, byAgent[agent.ID])
	}
	return out, nil
}

func (svc *Service) compileTaskInput(workflow WorkflowDefinition, planID string, step WorkflowStep, gates []WorkflowReviewGate, taskByStep map[string]projectworkplan.WorkTask, runID string, traceID string) projectworkplan.CreateWorkTaskInput {
	evidence := append([]string(nil), step.EvidenceNeeded...)
	for _, gate := range gates {
		evidence = append(evidence, "review gate "+gate.ID)
	}
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               workflow.ProjectID,
		PlanID:                  planID,
		TaskRef:                 step.ID,
		Title:                   step.Title,
		Description:             step.Description,
		OwnerAgent:              step.Agent,
		RunID:                   runID,
		TraceID:                 firstNonEmpty(traceID, workflow.TraceID),
		EvidenceNeeded:          fallbackList(evidence, "implementation-evidence-required"),
		ContextPackRefs:         step.ContextPackRefs,
		LikelyFilesAffected:     step.LikelyFilesAffected,
		DependencyTaskIDs:       dependencyTaskIDs(step, taskByStep),
		VerificationRequirement: firstNonEmpty(step.VerificationRequirement, "orchestrator runs focused verifier"),
		ExpectedOutput:          firstNonEmpty(step.ExpectedOutput, "bounded implementation artifact"),
		FailureCriteria:         firstNonEmpty(step.FailureCriteria, "block if evidence or verifier scope is missing"),
		ResumeInstructions:      firstNonEmpty(step.ResumeInstructions, "resume from task metadata and attached refs only"),
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
}

func (svc *Service) reviewTaskInput(workflow WorkflowDefinition, planID string, reviewed projectworkplan.WorkTask, gate WorkflowReviewGate, runID string, traceID string) projectworkplan.CreateWorkTaskInput {
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               workflow.ProjectID,
		PlanID:                  planID,
		TaskRef:                 reviewed.TaskRef + "-review-" + gate.ID,
		Title:                   "Review " + reviewed.Title,
		Description:             "TOML instructions: " + truncateForCompile(gate.Instructions, 980),
		OwnerAgent:              gate.ReviewerAgent,
		RunID:                   runID,
		TraceID:                 firstNonEmpty(traceID, workflow.TraceID),
		EvidenceNeeded:          reviewerEvidence(gate, reviewed.ID),
		ContextPackRefs:         reviewed.ContextPackRefs,
		LikelyFilesAffected:     reviewed.LikelyFilesAffected,
		DependencyTaskIDs:       []string{reviewed.ID},
		VerificationRequirement: "attach review_result_ref",
		ExpectedOutput:          "decision and rationale",
		FailureCriteria:         "block on self-review, missing evidence, missing verifier, or unclear decision",
		ResumeInstructions:      "review only the referenced task, changed files, evidence refs, verifier refs, and gate instructions",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
}

func (svc *Service) reviewAutomationTaskInput(workflow WorkflowDefinition, planID string, step WorkflowStep, gate WorkflowReviewGate, taskByStep map[string]projectworkplan.WorkTask, runID string, traceID string) projectworkplan.CreateWorkTaskInput {
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               workflow.ProjectID,
		PlanID:                  planID,
		TaskRef:                 step.ID + "-review-" + gate.ID,
		Title:                   "Review " + step.Title,
		Description:             "TOML instructions: " + truncateForCompile(gate.Instructions, 980),
		OwnerAgent:              gate.ReviewerAgent,
		RunID:                   runID,
		TraceID:                 firstNonEmpty(traceID, workflow.TraceID),
		EvidenceNeeded:          reviewerAutomationEvidence(gate, step.ID),
		ContextPackRefs:         step.ContextPackRefs,
		LikelyFilesAffected:     step.LikelyFilesAffected,
		DependencyTaskIDs:       dependencyTaskIDs(step, taskByStep),
		VerificationRequirement: "attach review_result_ref before automation execution",
		ExpectedOutput:          "automation review decision placeholder",
		FailureCriteria:         "block on self-review, missing automation ref, missing verifier, or unclear decision",
		ResumeInstructions:      "review only the automation step metadata, allowed task refs, permission snapshot ref, and gate instructions",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
}

func requiredGatesByStep(gates []WorkflowReviewGate) map[string][]WorkflowReviewGate {
	out := map[string][]WorkflowReviewGate{}
	for _, gate := range gates {
		if !gate.Required {
			continue
		}
		for _, stepID := range gate.AppliesTo {
			out[stepID] = append(out[stepID], gate)
		}
	}
	return out
}

func dependencyTaskIDs(step WorkflowStep, taskByStep map[string]projectworkplan.WorkTask) []string {
	out := make([]string, 0, len(step.DependsOn))
	for _, dep := range step.DependsOn {
		if task := taskByStep[dep]; task.ID != "" {
			out = append(out, task.ID)
		}
	}
	return out
}

func allowedTaskRefs(step WorkflowStep, taskByStep map[string]projectworkplan.WorkTask) []string {
	out := make([]string, 0, len(step.DependsOn))
	for _, dep := range step.DependsOn {
		if task := taskByStep[dep]; task.TaskRef != "" {
			out = append(out, task.TaskRef)
		}
	}
	return out
}

func compilePlanRef(workflowRef string, runID string, newID func(string) string) string {
	suffix := newID("compile")
	base := strings.TrimSpace(workflowRef)
	if strings.TrimSpace(runID) != "" {
		base = base + ":" + strings.TrimSpace(runID)
	}
	return refWithSuffix(base, suffix)
}

func compileAutomationRef(planRef string, stepID string) string {
	return refWithSuffix(planRef, stepID)
}

type compileIsolation struct {
	parallelGroupRef string
	workspaceRef     string
	gitBaseRef       string
	gitBranchRef     string
	gitWorktreeRef   string
}

func compileIsolationRefs(workflow WorkflowDefinition, planRef string, userRequestRef string, runID string) compileIsolation {
	token := safeCompileGitToken(firstNonEmpty(planRef, userRequestRef, runID, workflow.WorkflowRef, workflow.ID))
	if token == "" {
		token = safeCompileGitToken(workflow.ProjectID)
	}
	if token == "" {
		token = "workflow"
	}
	return compileIsolation{
		parallelGroupRef: "workflow/" + token,
		workspaceRef:     "workflow/" + token,
		gitBaseRef:       "main",
		gitBranchRef:     "mivia/" + token,
		gitWorktreeRef:   "workflow/" + token,
	}
}

func safeCompileGitToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(builder.String(), "-")
	if len(out) > 96 {
		out = strings.Trim(out[:96], "-")
	}
	return out
}

func refWithSuffix(base string, suffix string) string {
	base = strings.TrimSpace(base)
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return truncateRef(base)
	}
	maxBase := 200 - len(suffix) - 1
	if maxBase < 1 {
		return truncateRef(suffix)
	}
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], ".:/@+-")
	}
	if base == "" {
		return truncateRef(suffix)
	}
	return base + ":" + suffix
}

func truncateRef(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 200 {
		return value
	}
	return strings.TrimRight(value[:200], ".:/@+-")
}

func hasTaskProducingDependency(step WorkflowStep, stepsByID map[string]WorkflowStep) bool {
	for _, dep := range step.DependsOn {
		if stepsByID[dep].Kind == WorkflowStepKindWorkTask {
			return true
		}
	}
	return false
}

func reviewerEvidence(gate WorkflowReviewGate, reviewedTaskID string) []string {
	evidence := []string{"changed files", "evidence refs", "verifier refs", "reviewed task id"}
	evidence = append(evidence, gate.RequiredArtifacts...)
	return evidence
}

func reviewerAutomationEvidence(gate WorkflowReviewGate, stepID string) []string {
	evidence := []string{"automation ref", "permission snapshot ref", "allowed task refs", "automation step " + stepID}
	evidence = append(evidence, gate.RequiredArtifacts...)
	return evidence
}

func fallbackList(values []string, fallback string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return []string{fallback}
	}
	return out
}

func safeOptionalCompileText(value string, field string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > max || containsUnsafeWorkflowText(value) || containsRootMarker(value) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return value, nil
}

func truncateForCompile(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max])
}
