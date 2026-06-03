package projectreliability

import (
	"context"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

func TestImpactAnalyzer_MapsKnownPaths(t *testing.T) {
	result, err := NewImpactAnalyzer(nil).Analyze(context.Background(), ImpactAnalysisRequest{
		ProjectID: "example-service",
		ChangedPaths: []string{
			"internal/projectreliability/impact.go",
			"internal/projectevidence/service.go",
			"internal/projectconfidence/input.go",
			"internal/projectcontext/service.go",
			"internal/projectregistry/mcpapi/mcpapi.go",
			"internal/agentcontrol/httpapi/httpapi.go",
			"docs/security/privacy-baseline.md",
		},
	})
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	assertDomain(t, result, "project_reliability")
	assertDomain(t, result, "evidence_graph")
	assertDomain(t, result, "confidence_engine")
	assertDomain(t, result, "context_pack")
	assertDomain(t, result, "mcp_project_tools")
	assertDomain(t, result, "agent_control")
	assertDomain(t, result, "security_privacy_docs")
	assertContains(t, result.AffectedTools, "projects.impact.analyze")
	assertContains(t, result.AffectedTools, "projects.evidence_graph.*")
	assertContains(t, result.AffectedTools, "projects.confidence.*")
	assertContains(t, result.AffectedTools, "projects.context_pack.build")
	assertContains(t, result.AffectedTools, "projects.*")
	assertContains(t, result.AffectedRoutes, "/api/v1/projects/*/evidence-graph/*")
	assertContains(t, result.AffectedRoutes, "/api/v1/projects/*/confidence/*")
	assertContains(t, result.AffectedRoutes, "/api/v1/agent-runs")
	assertContains(t, result.SecurityFlags, "redacted_metadata_boundary")
	assertContains(t, result.SecurityFlags, "confidence_metadata_boundary")
	assertContains(t, result.SecurityFlags, "security_privacy_policy")
	assertNotContains(t, result.ResidualUnknowns, "unmapped_path")
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

func TestImpactAnalyzer_UsesGraphEdgesForExternalProject(t *testing.T) {
	graph := &fakeImpactGraph{
		files: map[string]projectingestion.FileMetadata{
			"interface":   {ID: "interface", ProjectID: "mass-monorepo", RelativePath: "apps/domain-loyalty/src/domain/repositories/point-wallet.repository.interface.ts", Status: "eligible", Present: true, RelativePathOK: true},
			"implementer": {ID: "implementer", ProjectID: "mass-monorepo", RelativePath: "apps/domain-loyalty/src/infrastructure/database/repositories/drizzle-point-wallet.repository.ts", Status: "eligible", Present: true, RelativePathOK: true},
			"module":      {ID: "module", ProjectID: "mass-monorepo", RelativePath: "apps/domain-loyalty/src/point-wallet.module.ts", Status: "eligible", Present: true, RelativePathOK: true},
		},
		symbolsByFile: map[string][]projectingestion.SymbolMetadata{
			"interface": {{ID: "sym-interface", FileID: "interface", ProjectID: "mass-monorepo", Kind: string(projectingestion.SymbolKindType), Name: "PointWalletRepository"}},
		},
		refsBySymbol: map[string][]projectingestion.SymbolReferenceMetadata{
			"sym-interface": {{ID: "ref-module", FileID: "module", ProjectID: "mass-monorepo", TargetSymbolID: "sym-interface", TargetName: "PointWalletRepository"}},
		},
		nameRefs: map[string][]projectingestion.SymbolReferenceMetadata{
			"PointWalletRepository": {{ID: "ref-impl", FileID: "implementer", ProjectID: "mass-monorepo", TargetName: "PointWalletRepository", Kind: "implements"}},
		},
		implementersBySym: map[string][]projectingestion.SymbolImplementation{
			"sym-interface": {{ID: "impl-edge", FileID: "implementer", ProjectID: "mass-monorepo", Kind: "implements", ImplementerName: "DrizzlePointWalletRepository", ImplementedName: "PointWalletRepository"}},
		},
	}
	result, err := NewImpactAnalyzerWithGraph(nil, graph).Analyze(context.Background(), ImpactAnalysisRequest{
		ProjectID:    "mass-monorepo",
		ChangedPaths: []string{"apps/domain-loyalty/src/domain/repositories/point-wallet.repository.interface.ts"},
	})
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	assertAnchor(t, result, "apps/domain-loyalty/src/infrastructure/database/repositories/drizzle-point-wallet.repository.ts", "graph_implementer")
	assertAnchor(t, result, "apps/domain-loyalty/src/point-wallet.module.ts", "graph_reference")
	assertDomain(t, result, "graph_affected_files")
	assertNotContains(t, result.ResidualUnknowns, "implementer_edges_not_distinct")
	for _, domain := range result.AffectedDomains {
		if domain.Name == "unknown" {
			t.Fatalf("did not expect bare unknown domain when graph produced anchors: %#v", result.AffectedDomains)
		}
	}
}

func TestImpactAnalyzer_UsesSearchIndexHealthWithoutSearchSymbolProbe(t *testing.T) {
	graph := &fakeImpactGraph{
		files: map[string]projectingestion.FileMetadata{
			"service": {ID: "service", ProjectID: "example-service", RelativePath: "internal/projectreliability/service.go", Status: "eligible", Present: true, RelativePathOK: true},
		},
		symbolsByFile: map[string][]projectingestion.SymbolMetadata{},
		indexHealth:   projectingestion.SearchIndexHealth{Degraded: true, Reason: "search_index_drift"},
	}
	result, err := NewImpactAnalyzerWithGraph(nil, graph).Analyze(context.Background(), ImpactAnalysisRequest{
		ProjectID:    "example-service",
		ChangedPaths: []string{"internal/projectreliability/service.go"},
	})
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	if !result.Partial || result.PartialReason != "search_index_drift" {
		t.Fatalf("expected degraded index health, got %#v", result)
	}
	assertContains(t, result.ResidualUnknowns, "index_degraded_search_index_drift")
	if graph.contextIndexHealthCalls != 1 {
		t.Fatalf("expected lightweight context search index health call, got %d", graph.contextIndexHealthCalls)
	}
	if graph.searchIndexHealthCalls != 0 {
		t.Fatalf("did not expect full search index health call, got %d", graph.searchIndexHealthCalls)
	}
}

func TestImpactAnalyzer_PropagatesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewImpactAnalyzer(fakeWorkspace{}).Analyze(ctx, ImpactAnalysisRequest{ProjectID: "example-service"})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestImpactAnalyzer_ActiveSyncSkipsBlockingGraphLookup(t *testing.T) {
	block := make(chan struct{})
	graph := &fakeImpactGraph{
		files:          map[string]projectingestion.FileMetadata{},
		symbolsByFile:  map[string][]projectingestion.SymbolMetadata{},
		blockListFiles: block,
		activeSync:     true,
	}

	started := time.Now()
	result, err := NewImpactAnalyzerWithGraph(nil, graph).Analyze(context.Background(), ImpactAnalysisRequest{
		ProjectID:    "example-service",
		ChangedPaths: []string{"internal/projectreliability/impact.go"},
	})
	close(block)
	if err != nil {
		t.Fatalf("analyze impact: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("expected active sync fast path, elapsed %s", elapsed)
	}
	if !result.Partial || result.PartialReason != "index_syncing" {
		t.Fatalf("expected index syncing partial, got %#v", result)
	}
	assertContains(t, result.ResidualUnknowns, "index_syncing")
	if graph.listFilesCalls != 0 {
		t.Fatalf("expected graph lookup to be skipped, got %d calls", graph.listFilesCalls)
	}
}

type fakeWorkspace struct {
	diff         projectworkspace.GitDiff
	err          error
	blockGitDiff <-chan struct{}
}

type fakeImpactGraph struct {
	files             map[string]projectingestion.FileMetadata
	symbolsByFile     map[string][]projectingestion.SymbolMetadata
	refsBySymbol      map[string][]projectingestion.SymbolReferenceMetadata
	nameRefs          map[string][]projectingestion.SymbolReferenceMetadata
	callersBySym      map[string][]projectingestion.SymbolCallEdge
	implementersBySym map[string][]projectingestion.SymbolImplementation
	indexHealth       projectingestion.SearchIndexHealth
	blockListFiles    <-chan struct{}
	activeSync        bool

	listFilesCalls          int
	contextIndexHealthCalls int
	searchIndexHealthCalls  int
}

func (graph *fakeImpactGraph) ListFiles(_ context.Context, _ string, filter projectingestion.FileStateFilter, _ projectingestion.Pagination) (projectingestion.FileList, error) {
	graph.listFilesCalls++
	if graph.blockListFiles != nil {
		<-graph.blockListFiles
	}
	out := projectingestion.FileList{}
	for _, file := range graph.files {
		if filter.PathPrefix != "" && file.RelativePath != filter.PathPrefix {
			continue
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

func (graph *fakeImpactGraph) GetFile(_ context.Context, _ string, fileID string) (projectingestion.FileMetadata, error) {
	file, ok := graph.files[fileID]
	if !ok {
		return projectingestion.FileMetadata{}, projectingestion.ErrIngestionNotFound
	}
	return file, nil
}

func (graph *fakeImpactGraph) ListSymbols(_ context.Context, _ string, filter projectingestion.SymbolFilter, _ projectingestion.Pagination) (projectingestion.SymbolList, error) {
	return projectingestion.SymbolList{Symbols: append([]projectingestion.SymbolMetadata(nil), graph.symbolsByFile[filter.FileID]...)}, nil
}

func (graph *fakeImpactGraph) SearchReferences(_ context.Context, _ string, options projectingestion.ReferenceSearchOptions) (projectingestion.SymbolReferenceList, error) {
	return projectingestion.SymbolReferenceList{References: append([]projectingestion.SymbolReferenceMetadata(nil), graph.nameRefs[options.TargetNameContains]...)}, nil
}

func (graph *fakeImpactGraph) ListSymbolReferences(_ context.Context, _ string, symbolID string, _ projectingestion.Pagination) (projectingestion.SymbolReferenceList, error) {
	return projectingestion.SymbolReferenceList{References: append([]projectingestion.SymbolReferenceMetadata(nil), graph.refsBySymbol[symbolID]...)}, nil
}

func (graph *fakeImpactGraph) ListSymbolCallers(_ context.Context, _ string, symbolID string, _ projectingestion.Pagination) (projectingestion.SymbolCallEdgeList, error) {
	return projectingestion.SymbolCallEdgeList{Edges: append([]projectingestion.SymbolCallEdge(nil), graph.callersBySym[symbolID]...)}, nil
}

func (graph *fakeImpactGraph) ListSymbolImplementers(_ context.Context, _ string, symbolID string, _ projectingestion.Pagination) (projectingestion.SymbolImplementationList, error) {
	return projectingestion.SymbolImplementationList{Implementations: append([]projectingestion.SymbolImplementation(nil), graph.implementersBySym[symbolID]...)}, nil
}

func (graph *fakeImpactGraph) LatestRunMetadata(context.Context, string) (projectingestion.RunMetadata, error) {
	return projectingestion.RunMetadata{Status: string(projectingestion.RunStatusCompleted)}, nil
}

func (graph *fakeImpactGraph) SearchIndexHealth(context.Context, string) (projectingestion.SearchIndexHealth, error) {
	graph.searchIndexHealthCalls++
	return graph.indexHealth, nil
}

func (graph *fakeImpactGraph) ContextSearchIndexHealth(context.Context, string) (projectingestion.SearchIndexHealth, error) {
	graph.contextIndexHealthCalls++
	return graph.indexHealth, nil
}

func (graph *fakeImpactGraph) Diagnostics() projectingestion.SchedulerDiagnostics {
	if !graph.activeSync {
		return projectingestion.SchedulerDiagnostics{}
	}
	return projectingestion.SchedulerDiagnostics{
		ActiveProjectTaskCount: map[string]int{"example-service": 1},
	}
}

func (workspace fakeWorkspace) GitStatus(context.Context, string, projectworkspace.GitStatusOptions) (projectworkspace.GitStatus, error) {
	return projectworkspace.GitStatus{}, nil
}

func (workspace fakeWorkspace) GitAvailable(context.Context, string) (bool, error) {
	return true, nil
}

func (workspace fakeWorkspace) GitDiff(context.Context, string, projectworkspace.GitDiffOptions) (projectworkspace.GitDiff, error) {
	if workspace.blockGitDiff != nil {
		<-workspace.blockGitDiff
	}
	return workspace.diff, workspace.err
}

func (workspace fakeWorkspace) GitCreateWorktree(context.Context, string, projectworkspace.GitCreateWorktreeOptions) (projectworkspace.GitCreateWorktreeResult, error) {
	return projectworkspace.GitCreateWorktreeResult{}, nil
}

func (workspace fakeWorkspace) ReadFile(context.Context, string, projectworkspace.ReadFileOptions) (projectworkspace.WorkspaceFile, error) {
	return projectworkspace.WorkspaceFile{}, nil
}

func (workspace fakeWorkspace) EditFile(context.Context, string, projectworkspace.EditFileOptions) (projectworkspace.EditResult, error) {
	return projectworkspace.EditResult{}, nil
}

func (workspace fakeWorkspace) CreateFile(context.Context, string, projectworkspace.CreateFileOptions) (projectworkspace.CreateFileResult, error) {
	return projectworkspace.CreateFileResult{}, nil
}

func (workspace fakeWorkspace) DeleteFile(context.Context, string, projectworkspace.DeleteFileOptions) (projectworkspace.DeleteFileResult, error) {
	return projectworkspace.DeleteFileResult{}, nil
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

func assertNotContains(t *testing.T, values []string, unwanted string) {
	t.Helper()
	for _, value := range values {
		if value == unwanted {
			t.Fatalf("did not expect %q in %#v", unwanted, values)
		}
	}
}

func assertAnchor(t *testing.T, result ImpactAnalysis, path string, kind string) {
	t.Helper()
	for _, anchor := range result.SourceAnchors {
		if anchor.Path == path && anchor.Kind == kind {
			return
		}
	}
	t.Fatalf("expected anchor %s/%s in %#v", path, kind, result.SourceAnchors)
}
