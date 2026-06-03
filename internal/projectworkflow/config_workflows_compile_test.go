package projectworkflow_test

import (
	"context"
	"os"
	"path/filepath"
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
