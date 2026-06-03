package projectworkflow

import (
	"strings"
	"testing"
)

func TestParseWorkflowTOMLValid(t *testing.T) {
	defs, issues, err := ParseWorkflowTOML([]byte(validWorkflowTOML()))
	if err != nil {
		t.Fatalf("ParseWorkflowTOML returned error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected one workflow, got %d", len(defs))
	}
	if len(issues) != 0 {
		t.Fatalf("expected no validation issues, got %#v", issues)
	}
	if defs[0].Agents[0].SecretPolicy != "deny" {
		t.Fatalf("expected default secret policy deny, got %q", defs[0].Agents[0].SecretPolicy)
	}
	if defs[0].Agents[0].LogPolicy != "metadata_only" {
		t.Fatalf("expected default log policy metadata_only, got %q", defs[0].Agents[0].LogPolicy)
	}
	if !defs[0].ReviewGates[0].IndependentFromOwner {
		t.Fatal("expected review gate independent_from_owner to default true")
	}
}

func TestParseWorkflowTOMLMissingReviewerInstructionsFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `instructions = "Review the implementation evidence."`, `instructions = ""`, 1)
	assertWorkflowIssue(t, toml, "required", "review_gates[0].instructions")
}

func TestParseWorkflowTOMLUnknownReviewerAgentFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `reviewer_agent = "reviewer"`, `reviewer_agent = "missing-reviewer"`, 1)
	assertWorkflowIssue(t, toml, "unknown_reviewer_agent", "review_gates[0].reviewer_agent")
}

func TestParseWorkflowTOMLUnknownReviewedStepFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `applies_to = ["task-1"]`, `applies_to = ["missing-step"]`, 1)
	assertWorkflowIssue(t, toml, "unknown_step", "review_gates[0].applies_to")
}

func TestParseWorkflowTOMLUnsafeAbsolutePathFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `likely_files_affected = ["internal/projectworkflow/toml.go"]`, `likely_files_affected = ["/home/mac/secret.txt"]`, 1)
	assertWorkflowIssue(t, toml, "unsafe_path", "steps[0].likely_files_affected[0]")
}

func TestParseWorkflowTOMLAbsoluteRootInTextFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `purpose = "Coordinate metadata-only workflow execution."`, `purpose = "Read /home/mac/secret.txt for context."`, 1)
	assertWorkflowIssue(t, toml, "unsafe_text", "purpose")
}

func TestParseWorkflowTOMLSecretLookingValueFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `purpose = "Coordinate metadata-only workflow execution."`, `purpose = "token=secret"`, 1)
	assertWorkflowIssue(t, toml, "unsafe_text", "purpose")
}

func TestParseWorkflowTOMLEmailShapedRefFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `id = "worker"`, `id = "person@example.com"`, 1)
	assertWorkflowIssue(t, toml, "unsafe_ref", "agents[0].id")
}

func TestParseWorkflowTOMLUnknownStepKindFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `kind = "work_task"`, `kind = "shell_script"`, 1)
	assertWorkflowIssue(t, toml, "unknown_step_kind", "steps[0].kind")
}

func TestParseWorkflowTOMLDependencyCycleFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `depends_on = []`, `depends_on = ["task-2"]`, 1)
	assertWorkflowIssue(t, toml, "dependency_cycle", "steps")
}

func TestParseWorkflowTOMLZeroMaxParallelTasksFails(t *testing.T) {
	toml := strings.Replace(validWorkflowTOML(), `max_parallel_tasks = 1`, `max_parallel_tasks = 0`, 1)
	assertWorkflowIssue(t, toml, "invalid_value", "steps[1].max_parallel_tasks")
}

func assertWorkflowIssue(t *testing.T, toml string, code string, field string) {
	t.Helper()
	_, issues, err := ParseWorkflowTOML([]byte(toml))
	if err != nil {
		t.Fatalf("ParseWorkflowTOML returned error: %v", err)
	}
	for _, issue := range issues {
		if issue.Code == code && issue.FieldPath == field {
			return
		}
	}
	t.Fatalf("expected issue %s at %s, got %#v", code, field, issues)
}

func validWorkflowTOML() string {
	return `
id = "workflow-1"
project_id = "mivialabs-agents-monorepo"
workflow_ref = "workflow-parser-validator"
title = "Workflow Parser Validator"
purpose = "Coordinate metadata-only workflow execution."
status = "draft"

[[agents]]
id = "worker"
display_name = "Worker"
purpose = "Implement scoped metadata workflow tasks."
allowed_skills = ["mivia-mcp"]
allowed_tools = ["projects.workspace.file_read"]
workspace_mode = "edit"
network_policy = "disabled"

[[agents]]
id = "reviewer"
display_name = "Reviewer"
purpose = "Review scoped metadata workflow tasks."
secret_policy = "deny"
log_policy = "metadata_only"

[[steps]]
id = "task-1"
kind = "work_task"
title = "Implement parser"
agent = "worker"
depends_on = []
description = "Add metadata-only parser."
evidence_needed = ["source-anchors-read"]
context_pack_refs = ["context:workflow"]
likely_files_affected = ["internal/projectworkflow/toml.go"]
verification_requirement = "go test ./internal/projectworkflow/..."
expected_output = "Parser validates metadata."
failure_criteria = "Unsafe metadata is accepted."
resume_instructions = "Run focused workflow tests."

[[steps]]
id = "task-2"
kind = "automation"
title = "Run focused verifier"
agent = "worker"
depends_on = ["task-1"]
max_parallel_tasks = 1

[[review_gates]]
id = "gate-1"
applies_to = ["task-1"]
reviewer_agent = "reviewer"
required = true
required_artifacts = ["source-anchors-read"]
allowed_actions = ["approved", "rejected", "needs_changes", "blocked"]
instructions = "Review the implementation evidence."
`
}
