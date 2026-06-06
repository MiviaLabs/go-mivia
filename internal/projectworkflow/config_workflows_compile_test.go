package projectworkflow_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestConfigWorkflowDefinitionsDryRunCompile(t *testing.T) {
	ctx := context.Background()
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "workflows", "*.toml"))
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
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "workflows", "*.toml"))
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
