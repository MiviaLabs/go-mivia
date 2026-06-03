package projectreliability

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

type ClaimCheckRequest struct {
	ProjectID       string          `json:"project_id,omitempty"`
	Documents       []ClaimDocument `json:"documents,omitempty"`
	SelectedPaths   []string        `json:"selected_paths,omitempty"`
	KnownTools      []string        `json:"known_tools,omitempty"`
	KnownRoutes     []string        `json:"known_routes,omitempty"`
	IncludeVerified bool            `json:"include_verified,omitempty"`
}

type ClaimDocument struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type ClaimCheckResult struct {
	ProjectID       string            `json:"project_id,omitempty"`
	Summary         ClaimCheckSummary `json:"summary"`
	Claims          []ClaimFinding    `json:"claims"`
	VerifiedOmitted int               `json:"verified_omitted,omitempty"`
	AllVerified     bool              `json:"all_verified"`
}

type ClaimCheckSummary struct {
	Total      int            `json:"total"`
	Verified   int            `json:"verified"`
	Actionable int            `json:"actionable"`
	ByStatus   map[string]int `json:"by_status,omitempty"`
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

var toolClaimPattern = regexp.MustCompile(`\b(?:projects|orgs|agent_runs|tasks|research_runs)(?:\.|_)[a-zA-Z0-9_.*]+`)
var routeClaimPattern = regexp.MustCompile(`/api/v1/[a-zA-Z0-9_./{}*<>=?&-]+`)

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
	allClaims := []ClaimFinding{}
	for _, doc := range docs {
		path := strings.TrimSpace(strings.ReplaceAll(doc.Path, "\\", "/"))
		if !claimPathAllowed(path) {
			allClaims = append(allClaims, ClaimFinding{
				Path:        path,
				Line:        0,
				Kind:        "path",
				Status:      "out_of_scope",
				SafeMessage: "claim checker accepts selected stable docs, API docs, README, and MCP skill files only",
			})
			continue
		}
		if strings.TrimSpace(doc.Text) == "" {
			allClaims = append(allClaims, ClaimFinding{
				Path:        path,
				Line:        0,
				Kind:        "document",
				Status:      "unverified",
				SafeMessage: "document text unavailable for deterministic claim check",
			})
			continue
		}
		allClaims = append(allClaims, checkDocumentClaims(path, doc.Text, knownTools, knownRoutes)...)
	}
	sort.SliceStable(allClaims, func(i, j int) bool {
		if allClaims[i].Path != allClaims[j].Path {
			return allClaims[i].Path < allClaims[j].Path
		}
		return allClaims[i].Line < allClaims[j].Line
	})
	result.Summary = summarizeClaims(allClaims)
	result.AllVerified = result.Summary.Actionable == 0
	if request.IncludeVerified {
		result.Claims = allClaims
		return result, nil
	}
	for _, claim := range allClaims {
		if claim.Status == "verified" {
			result.VerifiedOmitted++
			continue
		}
		result.Claims = append(result.Claims, claim)
	}
	return result, nil
}

func summarizeClaims(claims []ClaimFinding) ClaimCheckSummary {
	summary := ClaimCheckSummary{ByStatus: map[string]int{}}
	for _, claim := range claims {
		summary.Total++
		summary.ByStatus[claim.Status]++
		if claim.Status == "verified" {
			summary.Verified++
		} else {
			summary.Actionable++
		}
	}
	if len(summary.ByStatus) == 0 {
		summary.ByStatus = nil
	}
	return summary
}

func checkDocumentClaims(path string, text string, knownTools map[string]struct{}, knownRoutes map[string]struct{}) []ClaimFinding {
	findings := []ClaimFinding{}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lineNo := index + 1
		plainLine := stripInlineCodeSpans(line)
		if strings.Contains(plainLine, ".ai/tasks/") && shouldFlagTaskLink(path) {
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
			if shouldIgnoreToolClaim(tool, knownTools) {
				continue
			}
			status := "verified"
			evidence := "known MCP tool"
			if _, ok := knownTools[tool]; !ok {
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
			displayRoute := strings.TrimRight(route, ".,)")
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
				Claim:       displayRoute,
				Status:      status,
				Evidence:    evidence,
				SafeMessage: "REST route claim checked against registered route patterns",
			})
		}
	}
	return findings
}

