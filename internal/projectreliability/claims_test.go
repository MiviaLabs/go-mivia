package projectreliability

import (
	"context"
	"testing"
)

func TestClaimChecker_VerifiesKnownToolAndRoute(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "docs/agent-context-guide.md",
			Text: "Use projects.context_health and GET /api/v1/projects/{id}/context-health before implementation.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	assertClaimStatus(t, result, "projects.context_health", "verified")
	assertClaimStatus(t, result, "/api/v1/projects/{id}/context-health", "verified")
}

func TestClaimChecker_VerifiesDelegatedMCPToolsAndAliases(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "api/mcp/agent-control.v1.md",
			Text: "Use projects.integrations.status, projects.jira.issue.get, projects.graph_status, agent_runs.promote_artifact, projects_integrations_status, projects_jira_issue_get, projects_graph_status, and agent_runs_promote_artifact.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}

	for _, claim := range []string{
		"projects.integrations.status",
		"projects.jira.issue.get",
		"projects.graph_status",
		"agent_runs.promote_artifact",
		"projects_integrations_status",
		"projects_jira_issue_get",
		"projects_graph_status",
		"agent_runs_promote_artifact",
	} {
		assertClaimStatus(t, result, claim, "verified")
	}
}

func TestClaimChecker_FlagsStaleToolAndTaskLink(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		Documents: []ClaimDocument{{
			Path: "README.md",
			Text: "Old tool projects.verifiers.recommend links .ai/tasks/active/local-plan.md",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	assertClaimStatus(t, result, "projects.verifiers.recommend", "stale")
	assertClaimStatus(t, result, ".ai/tasks/*", "stale")
}

func TestClaimChecker_DefaultOutputOmitsVerifiedClaims(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		Documents: []ClaimDocument{{
			Path: "README.md",
			Text: "Use projects.context_health not projects.verifiers.recommend.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	assertClaimStatus(t, result, "projects.verifiers.recommend", "stale")
	assertNoClaim(t, result, "projects.context_health")
	if result.Summary.Total != 2 || result.Summary.Verified != 1 || result.Summary.Actionable != 1 || result.VerifiedOmitted != 1 || result.AllVerified {
		t.Fatalf("expected concise summary with one omitted verified claim, got %#v", result)
	}
}

func TestClaimChecker_AllVerifiedDefaultOutputReturnsSummaryOnly(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		Documents: []ClaimDocument{{
			Path: "README.md",
			Text: "Use projects.context_health and projects.graph_status.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	if len(result.Claims) != 0 || result.Summary.Total != 2 || result.Summary.Verified != 2 || result.Summary.Actionable != 0 || result.VerifiedOmitted != 2 || !result.AllVerified {
		t.Fatalf("expected all-verified concise summary, got %#v", result)
	}
}

func TestClaimChecker_IgnoresToolFieldsWildcardsAndSkillTaskMentions(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		Documents: []ClaimDocument{
			{
				Path: ".ai/skills/mivia-mcp/SKILL.md",
				Text: "Use projects.graph_status.status and projects.context_health.status as response fields. projects.search.* and agent_runs.* are shorthand families. Keep .ai/tasks/ local-only.",
			},
			{
				Path: "api/mcp/agent-control.v1.md",
				Text: "Output: forbidden `.ai/tasks/` links in stable docs.",
			},
		},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	assertNoClaim(t, result, "projects.graph_status.status")
	assertNoClaim(t, result, "projects.context_health.status")
	assertNoClaim(t, result, "projects.search.*")
	assertNoClaim(t, result, "agent_runs.*")
	assertNoClaim(t, result, ".ai/tasks/*")
}

func TestClaimChecker_RefusesSensitiveOrOutOfScopePath(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		Documents: []ClaimDocument{{Path: ".env", Text: "projects.context_health"}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	if len(result.Claims) != 1 || result.Claims[0].Status != "out_of_scope" {
		t.Fatalf("expected out_of_scope, got %#v", result.Claims)
	}
}

func assertClaimStatus(t *testing.T, result ClaimCheckResult, claim string, status string) {
	t.Helper()
	for _, finding := range result.Claims {
		if finding.Claim == claim && finding.Status == status {
			return
		}
	}
	t.Fatalf("expected claim %q status %q in %#v", claim, status, result.Claims)
}

func assertNoClaim(t *testing.T, result ClaimCheckResult, claim string) {
	t.Helper()
	for _, finding := range result.Claims {
		if finding.Claim == claim {
			t.Fatalf("expected claim %q to be ignored, got %#v", claim, result.Claims)
		}
	}
}
