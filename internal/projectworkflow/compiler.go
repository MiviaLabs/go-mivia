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

type plannedAutomationReview struct {
	step WorkflowStep
	gate WorkflowReviewGate
	task projectworkplan.WorkTask
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
	isolation := compileIsolationRefs(workflow, planRef, userRequestRef, runID, svc.compileOptions[workflow.ProjectID])
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
	reviewTaskIDsByReviewedStep := map[string][]string{}
	reviewTasks := []plannedAutomationReview{}
	for _, task := range taskByStep {
		for _, gate := range graph.gatesByStep[task.TaskRef] {
			reviewer, err := svc.workPlans.CreateWorkTask(ctx, svc.reviewTaskInput(workflow, plan.ID, task, gate, runID, traceID))
			if err != nil {
				return result, fmt.Errorf("create compiled review task %s/%s: %w", task.TaskRef, gate.ID, err)
			}
			reviewTaskIDsByReviewedStep[task.TaskRef] = append(reviewTaskIDsByReviewedStep[task.TaskRef], reviewer.ID)
			reviewTasks = append(reviewTasks, plannedAutomationReview{step: graph.stepsByID[task.TaskRef], gate: gate, task: reviewer})
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, reviewer.ID)
		}
	}
	reviewTaskIDsByAutomationStep := map[string][]string{}
	automationReviews := []plannedAutomationReview{}
	for _, step := range graph.automationSteps {
		if isTaskProducingWorkflowStep(step) {
			continue
		}
		for _, gate := range graph.gatesByStep[step.ID] {
			reviewer, err := svc.workPlans.CreateWorkTask(ctx, svc.reviewAutomationTaskInput(workflow, plan.ID, step, gate, runID, traceID))
			if err != nil {
				return result, fmt.Errorf("create compiled automation review task %s/%s: %w", step.ID, gate.ID, err)
			}
			reviewTaskIDsByAutomationStep[step.ID] = append(reviewTaskIDsByAutomationStep[step.ID], reviewer.ID)
			automationReviews = append(automationReviews, plannedAutomationReview{step: step, gate: gate, task: reviewer})
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, reviewer.ID)
		}
	}
	for _, item := range graph.tasks {
		task := taskByStep[item.step.ID]
		task.DependencyTaskIDs = compiledDependencyTaskIDs(item.step, graph.stepsByID, taskByStep, reviewTaskIDsByReviewedStep, reviewTaskIDsByAutomationStep)
		updated, err := svc.workPlans.UpdateWorkTask(ctx, task)
		if err != nil {
			return result, fmt.Errorf("update compiled work task dependencies %s: %w", item.step.ID, err)
		}
		taskByStep[item.step.ID] = updated
	}
	for _, review := range reviewTasks {
		snapshot := snapshotByAgent[review.gate.ReviewerAgent]
		automation, err := svc.automations.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
			ProjectID:       workflow.ProjectID,
			AutomationRef:   compileAutomationRef(plan.PlanRef, "review-"+review.step.ID+"-"+review.gate.ID),
			Title:           "Review " + review.step.Title,
			Purpose:         "Automatically run independent workflow task review.",
			Status:          projectautomation.AutomationStatusEnabled,
			AgentID:         review.gate.ReviewerAgent,
			PlanID:          plan.ID,
			AllowedTaskRefs: []string{review.task.TaskRef},
			TriggerKind:     projectautomation.TriggerKindAutomatic,
			SchedulePolicy:  "on-ready-task",
			PermissionRef:   "permission_snapshot:" + snapshot.ID,
			SourceKind:      projectautomation.AutomationSourceWorkflow,
			CreatedByRunID:  firstNonEmpty(runID, workflow.CreatedByRunID),
			TraceID:         firstNonEmpty(traceID, workflow.TraceID),
		})
		if err != nil {
			return result, fmt.Errorf("%w: create compiled task review automation %s/%s: %v", ErrInvalidInput, review.step.ID, review.gate.ID, err)
		}
		result.AutomationIDs = append(result.AutomationIDs, automation.ID)
	}
	for _, review := range automationReviews {
		snapshot := snapshotByAgent[review.gate.ReviewerAgent]
		automation, err := svc.automations.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
			ProjectID:       workflow.ProjectID,
			AutomationRef:   compileAutomationRef(plan.PlanRef, "review-"+review.step.ID+"-"+review.gate.ID),
			Title:           "Review " + review.step.Title,
			Purpose:         "Automatically run independent workflow automation review.",
			Status:          projectautomation.AutomationStatusEnabled,
			AgentID:         review.gate.ReviewerAgent,
			PlanID:          plan.ID,
			AllowedTaskRefs: []string{review.task.TaskRef},
			TriggerKind:     projectautomation.TriggerKindAutomatic,
			SchedulePolicy:  "on-ready-task",
			PermissionRef:   "permission_snapshot:" + snapshot.ID,
			SourceKind:      projectautomation.AutomationSourceWorkflow,
			CreatedByRunID:  firstNonEmpty(runID, workflow.CreatedByRunID),
			TraceID:         firstNonEmpty(traceID, workflow.TraceID),
		})
		if err != nil {
			return result, fmt.Errorf("%w: create compiled automation review %s/%s: %v", ErrInvalidInput, review.step.ID, review.gate.ID, err)
		}
		result.AutomationIDs = append(result.AutomationIDs, automation.ID)
	}
	coveredTaskRefs := map[string]bool{}
	for _, step := range graph.automationSteps {
		refs := automationStepAllowedTaskRefs(step, graph.stepsByID, taskByStep)
		stepStatus := workflowAutomationStatus(step)
		stepTrigger := workflowAutomationTrigger(step)
		if stepStatus == projectautomation.AutomationStatusEnabled && stepTrigger == projectautomation.TriggerKindAutomatic {
			for _, ref := range refs {
				coveredTaskRefs[ref] = true
			}
		}
		snapshot := snapshotByAgent[step.Agent]
		automation, err := svc.automations.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
			ProjectID:             workflow.ProjectID,
			AutomationRef:         compileAutomationRef(plan.PlanRef, step.ID),
			Title:                 step.Title,
			Purpose:               firstNonEmpty(step.Description, "Run workflow automation step "+step.ID),
			Status:                stepStatus,
			AgentID:               step.Agent,
			PlanID:                plan.ID,
			AllowedTaskRefs:       refs,
			RequiredReviewTaskIDs: automationRequiredReviewTaskIDs(step.ID, reviewTaskIDsByReviewedStep, reviewTaskIDsByAutomationStep),
			TriggerKind:           stepTrigger,
			SchedulePolicy:        workflowAutomationSchedulePolicy(step),
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
	for _, item := range graph.tasks {
		task := taskByStep[item.step.ID]
		if coveredTaskRefs[task.TaskRef] {
			continue
		}
		snapshot := snapshotByAgent[item.step.Agent]
		automation, err := svc.automations.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
			ProjectID:       workflow.ProjectID,
			AutomationRef:   compileAutomationRef(plan.PlanRef, item.step.ID),
			Title:           item.step.Title,
			Purpose:         firstNonEmpty(item.step.Description, "Run workflow task "+item.step.ID),
			Status:          projectautomation.AutomationStatusEnabled,
			AgentID:         item.step.Agent,
			PlanID:          plan.ID,
			AllowedTaskRefs: []string{task.TaskRef},
			TriggerKind:     projectautomation.TriggerKindAutomatic,
			SchedulePolicy:  "on-ready-task",
			PermissionRef:   "permission_snapshot:" + snapshot.ID,
			SourceKind:      projectautomation.AutomationSourceWorkflow,
			CreatedByRunID:  firstNonEmpty(runID, workflow.CreatedByRunID),
			TraceID:         firstNonEmpty(traceID, workflow.TraceID),
		})
		if err != nil {
			return result, fmt.Errorf("%w: create compiled task automation %s: %v", ErrInvalidInput, item.step.ID, err)
		}
		result.AutomationIDs = append(result.AutomationIDs, automation.ID)
	}
	return result, nil
}

