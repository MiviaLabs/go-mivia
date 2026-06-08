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
	decompose := compiledTaskByRef(t, tasks, "decompose-work-plan")
	if decompose.ReviewGate == "" || decompose.VerificationRequirement == "" || decompose.ResumeInstructions == "" {
		t.Fatalf("decompose task is not low-intelligence-ready: %#v", decompose)
	}
	if !strings.Contains(decompose.ReviewGate, "planning-readiness-review") {
		t.Fatalf("decompose task missing planning review gate: %q", decompose.ReviewGate)
	}
	if len(decompose.AcceptanceCriteria) == 0 || len(decompose.StopConditions) == 0 || len(decompose.VerifierLadder) == 0 || decompose.RegressionApplicability == "" || len(decompose.DownstreamImpactRefs) == 0 || decompose.OutputContract == "" {
		t.Fatalf("decompose task missing first-class governance fields: %#v", decompose)
	}
}

func TestMassGovernedWorkflowsCompileRequiredAutomationInvariants(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	workflowStore := workflowstore.NewMemoryStore()
	svc := projectworkflow.New(workflowStore)
	svc.SetCompilerDependencies(workPlans, automations)
	svc.SetCompileOptionsByProject(map[string]projectworkflow.CompileOptions{
		"mass-monorepo": {BranchPrefix: "", BranchSummaryTemplate: "chore-{{ticket_ref}}-{{workflow_ref}}"},
	})

	decomposition := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "mass", "governed-decomposition-planning.toml"), "mass-monorepo")
	assertMassAgentsCanReadLocalTicketEvidence(t, decomposition)
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
	decompositionResult, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: decomposition.ProjectID, WorkflowID: decomposition.ID, UserRequestRef: "jira:MASS-1044", CreatedByRunID: "mass-decomposition-test-run"})
	if err != nil {
		t.Fatalf("compile MASS decomposition workflow: %v", err)
	}
	assertMassPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, decomposition)
	decompositionTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "mass-monorepo", PlanID: decompositionResult.WorkPlanID})
	if err != nil {
		t.Fatalf("list MASS decomposition tasks: %v", err)
	}
	compiledDecomposeTask := compiledTaskByRef(t, decompositionTasks, "decompose-work-plan")
	if len(compiledDecomposeTask.AcceptanceCriteria) == 0 || len(compiledDecomposeTask.StopConditions) == 0 || len(compiledDecomposeTask.VerifierLadder) == 0 || compiledDecomposeTask.RegressionApplicability == "" || len(compiledDecomposeTask.DownstreamImpactRefs) == 0 || compiledDecomposeTask.OutputContract == "" {
		t.Fatalf("MASS decompose-work-plan must compile first-class governance fields, got %#v", compiledDecomposeTask)
	}

	smokeGitOps := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "mass", "governed-smoke-gitops.toml"), "mass-monorepo")
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
		t.Fatalf("compile MASS smoke GitOps workflow: %v", err)
	}
	smokePlan, err := workPlans.GetWorkPlan(ctx, "mass-monorepo", smokeResult.WorkPlanID)
	if err != nil {
		t.Fatalf("get MASS smoke GitOps plan: %v", err)
	}
	if !strings.HasPrefix(smokePlan.GitBranchRef, "chore-MASS-0000-governed-smoke-gitops-compile-") || strings.Contains(smokePlan.GitBranchRef, "input-smoke") {
		t.Fatalf("MASS smoke GitOps branch must satisfy ticket branch policy with fake MASS-0000, got %q", smokePlan.GitBranchRef)
	}
	smokeTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "mass-monorepo", PlanID: smokeResult.WorkPlanID})
	if err != nil {
		t.Fatalf("list MASS smoke GitOps tasks: %v", err)
	}
	smokeTask := compiledTaskByRef(t, smokeTasks, "smoke-draft-pr")
	if !strings.Contains(smokeTask.Description, "input:smoke-20260608g") || strings.Contains(smokeTask.Description, "{{user_request_ref}}") {
		t.Fatalf("MASS smoke GitOps task must compile concrete input ref into worker packet, got %q", smokeTask.Description)
	}

	implementation := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "mass", "governed-workplan-implementation.toml"), "mass-monorepo")
	assertMassAgentsCanReadLocalTicketEvidence(t, implementation)
	for i := 0; i < 2; i++ {
		if _, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: implementation.ProjectID, WorkflowID: implementation.ID, UserRequestRef: "jira:MASS-1044", CreatedByRunID: "same-ticket-run"}); err != nil {
			t.Fatalf("compile implementation workflow %d: %v", i+1, err)
		}
	}
	assertMassPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, implementation)
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "mass-monorepo"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	branches := map[string]struct{}{}
	var implementationPlan projectworkplan.WorkPlan
	for _, plan := range plans {
		if strings.Contains(plan.PlanRef, "governed-workplan-implementation") {
			if !strings.HasPrefix(plan.GitBranchRef, "chore-MASS-1044-governed-workplan-implementation-compile-") {
				t.Fatalf("implementation branch must preserve MASS policy and compile uniqueness, got %q", plan.GitBranchRef)
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
	implementationTasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "mass-monorepo", PlanID: implementationPlan.ID})
	if err != nil {
		t.Fatalf("list implementation tasks: %v", err)
	}
	batchTask := compiledTaskByRef(t, implementationTasks, "run-implementation-batch")
	if batchTask.ID == "" || batchTask.ReviewGate == "" {
		t.Fatalf("run-implementation-batch must compile as a concrete reviewed Work Task: %#v", batchTask)
	}
	allAutomations, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: "mass-monorepo"})
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

	validation := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "mass", "governed-post-implementation-validation.toml"), "mass-monorepo")
	assertMassAgentsCanReadLocalTicketEvidence(t, validation)
	if _, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: validation.ProjectID, WorkflowID: validation.ID, UserRequestRef: "jira:MASS-1044", CreatedByRunID: "validation-ticket-run"}); err != nil {
		t.Fatalf("compile validation workflow: %v", err)
	}
	assertMassPermissionSnapshotsCanReadLocalTicketEvidence(t, ctx, workflowStore, validation)
}

