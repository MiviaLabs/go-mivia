package projectreliability

import (
	"context"
	"strings"

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
}

func NewImpactAnalyzer(workspace projectworkspace.API) *ImpactAnalyzer {
	return &ImpactAnalyzer{workspace: workspace}
}

func (analyzer *ImpactAnalyzer) Analyze(ctx context.Context, request ImpactAnalysisRequest) (ImpactAnalysis, error) {
	projectID := strings.TrimSpace(request.ProjectID)
	paths := cleanPathList(request.ChangedPaths)
	scope := strings.TrimSpace(request.DiffScope)
	if scope == "" {
		scope = projectworkspace.DiffScopeWorkingTree
	}
	result := ImpactAnalysis{ProjectID: projectID, DiffScope: scope}
	if len(paths) == 0 && analyzer.workspace != nil {
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
	for _, path := range paths {
		result = addPathImpact(result, path)
	}
	return result, nil
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

func addPathImpact(result ImpactAnalysis, path string) ImpactAnalysis {
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
		result = addDomain(result, "unknown", "no deterministic path mapping", path)
		result.ResidualUnknowns = appendUnique(result.ResidualUnknowns, "unmapped_path")
	}
	result.SourceAnchors = append(result.SourceAnchors, SourceAnchor{Path: path, Kind: "changed_path"})
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
