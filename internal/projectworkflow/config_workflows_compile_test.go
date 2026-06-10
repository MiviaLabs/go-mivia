package projectworkflow_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestConfigWorkflowDefinitionsDryRunCompile(t *testing.T) {
	ctx := context.Background()
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected at least one workflow definition")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow definition: %v", err)
			}
			defs, _, err := projectworkflow.ParseWorkflowTOML(data)
			if err != nil {
				t.Fatalf("parse workflow definition: %v", err)
			}
			if len(defs) == 0 {
				t.Fatal("expected at least one workflow")
			}
			workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
			automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
			svc := projectworkflow.New(workflowstore.NewMemoryStore())
			svc.SetCompilerDependencies(workPlans, automations)
			imported, err := svc.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: defs[0].ProjectID, Data: data})
			if err != nil {
				t.Fatalf("import workflow definition: %v", err)
			}
			for _, workflow := range imported.Workflows {
				if _, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID, DryRun: true}); err != nil {
					t.Fatalf("dry-run compile workflow %s: %v", workflow.ID, err)
				}
			}
		})
	}
}

func TestConfigWorkflowPathsAreTrackedFixtures(t *testing.T) {
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("stat workflow fixture: %v", err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("workflow fixture must be a tracked repo file, not a symlink: %s", path)
			}
			cmd := exec.Command("git", "ls-files", "--error-unmatch", filepath.ToSlash(path))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("workflow fixture must be tracked by git: %s: %s", path, strings.TrimSpace(string(output)))
			}
		})
	}
}

func TestDecompositionWorkflowCompilesRichTaskPackets(t *testing.T) {
	ctx := context.Background()
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "workflows", "governed-decomposition-planning.toml"))
	if err != nil {
		t.Fatalf("read decomposition workflow definition: %v", err)
	}
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	svc.SetCompilerDependencies(workPlans, automations)
	imported, err := svc.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: data})
	if err != nil {
		t.Fatalf("import decomposition workflow: %v", err)
	}
	if len(imported.Workflows) != 1 {
		t.Fatalf("expected one decomposition workflow, got %d", len(imported.Workflows))
	}
	result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: "mivialabs-agents-monorepo", WorkflowID: imported.Workflows[0].ID, CreatedByRunID: "config-workflow-test-run"})
	if err != nil {
		t.Fatalf("compile decomposition workflow: %v", err)
	}
	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "mivialabs-agents-monorepo", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list compiled decomposition tasks: %v", err)
	}
	discover := compiledTaskByRef(t, tasks, "discover-planning-context")
	if discover.ReviewGate == "" || discover.DecompositionQuality != projectworkplan.DecompositionReady {
		t.Fatalf("discover task missing review/decomposition packet fields: %#v", discover)
	}
	if !containsConfigString(discover.FilesToRead, ".ai/INDEX.md") || !containsConfigString(discover.FilesToRead, ".ai/skills/mivia-mcp/SKILL.md") {
		t.Fatalf("discover task missing required instruction reads: %#v", discover.FilesToRead)
	}
	impactMap := compiledTaskByRef(t, tasks, "map-downstream-impact")
	if impactMap.Status != projectworkplan.WorkTaskStatusPlanned || len(impactMap.DependencyTaskIDs) == 0 {
		t.Fatalf("map-downstream-impact must compile as a dependent planning task, got %#v", impactMap)
	}
	if !containsConfigString(impactMap.DownstreamImpactRefs, "dependency-map-ref") || !containsConfigString(impactMap.DownstreamImpactRefs, "downstream-impact-ref") {
		t.Fatalf("map-downstream-impact must produce decomposition impact refs, got %#v", impactMap.DownstreamImpactRefs)
	}
	for _, producedRef := range impactMap.DownstreamImpactRefs {
		if containsConfigString(impactMap.EvidenceNeeded, producedRef) {
			t.Fatalf("map-downstream-impact must not require its own produced ref %q as evidence: %#v", producedRef, impactMap.EvidenceNeeded)
		}
	}
	decompose := compiledTaskByRef(t, tasks, "decompose-work-plan")
	if decompose.ReviewGate == "" || decompose.VerificationRequirement == "" || decompose.ResumeInstructions == "" {
		t.Fatalf("decompose task is not low-intelligence-ready: %#v", decompose)
	}
	if !containsConfigString(decompose.DependencyTaskIDs, impactMap.ID) {
		t.Fatalf("decompose-work-plan must depend on map-downstream-impact before it can run, deps=%#v map=%s", decompose.DependencyTaskIDs, impactMap.ID)
	}
	if !strings.Contains(decompose.ReviewGate, "planning-readiness-review") {
		t.Fatalf("decompose task missing planning review gate: %q", decompose.ReviewGate)
	}
	if len(decompose.AcceptanceCriteria) == 0 || len(decompose.StopConditions) == 0 || len(decompose.VerifierLadder) == 0 || decompose.RegressionApplicability == "" || len(decompose.DownstreamImpactRefs) == 0 || decompose.OutputContract == "" {
		t.Fatalf("decompose task missing first-class governance fields: %#v", decompose)
	}
	for _, producedRef := range []string{"task-decomposition-ref"} {
		if containsConfigString(decompose.EvidenceNeeded, producedRef) {
			t.Fatalf("decompose-work-plan must not require its own produced ref %q as evidence: %#v", producedRef, decompose.EvidenceNeeded)
		}
	}
}

