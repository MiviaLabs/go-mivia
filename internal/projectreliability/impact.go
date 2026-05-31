package projectreliability

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

type ImpactAnalysisRequest struct {
	ProjectID    string   `json:"project_id,omitempty"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
	DiffScope    string   `json:"diff_scope,omitempty"`
	MaxDiffBytes int      `json:"max_diff_bytes,omitempty"`
}

type ImpactAnalysis struct {
	ProjectID          string         `json:"project_id"`
	DiffScope          string         `json:"diff_scope,omitempty"`
	ChangedPaths       []string       `json:"changed_paths,omitempty"`
	AffectedDomains    []ImpactDomain `json:"affected_domains,omitempty"`
	AffectedRoutes     []string       `json:"affected_routes,omitempty"`
	AffectedTools      []string       `json:"affected_tools,omitempty"`
	SecurityFlags      []string       `json:"security_flags,omitempty"`
	SourceAnchors      []SourceAnchor `json:"source_anchors,omitempty"`
	ResidualUnknowns   []string       `json:"residual_unknowns,omitempty"`
	Partial            bool           `json:"partial,omitempty"`
	PartialReason      string         `json:"partial_reason,omitempty"`
	WorkspaceDiffUsed  bool           `json:"workspace_diff_used"`
	WorkspaceTruncated bool           `json:"workspace_truncated,omitempty"`
}

type ImpactDomain struct {
	Name       string   `json:"name"`
	Reason     string   `json:"reason"`
	Confidence string   `json:"confidence"`
	Paths      []string `json:"paths,omitempty"`
}

type SourceAnchor struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
}

type ImpactAnalyzer struct {
	workspace projectworkspace.API
	ingestion impactGraphAPI
}

func NewImpactAnalyzer(workspace projectworkspace.API) *ImpactAnalyzer {
	return &ImpactAnalyzer{workspace: workspace}
}

func NewImpactAnalyzerWithGraph(workspace projectworkspace.API, ingestion impactGraphAPI) *ImpactAnalyzer {
	return &ImpactAnalyzer{workspace: workspace, ingestion: ingestion}
}

type impactGraphAPI interface {
	ListFiles(context.Context, string, projectingestion.FileStateFilter, projectingestion.Pagination) (projectingestion.FileList, error)
	GetFile(context.Context, string, string) (projectingestion.FileMetadata, error)
	ListSymbols(context.Context, string, projectingestion.SymbolFilter, projectingestion.Pagination) (projectingestion.SymbolList, error)
	SearchSymbols(context.Context, string, projectingestion.SymbolFilter, projectingestion.Pagination) (projectingestion.SymbolList, error)
	SearchReferences(context.Context, string, projectingestion.ReferenceSearchOptions) (projectingestion.SymbolReferenceList, error)
	ListSymbolReferences(context.Context, string, string, projectingestion.Pagination) (projectingestion.SymbolReferenceList, error)
	ListSymbolCallers(context.Context, string, string, projectingestion.Pagination) (projectingestion.SymbolCallEdgeList, error)
	ListSymbolImplementers(context.Context, string, string, projectingestion.Pagination) (projectingestion.SymbolImplementationList, error)
	LatestRunMetadata(context.Context, string) (projectingestion.RunMetadata, error)
}

func (analyzer *ImpactAnalyzer) Analyze(ctx context.Context, request ImpactAnalysisRequest) (ImpactAnalysis, error) {
	projectID := strings.TrimSpace(request.ProjectID)
	paths := cleanPathList(request.ChangedPaths)
	scope := strings.TrimSpace(request.DiffScope)
	if scope == "" {
		scope = projectworkspace.DiffScopeWorkingTree
	}
	result := ImpactAnalysis{ProjectID: projectID, DiffScope: scope}
	if analyzer.workspace != nil && (len(paths) == 0 || strings.TrimSpace(request.DiffScope) != "") {
		diff, err := analyzer.workspace.GitDiff(ctx, projectID, projectworkspace.GitDiffOptions{
			Scope:        scope,
			MaxDiffBytes: effectiveImpactDiffBytes(request.MaxDiffBytes),
		})
		if err == nil {
			result.WorkspaceDiffUsed = true
			result.WorkspaceTruncated = diff.DiffTruncated
			for _, file := range diff.Files {
				paths = append(paths, file.RelativePath)
			}
			for _, skipped := range diff.Skipped {
				if skipped.Reason != "" {
					result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "workspace_diff_skipped_"+safeCategory(skipped.Reason, "unknown"))
				}
			}
		} else {
			result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "workspace_diff_unavailable")
		}
	}
	if len(paths) == 0 {
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "no_changed_paths")
	}
	result.ChangedPaths = paths
	if analyzer.ingestion != nil {
		result = analyzer.addGraphImpact(ctx, result, projectID, paths)
	}
	for _, path := range paths {
		result = addPathImpact(result, path, len(result.SourceAnchors) == 0)
	}
	sort.Slice(result.SourceAnchors, func(i, j int) bool {
		if result.SourceAnchors[i].Path == result.SourceAnchors[j].Path {
			return result.SourceAnchors[i].Kind < result.SourceAnchors[j].Kind
		}
		return result.SourceAnchors[i].Path < result.SourceAnchors[j].Path
	})
	return result, nil
}

func (analyzer *ImpactAnalyzer) addGraphImpact(ctx context.Context, result ImpactAnalysis, projectID string, paths []string) ImpactAnalysis {
	if symbols, err := analyzer.ingestion.SearchSymbols(ctx, projectID, projectingestion.SymbolFilter{NameContains: "__mivia_impact_health_probe__"}, projectingestion.Pagination{PageSize: 1}); err == nil && symbols.Index != nil && symbols.Index.Degraded {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, symbols.Index.DegradedReason, "index_degraded")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "index_degraded_"+safeCategory(symbols.Index.DegradedReason, "unknown"))
	} else if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "index_health_unknown")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "index_health_unknown")
	}
	if run, err := analyzer.ingestion.LatestRunMetadata(ctx, projectID); err == nil && run.Status != "completed" {
		result.Partial = true
		result.PartialReason = "index_not_completed"
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "index_not_completed")
	} else if err != nil && !errors.Is(err, projectingestion.ErrRunNotFound) {
		result.Partial = true
		result.PartialReason = "index_health_unknown"
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "index_health_unknown")
	}
	if len(paths) == 0 {
		return result
	}
	for _, changedPath := range paths {
		files, err := analyzer.ingestion.ListFiles(ctx, projectID, projectingestion.FileStateFilter{
			Status:     projectingestion.FileStatusEligible,
			PathPrefix: changedPath,
		}, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize})
		if err != nil {
			result.Partial = true
			result.PartialReason = firstNonEmpty(result.PartialReason, "graph_file_lookup_failed")
			result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_file_lookup_failed")
			continue
		}
		found := false
		for _, file := range files.Files {
			if file.RelativePath != changedPath {
				continue
			}
			found = true
			result.SourceAnchors = appendAnchor(result.SourceAnchors, file.RelativePath, "changed_file")
			result = analyzer.addFileSymbolImpact(ctx, result, projectID, file)
		}
		if !found {
			result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "changed_path_not_indexed")
		}
	}
	return result
}

func (analyzer *ImpactAnalyzer) addFileSymbolImpact(ctx context.Context, result ImpactAnalysis, projectID string, file projectingestion.FileMetadata) ImpactAnalysis {
	symbols, err := analyzer.ingestion.ListSymbols(ctx, projectID, projectingestion.SymbolFilter{FileID: file.ID}, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize})
	if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "graph_symbol_lookup_failed")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_symbol_lookup_failed")
		return result
	}
	if len(symbols.Symbols) == 0 {
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "changed_file_defines_no_symbols")
		return result
	}
	for _, symbol := range symbols.Symbols {
		result.SourceAnchors = appendAnchor(result.SourceAnchors, file.RelativePath, "defines_symbol:"+symbol.Name)
		result = analyzer.addSymbolReferences(ctx, result, projectID, symbol)
		result = analyzer.addSymbolCallers(ctx, result, projectID, symbol)
		if symbol.Kind == string(projectingestion.SymbolKindType) || symbol.Kind == string(projectingestion.SymbolKindClass) {
			result = analyzer.addSymbolImplementers(ctx, result, projectID, symbol)
			result = analyzer.addNameReferences(ctx, result, projectID, symbol)
		}
	}
	return result
}

func (analyzer *ImpactAnalyzer) addSymbolImplementers(ctx context.Context, result ImpactAnalysis, projectID string, symbol projectingestion.SymbolMetadata) ImpactAnalysis {
	implementers, err := analyzer.ingestion.ListSymbolImplementers(ctx, projectID, symbol.ID, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize})
	if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "graph_implementer_lookup_failed")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_implementer_lookup_failed")
		return result
	}
	for _, implementation := range implementers.Implementations {
		result = analyzer.addAffectedFile(ctx, result, projectID, implementation.FileID, "graph_implementer")
	}
	return result
}

func (analyzer *ImpactAnalyzer) addSymbolReferences(ctx context.Context, result ImpactAnalysis, projectID string, symbol projectingestion.SymbolMetadata) ImpactAnalysis {
	refs, err := analyzer.ingestion.ListSymbolReferences(ctx, projectID, symbol.ID, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize})
	if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "graph_reference_lookup_failed")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_reference_lookup_failed")
		return result
	}
	for _, ref := range refs.References {
		result = analyzer.addAffectedFile(ctx, result, projectID, ref.FileID, "graph_reference")
	}
	return result
}

func (analyzer *ImpactAnalyzer) addNameReferences(ctx context.Context, result ImpactAnalysis, projectID string, symbol projectingestion.SymbolMetadata) ImpactAnalysis {
	refs, err := analyzer.ingestion.SearchReferences(ctx, projectID, projectingestion.ReferenceSearchOptions{TargetNameContains: symbol.Name, PageSize: projectingestion.MaxPageSize})
	if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "graph_name_reference_lookup_failed")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_name_reference_lookup_failed")
		return result
	}
	for _, ref := range refs.References {
		result = analyzer.addAffectedFile(ctx, result, projectID, ref.FileID, "graph_name_reference")
	}
	return result
}

func (analyzer *ImpactAnalyzer) addSymbolCallers(ctx context.Context, result ImpactAnalysis, projectID string, symbol projectingestion.SymbolMetadata) ImpactAnalysis {
	callers, err := analyzer.ingestion.ListSymbolCallers(ctx, projectID, symbol.ID, projectingestion.Pagination{PageSize: projectingestion.MaxPageSize})
	if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "graph_caller_lookup_failed")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_caller_lookup_failed")
		return result
	}
	for _, edge := range callers.Edges {
		result = analyzer.addAffectedFile(ctx, result, projectID, edge.FileID, "graph_caller")
	}
	return result
}

func (analyzer *ImpactAnalyzer) addAffectedFile(ctx context.Context, result ImpactAnalysis, projectID string, fileID string, kind string) ImpactAnalysis {
	if strings.TrimSpace(fileID) == "" {
		return result
	}
	file, err := analyzer.ingestion.GetFile(ctx, projectID, fileID)
	if err != nil {
		result.Partial = true
		result.PartialReason = firstNonEmpty(result.PartialReason, "graph_affected_file_lookup_failed")
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_affected_file_lookup_failed")
		return result
	}
	if file.RelativePath != "" {
		result.SourceAnchors = appendAnchor(result.SourceAnchors, file.RelativePath, kind)
		result = addDomainWithConfidence(result, "graph_affected_files", "affected by symbol graph edges", "graph_edge", file.RelativePath)
		return result
	}
	result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "graph_affected_file_unresolved")
	return result
}

func effectiveImpactDiffBytes(value int) int {
	if value <= 0 {
		return projectworkspace.DefaultMaxDiffBytes
	}
	if value > projectworkspace.MaxDiffBytes {
		return projectworkspace.MaxDiffBytes
	}
	return value
}

func cleanPathList(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
		if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			continue
		}
		out = append(out, path)
	}
	return out
}

func addPathImpact(result ImpactAnalysis, path string, addUnknown bool) ImpactAnalysis {
	switch {
	case strings.HasPrefix(path, "internal/projectingestion/"):
		result = addDomain(result, "ingestion_search_index", "content graph ingestion, search, or index behavior", path)
		result.AffectedTools = appendUnique(result.AffectedTools, "projects.ingest", "projects.files.list", "projects.search.*")
	case strings.HasPrefix(path, "internal/projectworkspace/"):
		result = addDomain(result, "workspace", "workspace git/read/edit behavior", path)
		result.AffectedTools = appendUnique(result.AffectedTools, "projects.workspace.git_status", "projects.workspace.git_diff", "projects.workspace.file_read", "projects.workspace.file_edit")
		result.SecurityFlags = appendUnique(result.SecurityFlags, "token_guarded_edit_boundary")
	case strings.HasPrefix(path, "internal/projectregistry/mcpapi/"):
		result = addDomain(result, "mcp_project_tools", "project MCP tool definitions or routing", path)
		result.AffectedTools = appendUnique(result.AffectedTools, "projects.*")
	case strings.HasPrefix(path, "internal/projectregistry/httpapi/"):
		result = addDomain(result, "rest_project_api", "project REST route behavior", path)
		result.AffectedRoutes = appendUnique(result.AffectedRoutes, "/api/v1/projects/*")
	case strings.HasPrefix(path, "internal/agentcontrol/"):
		result = addDomain(result, "agent_control", "task, research, MCP, or agent-run control plane", path)
		result.AffectedTools = appendUnique(result.AffectedTools, "tasks.*", "research_runs.*", "agent_runs.*")
		result.AffectedRoutes = appendUnique(result.AffectedRoutes, "/api/v1/tasks", "/api/v1/research-runs", "/api/v1/agent-runs")
		if strings.Contains(path, "agent") || strings.Contains(path, "mcpapi") || strings.Contains(path, "httpapi") {
			result.SecurityFlags = appendUnique(result.SecurityFlags, "redacted_metadata_boundary")
		}
	case strings.HasPrefix(path, "internal/research/"):
		result = addDomain(result, "research_metadata", "research metadata and redaction behavior", path)
		result.SecurityFlags = appendUnique(result.SecurityFlags, "provider_payload_and_pii_boundary")
	case strings.HasPrefix(path, "api/openapi/"):
		result = addDomain(result, "rest_contract", "OpenAPI contract", path)
		result.AffectedRoutes = appendUnique(result.AffectedRoutes, "/api/v1/*")
	case strings.HasPrefix(path, "api/mcp/"):
		result = addDomain(result, "mcp_contract", "MCP contract", path)
		result.AffectedTools = appendUnique(result.AffectedTools, "tools/list", "tools/call")
	case strings.HasPrefix(path, "configs/") || strings.HasPrefix(path, "internal/platform/config/"):
		result = addDomain(result, "runtime_config", "runtime configuration", path)
		result.SecurityFlags = appendUnique(result.SecurityFlags, "local_configuration_boundary")
	case strings.HasPrefix(path, "docs/security/"):
		result = addDomain(result, "security_privacy_docs", "security or privacy policy", path)
		result.SecurityFlags = appendUnique(result.SecurityFlags, "security_privacy_policy")
	case strings.HasPrefix(path, "docs/") || strings.HasPrefix(path, ".ai/"):
		result = addDomain(result, "documentation", "agent or project documentation", path)
	default:
		if addUnknown {
			result = addDomain(result, "unknown", "no deterministic path mapping", path)
		}
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "unmapped_path")
	}
	result.SourceAnchors = appendAnchor(result.SourceAnchors, path, "changed_path")
	return result
}

func addDomain(result ImpactAnalysis, name string, reason string, path string) ImpactAnalysis {
	for i := range result.AffectedDomains {
		if result.AffectedDomains[i].Name == name {
			result.AffectedDomains[i].Paths = appendUnique(result.AffectedDomains[i].Paths, path)
			return result
		}
	}
	result.AffectedDomains = append(result.AffectedDomains, ImpactDomain{
		Name:       name,
		Reason:     reason,
		Confidence: "path_rule",
		Paths:      []string{path},
	})
	return result
}

func addDomainWithConfidence(result ImpactAnalysis, name string, reason string, confidence string, path string) ImpactAnalysis {
	for i := range result.AffectedDomains {
		if result.AffectedDomains[i].Name == name && result.AffectedDomains[i].Confidence == confidence {
			result.AffectedDomains[i].Paths = appendUnique(result.AffectedDomains[i].Paths, path)
			return result
		}
	}
	result.AffectedDomains = append(result.AffectedDomains, ImpactDomain{
		Name:       name,
		Reason:     reason,
		Confidence: confidence,
		Paths:      []string{path},
	})
	return result
}

func appendAnchor(anchors []SourceAnchor, path string, kind string) []SourceAnchor {
	path = strings.TrimSpace(path)
	kind = strings.TrimSpace(kind)
	if path == "" {
		return anchors
	}
	for _, anchor := range anchors {
		if anchor.Path == path && anchor.Kind == kind {
			return anchors
		}
	}
	return append(anchors, SourceAnchor{Path: path, Kind: kind})
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
