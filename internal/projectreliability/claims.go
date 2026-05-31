package projectreliability

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

type ClaimCheckRequest struct {
	ProjectID     string          `json:"project_id,omitempty"`
	Documents     []ClaimDocument `json:"documents,omitempty"`
	SelectedPaths []string        `json:"selected_paths,omitempty"`
	KnownTools    []string        `json:"known_tools,omitempty"`
	KnownRoutes   []string        `json:"known_routes,omitempty"`
}

type ClaimDocument struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type ClaimCheckResult struct {
	ProjectID string         `json:"project_id,omitempty"`
	Claims    []ClaimFinding `json:"claims"`
}

type ClaimFinding struct {
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Kind        string `json:"kind"`
	Claim       string `json:"claim"`
	Status      string `json:"status"`
	Evidence    string `json:"evidence,omitempty"`
	SafeMessage string `json:"safe_message"`
}

type ClaimChecker struct {
	workspace projectworkspace.API
}

func NewClaimChecker(workspace projectworkspace.API) *ClaimChecker {
	return &ClaimChecker{workspace: workspace}
}

var toolClaimPattern = regexp.MustCompile(`\b(?:projects|agent_runs|tasks|research_runs)\.[a-zA-Z0-9_.*]+`)
var routeClaimPattern = regexp.MustCompile(`/api/v1/[a-zA-Z0-9_./{}*-]+`)

func (checker *ClaimChecker) Check(ctx context.Context, request ClaimCheckRequest) (ClaimCheckResult, error) {
	docs := append([]ClaimDocument(nil), request.Documents...)
	for _, path := range cleanPathList(request.SelectedPaths) {
		if !claimPathAllowed(path) {
			docs = append(docs, ClaimDocument{Path: path, Text: ""})
			continue
		}
		if checker.workspace == nil {
			docs = append(docs, ClaimDocument{Path: path, Text: ""})
			continue
		}
		file, err := checker.workspace.ReadFile(ctx, strings.TrimSpace(request.ProjectID), projectworkspace.ReadFileOptions{RelativePath: path, MaxBytes: projectworkspace.DefaultMaxReadBytes})
		if err != nil {
			docs = append(docs, ClaimDocument{Path: path, Text: ""})
			continue
		}
		docs = append(docs, ClaimDocument{Path: path, Text: file.Text})
	}

	knownTools := setFrom(defaultKnownTools())
	for _, tool := range request.KnownTools {
		knownTools[strings.TrimSpace(tool)] = struct{}{}
	}
	knownRoutes := setFrom(defaultKnownRoutes())
	for _, route := range request.KnownRoutes {
		knownRoutes[strings.TrimSpace(route)] = struct{}{}
	}

	result := ClaimCheckResult{ProjectID: strings.TrimSpace(request.ProjectID)}
	for _, doc := range docs {
		path := strings.TrimSpace(strings.ReplaceAll(doc.Path, "\\", "/"))
		if !claimPathAllowed(path) {
			result.Claims = append(result.Claims, ClaimFinding{
				Path:        path,
				Line:        0,
				Kind:        "path",
				Status:      "out_of_scope",
				SafeMessage: "claim checker accepts selected stable docs, API docs, README, and MCP skill files only",
			})
			continue
		}
		if strings.TrimSpace(doc.Text) == "" {
			result.Claims = append(result.Claims, ClaimFinding{
				Path:        path,
				Line:        0,
				Kind:        "document",
				Status:      "unverified",
				SafeMessage: "document text unavailable for deterministic claim check",
			})
			continue
		}
		result.Claims = append(result.Claims, checkDocumentClaims(path, doc.Text, knownTools, knownRoutes)...)
	}
	sort.SliceStable(result.Claims, func(i, j int) bool {
		if result.Claims[i].Path != result.Claims[j].Path {
			return result.Claims[i].Path < result.Claims[j].Path
		}
		return result.Claims[i].Line < result.Claims[j].Line
	})
	return result, nil
}