func TestGENERICGovernedWorkflowsCompileRequiredAutomationInvariants(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	workflowStore := workflowstore.NewMemoryStore()
	svc := projectworkflow.New(workflowStore)
	svc.SetCompilerDependencies(workPlans, automations)
	svc.SetCompileOptionsByProject(map[string]projectworkflow.CompileOptions{
		"generic-monorepo": {BranchPrefix: "", BranchSummaryTemplate: "chore-{{ticket_ref}}-{{workflow_ref}}"},
	})

	decomposition := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-decomposition-planning.toml"), "generic-monorepo")
	assertGENERICAgentsCanReadLocalTicketEvidence(t, decomposition)
	planningWorker := configAgentByID(t, decomposition, "planning-worker")
	if !containsConfigString(planningWorker.AllowedTools, "projects.work_tasks.create") {
		t.Fatalf("planning-worker must be able to create concrete child implementation tasks, tools=%#v", planningWorker.AllowedTools)
	}
	decomposeStep := configStepByID(t, decomposition, "decompose-work-plan")
	for _, want := range []string{"Work Tasks in planned status", "regression-test decision", "acceptance criteria", "verifier ladder"} {
		if !strings.Contains(decomposeStep.ExpectedOutput, want) {
			t.Fatalf("decompose-work-plan expected_output must require %q, got %q", want, decomposeStep.ExpectedOutput)
		}
	}
	decompositionResult, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: decomposition.ProjectID, WorkflowID: decomposition.ID, UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "GENERIC-decomposition-test-run"})
	if err != nil {
		t.Fatalf("compile GENERIC decomposition workflow: %v", err)
	}
	assertGENERICPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, decomposition)
	decompositionTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "generic-monorepo", PlanID: decompositionResult.WorkPlanID})
	if err != nil {
		t.Fatalf("list GENERIC decomposition tasks: %v", err)
	}
	compiledDecomposeTask := compiledTaskByRef(t, decompositionTasks, "decompose-work-plan")
	if len(compiledDecomposeTask.AcceptanceCriteria) == 0 || len(compiledDecomposeTask.StopConditions) == 0 || len(compiledDecomposeTask.VerifierLadder) == 0 || compiledDecomposeTask.RegressionApplicability == "" || len(compiledDecomposeTask.DownstreamImpactRefs) == 0 || compiledDecomposeTask.OutputContract == "" {
		t.Fatalf("GENERIC decompose-work-plan must compile first-class governance fields, got %#v", compiledDecomposeTask)
	}
	if discover := compiledTaskByRef(t, decompositionTasks, "discover-planning-context"); discover.Status != projectworkplan.WorkTaskStatusReady || len(discover.DependencyTaskIDs) != 0 {
		t.Fatalf("GENERIC root decomposition task must compile ready with no dependencies, got %#v", discover)
	}
	impactMap := compiledTaskByRef(t, decompositionTasks, "map-downstream-impact")
	if !containsConfigString(compiledDecomposeTask.DependencyTaskIDs, impactMap.ID) {
		t.Fatalf("GENERIC decompose-work-plan must depend on map-downstream-impact before it can run, deps=%#v map=%s", compiledDecomposeTask.DependencyTaskIDs, impactMap.ID)
	}
	for _, producedRef := range impactMap.DownstreamImpactRefs {
		if containsConfigString(impactMap.EvidenceNeeded, producedRef) {
			t.Fatalf("GENERIC map-downstream-impact must not require its own produced ref %q as evidence: %#v", producedRef, impactMap.EvidenceNeeded)
		}
	}
	for _, producedRef := range []string{"task-decomposition-ref", "regression-test-applicability-ref", "acceptance-criteria-ref", "verifier-ladder-ref"} {
		if containsConfigString(compiledDecomposeTask.EvidenceNeeded, producedRef) {
			t.Fatalf("GENERIC decompose-work-plan must not require its own produced ref %q as evidence: %#v", producedRef, compiledDecomposeTask.EvidenceNeeded)
		}
	}
	for _, ref := range []string{"map-downstream-impact", "decompose-work-plan", "mark-ready-after-review"} {
		task := compiledTaskByRef(t, decompositionTasks, ref)
		if task.Status != projectworkplan.WorkTaskStatusPlanned || len(task.DependencyTaskIDs) == 0 {
			t.Fatalf("GENERIC dependent decomposition task %s must compile planned with dependencies, got %#v", ref, task)
		}
	}

	smokeGitOps := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-smoke-gitops.toml"), "generic-monorepo")
	smokeWorker := configAgentByID(t, smokeGitOps, "smoke-gitops-worker")
	smokeRuntime, err := time.ParseDuration(smokeWorker.MaxRuntime)
	if err != nil {
		t.Fatalf("parse smoke GitOps worker max_runtime: %v", err)
	}
	if smokeRuntime < 10*time.Minute {
		t.Fatalf("smoke GitOps worker max_runtime must cover observed Codex+GitOps latency, got %s", smokeWorker.MaxRuntime)
	}
	smokeResult, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: smokeGitOps.ProjectID, WorkflowID: smokeGitOps.ID, UserRequestRef: "input:smoke-20260608g", CreatedByRunID: "same-smoke-run"})
	if err != nil {
		t.Fatalf("compile GENERIC smoke GitOps workflow: %v", err)
	}
	smokePlan, err := workPlans.GetWorkPlan(ctx, "generic-monorepo", smokeResult.WorkPlanID)
	if err != nil {
		t.Fatalf("get GENERIC smoke GitOps plan: %v", err)
	}
	if !strings.HasPrefix(smokePlan.GitBranchRef, "chore-smoke-20260608g-governed-smoke-gitops-compile-") || strings.Contains(smokePlan.GitBranchRef, "input-smoke") {
		t.Fatalf("smoke GitOps branch must satisfy ticket branch policy without leaking input prefix, got %q", smokePlan.GitBranchRef)
	}
	smokeTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "generic-monorepo", PlanID: smokeResult.WorkPlanID})
	if err != nil {
		t.Fatalf("list GENERIC smoke GitOps tasks: %v", err)
	}
	smokeTask := compiledTaskByRef(t, smokeTasks, "smoke-draft-pr")
	if !strings.Contains(smokeTask.Description, "input:smoke-20260608g") || strings.Contains(smokeTask.Description, "{{user_request_ref}}") {
		t.Fatalf("GENERIC smoke GitOps task must compile concrete input ref into worker packet, got %q", smokeTask.Description)
	}

	implementation := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-workplan-implementation.toml"), "generic-monorepo")
	assertGENERICAgentsCanReadLocalTicketEvidence(t, implementation)
	for i := 0; i < 2; i++ {
		if _, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: implementation.ProjectID, WorkflowID: implementation.ID, UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "same-ticket-run"}); err != nil {
			t.Fatalf("compile implementation workflow %d: %v", i+1, err)
		}
	}
	assertGENERICPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, implementation)
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "generic-monorepo"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	branches := map[string]struct{}{}
	var implementationPlan projectworkplan.WorkPlan
	for _, plan := range plans {
		if strings.Contains(plan.PlanRef, "governed-workplan-implementation") {
			if !strings.HasPrefix(plan.GitBranchRef, "chore-GENERIC-1044-governed-workplan-implementation-compile-") {
				t.Fatalf("implementation branch must preserve GENERIC policy and compile uniqueness, got %q", plan.GitBranchRef)
			}
			if _, exists := branches[plan.GitBranchRef]; exists {
				t.Fatalf("implementation branch refs must be unique across same-ticket compiles, duplicate %q", plan.GitBranchRef)
			}
			branches[plan.GitBranchRef] = struct{}{}
			implementationPlan = plan
		}
	}
	if len(branches) != 2 {
		t.Fatalf("expected two unique implementation branch refs, got %#v", branches)
	}
	implementationTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "generic-monorepo", PlanID: implementationPlan.ID})
	if err != nil {
		t.Fatalf("list implementation tasks: %v", err)
	}
	batchTask := compiledTaskByRef(t, implementationTasks, "run-implementation-batch")
	if batchTask.ID == "" || batchTask.ReviewGate == "" {
		t.Fatalf("run-implementation-batch must compile as a concrete reviewed Work Task: %#v", batchTask)
	}
	allAutomations, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: "generic-monorepo"})
	if err != nil {
		t.Fatalf("list implementation automations: %v", err)
	}
	implementationAutomations := configAutomationsForPlan(allAutomations, implementationPlan.ID)
	batchAutomation := configAutomationByAllowedRef(t, implementationAutomations, "run-implementation-batch")
	if len(batchAutomation.RequiredReviewTaskIDs) != 0 {
		t.Fatalf("run-implementation-batch automation must not require its post-execution review before execution: %#v", batchAutomation.RequiredReviewTaskIDs)
	}
	reviewAutomation := configAutomationByAllowedRef(t, implementationAutomations, "review-run-implementation-batch-implementation-independent-review")
	if reviewAutomation.ID == "" {
		t.Fatal("expected independent review automation for run-implementation-batch")
	}

	validation := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-post-implementation-validation.toml"), "generic-monorepo")
	assertGENERICAgentsCanReadLocalTicketEvidence(t, validation)
	if _, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: validation.ProjectID, WorkflowID: validation.ID, UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "validation-ticket-run"}); err != nil {
		t.Fatalf("compile validation workflow: %v", err)
	}
	assertGENERICPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, validation)
}

