package mcpapi

import (
	"strings"
	"testing"
)

func TestToolDefinitionsExposeCodexAndParallelRequirements(t *testing.T) {
	definitions := ToolDefinitions()
	var runDescription string
	var batchDescription string
	var claimDescription string
	var completeDescription string
	for _, definition := range definitions {
		switch definition["name"] {
		case "projects.automations.run":
			runDescription, _ = definition["description"].(string)
		case "projects.automations.run_parallel_batch":
			batchDescription, _ = definition["description"].(string)
		case "projects.automation_runs.claim_next":
			claimDescription, _ = definition["description"].(string)
		case "projects.automation_runs.complete_attempt":
			completeDescription, _ = definition["description"].(string)
		}
	}
	if !strings.Contains(runDescription, "MUST use codex_cli") {
		t.Fatalf("run description does not enforce codex_cli: %q", runDescription)
	}
	if !strings.Contains(runDescription, "MUST NOT fall back to manual") {
		t.Fatalf("run description does not forbid fallback: %q", runDescription)
	}
	if !strings.Contains(batchDescription, "orchestrator-owned") {
		t.Fatalf("batch description does not require orchestrator ownership: %q", batchDescription)
	}
	if !strings.Contains(batchDescription, "independent ready Work Tasks") {
		t.Fatalf("batch description does not require independent ready tasks: %q", batchDescription)
	}
	if !strings.Contains(claimDescription, "logged-in Codex environment") {
		t.Fatalf("claim description does not require local logged-in runner: %q", claimDescription)
	}
	if !strings.Contains(completeDescription, "does not complete the Work Task") {
		t.Fatalf("complete description does not preserve orchestrator verification: %q", completeDescription)
	}
}

func TestIsAutomationToolAcceptsUnderscoreAlias(t *testing.T) {
	if !IsAutomationTool("projects_automations_run_parallel_batch") {
		t.Fatal("expected underscore alias to be accepted")
	}
	if !IsAutomationTool("projects_automation_runs_claim_next") {
		t.Fatal("expected external runner alias to be accepted")
	}
}
