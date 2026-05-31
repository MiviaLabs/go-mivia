package projectreliability

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

func TestImpactAnalyzer_MapsKnownPaths(t *testing.T) {
	result, err := NewImpactAnalyzer(nil).Analyze(context.Background(), ImpactAnalysisRequest{
		ProjectID: "example-service",
		ChangedPaths: []string{
			"internal/projectregistry/mcpapi/mcpapi.go",
			"internal/agentcontrol/httpapi/httpapi.go",
			"docs/security/privacy-baseline.md",
		},
	})
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	assertDomain(t, result, "mcp_project_tools")
	assertDomain(t, result, "agent_control")
	assertDomain(t, result, "security_privacy_docs")
	assertContains(t, result.AffectedTools, "projects.*")
	assertContains(t, result.AffectedRoutes, "/api/v1/agent-runs")
	assertContains(t, result.SecurityFlags, "security_privacy_policy")
}

func TestImpactAnalyzer_UsesWorkspaceDiffMetadataWithoutRawDiff(t *testing.T) {
	workspace := fakeWorkspace{diff: projectworkspace.GitDiff{
		ProjectID: "example-service",
		Scope:     projectworkspace.DiffScopeWorkingTree,
		Files: []projectworkspace.DiffFile{{
			RelativePath: "internal/projectworkspace/service.go",
			Status:       "modified",
			Diff:         "package main\nsecret=raw",
		}},
		DiffTruncated: true,
	}}
	result, err := NewImpactAnalyzer(workspace).Analyze(context.Background(), ImpactAnalysisRequest{ProjectID: "example-service"})
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	if !result.WorkspaceDiffUsed || !result.WorkspaceTruncated {
		t.Fatalf("expected workspace metadata, got %#v", result)
	}
	assertDomain(t, result, "workspace")
	for _, anchor := range result.SourceAnchors {
		if anchor.Path == "package main\nsecret=raw" {
			t.Fatalf("raw diff leaked into anchors: %#v", result.SourceAnchors)
		}
	}
}

func TestImpactAnalyzer_WorkspaceUnavailableFallsBack(t *testing.T) {
	result, err := NewImpactAnalyzer(fakeWorkspace{err: projectworkspace.ErrWorkspaceDisabled}).Analyze(context.Background(), ImpactAnalysisRequest{ProjectID: "example-service"})
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	assertContains(t, result.ResidualUnknowns, "workspace_diff_unavailable")
	assertContains(t, result.ResidualUnknowns, "no_changed_paths")
}

type fakeWorkspace struct {
	diff projectworkspace.GitDiff
	err  error
}

func (workspace fakeWorkspace) GitStatus(context.Context, string, projectworkspace.GitStatusOptions) (projectworkspace.GitStatus, error) {
	return projectworkspace.GitStatus{}, nil
}

func (workspace fakeWorkspace) GitDiff(context.Context, string, projectworkspace.GitDiffOptions) (projectworkspace.GitDiff, error) {
	return workspace.diff, workspace.err
}

func (workspace fakeWorkspace) ReadFile(context.Context, string, projectworkspace.ReadFileOptions) (projectworkspace.WorkspaceFile, error) {
	return projectworkspace.WorkspaceFile{}, nil
}

func (workspace fakeWorkspace) EditFile(context.Context, string, projectworkspace.EditFileOptions) (projectworkspace.EditResult, error) {
	return projectworkspace.EditResult{}, nil
}

func assertDomain(t *testing.T, result ImpactAnalysis, name string) {
	t.Helper()
	for _, domain := range result.AffectedDomains {
		if domain.Name == name {
			return
		}
	}
	t.Fatalf("expected domain %q in %#v", name, result.AffectedDomains)
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("expected %q in %#v", want, values)
}