func TestGENERICGovernedDecompositionPlanningStageSequence(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	svc.SetCompilerDependencies(workPlans, automations)

	decomposition := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-decomposition-planning.toml"), "generic-monorepo")
	expectedOrder := []string{
		"discover-planning-context",
		"map-downstream-impact",
		"decompose-work-plan",
		"mark-ready-after-review",
	}
	if len(decomposition.Steps) != len(expectedOrder) {
		t.Fatalf("expected exact decomposition stage sequence %v, got %#v", expectedOrder, decomposition.Steps)
	}
	for index, want := range expectedOrder {
		step := decomposition.Steps[index]
		if step.ID != want {
			t.Fatalf("stage %d must be %q, got %q", index, want, step.ID)
		}
		if !strings.Contains(step.ReviewGate, "planning-readiness-review") {
			t.Fatalf("stage %q must require planning-readiness-review, got %q", step.ID, step.ReviewGate)
		}
		if index == 0 && len(step.DependsOn) != 0 {
			t.Fatalf("discover stage must have no dependencies, got %#v", step.DependsOn)
		}
		if index > 0 && (len(step.DependsOn) != 1 || step.DependsOn[0] != expectedOrder[index-1]) {
			t.Fatalf("stage %q must depend only on %q, got %#v", step.ID, expectedOrder[index-1], step.DependsOn)
		}
	}
	gate := configReviewGateByID(t, decomposition, "planning-readiness-review")
	if !gate.Required || !gate.IndependentFromOwner {
		t.Fatalf("planning readiness review must be required and independent, got %#v", gate)
	}
	for _, stepID := range expectedOrder {
		if !containsConfigString(gate.AppliesTo, stepID) {
			t.Fatalf("planning readiness review must apply to %q, applies_to=%#v", stepID, gate.AppliesTo)
		}
	}
}

func TestGENERICGovernedDecompositionQueuesOnlyRootTaskOnPlanActivation(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  projectautomation.RunnerExecutionExternal,
		MaxParallelTasks: 2,
		PermissionResolver: workflowTestPermissionResolver{
			allowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
		},
		WorkPlanStatusTrigger: projectautomation.WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	svc.SetCompilerDependencies(workPlans, automations)

	decomposition := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-decomposition-planning.toml"), "generic-monorepo")
	result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: decomposition.ProjectID, WorkflowID: decomposition.ID, UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "GENERIC-decomposition-activation-test"})
	if err != nil {
		t.Fatalf("compile GENERIC decomposition workflow: %v", err)
	}
	plan, err := workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{ProjectID: decomposition.ProjectID, PlanID: result.WorkPlanID, Status: projectworkplan.WorkPlanStatusActive})
	if err != nil {
		t.Fatalf("activate compiled plan: %v", err)
	}
	if err := automations.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: decomposition.ProjectID, PlanID: plan.ID, NewStatus: projectworkplan.WorkPlanStatusActive}); err != nil {
		t.Fatalf("handle active plan automation trigger: %v", err)
	}
	runs, err := automations.ListRuns(ctx, projectautomation.RunFilter{ProjectID: decomposition.ProjectID, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("list automation runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly one queued root automation run, got %#v", runs)
	}
	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: decomposition.ProjectID, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("list compiled tasks: %v", err)
	}
	discover := compiledTaskByRef(t, tasks, "discover-planning-context")
	if runs[0].TaskID != discover.ID || runs[0].WorkTaskStatus != projectworkplan.WorkTaskStatusReady || runs[0].Status != projectautomation.RunStatusQueued {
		t.Fatalf("expected queued run for ready root task, run=%#v root=%#v", runs[0], discover)
	}
	for _, ref := range []string{"map-downstream-impact", "decompose-work-plan", "mark-ready-after-review"} {
		task := compiledTaskByRef(t, tasks, ref)
		if task.Status != projectworkplan.WorkTaskStatusPlanned {
			t.Fatalf("dependent task %s must remain planned after plan activation, got %#v", ref, task)
		}
	}
}

func TestGENERICGovernedGitOpsReadinessContracts(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	svc.SetCompilerDependencies(workPlans, automations)

	implementation := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-workplan-implementation.toml"), "generic-monorepo")
	implementationReady := configStepByID(t, implementation, "pr-gitops-readiness")
	assertGENERICGitOpsReadinessStep(t, implementation, implementationReady, "implementation-independent-review")
	implementationResult, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: implementation.ProjectID, WorkflowID: implementation.ID, UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "gitops-readiness-implementation-test"})
	if err != nil {
		t.Fatalf("compile implementation workflow: %v", err)
	}
	implementationTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: implementation.ProjectID, PlanID: implementationResult.WorkPlanID})
	if err != nil {
		t.Fatalf("list implementation tasks: %v", err)
	}
	assertCompiledGENERICGitOpsReadinessTask(t, compiledTaskByRef(t, implementationTasks, "pr-gitops-readiness"), "implementation-independent-review")

	validation := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "generic", "governed-post-implementation-validation.toml"), "generic-monorepo")
	validationReady := configStepByID(t, validation, "final-pr-readiness")
	assertGENERICGitOpsReadinessStep(t, validation, validationReady, "post-implementation-validation-review")
	validationResult, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: validation.ProjectID, WorkflowID: validation.ID, UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "gitops-readiness-validation-test"})
	if err != nil {
		t.Fatalf("compile validation workflow: %v", err)
	}
	validationTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: validation.ProjectID, PlanID: validationResult.WorkPlanID})
	if err != nil {
		t.Fatalf("list validation tasks: %v", err)
	}
	assertCompiledGENERICGitOpsReadinessTask(t, compiledTaskByRef(t, validationTasks, "final-pr-readiness"), "post-implementation-validation-review")
}

