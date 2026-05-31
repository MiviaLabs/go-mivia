package projectreliability

import (
	"context"
	"testing"
)

func TestClaimChecker_VerifiesKnownToolAndRoute(t *testing.T) {
	result, err := NewClaimChecker(nil).Check(context.Background(), ClaimCheckRequest{
		ProjectID: "example-service",
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
