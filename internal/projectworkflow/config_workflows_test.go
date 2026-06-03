package projectworkflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigWorkflowDefinitionsParseAndValidate(t *testing.T) {
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
			defs, issues, err := ParseWorkflowTOML(data)
			if err != nil {
				t.Fatalf("parse workflow definition: %v", err)
			}
			if len(defs) == 0 {
				t.Fatal("expected at least one workflow")
			}
			for _, issue := range issues {
				if issue.Severity == workflowIssueError {
					t.Fatalf("workflow validation issue at %s: %s", issue.FieldPath, issue.Message)
				}
			}
		})
	}
}