func TestGENERICJiraTicketToPRPipelineCompilesEveryAutomationHandoff(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	workflowStore := workflowstore.NewMemoryStore()
	svc := projectworkflow.New(workflowStore)
	svc.SetCompilerDependencies(workPlans, automations)
	svc.SetCompileOptionsByProject(map[string]projectworkflow.CompileOptions{
		"generic-monorepo": {BranchPrefix: "", BranchSummaryTemplate: "chore-{{ticket_ref}}-{{workflow_ref}}"},
	})

	stages := []struct {
		name          string
		path          string
		workflowRef   string
		reviewGate    string
		expectedTasks []string
		rootTask      string
		prReadyTask   string
		createdByRun  string
		requiredTexts []string
	}{
		{
			name:         "decomposition",
			path:         filepath.Join("..", "..", "configs", "workflows", "generic", "governed-decomposition-planning.toml"),
			workflowRef:  "governed-decomposition-planning",
			reviewGate:   "planning-readiness-review",
			rootTask:     "discover-planning-context",
			createdByRun: "GENERIC-pipeline-decomposition",
			expectedTasks: []string{
				"discover-planning-context",
				"map-downstream-impact",
				"decompose-work-plan",
				"mark-ready-after-review",
			},
			requiredTexts: []string{},
		},
		{
			name:         "implementation",
			path:         filepath.Join("..", "..", "configs", "workflows", "generic", "governed-workplan-implementation.toml"),
			workflowRef:  "governed-workplan-implementation",
			reviewGate:   "implementation-independent-review",
			rootTask:     "select-ready-tasks",
			prReadyTask:  "pr-gitops-readiness",
			createdByRun: "GENERIC-pipeline-implementation",
			expectedTasks: []string{
				"select-ready-tasks",
				"analyze-downstream-impact",
				"run-implementation-batch",
				"review-implementation-batch",
				"orchestrator-verification",
				"pr-gitops-readiness",
			},
			requiredTexts: []string{},
		},
		{
			name:         "post-validation",
			path:         filepath.Join("..", "..", "configs", "workflows", "generic", "governed-post-implementation-validation.toml"),
			workflowRef:  "governed-post-implementation-validation",
			reviewGate:   "post-implementation-validation-review",
			rootTask:     "collect-final-scope",
			prReadyTask:  "final-pr-readiness",
			createdByRun: "GENERIC-pipeline-validation",
			expectedTasks: []string{
				"collect-final-scope",
				"validate-regression-and-downstream",
				"run-final-verification",
				"final-pr-readiness",
			},
			requiredTexts: []string{},
		},
	}

	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			workflow := importConfigWorkflow(t, ctx, svc, stage.path, "generic-monorepo")
			if workflow.WorkflowRef != stage.workflowRef {
				t.Fatalf("expected workflow %q, got %q", stage.workflowRef, workflow.WorkflowRef)
			}
			assertGENERICAgentsCanReadLocalTicketEvidence(t, workflow)
			result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{
				ProjectID:      workflow.ProjectID,
				WorkflowID:     workflow.ID,
				UserRequestRef: "jira:GENERIC-1044",
				CreatedByRunID: stage.createdByRun,
				TraceID:        "trace-" + stage.name,
			})
			if err != nil {
				t.Fatalf("compile %s workflow: %v", stage.name, err)
			}
			assertGENERICPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, workflow)
			plan, err := workPlans.GetWorkPlan(ctx, workflow.ProjectID, result.WorkPlanID)
			if err != nil {
				t.Fatalf("get %s plan: %v", stage.name, err)
			}
			if plan.UserRequestRef != "jira:GENERIC-1044" || plan.CreatedByRunID != stage.createdByRun || plan.TraceID != "trace-"+stage.name {
				t.Fatalf("%s plan lost Jira/run/trace handoff refs: %#v", stage.name, plan)
			}
			if !strings.Contains(plan.PlanRef, stage.workflowRef) || !strings.HasPrefix(plan.GitBranchRef, "chore-GENERIC-1044-"+stage.workflowRef+"-compile-") {
				t.Fatalf("%s plan lost workflow or branch handoff policy: %#v", stage.name, plan)
			}
			tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: workflow.ProjectID, PlanID: plan.ID})
			if err != nil {
				t.Fatalf("list %s tasks: %v", stage.name, err)
			}
			compiledByRef := map[string]projectworkplan.WorkTask{}
			for _, task := range tasks {
				compiledByRef[task.TaskRef] = task
			}
			for index, taskRef := range stage.expectedTasks {
				task, ok := compiledByRef[taskRef]
				if !ok {
					t.Fatalf("%s missing compiled task %q in %#v", stage.name, taskRef, tasks)
				}
				assertGENERICPipelineCompiledTaskHandoff(t, stage.name, task, stage.reviewGate, stage.requiredTexts, index == 0)
				if index == 0 && task.Status != projectworkplan.WorkTaskStatusReady {
					t.Fatalf("%s root task must compile ready, got %#v", stage.name, task)
				}
				if index > 0 && (task.Status != projectworkplan.WorkTaskStatusPlanned || len(task.DependencyTaskIDs) == 0) {
					t.Fatalf("%s dependent task %q must compile planned with dependencies, got %#v", stage.name, taskRef, task)
				}
				if stage.prReadyTask != "" && taskRef == stage.prReadyTask {
					assertCompiledGENERICGitOpsReadinessTask(t, task, stage.reviewGate)
				}
			}
			compiledAutomations, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: workflow.ProjectID})
			if err != nil {
				t.Fatalf("list %s automations: %v", stage.name, err)
			}
			planAutomations := configAutomationsForPlan(compiledAutomations, plan.ID)
			assertGENERICPipelineAutomationsForTasks(t, stage.name, planAutomations, compiledByRef, stage.expectedTasks, stage.reviewGate)
		})
	}
}