func stripInlineCodeSpans(line string) string {
	var out strings.Builder
	inCode := false
	for _, r := range line {
		if r == '`' {
			inCode = !inCode
			continue
		}
		if !inCode {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func shouldFlagTaskLink(path string) bool {
	return path != ".ai/skills/mivia-mcp/SKILL.md"
}

func shouldIgnoreToolClaim(tool string, knownTools map[string]struct{}) bool {
	if strings.Contains(tool, "*") {
		return true
	}
	if looksLikeFilenameClaim(tool) {
		return true
	}
	if _, ok := knownTools[tool]; ok {
		return false
	}
	for known := range knownTools {
		if strings.HasPrefix(tool, known+".") || strings.HasPrefix(tool, known+"_") {
			return true
		}
	}
	return false
}

func normalizeToolClaim(tool string) string {
	return strings.TrimRight(tool, ".,)")
}

func looksLikeFilenameClaim(tool string) bool {
	lower := strings.ToLower(strings.TrimRight(tool, ".,)"))
	for _, suffix := range []string{".md", ".go", ".yaml", ".yml", ".json", ".toml", ".txt"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
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
	if before, _, ok := strings.Cut(route, "?"); ok {
		route = before
	}
	route = strings.TrimRight(route, "/")
	for _, placeholder := range []string{"id", "project_id", "run_id", "file_id", "symbol_id", "plan_id", "task_id", "claim_id", "knowledge_id", "automation_id", "workflow_id", "agent_id", "snapshot_id", "org_ref"} {
		route = strings.ReplaceAll(route, "{"+placeholder+"}", "*")
		route = strings.ReplaceAll(route, "<"+placeholder+">", "*")
	}
	route = strings.ReplaceAll(route, "{id}", "*")
	route = strings.ReplaceAll(route, "{run_id}", "*")
	route = strings.ReplaceAll(route, "{file_id}", "*")
	route = strings.ReplaceAll(route, "{symbol_id}", "*")
	parts := strings.Split(route, "/")
	for index, part := range parts {
		if shouldWildcardRouteSegment(parts, index, part) {
			parts[index] = "*"
		}
	}
	route = strings.Join(parts, "/")
	return route
}

func shouldWildcardRouteSegment(parts []string, index int, segment string) bool {
	if segment == "" || segment == "*" {
		return false
	}
	previous := ""
	if index > 0 {
		previous = parts[index-1]
	}
	switch previous {
	case "claims":
		return segment != "check"
	case "files":
		if index > 1 && parts[index-2] == "workspace" {
			return false
		}
		return true
	case "work-plans":
		return true
	case "work-tasks":
		return segment != "open" && segment != "mine" && segment != "blocked" && segment != "next"
	case "automations":
		return true
	case "automation-runs":
		return true
	case "projects", "tasks", "research-runs", "agent-runs", "symbols", "ingestion-runs", "digest-runs":
		return true
	default:
		return false
	}
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
	return withToolAliases([]string{
		"tasks.create", "tasks.get",
		"research_runs.create", "research_runs.get",
		"agent_runs.create", "agent_runs.step_append", "agent_runs.promote_artifact", "agent_runs.complete", "agent_runs.get",
		"projects.list", "projects.get", "projects.digest", "projects.ingest",
		"projects.context_health", "projects.graph_status", "projects.impact.analyze", "projects.context_pack.build", "projects.claims.check",
		"projects.search_index.rebuild", "projects.ingestion_status", "projects.ingestion_status_latest",
		"projects.files.list", "projects.files.get", "projects.file.chunks", "projects.symbols.list",
		"projects.search.text", "projects.search.files", "projects.search.symbols", "projects.search.references", "projects.search.calls",
		"projects.search.ast.queries", "projects.search.ast", "projects.symbol.source", "projects.symbol.references",
		"projects.symbol.callers", "projects.symbol.callees", "projects.symbol.call_graph", "projects.headings.list", "projects.file.outline",
		"projects.workspace.git_status", "projects.workspace.git_diff", "projects.workspace.git_worktree_create", "projects.workspace.file_read", "projects.workspace.file_edit", "projects.workspace.file_create", "projects.workspace.file_delete",
		"projects.diagnostics.ingestion",
		"projects.evidence_graph.claims.create", "projects.evidence_graph.claims.get", "projects.evidence_graph.claims.list",
		"projects.evidence_graph.evidence.append", "projects.evidence_graph.decisions.create", "projects.evidence_graph.actions.create",
		"projects.evidence_graph.outcomes.create", "projects.evidence_graph.artifacts.link", "projects.evidence_graph.promotions.link",
		"projects.confidence.claims.score", "projects.confidence.claims.get", "projects.confidence.claims.list",
		"projects.knowledge.candidates.create", "projects.knowledge.validate", "projects.knowledge.promote_project",
		"projects.knowledge.submit_org_review", "projects.knowledge.promote_org", "projects.knowledge.reject",
		"projects.knowledge.supersede", "projects.knowledge.reuse_events.record", "projects.knowledge.get",
		"projects.knowledge.list", "orgs.knowledge.list",
		"projects.integrations.list", "projects.integrations.status", "projects.integrations.counts", "projects.integrations.poll",
		"projects.integrations.poll_status", "projects.integrations.search", "projects.jira.issue.get", "projects.confluence.page.get",
		"projects.work_plans.create", "projects.work_plans.get", "projects.work_plans.list", "projects.work_plans.update_status", "projects.work_plans.resume",
		"projects.work_tasks.create", "projects.work_tasks.get", "projects.work_tasks.update_status", "projects.work_tasks.claim", "projects.work_tasks.release", "projects.work_tasks.start",
		"projects.work_tasks.complete", "projects.work_tasks.fail", "projects.work_tasks.block", "projects.work_tasks.list_open",
		"projects.work_tasks.list_mine", "projects.work_tasks.list_blocked", "projects.work_tasks.get_next",
		"projects.work_tasks.attach_evidence", "projects.work_tasks.attach_context_pack", "projects.work_tasks.attach_claim",
		"projects.work_tasks.attach_verifier_result", "projects.work_tasks.attach_review_result", "projects.work_tasks.promote_knowledge_candidate",
		"projects.automations.create", "projects.automations.get", "projects.automations.list", "projects.automations.run",
		"projects.automations.run_parallel_batch", "projects.automation_runs.get", "projects.automation_runs.list",
		"projects.automation_runs.claim_next", "projects.automation_runs.complete_attempt",
		"projects.workflows.validate_toml", "projects.workflows.import_toml", "projects.workflows.get", "projects.workflows.list",
		"projects.workflows.update_status", "projects.workflows.compile_to_work_plan",
		"projects.agent_definitions.list", "projects.agent_definitions.get",
		"projects.permission_snapshots.get", "projects.permission_snapshots.list",
	})
}

func withToolAliases(tools []string) []string {
	out := make([]string, 0, len(tools)*2)
	seen := map[string]struct{}{}
	for _, tool := range tools {
		for _, name := range []string{tool, strings.ReplaceAll(tool, ".", "_")} {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func defaultKnownRoutes() []string {
	return []string{
		"/api/v1/tasks", "/api/v1/tasks/*",
		"/api/v1/research-runs", "/api/v1/research-runs/*",
		"/api/v1/agent-runs", "/api/v1/agent-runs/*", "/api/v1/agent-runs/*/steps", "/api/v1/agent-runs/*/complete",
		"/api/v1/projects", "/api/v1/projects/*", "/api/v1/projects/*/digest-runs", "/api/v1/projects/*/dashboard-summary", "/api/v1/projects/*/agent-activity/stream",
		"/api/v1/projects/*/context-health", "/api/v1/projects/*/context-pack", "/api/v1/projects/*/impact/analyze", "/api/v1/projects/*/claims/check",
		"/api/v1/projects/*/ingestion-runs", "/api/v1/projects/*/ingestion-runs/latest", "/api/v1/projects/*/ingestion-runs/*",
		"/api/v1/projects/*/search-index/rebuild", "/api/v1/projects/*/files", "/api/v1/projects/*/files/*",
		"/api/v1/projects/*/files/*/chunks", "/api/v1/projects/*/files/*/outline",
		"/api/v1/projects/*/symbols", "/api/v1/projects/*/symbols/*/source", "/api/v1/projects/*/symbols/*/references",
		"/api/v1/projects/*/symbols/*/callers", "/api/v1/projects/*/symbols/*/callees", "/api/v1/projects/*/symbols/*/call-graph",
		"/api/v1/projects/*/search/text", "/api/v1/projects/*/search/files", "/api/v1/projects/*/search/symbols",
		"/api/v1/projects/*/search/references", "/api/v1/projects/*/search/calls", "/api/v1/projects/*/search/ast", "/api/v1/projects/*/search/ast/queries",
		"/api/v1/projects/*/headings",
		"/api/v1/projects/*/workspace/git/status", "/api/v1/projects/*/workspace/git/diff", "/api/v1/projects/*/workspace/files/read", "/api/v1/projects/*/workspace/files/edit",
		"/api/v1/projects/*/workspace/files/create", "/api/v1/projects/*/workspace/files/delete",
		"/api/v1/projects/*/evidence-graph/claims", "/api/v1/projects/*/evidence-graph/claims/*",
		"/api/v1/projects/*/evidence-graph/claims/*/evidence", "/api/v1/projects/*/evidence-graph/claims/*/decisions",
		"/api/v1/projects/*/evidence-graph/claims/*/actions", "/api/v1/projects/*/evidence-graph/claims/*/outcomes",
		"/api/v1/projects/*/evidence-graph/claims/*/artifact-links", "/api/v1/projects/*/evidence-graph/claims/*/promotion-links",
		"/api/v1/projects/*/confidence/claims", "/api/v1/projects/*/confidence/claims/*", "/api/v1/projects/*/confidence/claims/*/score",
		"/api/v1/projects/*/knowledge/candidates", "/api/v1/projects/*/knowledge/*/validate", "/api/v1/projects/*/knowledge/*/promote-project",
		"/api/v1/projects/*/knowledge/*/submit-org-review", "/api/v1/projects/*/knowledge/*/promote-org", "/api/v1/projects/*/knowledge/*/reject",
		"/api/v1/projects/*/knowledge/*/supersede", "/api/v1/projects/*/knowledge/*/reuse-events", "/api/v1/projects/*/knowledge/*",
		"/api/v1/projects/*/knowledge", "/api/v1/orgs/*/knowledge",
		"/api/v1/projects/*/work-plans", "/api/v1/projects/*/work-plans/*", "/api/v1/projects/*/work-plans/*/status",
		"/api/v1/projects/*/work-plans/*/resume", "/api/v1/projects/*/work-plans/*/tasks",
		"/api/v1/projects/*/work-tasks", "/api/v1/projects/*/work-tasks/open", "/api/v1/projects/*/work-tasks/mine",
		"/api/v1/projects/*/work-tasks/blocked", "/api/v1/projects/*/work-tasks/next", "/api/v1/projects/*/work-tasks/*",
		"/api/v1/projects/*/work-tasks/*/claim", "/api/v1/projects/*/work-tasks/*/release", "/api/v1/projects/*/work-tasks/*/start",
		"/api/v1/projects/*/work-tasks/*/complete", "/api/v1/projects/*/work-tasks/*/fail", "/api/v1/projects/*/work-tasks/*/block",
		"/api/v1/projects/*/work-tasks/*/evidence", "/api/v1/projects/*/work-tasks/*/context-packs",
		"/api/v1/projects/*/work-tasks/*/claims", "/api/v1/projects/*/work-tasks/*/verifier-results",
		"/api/v1/projects/*/work-tasks/*/knowledge-candidates",
		"/api/v1/projects/*/workflows/validate-toml", "/api/v1/projects/*/workflows/import-toml", "/api/v1/projects/*/workflows",
		"/api/v1/projects/*/workflows/*", "/api/v1/projects/*/workflows/*/status", "/api/v1/projects/*/workflows/*/compile",
		"/api/v1/projects/*/workflows/*/agent-definitions", "/api/v1/projects/*/workflows/*/agent-definitions/*",
		"/api/v1/projects/*/permission-snapshots", "/api/v1/projects/*/permission-snapshots/*",
		"/api/v1/projects/*/automations", "/api/v1/projects/*/automations/*",
		"/api/v1/projects/*/automations/*/runs", "/api/v1/projects/*/automations/*/parallel-batches",
		"/api/v1/projects/*/automation-runs", "/api/v1/projects/*/automation-runs/*",
		"/api/v1/projects/*/automation-runs/claim-next", "/api/v1/projects/*/automation-runs/*/attempt-result",
		"/api/v1/projects/{id}/automations", "/api/v1/projects/{id}/automations/{automation_id}",
		"/api/v1/projects/{id}/automations/{automation_id}/runs", "/api/v1/projects/{id}/automations/{automation_id}/parallel-batches",
		"/api/v1/projects/{id}/automation-runs", "/api/v1/projects/{id}/automation-runs/claim-next",
		"/api/v1/projects/{id}/automation-runs/{run_id}", "/api/v1/projects/{id}/automation-runs/{run_id}/attempt-result",
		"/api/v1/projects/{id}/workflows/validate-toml", "/api/v1/projects/{id}/workflows/import-toml", "/api/v1/projects/{id}/workflows",
		"/api/v1/projects/{id}/workflows/{workflow_id}", "/api/v1/projects/{id}/workflows/{workflow_id}/status", "/api/v1/projects/{id}/workflows/{workflow_id}/compile",
		"/api/v1/projects/{id}/workflows/{workflow_id}/agent-definitions", "/api/v1/projects/{id}/workflows/{workflow_id}/agent-definitions/{agent_id}",
		"/api/v1/projects/{id}/permission-snapshots", "/api/v1/projects/{id}/permission-snapshots/{snapshot_id}",
	}
}
