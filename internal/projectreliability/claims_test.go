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

func TestClaimChecker_HandlesConcreteRoutesAndIgnoresFilenames(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "README.md",
			Text: "See docs/configuration/local-projects.md and GET /api/v1/projects/go-mivia/context-pack?max_items=2 plus /api/v1/projects/<project_id>/context-health/.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	assertNoClaim(t, result, "projects.md")
	assertClaimStatus(t, result, "/api/v1/projects/go-mivia/context-pack?max_items=2", "verified")
	assertClaimStatus(t, result, "/api/v1/projects/<project_id>/context-health/", "verified")
}

func TestClaimChecker_VerifiesStaticProjectSubroutes(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "README.md",
			Text: "Use POST /api/v1/projects/{id}/claims/check, GET /api/v1/projects/{id}/workspace/files/read, POST /api/v1/projects/{id}/workspace/files/edit, POST /api/v1/projects/{id}/workspace/files/create, and POST /api/v1/projects/{id}/workspace/files/delete.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	for _, claim := range []string{
		"/api/v1/projects/{id}/claims/check",
		"/api/v1/projects/{id}/workspace/files/read",
		"/api/v1/projects/{id}/workspace/files/edit",
		"/api/v1/projects/{id}/workspace/files/create",
		"/api/v1/projects/{id}/workspace/files/delete",
	} {
		assertClaimStatus(t, result, claim, "verified")
	}
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

func TestClaimChecker_VerifiesEvidenceGraphToolsRoutesAndAliases(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "api/mcp/agent-control.v1.md",
			Text: "Use projects.evidence_graph.claims.create, projects_evidence_graph_claims_create, projects.evidence_graph.promotions.link, and POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/promotion-links.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	for _, claim := range []string{
		"projects.evidence_graph.claims.create",
		"projects_evidence_graph_claims_create",
		"projects.evidence_graph.promotions.link",
		"/api/v1/projects/{id}/evidence-graph/claims/{claim_id}/promotion-links",
	} {
		assertClaimStatus(t, result, claim, "verified")
	}
}

func TestClaimChecker_VerifiesKnowledgeToolsRoutesAndAliases(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "api/mcp/agent-control.v1.md",
			Text: "Use projects.knowledge.candidates.create, projects_knowledge_candidates_create, projects.knowledge.promote_org, orgs.knowledge.list, POST /api/v1/projects/{id}/knowledge/{knowledge_id}/promote-org, GET /api/v1/orgs/{org_ref}/knowledge?state=org_promoted&claim_id=&knowledge_ref=&confidence_band=&min_confidence=&max_confidence=&page_size=&page_token=, and GET /api/v1/projects/{id}/knowledge/{knowledge_id}.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	for _, claim := range []string{
		"projects.knowledge.candidates.create",
		"projects_knowledge_candidates_create",
		"projects.knowledge.promote_org",
		"orgs.knowledge.list",
		"/api/v1/projects/{id}/knowledge/{knowledge_id}/promote-org",
		"/api/v1/orgs/{org_ref}/knowledge?state=org_promoted&claim_id=&knowledge_ref=&confidence_band=&min_confidence=&max_confidence=&page_size=&page_token=",
		"/api/v1/projects/{id}/knowledge/{knowledge_id}",
	} {
		assertClaimStatus(t, result, claim, "verified")
	}
}

func TestClaimChecker_VerifiesWorkPlanToolsRoutesAndAliases(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "api/mcp/agent-control.v1.md",
			Text: "Use projects.work_plans.create, projects_work_plans_resume, projects.work_tasks.get_next, projects_work_tasks_attach_verifier_result, POST /api/v1/projects/{id}/work-plans/{plan_id}/resume, POST /api/v1/projects/example-service/work-tasks/task-123/claim, GET /api/v1/projects/example-service/work-tasks/next, and POST /api/v1/projects/{id}/work-tasks/{task_id}/knowledge-candidates.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	for _, claim := range []string{
		"projects.work_plans.create",
		"projects_work_plans_resume",
		"projects.work_tasks.get_next",
		"projects_work_tasks_attach_verifier_result",
		"/api/v1/projects/{id}/work-plans/{plan_id}/resume",
		"/api/v1/projects/example-service/work-tasks/task-123/claim",
		"/api/v1/projects/example-service/work-tasks/next",
		"/api/v1/projects/{id}/work-tasks/{task_id}/knowledge-candidates",
	} {
		assertClaimStatus(t, result, claim, "verified")
	}
}

func TestClaimChecker_VerifiesAutomationToolsRoutesAndAliases(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID:       "example-service",
		IncludeVerified: true,
		Documents: []ClaimDocument{{
			Path: "api/mcp/agent-control.v1.md",
			Text: "Use projects.automations.create, projects_automations_run_parallel_batch, projects.automation_runs.list, projects.automation_runs.claim_next, projects_automation_runs_complete_attempt, GET /api/v1/projects/{id}/automations/{automation_id}, POST /api/v1/projects/{id}/automations/{automation_id}/runs, POST /api/v1/projects/{id}/automations/{automation_id}/parallel-batches, GET /api/v1/projects/{id}/automation-runs, POST /api/v1/projects/{id}/automation-runs/claim-next, GET /api/v1/projects/{id}/automation-runs/{run_id}, and POST /api/v1/projects/{id}/automation-runs/{run_id}/attempt-result.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	for _, claim := range []string{
		"projects.automations.create",
		"projects_automations_run_parallel_batch",
		"projects.automation_runs.list",
		"projects.automation_runs.claim_next",
		"projects_automation_runs_complete_attempt",
		"/api/v1/projects/{id}/automations/{automation_id}",
		"/api/v1/projects/{id}/automations/{automation_id}/runs",
		"/api/v1/projects/{id}/automations/{automation_id}/parallel-batches",
		"/api/v1/projects/{id}/automation-runs",
		"/api/v1/projects/{id}/automation-runs/claim-next",
		"/api/v1/projects/{id}/automation-runs/{run_id}",
		"/api/v1/projects/{id}/automation-runs/{run_id}/attempt-result",
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

func TestClaimChecker_UsesKnownToolAndRouteOverrides(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		IncludeVerified: true,
		KnownTools:      []string{"projects.custom.tool"},
		KnownRoutes:     []string{"/api/v1/projects/*/custom-route"},
		Documents: []ClaimDocument{{
			Path: "README.md",
			Text: "Use projects.custom.tool and GET /api/v1/projects/example-service/custom-route.",
		}},
	})
	if err != nil {
		t.Fatalf("check claims: %v", err)
	}
	assertClaimStatus(t, result, "projects.custom.tool", "verified")
	assertClaimStatus(t, result, "/api/v1/projects/example-service/custom-route", "verified")
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