func TestGenericTicketToPRPipelineCompilesEveryAutomationHandoff(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	workflowStore := workflowstore.NewMemoryStore()
	svc := projectworkflow.New(workflowStore)
	svc.SetCompilerDependencies(workPlans, automations)
	svc.SetCompileOptionsByProject(map[string]projectworkflow.CompileOptions{
		"mivialabs-agents-monorepo": {BranchPrefix: "generic/", BranchSummaryTemplate: "ticket-{{ticket_ref}}-{{workflow_ref}}"},
	})

	stages := []struct {
		name          string
		path          string
		workflowRef   string
		reviewGate    string
		expectedTasks []string
		rootTask      string
		prReadyTask   string
	}{
		{
			name:        "decomposition",
			path:        filepath.Join("..", "..", "configs", "workflows", "governed-decomposition-planning.toml"),
			workflowRef: "governed-decomposition-planning",
			reviewGate:  "planning-readiness-review",
			rootTask:    "discover-planning-context",
			expectedTasks: []string{
				"discover-planning-context",
				"map-downstream-impact",
				"decompose-work-plan",
				"mark-ready-after-review",
			},
		},
		{
			name:        "implementation",
			path:        filepath.Join("..", "..", "configs", "workflows", "governed-workplan-implementation.toml"),
			workflowRef: "governed-workplan-implementation",
			reviewGate:  "implementation-independent-review",
			rootTask:    "select-ready-tasks",
			prReadyTask: "pr-gitops-readiness",
			expectedTasks: []string{
				"select-ready-tasks",
				"run-implementation-batch",
				"review-implementation-batch",
				"orchestrator-verification",
				"pr-gitops-readiness",
			},
		},
		{
			name:        "post-validation",
			path:        filepath.Join("..", "..", "configs", "workflows", "governed-post-implementation-validation.toml"),
			workflowRef: "governed-post-implementation-validation",
			reviewGate:  "post-implementation-validation-review",
			rootTask:    "collect-final-scope",
			prReadyTask: "final-pr-readiness",
			expectedTasks: []string{
				"collect-final-scope",
				"validate-regression-and-downstream",
				"run-final-verification",
				"final-pr-readiness",
			},
		},
	}

	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			workflow := importConfigWorkflow(t, ctx, svc, stage.path, "mivialabs-agents-monorepo")
			if workflow.WorkflowRef != stage.workflowRef {
				t.Fatalf("expected workflow %q, got %q", stage.workflowRef, workflow.WorkflowRef)
			}
			result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{
				ProjectID:      workflow.ProjectID,
				WorkflowID:     workflow.ID,
				UserRequestRef: "ticket:GENERIC-1044",
				CreatedByRunID: "generic-pipeline-" + stage.name,
				TraceID:        "trace-generic-" + stage.name,
			})
			if err != nil {
				t.Fatalf("compile %s workflow: %v", stage.name, err)
			}
			assertGenericPermissionSnapshotsCompile(t, ctx, workflowStore, workflow)
			plan, err := workPlans.GetWorkPlan(ctx, workflow.ProjectID, result.WorkPlanID)
			if err != nil {
				t.Fatalf("get %s plan: %v", stage.name, err)
			}
			if plan.UserRequestRef != "ticket:GENERIC-1044" || plan.CreatedByRunID != "generic-pipeline-"+stage.name || plan.TraceID != "trace-generic-"+stage.name {
				t.Fatalf("%s plan lost input/run/trace refs: %#v", stage.name, plan)
			}
			if !strings.Contains(plan.PlanRef, stage.workflowRef) || !strings.HasPrefix(plan.GitBranchRef, "generic/ticket-GENERIC-1044-"+stage.workflowRef+"-compile-") {
				t.Fatalf("%s plan lost workflow or branch handoff policy: %#v", stage.name, plan)
			}
			tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: workflow.ProjectID, PlanID: plan.ID})
			if err != nil {
				t.Fatalf("list %s tasks: %v", stage.name, err)
			}
			compiledByRef := map[string]projectworkplan.WorkTask{}
			for _, task := range tasks {
				compiledByRef[task.TaskRef] = task
			}
			for index, taskRef := range stage.expectedTasks {
				task, ok := compiledByRef[taskRef]
				if !ok {
					t.Fatalf("%s missing compiled task %q in %#v", stage.name, taskRef, tasks)
				}
				assertGenericPipelineCompiledTaskHandoff(t, stage.name, task, stage.reviewGate, index == 0)
				if index == 0 && task.TaskRef != stage.rootTask {
					t.Fatalf("%s root task mismatch: want %q got %#v", stage.name, stage.rootTask, task)
				}
				if index == 0 && task.Status != projectworkplan.WorkTaskStatusReady {
					t.Fatalf("%s root task must compile ready, got %#v", stage.name, task)
				}
				if index > 0 && (task.Status != projectworkplan.WorkTaskStatusPlanned || len(task.DependencyTaskIDs) == 0) {
					t.Fatalf("%s dependent task %q must compile planned with dependencies, got %#v", stage.name, taskRef, task)
				}
				if stage.prReadyTask != "" && taskRef == stage.prReadyTask {
					assertCompiledGenericGitOpsReadinessTask(t, task, stage.reviewGate)
				}
			}
			compiledAutomations, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: workflow.ProjectID})
			if err != nil {
				t.Fatalf("list %s automations: %v", stage.name, err)
			}
			planAutomations := configAutomationsForPlan(compiledAutomations, plan.ID)
			assertGenericPipelineAutomationsForTasks(t, stage.name, planAutomations, compiledByRef, stage.expectedTasks, stage.reviewGate)
		})
	}
}

func TestConfigWorkflowReviewGateCoverage(t *testing.T) {
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected at least one workflow definition")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow definition: %v", err)
			}
			defs, _, err := projectworkflow.ParseWorkflowTOML(data)
			if err != nil {
				t.Fatalf("parse workflow definition: %v", err)
			}
			for _, workflow := range defs {
				requiredGateByID := map[string]projectworkflow.WorkflowReviewGate{}
				for _, gate := range workflow.ReviewGates {
					if gate.Required {
						requiredGateByID[gate.ID] = gate
					}
				}
				for _, step := range workflow.Steps {
					for _, gateID := range stepReviewGateIDs(step.ReviewGate, requiredGateByID) {
						gate, ok := requiredGateByID[gateID]
						if !ok {
							t.Fatalf("workflow %s step %s references missing required review gate %q", workflow.WorkflowRef, step.ID, gateID)
						}
						if !containsConfigString(gate.AppliesTo, step.ID) {
							t.Fatalf("workflow %s step %s review gate %q must include step in applies_to=%#v", workflow.WorkflowRef, step.ID, gateID, gate.AppliesTo)
						}
					}
				}
			}
		})
	}
}

func TestConfigWorkflowTaskProducingStepsHaveGateOrReviewerRole(t *testing.T) {
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected at least one workflow definition")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow definition: %v", err)
			}
			defs, _, err := projectworkflow.ParseWorkflowTOML(data)
			if err != nil {
				t.Fatalf("parse workflow definition: %v", err)
			}
			for _, workflow := range defs {
				requiredGateByID := map[string]projectworkflow.WorkflowReviewGate{}
				requiredReviewerAgents := map[string]bool{}
				for _, gate := range workflow.ReviewGates {
					if !gate.Required {
						continue
					}
					requiredGateByID[gate.ID] = gate
					requiredReviewerAgents[gate.ReviewerAgent] = true
				}
				if len(requiredGateByID) == 0 {
					continue
				}
				for _, step := range workflow.Steps {
					if step.Kind != "work_task" && step.Kind != "automation_batch" {
						continue
					}
					if len(stepReviewGateIDs(step.ReviewGate, requiredGateByID)) > 0 {
						continue
					}
					if stepCoveredByRequiredGate(step.ID, requiredGateByID) {
						continue
					}
					if requiredReviewerAgents[step.Agent] && reviewerOwnedGateTaskExemptionAllowed(workflow.WorkflowRef, step.ID) {
						continue
					}
					t.Fatalf("workflow %s step %s produces work without a required review gate or explicit reviewer-owner exemption", workflow.WorkflowRef, step.ID)
				}
			}
		})
	}
}

func reviewerOwnedGateTaskExemptionAllowed(workflowRef string, stepID string) bool {
	allowed := map[string]map[string]bool{
		"governed-workplan-implementation": {
			"analyze-downstream-impact":   true,
			"review-implementation-batch": true,
		},
		"governed-post-implementation-validation": {
			"validate-regression-and-downstream": true,
		},
		"governed-code-review-bug-planning": {
			"review-candidate-bugs": true,
		},
	}
	return allowed[workflowRef][stepID]
}

func TestConfigWorkflowRequiredReviewGatesCompileReviewAutomations(t *testing.T) {
	ctx := context.Background()
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
			automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
			workflowStore := workflowstore.NewMemoryStore()
			svc := projectworkflow.New(workflowStore)
			svc.SetCompilerDependencies(workPlans, automations)
			workflow := importConfigWorkflow(t, ctx, svc, path, "mivialabs-agents-monorepo")
			result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID, UserRequestRef: "input:generic-review-automation", CreatedByRunID: "review-automation-test-run"})
			if err != nil {
				t.Fatalf("compile workflow: %v", err)
			}
			compiledAutomations, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: workflow.ProjectID})
			if err != nil {
				t.Fatalf("list automations: %v", err)
			}
			planAutomations := configAutomationsForPlan(compiledAutomations, result.WorkPlanID)
			for _, gate := range workflow.ReviewGates {
				if !gate.Required {
					continue
				}
				for _, stepID := range gate.AppliesTo {
					wantRef := "review-" + stepID + "-" + gate.ID
					reviewAutomation := configAutomationByAllowedRef(t, planAutomations, wantRef)
					if reviewAutomation.Status != projectautomation.AutomationStatusEnabled || reviewAutomation.TriggerKind != projectautomation.TriggerKindAutomatic {
						t.Fatalf("review automation %q must be enabled automatic, got %#v", wantRef, reviewAutomation)
					}
				}
			}
		})
	}
}