func TestMassGovernedDecompositionPlanningStageSequence(t *testing.T) {
	ctx := context.Background()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	svc.SetCompilerDependencies(workPlans, automations)

	decomposition := importConfigWorkflow(t, ctx, svc, filepath.Join("..", "..", "configs", "workflows", "mass", "governed-decomposition-planning.toml"), "mass-monorepo")
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
			if !strings.Contains(filepath.ToSlash(path), "/mass/") {
				t.Skip("MASS workflow gate coverage invariant")
			}
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
					if requiredReviewerAgents[step.Agent] {
						continue
					}
					t.Fatalf("workflow %s step %s produces work without a required review gate or reviewer-owner exemption", workflow.WorkflowRef, step.ID)
				}
			}
		})
	}
}

func TestMassConfigWorkflowRequiredReviewGatesCompileReviewAutomations(t *testing.T) {
	ctx := context.Background()
	paths, err := configWorkflowPaths()
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if !strings.Contains(filepath.ToSlash(path), "/mass/") {
				t.Skip("MASS workflow review automation invariant")
			}
			workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
			automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
			workflowStore := workflowstore.NewMemoryStore()
			svc := projectworkflow.New(workflowStore)
			svc.SetCompilerDependencies(workPlans, automations)
			workflow := importConfigWorkflow(t, ctx, svc, path, "mass-monorepo")
			result, err := svc.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{ProjectID: workflow.ProjectID, WorkflowID: workflow.ID, UserRequestRef: "input:smoke-review-automation", CreatedByRunID: "review-automation-test-run"})
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

func assertMassAgentsCanReadLocalTicketEvidence(t *testing.T, workflow projectworkflow.WorkflowDefinition) {
	t.Helper()
	for _, agent := range workflow.Agents {
		if !containsConfigString(agent.AllowedTools, "projects.jira.issue.get") {
			t.Fatalf("MASS agent %s/%s must be able to read bounded local Jira issue content for ticket-scoped work, tools=%#v", workflow.WorkflowRef, agent.ID, agent.AllowedTools)
		}
		if !containsConfigString(agent.AllowedTools, "projects.integrations.search") {
			t.Fatalf("MASS agent %s/%s must be able to search bounded local integration content for ticket-scoped work, tools=%#v", workflow.WorkflowRef, agent.ID, agent.AllowedTools)
		}
	}
}

func assertMassPermissionSnapshotsCanReadLocalTicketEvidence(t *testing.T, ctx context.Context, store projectworkflow.Store, workflow projectworkflow.WorkflowDefinition) {
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
			t.Fatalf("MASS snapshot %s/%s must carry local Jira read permission, tools=%#v", workflow.WorkflowRef, snapshot.AgentID, snapshot.AllowedTools)
		}
		if !containsConfigString(snapshot.AllowedTools, "projects.integrations.search") {
			t.Fatalf("MASS snapshot %s/%s must carry local integration search permission, tools=%#v", workflow.WorkflowRef, snapshot.AgentID, snapshot.AllowedTools)
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