func checkDocumentClaims(path string, text string, knownTools map[string]struct{}, knownRoutes map[string]struct{}) []ClaimFinding {
	findings := []ClaimFinding{}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lineNo := index + 1
		if strings.Contains(line, ".ai/tasks/") {
			findings = append(findings, ClaimFinding{
				Path:        path,
				Line:        lineNo,
				Kind:        "stable_doc_link",
				Claim:       ".ai/tasks/*",
				Status:      "stale",
				SafeMessage: "stable docs must not link ignored .ai/tasks files",
			})
		}
		for _, tool := range toolClaimPattern.FindAllString(line, -1) {
			tool = normalizeToolClaim(tool)
			status := "verified"
			evidence := "known MCP tool"
			if strings.Contains(tool, "*") {
				status = "unverified"
				evidence = "wildcard tool claim"
			} else if _, ok := knownTools[tool]; !ok {
				status = "stale"
				evidence = "tool not registered"
			}
			findings = append(findings, ClaimFinding{
				Path:        path,
				Line:        lineNo,
				Kind:        "mcp_tool",
				Claim:       tool,
				Status:      status,
				Evidence:    evidence,
				SafeMessage: "MCP tool claim checked against registered tool names",
			})
		}
		for _, route := range routeClaimPattern.FindAllString(line, -1) {
			normalized := normalizeRouteClaim(route)
			status := "verified"
			evidence := "known REST route"
			if _, ok := knownRoutes[normalized]; !ok {
				status = "stale"
				evidence = "route not registered"
			}
			findings = append(findings, ClaimFinding{
				Path:        path,
				Line:        lineNo,
				Kind:        "rest_route",
				Claim:       route,
				Status:      status,
				Evidence:    evidence,
				SafeMessage: "REST route claim checked against registered route patterns",
			})
		}
	}
	return findings
}

func normalizeToolClaim(tool string) string {
	return strings.TrimRight(tool, ".,)")
}

func claimPathAllowed(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return false
	}
	if strings.Contains(strings.ToLower(path), ".env") || strings.Contains(strings.ToLower(path), "secret") {
		return false
	}
	return path == "README.md" ||
		strings.HasPrefix(path, "docs/") ||
		strings.HasPrefix(path, "api/") ||
		path == ".ai/skills/mivia-mcp/SKILL.md"
}

func normalizeRouteClaim(route string) string {
	route = strings.TrimRight(route, ".,)")
	route = strings.ReplaceAll(route, "{id}", "*")
	route = strings.ReplaceAll(route, "{run_id}", "*")
	route = strings.ReplaceAll(route, "{file_id}", "*")
	route = strings.ReplaceAll(route, "{symbol_id}", "*")
	return route
}

func setFrom(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func defaultKnownTools() []string {
	return []string{
		"tasks.create", "tasks.get",
		"research_runs.create", "research_runs.get",
		"agent_runs.create", "agent_runs.step_append", "agent_runs.complete", "agent_runs.get",
		"projects.list", "projects.get", "projects.digest", "projects.ingest",
		"projects.context_health", "projects.impact.analyze", "projects.claims.check",
		"projects.search_index.rebuild", "projects.ingestion_status", "projects.ingestion_status_latest",
		"projects.files.list", "projects.files.get", "projects.file.chunks", "projects.symbols.list",
		"projects.search.text", "projects.search.files", "projects.search.symbols", "projects.search.references", "projects.search.calls",
		"projects.search.ast.queries", "projects.search.ast", "projects.symbol.source", "projects.symbol.references",
		"projects.symbol.callers", "projects.symbol.callees", "projects.symbol.call_graph", "projects.headings.list", "projects.file.outline",
		"projects.workspace.git_status", "projects.workspace.git_diff", "projects.workspace.file_read", "projects.workspace.file_edit",
		"projects.diagnostics.ingestion",
	}
}

func defaultKnownRoutes() []string {
	return []string{
		"/api/v1/tasks", "/api/v1/tasks/*",
		"/api/v1/research-runs", "/api/v1/research-runs/*",
		"/api/v1/agent-runs", "/api/v1/agent-runs/*", "/api/v1/agent-runs/*/steps", "/api/v1/agent-runs/*/complete",
		"/api/v1/projects", "/api/v1/projects/*", "/api/v1/projects/*/digest-runs",
		"/api/v1/projects/*/context-health", "/api/v1/projects/*/impact/analyze", "/api/v1/projects/*/claims/check",
		"/api/v1/projects/*/ingestion-runs", "/api/v1/projects/*/ingestion-runs/latest", "/api/v1/projects/*/ingestion-runs/*",
		"/api/v1/projects/*/search-index/rebuild", "/api/v1/projects/*/files", "/api/v1/projects/*/files/*",
		"/api/v1/projects/*/files/*/chunks", "/api/v1/projects/*/files/*/outline",
		"/api/v1/projects/*/symbols", "/api/v1/projects/*/symbols/*/source", "/api/v1/projects/*/symbols/*/references",
		"/api/v1/projects/*/symbols/*/callers", "/api/v1/projects/*/symbols/*/callees", "/api/v1/projects/*/symbols/*/call-graph",
		"/api/v1/projects/*/search/text", "/api/v1/projects/*/search/files", "/api/v1/projects/*/search/symbols",
		"/api/v1/projects/*/search/references", "/api/v1/projects/*/search/calls", "/api/v1/projects/*/search/ast", "/api/v1/projects/*/search/ast/queries",
		"/api/v1/projects/*/headings",
		"/api/v1/projects/*/workspace/git/status", "/api/v1/projects/*/workspace/git/diff", "/api/v1/projects/*/workspace/files/read", "/api/v1/projects/*/workspace/files/edit",
	}
}