type compileGraph struct {
	tasks           []plannedTask
	automationSteps []WorkflowStep
	gatesByStep     map[string][]WorkflowReviewGate
	stepsByID       map[string]WorkflowStep
}

func (svc *Service) planCompileGraph(workflow WorkflowDefinition) (compileGraph, error) {
	graph := compileGraph{gatesByStep: requiredGatesByStep(workflow.ReviewGates), stepsByID: map[string]WorkflowStep{}}
	for _, step := range workflow.Steps {
		graph.stepsByID[step.ID] = step
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
			if !hasTaskProducingDependency(step, graph.stepsByID) {
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
		step, ok := graph.stepsByID[stepID]
		if !ok || !isTaskProducingWorkflowStep(step) {
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
			result.AutomationIDs = append(result.AutomationIDs, svc.newID("automation"))
		}
	}
	for _, step := range graph.automationSteps {
		if isTaskProducingWorkflowStep(step) {
			continue
		}
		for range graph.gatesByStep[step.ID] {
			result.ReviewerTaskIDs = append(result.ReviewerTaskIDs, svc.newID("work_task"))
			result.AutomationIDs = append(result.AutomationIDs, svc.newID("automation"))
		}
	}
	for range graph.automationSteps {
		result.AutomationIDs = append(result.AutomationIDs, svc.newID("automation"))
	}
	coveredTaskRefs := map[string]bool{}
	for _, step := range graph.automationSteps {
		stepStatus := workflowAutomationStatus(step)
		stepTrigger := workflowAutomationTrigger(step)
		if stepStatus == projectautomation.AutomationStatusEnabled && stepTrigger == projectautomation.TriggerKindAutomatic {
			for _, ref := range dryRunAutomationStepAllowedTaskRefs(step, graph.stepsByID) {
				coveredTaskRefs[ref] = true
			}
		}
	}
	for _, task := range graph.tasks {
		if !coveredTaskRefs[task.step.ID] {
			result.AutomationIDs = append(result.AutomationIDs, svc.newID("automation"))
		}
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
		expected, err := svc.permissionSnapshotForAgent(workflow, agent)
		if err != nil {
			return nil, err
		}
		if existing := byAgent[agent.ID]; existing.ID != "" {
			if existing.ContentHash != expected.ContentHash {
				expected.CreatedAt = existing.CreatedAt
				if expected.CreatedAt.IsZero() {
					expected.CreatedAt = svc.now()
				}
				expected.UpdatedAt = svc.now()
				updated, err := svc.store.UpdatePermissionSnapshot(ctx, expected)
				if err != nil {
					return nil, err
				}
				byAgent[agent.ID] = updated
			}
			continue
		}
		created, err := svc.store.CreatePermissionSnapshot(ctx, expected)
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
	acceptanceCriteria, stopConditions, verifierLadder, regressionApplicability, downstreamImpactRefs, outputContract := workflowStepGovernance(step)
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               workflow.ProjectID,
		PlanID:                  planID,
		TaskRef:                 step.ID,
		Title:                   step.Title,
		Description:             descriptionWithAgentInstructions(step.Description, workflowAgentInstructions(workflow, step.Agent)),
		OwnerAgent:              step.Agent,
		RunID:                   runID,
		TraceID:                 firstNonEmpty(traceID, workflow.TraceID),
		EvidenceNeeded:          fallbackList(evidence, "implementation-evidence-required"),
		ContextPackRefs:         step.ContextPackRefs,
		FilesToRead:             step.FilesToRead,
		FilesToEdit:             step.FilesToEdit,
		LikelyFilesAffected:     step.LikelyFilesAffected,
		DependencyTaskIDs:       nil,
		VerificationRequirement: firstNonEmpty(step.VerificationRequirement, "orchestrator runs focused verifier"),
		ExpectedOutput:          firstNonEmpty(step.ExpectedOutput, "bounded implementation artifact"),
		FailureCriteria:         firstNonEmpty(step.FailureCriteria, "block if evidence or verifier scope is missing"),
		ReviewGate:              firstNonEmpty(step.ReviewGate, compileReviewGate(gates)),
		ResumeInstructions:      firstNonEmpty(step.ResumeInstructions, "resume from task metadata and attached refs only"),
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      acceptanceCriteria,
		StopConditions:          stopConditions,
		VerifierLadder:          verifierLadder,
		RegressionApplicability: regressionApplicability,
		DownstreamImpactRefs:    downstreamImpactRefs,
		OutputContract:          outputContract,
	}
}

func workflowStepGovernance(step WorkflowStep) ([]string, []string, []string, string, []string, string) {
	acceptanceCriteria := append([]string(nil), step.AcceptanceCriteria...)
	stopConditions := append([]string(nil), step.StopConditions...)
	verifierLadder := append([]string(nil), step.VerifierLadder...)
	regressionApplicability := step.RegressionApplicability
	downstreamImpactRefs := append([]string(nil), step.DownstreamImpactRefs...)
	outputContract := step.OutputContract
	if step.ID != "decompose-work-plan" {
		return acceptanceCriteria, stopConditions, verifierLadder, regressionApplicability, downstreamImpactRefs, outputContract
	}
	if len(acceptanceCriteria) == 0 {
		acceptanceCriteria = []string{
			"Each child Work Task has one objective, bounded scope, dependencies, evidence needs, review gate, verifier requirement, and resume instructions.",
			"Each child Work Task can be executed by an isolated worker from task metadata and attached refs only.",
		}
	}
	if len(stopConditions) == 0 {
		stopConditions = []string{
			"Block instead of creating tasks when scope, evidence, dependencies, or verifier requirements are missing.",
			"Do not mark child tasks ready until independent planning review approves the task packets.",
		}
	}
	if len(verifierLadder) == 0 {
		verifierLadder = []string{
			"orchestrator reviews child task metadata completeness",
			"orchestrator verifies dependency and downstream-impact refs",
			"orchestrator checks review gate and verifier requirements before ready status",
		}
	}
	if regressionApplicability == "" {
		regressionApplicability = "required when decomposition identifies code-impacting work; otherwise record a concrete not-applicable reason"
	}
	if len(downstreamImpactRefs) == 0 {
		downstreamImpactRefs = []string{"dependency-map-ref", "downstream-impact-ref"}
	}
	if outputContract == "" {
		outputContract = "planned child Work Tasks with complete governance metadata and no hidden chat context"
	}
	return acceptanceCriteria, stopConditions, verifierLadder, regressionApplicability, downstreamImpactRefs, outputContract
}

func (svc *Service) reviewTaskInput(workflow WorkflowDefinition, planID string, reviewed projectworkplan.WorkTask, gate WorkflowReviewGate, runID string, traceID string) projectworkplan.CreateWorkTaskInput {
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               workflow.ProjectID,
		PlanID:                  planID,
		TaskRef:                 "review-" + reviewed.TaskRef + "-" + gate.ID,
		Title:                   "Review " + reviewed.Title,
		Description:             reviewGateDescription(gate, workflowAgentInstructions(workflow, gate.ReviewerAgent)),
		OwnerAgent:              gate.ReviewerAgent,
		RunID:                   runID,
		TraceID:                 firstNonEmpty(traceID, workflow.TraceID),
		EvidenceNeeded:          reviewerEvidence(gate, reviewed.ID),
		ContextPackRefs:         reviewed.ContextPackRefs,
		FilesToRead:             reviewFilesToRead(reviewed),
		LikelyFilesAffected:     reviewed.LikelyFilesAffected,
		DependencyTaskIDs:       []string{reviewed.ID},
		VerificationRequirement: "attach review_result_ref",
		ExpectedOutput:          "decision and rationale",
		FailureCriteria:         "block on self-review, missing evidence, missing verifier, or unclear decision",
		ReviewGate:              "review gate " + gate.ID,
		ResumeInstructions:      "review only the referenced task, changed files, evidence refs, verifier refs, and gate instructions",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
}

func (svc *Service) reviewAutomationTaskInput(workflow WorkflowDefinition, planID string, step WorkflowStep, gate WorkflowReviewGate, runID string, traceID string) projectworkplan.CreateWorkTaskInput {
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               workflow.ProjectID,
		PlanID:                  planID,
		TaskRef:                 "review-" + step.ID + "-" + gate.ID,
		Title:                   "Review " + step.Title,
		Description:             reviewGateDescription(gate, workflowAgentInstructions(workflow, gate.ReviewerAgent)),
		OwnerAgent:              gate.ReviewerAgent,
		RunID:                   runID,
		TraceID:                 firstNonEmpty(traceID, workflow.TraceID),
		EvidenceNeeded:          reviewerAutomationEvidence(gate, step.ID),
		ContextPackRefs:         step.ContextPackRefs,
		FilesToRead:             step.FilesToRead,
		LikelyFilesAffected:     step.LikelyFilesAffected,
		DependencyTaskIDs:       nil,
		VerificationRequirement: "attach review_result_ref before automation execution",
		ExpectedOutput:          "automation review decision placeholder",
		FailureCriteria:         "block on self-review, missing automation ref, missing verifier, or unclear decision",
		ReviewGate:              "review gate " + gate.ID,
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

func allowedTaskRefs(step WorkflowStep, stepsByID map[string]WorkflowStep, taskByStep map[string]projectworkplan.WorkTask) []string {
	seen := map[string]struct{}{}
	var out []string
	var visit func(string)
	visit = func(stepID string) {
		if _, ok := seen[stepID]; ok {
			return
		}
		seen[stepID] = struct{}{}
		depStep := stepsByID[stepID]
		for _, dep := range depStep.DependsOn {
			visit(dep)
		}
		if task := taskByStep[stepID]; task.TaskRef != "" {
			out = append(out, task.TaskRef)
			return
		}
		if isTaskProducingWorkflowStep(depStep) {
			out = append(out, depStep.ID)
		}
	}
	for _, dep := range step.DependsOn {
		visit(dep)
	}
	return out
}

func automationStepAllowedTaskRefs(step WorkflowStep, stepsByID map[string]WorkflowStep, taskByStep map[string]projectworkplan.WorkTask) []string {
	if isTaskProducingWorkflowStep(step) {
		if task := taskByStep[step.ID]; task.TaskRef != "" {
			return []string{task.TaskRef}
		}
		return []string{step.ID}
	}
	return allowedTaskRefs(step, stepsByID, taskByStep)
}

func dryRunAutomationStepAllowedTaskRefs(step WorkflowStep, stepsByID map[string]WorkflowStep) []string {
	if isTaskProducingWorkflowStep(step) {
		return []string{step.ID}
	}
	return allowedTaskRefs(step, stepsByID, map[string]projectworkplan.WorkTask{})
}

func workflowAutomationStatus(step WorkflowStep) string {
	if strings.TrimSpace(step.AutomationStatus) != "" {
		return step.AutomationStatus
	}
	if isTaskProducingWorkflowStep(step) {
		return projectautomation.AutomationStatusEnabled
	}
	return projectautomation.AutomationStatusDraft
}

func workflowAutomationTrigger(step WorkflowStep) string {
	if strings.TrimSpace(step.TriggerKind) != "" {
		return step.TriggerKind
	}
	if isTaskProducingWorkflowStep(step) {
		return projectautomation.TriggerKindAutomatic
	}
	return projectautomation.TriggerKindManual
}

func workflowAutomationSchedulePolicy(step WorkflowStep) string {
	if strings.TrimSpace(step.SchedulePolicy) != "" {
		return step.SchedulePolicy
	}
	if isTaskProducingWorkflowStep(step) {
		return "on-ready-task"
	}
	return ""
}

func compiledDependencyTaskIDs(step WorkflowStep, stepsByID map[string]WorkflowStep, taskByStep map[string]projectworkplan.WorkTask, reviewTaskIDsByReviewedStep map[string][]string, reviewTaskIDsByAutomationStep map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	var add func(string)
	add = func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, depID := range step.DependsOn {
		depStep := stepsByID[depID]
		if task := taskByStep[depID]; task.ID != "" {
			add(task.ID)
			for _, reviewTaskID := range reviewTaskIDsByReviewedStep[depID] {
				add(reviewTaskID)
			}
		}
		switch depStep.Kind {
		case WorkflowStepKindWorkTask:
		case WorkflowStepKindAutomation, WorkflowStepKindAutomationBatch:
			for _, reviewTaskID := range reviewTaskIDsByAutomationStep[depID] {
				add(reviewTaskID)
			}
			for _, taskRef := range allowedTaskRefs(depStep, stepsByID, taskByStep) {
				if task := taskByStep[taskRef]; task.ID != "" {
					add(task.ID)
				}
				for _, reviewTaskID := range reviewTaskIDsByReviewedStep[taskRef] {
					add(reviewTaskID)
				}
			}
		}
	}
	return out
}

func automationRequiredReviewTaskIDs(stepID string, reviewTaskIDsByReviewedStep map[string][]string, reviewTaskIDsByAutomationStep map[string][]string) []string {
	return append([]string(nil), reviewTaskIDsByAutomationStep[stepID]...)
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
	gitBranchRef     string
	gitWorktreeRef   string
}

func compileIsolationRefs(workflow WorkflowDefinition, planRef string, userRequestRef string, runID string, options CompileOptions) compileIsolation {
	token := safeCompileGitToken(firstNonEmpty(planRef, userRequestRef, runID, workflow.WorkflowRef, workflow.ID))
	if token == "" {
		token = safeCompileGitToken(workflow.ProjectID)
	}
	if token == "" {
		token = "workflow"
	}
	// Workflow metadata does not carry a verified repository default branch.
	// Leave git_base_ref unset so workspace creation falls back to HEAD.
	branchToken := token
	if summary := renderCompileBranchSummary(options.BranchSummaryTemplate, userRequestRef, workflow.WorkflowRef, token); summary != "" {
		branchToken = compileUniqueBranchSummary(summary, token)
	}
	branchPrefix := options.BranchPrefix
	if strings.TrimSpace(branchPrefix) == "" && options.BranchSummaryTemplate == "" {
		branchPrefix = "mivia/"
	}
	return compileIsolation{
		parallelGroupRef: "workflow/" + token,
		workspaceRef:     "workflow/" + token,
		gitBranchRef:     branchPrefix + branchToken,
		gitWorktreeRef:   "workflow/" + token,
	}
}

func renderCompileBranchSummary(template string, userRequestRef string, workflowRef string, token string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}
	ticket := compileTicketRef(userRequestRef)
	out := strings.ReplaceAll(template, "{{ticket_ref}}", ticket)
	out = strings.ReplaceAll(out, "{{user_request_ref}}", userRequestRef)
	out = strings.ReplaceAll(out, "{{workflow_ref}}", workflowRef)
	out = strings.ReplaceAll(out, "{{token}}", token)
	return safeCompileBranchName(out)
}

func compileTicketRef(userRequestRef string) string {
	userRequestRef = strings.TrimSpace(userRequestRef)
	if strings.HasPrefix(userRequestRef, "jira:") {
		ticket := strings.TrimSpace(strings.TrimPrefix(userRequestRef, "jira:"))
		if ticket != "" {
			return ticket
		}
	}
	return "MASS-0000"
}

func compileUniqueBranchSummary(summary string, token string) string {
	summary = safeCompileBranchName(summary)
	if summary == "" {
		return ""
	}
	unique := compileUniqueToken(token)
	if unique == "" || strings.Contains(summary, unique) {
		return summary
	}
	return safeCompileBranchName(summary + "-" + unique)
}

func compileUniqueToken(token string) string {
	token = safeCompileGitToken(token)
	if token == "" {
		return ""
	}
	if idx := strings.LastIndex(token, "compile-"); idx >= 0 {
		return token[idx:]
	}
	return token
}

func safeCompileBranchName(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		parts[i] = safeCompileBranchSegment(part)
	}
	out := strings.Join(parts, "/")
	out = strings.Trim(out, "/-")
	if len(out) > 160 {
		out = strings.Trim(out[:160], "/-")
	}
	return out
}