func stepCoveredByRequiredGate(stepID string, requiredGateByID map[string]projectworkflow.WorkflowReviewGate) bool {
	for _, gate := range requiredGateByID {
		if containsConfigString(gate.AppliesTo, stepID) {
			return true
		}
	}
	return false
}

func stepReviewGateIDs(reviewGate string, requiredGateByID map[string]projectworkflow.WorkflowReviewGate) []string {
	out := make([]string, 0, 1)
	for gateID := range requiredGateByID {
		if strings.Contains(reviewGate, gateID) {
			out = append(out, gateID)
		}
	}
	sort.Strings(out)
	return out
}

func importConfigWorkflow(t *testing.T, ctx context.Context, svc *projectworkflow.Service, path string, projectID string) projectworkflow.WorkflowDefinition {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow definition %s: %v", path, err)
	}
	imported, err := svc.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: projectID, Data: data})
	if err != nil {
		t.Fatalf("import workflow definition %s: %v", path, err)
	}
	if len(imported.Workflows) != 1 {
		t.Fatalf("expected one workflow in %s, got %d", path, len(imported.Workflows))
	}
	return imported.Workflows[0]
}

func configAgentByID(t *testing.T, workflow projectworkflow.WorkflowDefinition, id string) projectworkflow.WorkflowAgentDefinition {
	t.Helper()
	for _, agent := range workflow.Agents {
		if agent.ID == id {
			return agent
		}
	}
	t.Fatalf("missing workflow agent %q", id)
	return projectworkflow.WorkflowAgentDefinition{}
}

func assertGENERICAgentsCanReadLocalTicketEvidence(t *testing.T, workflow projectworkflow.WorkflowDefinition) {
	t.Helper()
	for _, agent := range workflow.Agents {
		if !containsConfigString(agent.AllowedTools, "projects.jira.issue.get") {
			t.Fatalf("GENERIC agent %s/%s must be able to read bounded local Jira issue content for ticket-scoped work, tools=%#v", workflow.WorkflowRef, agent.ID, agent.AllowedTools)
		}
		if !containsConfigString(agent.AllowedTools, "projects.integrations.search") {
			t.Fatalf("GENERIC agent %s/%s must be able to search bounded local integration content for ticket-scoped work, tools=%#v", workflow.WorkflowRef, agent.ID, agent.AllowedTools)
		}
	}
}

func assertGENERICPermissionSnapshotsCanReadLocalTicketEvidence(t *testing.T, ctx context.Context, store projectworkflow.Store, workflow projectworkflow.WorkflowDefinition) {
	t.Helper()
	snapshots, err := store.ListPermissionSnapshots(ctx, projectworkflow.PermissionSnapshotFilter{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID})
	if err != nil {
		t.Fatalf("list permission snapshots for %s: %v", workflow.WorkflowRef, err)
	}
	if len(snapshots) != len(workflow.Agents) {
		t.Fatalf("expected one permission snapshot per %s agent, snapshots=%#v agents=%#v", workflow.WorkflowRef, snapshots, workflow.Agents)
	}
	for _, snapshot := range snapshots {
		if !containsConfigString(snapshot.AllowedTools, "projects.jira.issue.get") {
			t.Fatalf("GENERIC snapshot %s/%s must carry local Jira read permission, tools=%#v", workflow.WorkflowRef, snapshot.AgentID, snapshot.AllowedTools)
		}
		if !containsConfigString(snapshot.AllowedTools, "projects.integrations.search") {
			t.Fatalf("GENERIC snapshot %s/%s must carry local integration search permission, tools=%#v", workflow.WorkflowRef, snapshot.AgentID, snapshot.AllowedTools)
		}
	}
}

func configStepByID(t *testing.T, workflow projectworkflow.WorkflowDefinition, id string) projectworkflow.WorkflowStep {
	t.Helper()
	for _, step := range workflow.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("missing workflow step %q", id)
	return projectworkflow.WorkflowStep{}
}

func configReviewGateByID(t *testing.T, workflow projectworkflow.WorkflowDefinition, id string) projectworkflow.WorkflowReviewGate {
	t.Helper()
	for _, gate := range workflow.ReviewGates {
		if gate.ID == id {
			return gate
		}
	}
	t.Fatalf("missing workflow review gate %q", id)
	return projectworkflow.WorkflowReviewGate{}
}

func assertGENERICGitOpsReadinessStep(t *testing.T, workflow projectworkflow.WorkflowDefinition, step projectworkflow.WorkflowStep, reviewGateID string) {
	t.Helper()
	if !strings.Contains(step.ReviewGate, reviewGateID) {
		t.Fatalf("workflow %s step %s must require review gate %q, got %q", workflow.WorkflowRef, step.ID, reviewGateID, step.ReviewGate)
	}
	for _, ref := range []string{"git-status-ref", "branch-policy-ref", "review-result-ref", "verifier-result-ref", "regression-test-ref", "generated-artifact-check-ref", "pr-description-ref", "metadata-redaction-ref"} {
		if !containsConfigString(step.EvidenceNeeded, ref) {
			t.Fatalf("workflow %s step %s missing GitOps readiness evidence %q, evidence=%#v", workflow.WorkflowRef, step.ID, ref, step.EvidenceNeeded)
		}
	}
	for _, text := range []string{"GitOps", "unsafe metadata", "review", "verifier", "regression", "generated artifact"} {
		if !strings.Contains(step.VerificationRequirement+step.FailureCriteria+step.ExpectedOutput, text) {
			t.Fatalf("workflow %s step %s GitOps readiness contract missing %q, step=%#v", workflow.WorkflowRef, step.ID, text, step)
		}
	}
}

func assertCompiledGENERICGitOpsReadinessTask(t *testing.T, task projectworkplan.WorkTask, reviewGateID string) {
	t.Helper()
	if task.Status != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("GitOps readiness task must compile planned until dependencies/review complete, got %#v", task)
	}
	if !strings.Contains(task.ReviewGate, reviewGateID) {
		t.Fatalf("GitOps readiness task missing review gate %q, got %#v", reviewGateID, task)
	}
	for _, ref := range []string{"git-status-ref", "branch-policy-ref", "review-result-ref", "verifier-result-ref", "regression-test-ref", "generated-artifact-check-ref", "pr-description-ref", "metadata-redaction-ref"} {
		if !containsConfigString(task.EvidenceNeeded, ref) {
			t.Fatalf("compiled GitOps readiness task missing evidence %q, got %#v", ref, task.EvidenceNeeded)
		}
	}
	if !strings.Contains(task.FailureCriteria, "unsafe metadata") || !strings.Contains(task.VerificationRequirement, "GitOps") {
		t.Fatalf("compiled GitOps readiness task must preserve GitOps/redaction failure contract, got %#v", task)
	}
}