func safeCompileBranchSegment(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
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
	return strings.Trim(builder.String(), "-")
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
		if isTaskProducingWorkflowStep(stepsByID[dep]) {
			return true
		}
	}
	return false
}

func isTaskProducingWorkflowStep(step WorkflowStep) bool {
	return step.Kind == WorkflowStepKindWorkTask || step.Kind == WorkflowStepKindAutomationBatch
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

func workflowAgentInstructions(workflow WorkflowDefinition, agentID string) string {
	agentID = strings.TrimSpace(agentID)
	for _, agent := range workflow.Agents {
		if agent.ID == agentID {
			return strings.TrimSpace(agent.Instructions)
		}
	}
	return ""
}

func descriptionWithAgentInstructions(description string, instructions string) string {
	description = strings.TrimSpace(description)
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return truncateForCompile(description, 1000)
	}
	if description == "" {
		return truncateForCompile("Agent instructions: "+instructions, 1000)
	}
	return truncateForCompile("Agent instructions: "+instructions+" Task description: "+description, 1000)
}

func reviewGateDescription(gate WorkflowReviewGate, reviewerInstructions string) string {
	return descriptionWithAgentInstructions("Gate instructions: "+gate.Instructions, reviewerInstructions)
}

func compileReviewGate(gates []WorkflowReviewGate) string {
	if len(gates) == 0 {
		return ""
	}
	ids := make([]string, 0, len(gates))
	for _, gate := range gates {
		if strings.TrimSpace(gate.ID) != "" {
			ids = append(ids, gate.ID)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return truncateForCompile("required review gates: "+strings.Join(ids, ","), 500)
}

func reviewFilesToRead(task projectworkplan.WorkTask) []string {
	out := make([]string, 0, len(task.FilesToRead)+len(task.FilesToEdit)+len(task.LikelyFilesAffected))
	seen := map[string]bool{}
	for _, list := range [][]string{task.FilesToRead, task.FilesToEdit, task.LikelyFilesAffected} {
		for _, value := range list {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
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