func assertGenericPermissionSnapshotsCompile(t *testing.T, ctx context.Context, store projectworkflow.Store, workflow projectworkflow.WorkflowDefinition) {
	t.Helper()
	snapshots, err := store.ListPermissionSnapshots(ctx, projectworkflow.PermissionSnapshotFilter{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID})
	if err != nil {
		t.Fatalf("list permission snapshots for %s: %v", workflow.WorkflowRef, err)
	}
	if len(snapshots) != len(workflow.Agents) {
		t.Fatalf("expected one permission snapshot per %s agent, snapshots=%#v agents=%#v", workflow.WorkflowRef, snapshots, workflow.Agents)
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == "" || snapshot.ContentHash == "" || len(snapshot.AllowedTools) == 0 || snapshot.WorkspaceMode == "" || snapshot.NetworkPolicy == "" || snapshot.SecretPolicy == "" {
			t.Fatalf("snapshot %s/%s missing reusable permission handoff fields: %#v", workflow.WorkflowRef, snapshot.AgentID, snapshot)
		}
	}
}

func assertCompiledGenericGitOpsReadinessTask(t *testing.T, task projectworkplan.WorkTask, reviewGateID string) {
	t.Helper()
	if task.Status != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("GitOps readiness task must compile planned until dependencies/review complete, got %#v", task)
	}
	if !strings.Contains(task.ReviewGate, reviewGateID) {
		t.Fatalf("GitOps readiness task missing review gate %q, got %#v", reviewGateID, task)
	}
	for _, ref := range []string{"git-status-ref", "branch-policy-ref", "review-result-ref", "verifier-result-ref", "regression-test-ref", "generated-artifact-check-ref", "pr-description-ref", "metadata-redaction-ref"} {
		if !containsConfigString(task.EvidenceNeeded, ref) {
			t.Fatalf("compiled GitOps readiness task missing evidence %q, got %#v", ref, task.EvidenceNeeded)
		}
	}
	if !strings.Contains(task.FailureCriteria, "unsafe metadata") || !strings.Contains(task.VerificationRequirement, "GitOps") {
		t.Fatalf("compiled GitOps readiness task must preserve GitOps/redaction failure contract, got %#v", task)
	}
}

func assertGenericPipelineCompiledTaskHandoff(t *testing.T, stageName string, task projectworkplan.WorkTask, reviewGateID string, isRoot bool) {
	t.Helper()
	if task.ID == "" || task.ProjectID == "" || task.PlanID == "" || task.TaskRef == "" {
		t.Fatalf("%s task missing stable refs: %#v", stageName, task)
	}
	if task.OwnerAgent == "" || task.Description == "" || task.ExpectedOutput == "" || task.FailureCriteria == "" || task.ResumeInstructions == "" {
		t.Fatalf("%s task %s missing executable handoff text fields: %#v", stageName, task.TaskRef, task)
	}
	reviewerOwned := strings.Contains(task.OwnerAgent, "reviewer")
	if !reviewerOwned && (task.ReviewGate == "" || !strings.Contains(task.ReviewGate, reviewGateID)) {
		t.Fatalf("%s task %s missing review gate %q: %#v", stageName, task.TaskRef, reviewGateID, task)
	}
	if len(task.EvidenceNeeded) == 0 || (len(task.LikelyFilesAffected) == 0 && len(task.FilesToRead) == 0 && len(task.FilesToEdit) == 0) || task.VerificationRequirement == "" {
		t.Fatalf("%s task %s missing evidence/scope/verifier handoff fields: %#v", stageName, task.TaskRef, task)
	}
	if len(task.AcceptanceCriteria) == 0 || len(task.StopConditions) == 0 || len(task.VerifierLadder) == 0 || task.RegressionApplicability == "" || len(task.DownstreamImpactRefs) == 0 || task.OutputContract == "" {
		t.Fatalf("%s task %s missing first-class governance fields: %#v", stageName, task.TaskRef, task)
	}
	if isRoot && len(task.DependencyTaskIDs) != 0 {
		t.Fatalf("%s root task %s must not depend on later work, got %#v", stageName, task.TaskRef, task.DependencyTaskIDs)
	}
}

func assertGenericPipelineAutomationsForTasks(t *testing.T, stageName string, automations []projectautomation.Automation, tasksByRef map[string]projectworkplan.WorkTask, taskRefs []string, reviewGateID string) {
	t.Helper()
	for _, taskRef := range taskRefs {
		task, ok := tasksByRef[taskRef]
		if !ok {
			t.Fatalf("%s missing compiled task for automation assertion %q", stageName, taskRef)
		}
		worker := configAutomationByAllowedRef(t, automations, taskRef)
		if worker.Status != projectautomation.AutomationStatusEnabled || worker.TriggerKind != projectautomation.TriggerKindAutomatic || worker.SourceKind != projectautomation.AutomationSourceWorkflow {
			t.Fatalf("%s worker automation for %s lost status/trigger/source handoff: %#v", stageName, taskRef, worker)
		}
		if worker.PermissionRef == "" || !strings.HasPrefix(worker.PermissionRef, projectautomation.PermissionSnapshotRefPrefix) {
			t.Fatalf("%s worker automation for %s missing permission snapshot ref: %#v", stageName, taskRef, worker)
		}
		if worker.CreatedByRunID == "" || worker.TraceID == "" {
			t.Fatalf("%s worker automation for %s missing creator/trace handoff: %#v", stageName, taskRef, worker)
		}
		if strings.Contains(task.OwnerAgent, "reviewer") {
			continue
		}
		reviewRef := "review-" + taskRef + "-" + reviewGateID
		review := configAutomationByAllowedRef(t, automations, reviewRef)
		if review.Status != projectautomation.AutomationStatusEnabled || review.TriggerKind != projectautomation.TriggerKindAutomatic || review.SourceKind != projectautomation.AutomationSourceWorkflow {
			t.Fatalf("%s review automation for %s lost status/trigger/source handoff: %#v", stageName, taskRef, review)
		}
		if review.PermissionRef == "" || !strings.HasPrefix(review.PermissionRef, projectautomation.PermissionSnapshotRefPrefix) {
			t.Fatalf("%s review automation for %s missing permission snapshot ref: %#v", stageName, taskRef, review)
		}
		if review.CreatedByRunID != worker.CreatedByRunID || review.TraceID != worker.TraceID {
			t.Fatalf("%s review automation for %s lost creator/trace continuity: worker=%#v review=%#v", stageName, taskRef, worker, review)
		}
	}
}

func assertGENERICPipelineCompiledTaskHandoff(t *testing.T, stageName string, task projectworkplan.WorkTask, reviewGateID string, requiredTexts []string, isRoot bool) {
	t.Helper()
	if task.ID == "" || task.ProjectID != "generic-monorepo" || task.PlanID == "" || task.TaskRef == "" {
		t.Fatalf("%s task missing stable refs: %#v", stageName, task)
	}
	if task.OwnerAgent == "" || task.Description == "" || task.ExpectedOutput == "" || task.FailureCriteria == "" || task.ResumeInstructions == "" {
		t.Fatalf("%s task %s missing executable handoff text fields: %#v", stageName, task.TaskRef, task)
	}
	reviewerOwned := strings.Contains(task.OwnerAgent, "reviewer")
	if !reviewerOwned && (task.ReviewGate == "" || !strings.Contains(task.ReviewGate, reviewGateID)) {
		t.Fatalf("%s task %s missing review gate %q: %#v", stageName, task.TaskRef, reviewGateID, task)
	}
	if len(task.EvidenceNeeded) == 0 || (len(task.LikelyFilesAffected) == 0 && len(task.FilesToRead) == 0 && len(task.FilesToEdit) == 0) || task.VerificationRequirement == "" {
		t.Fatalf("%s task %s missing evidence/scope/verifier handoff fields: %#v", stageName, task.TaskRef, task)
	}
	if len(task.AcceptanceCriteria) == 0 || len(task.StopConditions) == 0 || len(task.VerifierLadder) == 0 || task.RegressionApplicability == "" || len(task.DownstreamImpactRefs) == 0 || task.OutputContract == "" {
		t.Fatalf("%s task %s missing first-class governance fields: %#v", stageName, task.TaskRef, task)
	}
	if isRoot && len(task.DependencyTaskIDs) != 0 {
		t.Fatalf("%s root task %s must not depend on later work, got %#v", stageName, task.TaskRef, task.DependencyTaskIDs)
	}
	text := task.Description + " " + task.ExpectedOutput + " " + task.FailureCriteria + " " + task.VerificationRequirement + " " + task.OutputContract
	for _, want := range requiredTexts {
		if !strings.Contains(text, want) {
			t.Fatalf("%s task %s handoff contract missing %q in %#v", stageName, task.TaskRef, want, task)
		}
	}
}

func assertGENERICPipelineAutomationsForTasks(t *testing.T, stageName string, automations []projectautomation.Automation, tasksByRef map[string]projectworkplan.WorkTask, taskRefs []string, reviewGateID string) {
	t.Helper()
	for _, taskRef := range taskRefs {
		task, ok := tasksByRef[taskRef]
		if !ok {
			t.Fatalf("%s missing compiled task for automation assertion %q", stageName, taskRef)
		}
		worker := configAutomationByAllowedRef(t, automations, taskRef)
		if worker.Status != projectautomation.AutomationStatusEnabled || worker.TriggerKind != projectautomation.TriggerKindAutomatic || worker.SourceKind != projectautomation.AutomationSourceWorkflow {
			t.Fatalf("%s worker automation for %s lost status/trigger/source handoff: %#v", stageName, taskRef, worker)
		}
		if worker.PermissionRef == "" || !strings.HasPrefix(worker.PermissionRef, projectautomation.PermissionSnapshotRefPrefix) {
			t.Fatalf("%s worker automation for %s missing permission snapshot ref: %#v", stageName, taskRef, worker)
		}
		if worker.CreatedByRunID == "" || worker.TraceID == "" {
			t.Fatalf("%s worker automation for %s missing creator/trace handoff: %#v", stageName, taskRef, worker)
		}
		if strings.Contains(task.OwnerAgent, "reviewer") {
			continue
		}
		reviewRef := "review-" + taskRef + "-" + reviewGateID
		review := configAutomationByAllowedRef(t, automations, reviewRef)
		if review.Status != projectautomation.AutomationStatusEnabled || review.TriggerKind != projectautomation.TriggerKindAutomatic || review.SourceKind != projectautomation.AutomationSourceWorkflow {
			t.Fatalf("%s review automation for %s lost status/trigger/source handoff: %#v", stageName, taskRef, review)
		}
		if review.PermissionRef == "" || !strings.HasPrefix(review.PermissionRef, projectautomation.PermissionSnapshotRefPrefix) {
			t.Fatalf("%s review automation for %s missing permission snapshot ref: %#v", stageName, taskRef, review)
		}
		if review.CreatedByRunID != worker.CreatedByRunID || review.TraceID != worker.TraceID {
			t.Fatalf("%s review automation for %s lost creator/trace continuity: worker=%#v review=%#v", stageName, taskRef, worker, review)
		}
	}
}

func configAutomationByAllowedRef(t *testing.T, automations []projectautomation.Automation, ref string) projectautomation.Automation {
	t.Helper()
	for _, automation := range automations {
		if len(automation.AllowedTaskRefs) == 1 && automation.AllowedTaskRefs[0] == ref {
			return automation
		}
	}
	t.Fatalf("missing automation allowed ref %q in %#v", ref, automations)
	return projectautomation.Automation{}
}

func configAutomationsForPlan(automations []projectautomation.Automation, planID string) []projectautomation.Automation {
	out := make([]projectautomation.Automation, 0, len(automations))
	for _, automation := range automations {
		if automation.PlanID == planID {
			out = append(out, automation)
		}
	}
	return out
}

func compiledTaskByRef(t *testing.T, tasks []projectworkplan.WorkTask, ref string) projectworkplan.WorkTask {
	t.Helper()
	for _, task := range tasks {
		if task.TaskRef == ref {
			return task
		}
	}
	t.Fatalf("missing compiled task %q in %#v", ref, tasks)
	return projectworkplan.WorkTask{}
}

func containsConfigString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type workflowTestPermissionResolver struct {
	allowedRunnerKinds []string
}

func (resolver workflowTestPermissionResolver) CheckAutomationPermission(_ context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      input.PermissionRef,
		AgentID:            input.AgentID,
		AllowedRunnerKinds: resolver.allowedRunnerKinds,
	}, nil
}

func TestConfigWorkflowDefinitionsCompileCreatesGovernedObjects(t *testing.T) {
	ctx := context.Background()
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected at least one workflow definition")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow definition: %v", err)
			}
			defs, _, err := projectworkflow.ParseWorkflowTOML(data)
			if err != nil {
				t.Fatalf("parse workflow definition: %v", err)
			}
			if len(defs) == 0 {
				t.Fatal("expected at least one workflow")
			}
			workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
			automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
			svc := projectworkflow.New(workflowstore.NewMemoryStore())
			svc.SetCompilerDependencies(workPlans, automations)
			imported, err := svc.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: defs[0].ProjectID, Data: data})
			if err != nil {
				t.Fatalf("import workflow definition: %v", err)
			}
			for _, workflow := range imported.Workflows {
				result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID, CreatedByRunID: "config-workflow-test-run"})
				if err != nil {
					t.Fatalf("compile workflow %s: %v", workflow.ID, err)
				}
				if result.WorkPlanID == "" || len(result.WorkTaskIDs) == 0 || len(result.PermissionSnapshotIDs) == 0 {
					t.Fatalf("compile workflow %s returned incomplete refs: %#v", workflow.ID, result)
				}
				plan, err := workPlans.GetWorkPlan(ctx, workflow.ProjectID, result.WorkPlanID)
				if err != nil {
					t.Fatalf("get compiled work plan %s: %v", result.WorkPlanID, err)
				}
				if plan.GitBaseRef != "" {
					t.Fatalf("compile workflow %s must omit unverified git base ref, got %q", workflow.ID, plan.GitBaseRef)
				}
			}
		})
	}
}

func configWorkflowPaths() ([]string, error) {
	root := filepath.Join("..", "..", "configs", "workflows")
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}
