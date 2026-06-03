package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/mcpapi"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/service"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/diagnostics"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	knowledgestore "github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanmcpapi "github.com/MiviaLabs/go-mivia/internal/projectworkplan/mcpapi"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

func TestToolsList_ReturnsTaskAndResearchTools(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"tasks.create"`)) || !bytes.Contains(res.Body.Bytes(), []byte(`"research_runs.create"`)) {
		t.Fatalf("expected tool discovery response, got %s", res.Body.String())
	}
}

func TestToolsList_KnowledgeToolsOnlyWhenConfiguredAndMapsErrors(t *testing.T) {
	plain := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if bytes.Contains(plain.Body.Bytes(), []byte(`"projects.knowledge.candidates.create"`)) {
		t.Fatalf("knowledge tools must not be exposed without service: %s", plain.Body.String())
	}

	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	knowledgeSvc := projectknowledge.New(knowledgestore.NewMemoryStore())
	handler := mcpapi.NewHandlerWithActivityEvidenceGraphConfidenceAndKnowledge(svc, nil, nil, nil, nil, nil, nil, nil, nil, knowledgeSvc, nil, nil, nil, nil, slog.Default())
	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.knowledge.candidates.create"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"orgs.knowledge.list"`)) {
		t.Fatalf("expected configured knowledge tools, got %s", list.Body.String())
	}
	invalid := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.knowledge.candidates.create","arguments":{"id":"project_1","knowledge_ref":"knowledge/ref_1","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse","query":"MATCH (n)"}}}`)
	if !bytes.Contains(invalid.Body.Bytes(), []byte(`"code":-32602`)) {
		t.Fatalf("expected invalid argument mapping, got %s", invalid.Body.String())
	}
	missing := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects.knowledge.get","arguments":{"id":"project_1","knowledge_id":"missing"}}}`)
	if !bytes.Contains(missing.Body.Bytes(), []byte(`"code":-32002`)) {
		t.Fatalf("expected not found mapping, got %s", missing.Body.String())
	}
}

func TestToolsListAndCall_WorkPlanToolsOnlyWhenConfigured(t *testing.T) {
	plain := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if bytes.Contains(plain.Body.Bytes(), []byte(`"projects.work_plans.create"`)) {
		t.Fatalf("work plan tools must not be exposed without service: %s", plain.Body.String())
	}

	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	handler := mcpapi.NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeAndWorkPlans(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, topLevelWorkPlanAPI{}, nil, nil, nil, slog.Default())
	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, name := range []string{
		"projects.work_plans.create",
		"projects.work_plans.resume",
		"projects.work_tasks.create",
		"projects.work_tasks.claim",
		"projects.work_tasks.start",
		"projects.work_tasks.complete",
		"projects.work_tasks.list_blocked",
		"projects.work_tasks.promote_knowledge_candidate",
	} {
		if !bytes.Contains(list.Body.Bytes(), []byte(`"`+name+`"`)) {
			t.Fatalf("expected work plan tool %s, got %s", name, list.Body.String())
		}
	}
	if !bytes.Contains(list.Body.Bytes(), []byte("MUST be used before multi-step project work")) ||
		!bytes.Contains(list.Body.Bytes(), []byte("MUST be called before an agent edits files")) ||
		!bytes.Contains(list.Body.Bytes(), []byte("isolated low-intelligence worker")) {
		t.Fatalf("expected strict workflow descriptions, got %s", list.Body.String())
	}

	createPlan := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.work_plans.create","arguments":{"id":"example-service","plan_ref":"plan/ref","title":"Top-level route","goal_summary":"Route metadata-only work plan tools"}}}`)
	if bytes.Contains(createPlan.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(createPlan.Body.Bytes(), []byte(`"plan_id":"plan-1"`)) {
		t.Fatalf("expected work plan create route success, got %s", createPlan.Body.String())
	}
	claimTask := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects_work_tasks_claim","arguments":{"id":"example-service","task_id":"task-1","owner_agent":"worker-4","run_id":"run-1"}}}`)
	if bytes.Contains(claimTask.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(claimTask.Body.Bytes(), []byte(`"task_id":"task-1"`)) {
		t.Fatalf("expected underscore work task route success, got %s", claimTask.Body.String())
	}
	invalid := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects.work_plans.create","arguments":{"id":"example-service","plan_ref":"plan/ref","title":"Top-level route","goal_summary":"raw prompt: token=secret"}}}`)
	if !bytes.Contains(invalid.Body.Bytes(), []byte(`"code":-32602`)) {
		t.Fatalf("expected invalid argument mapping, got %s", invalid.Body.String())
	}
}

func TestToolsCall_WorkPlanServiceErrorsMapToClientErrors(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	handler := mcpapi.NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeAndWorkPlans(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, workPlans, nil, nil, nil, slog.Default())

	createPlan := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"projects.work_plans.create","arguments":{"id":"example-service","plan_ref":"plan/ref","title":"Top-level route","goal_summary":"Route metadata-only work plan tools","created_by_run_id":"agent_run_1"}}}`)
	if bytes.Contains(createPlan.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected real work plan create success, got %s", createPlan.Body.String())
	}
	var created rpcResponse
	if err := json.Unmarshal(createPlan.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create plan: %v", err)
	}
	planID := created.Result.StructuredContent["id"].(string)

	richTask := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.work_tasks.create","arguments":{"id":"example-service","plan_id":"`+planID+`","task_ref":"task/rich","title":"Prepare isolated fixture","description":"Metadata-only task; no raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, or PII.","owner_agent":"gpt-5.5-low-worker","evidence_needed":["current source and focused verifier refs"],"context_pack_refs":["context-pack:manifest:68c3ee2ad1556459"],"likely_files_affected":["tmp/mivia-workplan-smoke"],"verification_requirement":"orchestrator runs focused tests after worker output is reviewed","resume_instructions":"resume by claiming the next ready task and reading attached refs","expected_output":"safe metadata-only task output","failure_block_criteria":"block if context health is stale","knowledge_candidate_expectation":"none for smoke test","run_id":"agent_run_1","trace_id":"trace_1"}}}`)
	if bytes.Contains(richTask.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(richTask.Body.Bytes(), []byte(`"status":"ready"`)) || !bytes.Contains(richTask.Body.Bytes(), []byte(`"agent_run_ids":["agent_run_1"]`)) {
		t.Fatalf("expected rich task create success, got %s", richTask.Body.String())
	}

	invalidTask := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects.work_tasks.create","arguments":{"id":"example-service","plan_id":"missing-plan","task_ref":"task/missing","title":"Missing plan","evidence_needed":["current source"],"verification_requirement":"focused tests","resume_instructions":"continue safely"}}}`)
	if !bytes.Contains(invalidTask.Body.Bytes(), []byte(`"code":-32002`)) {
		t.Fatalf("expected missing plan to map to not found, got %s", invalidTask.Body.String())
	}
}

func TestInitialize_ReturnsServerInstructions(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.0.0"}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"instructions"`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`authoritative context and workspace interface`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`projects.graph_status`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`projects.ingestion_status_latest`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`do not use projects.ingestion_status_latest alone`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`Use the smallest MCP call set that answers the task`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`Before commit, use the smallest verification set appropriate`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`projects.impact.analyze with changed paths when blast radius is unclear`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`projects.context_health`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`projects.claims.check`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`agent_runs.step_append`)) ||
		!bytes.Contains(res.Body.Bytes(), []byte(`isolated-worker-ready tasks`)) {
		t.Fatalf("expected initialize instructions, got %s", res.Body.String())
	}
}

func TestToolsListAndCall_IngestionDiagnosticsWhenConfigured(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	registry, err := projectregistry.NewRegistry(nil, projectregistry.Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	diagnosticsService := diagnostics.NewService(fakeDiagnosticsSnapshotter{snapshot: projectingestion.DiagnosticsSnapshot{
		Stages: map[string]projectingestion.StageDiagnostic{
			"storage.search_write": {Count: 1, TotalMillis: 2, MaxMillis: 2, LastMillis: 2},
		},
	}}, diagnostics.RuntimeOptions{})
	handler := mcpapi.NewHandlerWithResearchProjectsIngestionWorkspaceIntegrationsAndDiagnostics(svc, nil, registry, digest, nil, nil, nil, diagnosticsService, slog.Default())

	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.diagnostics.ingestion"`)) {
		t.Fatalf("expected diagnostics tool, got %s", list.Body.String())
	}
	call := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.diagnostics.ingestion","arguments":{}}}`)
	if bytes.Contains(call.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(call.Body.Bytes(), []byte("storage.search_write")) {
		t.Fatalf("unexpected diagnostics call response: %s", call.Body.String())
	}
	for _, forbidden := range [][]byte{[]byte("/home/mac"), []byte(`C:\`), []byte("MIVIA_"), []byte("token"), []byte("credential"), []byte("package main")} {
		if bytes.Contains(call.Body.Bytes(), forbidden) {
			t.Fatalf("diagnostics leaked %q: %s", forbidden, call.Body.String())
		}
	}
}

func TestToolsCall_CreateAndGetTask(t *testing.T) {
	handler := newHandler()
	create := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP task"}}}`)
	if create.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", create.Code, create.Body.String())
	}
	var created rpcResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	taskID := created.Result.StructuredContent["id"].(string)

	get := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tasks.get","arguments":{"id":"`+taskID+`"}}}`)
	if get.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", get.Code, get.Body.String())
	}
	if !bytes.Contains(get.Body.Bytes(), []byte(taskID)) {
		t.Fatalf("expected fetched task id, got %s", get.Body.String())
	}
}

func TestToolsCall_RecordsProjectScopedActivityWithRawPayload(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "example-service",
		DisplayName:    "Example Service",
		RootPath:       t.TempDir(),
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     projectregistry.DigestModeContentGraph,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
		WorkspaceMode:  projectregistry.WorkspaceModeReadOnly,
		Include:        []string{"**/*.go"},
		MaxFileBytes:   4096,
		MaxChunkBytes:  1024,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	activity := agentactivity.NewRecorder(10)
	handler := mcpapi.NewHandlerWithActivity(svc, nil, registry, projectregistry.NewDigestService(registry, graph), nil, nil, nil, nil, activity, slog.Default())

	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":"abc","method":"tools/call","params":{"name":"projects.get","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected project tool success, got %s", res.Body.String())
	}
	events := activity.Recent("example-service", 10)
	if len(events) != 1 {
		t.Fatalf("expected one project-scoped activity event, got %#v", events)
	}
	event := events[0]
	if event.ToolName != "projects.get" || event.RequestID != "abc" || event.Status != "ok" {
		t.Fatalf("unexpected activity event %#v", event)
	}
	if !bytes.Contains(event.RawArgs, []byte("example-service")) {
		t.Fatalf("expected raw arguments to be retained for collapsed details, got %s", string(event.RawArgs))
	}
}

func TestToolsCall_AllowsMCPMeta(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP task"},"_meta":{"progressToken":"token-1"}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_AllowsMetaInsideArguments(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP task","_meta":{"source":"codex"}}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_AllowsJSONStringArguments(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":"{\"title\":\"MCP task\"}"}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_AllowsUnderscoreToolAlias(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks_create","arguments":{"title":"MCP task"}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_RejectsRawQueryArgument(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"Task","query":"MATCH (n)"}}}`)
	if !bytes.Contains(res.Body.Bytes(), []byte(`"code":-32602`)) {
		t.Fatalf("expected invalid argument error, got %s", res.Body.String())
	}
}

func TestToolsCall_AgentRunLifecycleIsRedacted(t *testing.T) {
	handler := newHandler()
	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"agent_runs.create"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"agent_runs.get"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"agent_runs.promote_artifact"`)) {
		t.Fatalf("expected agent run tools, got %s", list.Body.String())
	}

	create := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"agent_runs.create","arguments":{"project_id":"example-service","summary":"bounded run metadata","changed_files":["internal/agentcontrol/model/model.go"],"artifacts":[{"ref":"artifact-1","kind":"evidence"}]}}}`)
	if bytes.Contains(create.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected create success, got %s", create.Body.String())
	}
	var created rpcResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	runID := created.Result.StructuredContent["id"].(string)

	step := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent_runs_step_append","arguments":{"run_id":"`+runID+`","tool_name":"go","tool_category":"test","status":"completed","notes":"focused verifier passed"}}}`)
	if bytes.Contains(step.Body.Bytes(), []byte(`"error"`)) || bytes.Contains(step.Body.Bytes(), []byte("raw prompt")) || bytes.Contains(step.Body.Bytes(), []byte("package main")) {
		t.Fatalf("unexpected step response: %s", step.Body.String())
	}

	promote := postMCP(t, handler, `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"agent_runs.promote_artifact","arguments":{"run_id":"`+runID+`","artifact_ref":"artifact-1","state":"validated","source_ref":"agent_step_1","verifier_ref":"go/test","decision":"focused verifier passed"}}}`)
	if bytes.Contains(promote.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(promote.Body.Bytes(), []byte(`"state":"validated"`)) || bytes.Contains(promote.Body.Bytes(), []byte("package main")) {
		t.Fatalf("unexpected promote response: %s", promote.Body.String())
	}

	complete := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"agent_runs.complete","arguments":{"run_id":"`+runID+`","status":"completed"}}}`)
	if bytes.Contains(complete.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected complete success, got %s", complete.Body.String())
	}
}

func TestToolsCall_AgentRunAllowsSafetyGuidanceSummary(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agent_runs.create","arguments":{"project_id":"example-service","trace_id":"smoke-20260603-workplan","summary":"Smoke-test Work Plan MCP workflow. Metadata-only refs; no raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, or PII."}}}`)
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(res.Body.Bytes(), []byte(`"trace_id":"smoke-20260603-workplan"`)) {
		t.Fatalf("expected safety guidance summary to be accepted, got %s", res.Body.String())
	}
}

func TestToolsCall_AgentRunRejectsUnsafePayload(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agent_runs.create","arguments":{"project_id":"example-service","summary":"raw prompt: token=secret"}}}`)
	if !bytes.Contains(res.Body.Bytes(), []byte(`"code":-32602`)) {
		t.Fatalf("expected invalid argument error, got %s", res.Body.String())
	}
}

func TestResourcesRead_ReturnsTaskJSON(t *testing.T) {
	handler := newHandler()
	create := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"Resource task"}}}`)
	var created rpcResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	taskID := created.Result.StructuredContent["id"].(string)

	read := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"mivialabs://tasks/`+taskID+`"}}`)
	if read.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", read.Code, read.Body.String())
	}
	if !bytes.Contains(read.Body.Bytes(), []byte(`"mimeType":"application/json"`)) {
		t.Fatalf("expected json resource, got %s", read.Body.String())
	}

	agentCreate := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent_runs.create","arguments":{"project_id":"example-service","summary":"bounded metadata"}}}`)
	var createdAgent rpcResponse
	if err := json.Unmarshal(agentCreate.Body.Bytes(), &createdAgent); err != nil {
		t.Fatalf("decode agent run response: %v", err)
	}
	runID := createdAgent.Result.StructuredContent["id"].(string)
	agentRead := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"mivialabs://agent-runs/`+runID+`"}}`)
	if !bytes.Contains(agentRead.Body.Bytes(), []byte(`example-service`)) || bytes.Contains(agentRead.Body.Bytes(), []byte("raw prompt")) {
		t.Fatalf("expected redacted agent run resource, got %s", agentRead.Body.String())
	}
}

func TestProjectToolsListAndCall_WhenProjectRegistryConfigured(t *testing.T) {
	handler := newHandlerWithProjects(t)
	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if list.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", list.Code, list.Body.String())
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.list"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.digest"`)) {
		t.Fatalf("expected project tools, got %s", list.Body.String())
	}

	call := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.digest","arguments":{"id":"example-service"}}}`)
	if call.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", call.Code, call.Body.String())
	}
	if bytes.Contains(call.Body.Bytes(), []byte("package main")) || bytes.Contains(call.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("project digest leaked content markers: %s", call.Body.String())
	}
}

func TestProjectIntegrationMCPToolsListAndStatusAreRedacted(t *testing.T) {
	handler := newHandlerWithProjectIntegrations(t)

	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.integrations.list"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.integrations.status"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.integrations.counts"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.integrations.poll"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.integrations.poll_status"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.integrations.search"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.jira.issue.get"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.confluence.page.get"`)) {
		t.Fatalf("expected integration tools, got %s", list.Body.String())
	}
	for _, forbidden := range [][]byte{
		[]byte(`"projects.integrations.ingest"`),
		[]byte(`"source":"remote"`),
	} {
		if bytes.Contains(list.Body.Bytes(), forbidden) {
			t.Fatalf("unexpected later-phase integration tool %s in %s", forbidden, list.Body.String())
		}
	}

	providers := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects_integrations_list","arguments":{"id":"project-1"}}}`)
	if bytes.Contains(providers.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected providers success, got %s", providers.Body.String())
	}
	if !bytes.Contains(providers.Body.Bytes(), []byte(`"Provider":"jira"`)) || !bytes.Contains(providers.Body.Bytes(), []byte(`"CredentialSource":"env"`)) || !bytes.Contains(providers.Body.Bytes(), []byte(`"AllowlistCount":2`)) {
		t.Fatalf("expected redacted provider metadata, got %s", providers.Body.String())
	}

	status := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects.integrations.status","arguments":{"id":"project-1","provider":"jira"}}}`)
	if bytes.Contains(status.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected status success, got %s", status.Body.String())
	}
	if !bytes.Contains(status.Body.Bytes(), []byte(`"CursorHashPresent":true`)) || !bytes.Contains(status.Body.Bytes(), []byte(`"Status":"no_op"`)) {
		t.Fatalf("expected sync state and run status, got %s", status.Body.String())
	}

	counts := postMCP(t, handler, `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"projects.integrations.counts","arguments":{"id":"project-1"}}}`)
	if bytes.Contains(counts.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected counts success, got %s", counts.Body.String())
	}
	if !bytes.Contains(counts.Body.Bytes(), []byte(`"Provider":"jira"`)) || !bytes.Contains(counts.Body.Bytes(), []byte(`"Provider":"confluence"`)) || !bytes.Contains(counts.Body.Bytes(), []byte(`"Count":0`)) {
		t.Fatalf("expected redacted counts response, got %s", counts.Body.String())
	}

	poll := postMCP(t, handler, `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"projects.integrations.poll","arguments":{"id":"project-1","provider":"jira","kind":"incremental"}}}`)
	if bytes.Contains(poll.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected poll success, got %s", poll.Body.String())
	}
	if !bytes.Contains(poll.Body.Bytes(), []byte(`"Provider":"jira"`)) || !bytes.Contains(poll.Body.Bytes(), []byte(`"Accepted":true`)) || !bytes.Contains(poll.Body.Bytes(), []byte(`"Status":"pending"`)) || !bytes.Contains(poll.Body.Bytes(), []byte(`"ID":"run-2"`)) {
		t.Fatalf("expected redacted poll status, got %s", poll.Body.String())
	}

	pollStatus := postMCP(t, handler, `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"projects.integrations.poll_status","arguments":{"id":"project-1","provider":"jira","run_id":"run-1"}}}`)
	if bytes.Contains(pollStatus.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected poll status success, got %s", pollStatus.Body.String())
	}
	if !bytes.Contains(pollStatus.Body.Bytes(), []byte(`"Provider":"jira"`)) || !bytes.Contains(pollStatus.Body.Bytes(), []byte(`"Status":"no_op"`)) || !bytes.Contains(pollStatus.Body.Bytes(), []byte(`"ID":"run-1"`)) {
		t.Fatalf("expected redacted poll run status, got %s", pollStatus.Body.String())
	}
	for _, forbidden := range []string{
		"https://tenant.atlassian.net",
		"ACME",
		"OPS",
		"ENG",
		"TEAM",
		"MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
		"MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
		"/home/mac/secret-email",
		"/home/mac/secret-token",
		"/home/mac/mivialabs/mivialabs-agents-monorepo",
		"raw-provider-cursor-token",
		"sha256:",
		"ISSUE-1",
		"jira rich description",
		"confluence page body",
	} {
		if bytes.Contains(status.Body.Bytes(), []byte(forbidden)) ||
			bytes.Contains(providers.Body.Bytes(), []byte(forbidden)) ||
			bytes.Contains(counts.Body.Bytes(), []byte(forbidden)) ||
			bytes.Contains(poll.Body.Bytes(), []byte(forbidden)) ||
			bytes.Contains(pollStatus.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("integration MCP response leaked %q: providers=%s status=%s poll=%s poll_status=%s", forbidden, providers.Body.String(), status.Body.String(), poll.Body.String(), pollStatus.Body.String())
		}
	}

	missing := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects.integrations.status","arguments":{"id":"missing","provider":"jira"}}}`)
	if !bytes.Contains(missing.Body.Bytes(), []byte(`"message":"resource not found"`)) {
		t.Fatalf("expected stable missing project error, got %s", missing.Body.String())
	}
	if bytes.Contains(missing.Body.Bytes(), []byte("tenant.atlassian.net")) || bytes.Contains(missing.Body.Bytes(), []byte("MIVIA_ATLASSIAN")) {
		t.Fatalf("missing project error leaked raw integration data: %s", missing.Body.String())
	}

	withoutIntegrations := postMCP(t, newHandlerWithProjects(t), `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	if bytes.Contains(withoutIntegrations.Body.Bytes(), []byte(`"projects.integrations.list"`)) {
		t.Fatalf("unexpected integration tools without integration service: %s", withoutIntegrations.Body.String())
	}
}

func TestProjectIngestionMCPToolsAndResources(t *testing.T) {
	handler, root := newHandlerWithProjectIngestion(t)

	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.ingest"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.context_health"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.graph_status"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.impact.analyze"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.claims.check"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search_index.rebuild"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.file.chunks"`)) {
		t.Fatalf("expected ingestion tools, got %s", list.Body.String())
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search.text"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search.calls"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search.ast.queries"`)) {
		t.Fatalf("expected project search tools, got %s", list.Body.String())
	}

	templates := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"resources/templates/list"}`)
	if !bytes.Contains(templates.Body.Bytes(), []byte(`mivialabs://projects/{id}/files/{file_id}`)) {
		t.Fatalf("expected ingestion resource templates, got %s", templates.Body.String())
	}

	ingest := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects_ingest","arguments":{"id":"example-service","_meta":{"source":"test"}}}}`)
	if bytes.Contains(ingest.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected ingest success, got %s", ingest.Body.String())
	}
	if bytes.Contains(ingest.Body.Bytes(), []byte(root)) || bytes.Contains(ingest.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("ingest response leaked sensitive metadata: %s", ingest.Body.String())
	}
	var ingestRPC rpcResponse
	if err := json.Unmarshal(ingest.Body.Bytes(), &ingestRPC); err != nil {
		t.Fatalf("decode ingest response: %v", err)
	}
	waitMCPIngestionRun(t, handler, ingestRPC.Result.StructuredContent["id"].(string))

	latest := postMCP(t, handler, `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"projects.ingestion_status_latest","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(latest.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected latest ingestion status success, got %s", latest.Body.String())
	}
	if bytes.Contains(latest.Body.Bytes(), []byte(root)) || bytes.Contains(latest.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("latest ingestion status leaked sensitive metadata: %s", latest.Body.String())
	}

	health := postMCP(t, handler, `{"jsonrpc":"2.0","id":33,"method":"tools/call","params":{"name":"projects_context_health","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(health.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected context health success, got %s", health.Body.String())
	}
	if !bytes.Contains(health.Body.Bytes(), []byte(`"status":"ready"`)) || bytes.Contains(health.Body.Bytes(), []byte(root)) || bytes.Contains(health.Body.Bytes(), []byte("content_sha256")) || bytes.Contains(health.Body.Bytes(), []byte("package main")) {
		t.Fatalf("unexpected context health response: %s", health.Body.String())
	}
	graphStatus := postMCP(t, handler, `{"jsonrpc":"2.0","id":331,"method":"tools/call","params":{"name":"projects.graph_status","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(graphStatus.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(graphStatus.Body.Bytes(), []byte(`"indexed_content_available":true`)) || bytes.Contains(graphStatus.Body.Bytes(), []byte(root)) || bytes.Contains(graphStatus.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("unexpected graph status response: %s", graphStatus.Body.String())
	}

	impact := postMCP(t, handler, `{"jsonrpc":"2.0","id":34,"method":"tools/call","params":{"name":"projects.impact.analyze","arguments":{"id":"example-service","changed_paths":["internal/agentcontrol/mcpapi/mcpapi.go"]}}}`)
	if bytes.Contains(impact.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(impact.Body.Bytes(), []byte(`"agent_control"`)) || bytes.Contains(impact.Body.Bytes(), []byte(root)) {
		t.Fatalf("unexpected impact response: %s", impact.Body.String())
	}

	claims := postMCP(t, handler, `{"jsonrpc":"2.0","id":35,"method":"tools/call","params":{"name":"projects.claims.check","arguments":{"id":"example-service","documents":[{"path":"README.md","text":"Use projects.context_health not projects.verifiers.recommend"}]}}}`)
	if bytes.Contains(claims.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(claims.Body.Bytes(), []byte(`"projects.verifiers.recommend"`)) || bytes.Contains(claims.Body.Bytes(), []byte(root)) {
		t.Fatalf("unexpected claims response: %s", claims.Body.String())
	}

	digest := postMCP(t, handler, `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"projects.digest","arguments":{"id":"example-service"}}}`)
	if !bytes.Contains(digest.Body.Bytes(), []byte(`"code":-32004`)) || !bytes.Contains(digest.Body.Bytes(), []byte(`project digest unsupported`)) || bytes.Contains(digest.Body.Bytes(), []byte(`invalid tool arguments`)) {
		t.Fatalf("expected content_graph digest unsupported error, got %s", digest.Body.String())
	}

	files := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects.files.list","arguments":"{\"id\":\"example-service\",\"page_size\":1}"}}`)
	if bytes.Contains(files.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected files success, got %s", files.Body.String())
	}
	var filesRPC rpcResponse
	if err := json.Unmarshal(files.Body.Bytes(), &filesRPC); err != nil {
		t.Fatalf("decode files response: %v", err)
	}
	fileItems := filesRPC.Result.StructuredContent["files"].([]any)
	fileID := fileItems[0].(map[string]any)["id"].(string)

	fileGet := postMCP(t, handler, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"projects.files.get","arguments":{"id":"example-service","file_id":"`+fileID+`"}}}`)
	if bytes.Contains(fileGet.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected file get success, got %s", fileGet.Body.String())
	}
	if !bytes.Contains(fileGet.Body.Bytes(), []byte(`"extension":".go"`)) {
		t.Fatalf("expected file get extension metadata, got %s", fileGet.Body.String())
	}

	chunks := postMCP(t, handler, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"projects.file.chunks","arguments":{"id":"example-service","file_id":"`+fileID+`","max_chunk_bytes":10}}}`)
	if bytes.Contains(chunks.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected chunks success, got %s", chunks.Body.String())
	}
	if bytes.Contains(chunks.Body.Bytes(), []byte(root)) || bytes.Contains(chunks.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("chunk response leaked forbidden metadata: %s", chunks.Body.String())
	}

	symbols := postMCP(t, handler, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"projects.symbols.list","arguments":{"id":"example-service","kind":"function","page_size":10}}}`)
	if bytes.Contains(symbols.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected symbols success, got %s", symbols.Body.String())
	}
	var symbolsRPC rpcResponse
	if err := json.Unmarshal(symbols.Body.Bytes(), &symbolsRPC); err != nil {
		t.Fatalf("decode symbols response: %v", err)
	}
	symbolItems := symbolsRPC.Result.StructuredContent["symbols"].([]any)
	runID := symbolIDByName(t, symbolItems, "Run")
	helperID := symbolIDByName(t, symbolItems, "helper")

	source := postMCP(t, handler, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"projects.symbol.source","arguments":{"id":"example-service","symbol_id":"`+runID+`","max_source_bytes":12}}}`)
	if bytes.Contains(source.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected symbol source success, got %s", source.Body.String())
	}
	if !bytes.Contains(source.Body.Bytes(), []byte("func Run")) || !bytes.Contains(source.Body.Bytes(), []byte(`"text_truncated":true`)) || bytes.Contains(source.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("unexpected source response: %s", source.Body.String())
	}

	refs := postMCP(t, handler, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"projects.symbol.references","arguments":{"id":"example-service","symbol_id":"`+helperID+`"}}}`)
	if bytes.Contains(refs.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(refs.Body.Bytes(), []byte(`"resolution_status":"resolved"`)) {
		t.Fatalf("expected resolved references success, got %s", refs.Body.String())
	}
	callees := postMCP(t, handler, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"projects.symbol.callees","arguments":{"id":"example-service","symbol_id":"`+runID+`"}}}`)
	if bytes.Contains(callees.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(callees.Body.Bytes(), []byte(helperID)) {
		t.Fatalf("expected helper callee success, got %s", callees.Body.String())
	}
	graph := postMCP(t, handler, `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"projects.symbol.call_graph","arguments":{"id":"example-service","symbol_id":"`+runID+`","direction":"callees","max_depth":1,"max_nodes":10}}}`)
	if bytes.Contains(graph.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(graph.Body.Bytes(), []byte(helperID)) {
		t.Fatalf("expected call graph success, got %s", graph.Body.String())
	}

	searchText := postMCP(t, handler, `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"projects_search_text","arguments":{"id":"example-service","query":"helper","max_snippet_bytes":12}}}`)
	if bytes.Contains(searchText.Body.Bytes(), []byte(`"error"`)) || bytes.Contains(searchText.Body.Bytes(), []byte(root)) || bytes.Contains(searchText.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("expected safe text search success, got %s", searchText.Body.String())
	}
	searchFiles := postMCP(t, handler, `{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"projects_search_files","arguments":{"id":"example-service","path_contains":"main"}}}`)
	if bytes.Contains(searchFiles.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchFiles.Body.Bytes(), []byte(`"relative_path":"cmd/main.go"`)) {
		t.Fatalf("expected file search success, got %s", searchFiles.Body.String())
	}
	searchSymbols := postMCP(t, handler, `{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"projects_search_symbols","arguments":{"id":"example-service","name_contains":"Run"}}}`)
	if bytes.Contains(searchSymbols.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchSymbols.Body.Bytes(), []byte(`"name":"Run"`)) {
		t.Fatalf("expected symbol search success, got %s", searchSymbols.Body.String())
	}
	searchRefs := postMCP(t, handler, `{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"projects_search_references","arguments":{"id":"example-service","target_name_contains":"helper"}}}`)
	if bytes.Contains(searchRefs.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchRefs.Body.Bytes(), []byte(`"target_name":"helper"`)) {
		t.Fatalf("expected reference search success, got %s", searchRefs.Body.String())
	}
	searchCalls := postMCP(t, handler, `{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"projects_search_calls","arguments":{"id":"example-service","caller_name_contains":"Run","callee_name_contains":"helper"}}}`)
	if bytes.Contains(searchCalls.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchCalls.Body.Bytes(), []byte(`"callee_name":"helper"`)) {
		t.Fatalf("expected call search success, got %s", searchCalls.Body.String())
	}
	astQueries := postMCP(t, handler, `{"jsonrpc":"2.0","id":171,"method":"tools/call","params":{"name":"projects_search_ast_queries","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(astQueries.Body.Bytes(), []byte(`"error":{`)) || bytes.Contains(astQueries.Body.Bytes(), []byte(`"isError":true`)) || !bytes.Contains(astQueries.Body.Bytes(), []byte(`"language":"dart"`)) || !bytes.Contains(astQueries.Body.Bytes(), []byte(`"id":"function_declarations"`)) || strings.Contains(astQueries.Body.String(), "(function_declaration") {
		t.Fatalf("expected AST query catalog success, got %s", astQueries.Body.String())
	}

	repair := postMCP(t, handler, `{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"projects_search_index_rebuild","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(repair.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(repair.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("expected search index repair success, got %s", repair.Body.String())
	}
	if bytes.Contains(repair.Body.Bytes(), []byte(root)) || bytes.Contains(repair.Body.Bytes(), []byte("content_sha256")) || bytes.Contains(repair.Body.Bytes(), []byte("project_search")) {
		t.Fatalf("repair response leaked forbidden metadata: %s", repair.Body.String())
	}

	read := postMCP(t, handler, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"mivialabs://projects/example-service/files/`+fileID+`"}}`)
	if read.Code != http.StatusOK || bytes.Contains(read.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected file resource success, got %s", read.Body.String())
	}
}

type rpcResponse struct {
	Result struct {
		StructuredContent map[string]any `json:"structuredContent"`
	} `json:"result"`
}

func symbolIDByName(t *testing.T, symbols []any, name string) string {
	t.Helper()
	for _, item := range symbols {
		symbol := item.(map[string]any)
		if symbol["name"] == name {
			return symbol["id"].(string)
		}
	}
	t.Fatalf("missing symbol %q in %#v", name, symbols)
	return ""
}

func newHandler() http.Handler {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	return mcpapi.NewHandler(svc, slog.Default())
}

func newHandlerWithProjects(t *testing.T) http.Handler {
	t.Helper()
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "example-service",
		DisplayName:    "Example Service",
		RootPath:       root,
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     projectregistry.DigestModeMetadataOnly,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
		Include:        []string{"**/*.go"},
	}}, projectregistry.Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	return mcpapi.NewHandlerWithResearchAndProjects(svc, nil, registry, digest, slog.Default())
}

func newHandlerWithProjectIntegrations(t *testing.T) http.Handler {
	t.Helper()
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(t.Context(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	integrationStore := projectintegrations.NewSQLiteStore(db.SQLDB())
	project := config.Project{
		ID:       "project-1",
		RootPath: "/home/mac/mivialabs/mivialabs-agents-monorepo",
		Integrations: config.IntegrationConfig{
			Jira: &config.JiraIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "cloud-id-1",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailEnv:    "MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
					APITokenEnv: "MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
				},
				Polling: config.IntegrationPolling{
					IngestionEnabled:    false,
					InitialFullSync:     "manual",
					IncrementalInterval: time.Minute,
					EmptyPollSleep:      10 * time.Minute,
					MaxIdleSleep:        30 * time.Minute,
					OverlapWindow:       2 * time.Minute,
					InitialPageSize:     50,
					IncrementalPageSize: 25,
				},
				ProjectKeys: []string{"ACME", "OPS"},
			},
			Confluence: &config.ConfluenceIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "cloud-id-1",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailFile:    "/home/mac/secret-email",
					APITokenFile: "/home/mac/secret-token",
				},
				Polling: config.IntegrationPolling{
					IngestionEnabled:    false,
					InitialFullSync:     "manual",
					IncrementalInterval: time.Minute,
					EmptyPollSleep:      10 * time.Minute,
					MaxIdleSleep:        30 * time.Minute,
					OverlapWindow:       2 * time.Minute,
					InitialPageSize:     50,
					IncrementalPageSize: 25,
				},
				SpaceKeys: []string{"ENG", "TEAM"},
			},
		},
	}
	runner := &fakeIntegrationPollRunner{
		result: projectintegrations.PollRunResult{
			Run: projectintegrations.SyncRun{
				ID:        "run-2",
				ProjectID: "project-1",
				Provider:  projectintegrations.ProviderJira,
				Kind:      projectintegrations.SyncKindIncremental,
				Status:    projectintegrations.SyncRunStatusPending,
				StartedAt: time.Date(2026, 5, 31, 10, 2, 0, 0, time.UTC),
			},
		},
	}
	integrations, err := projectintegrations.NewServiceWithOptions([]config.Project{project}, integrationStore, projectintegrations.ServiceOptions{Runner: runner})
	if err != nil {
		t.Fatalf("new integration service: %v", err)
	}
	if _, err := integrations.UpsertConfiguredSources(t.Context(), "project-1"); err != nil {
		t.Fatalf("upsert integration sources: %v", err)
	}
	run := projectintegrations.SyncRun{
		ID:            "run-1",
		ProjectID:     "project-1",
		Provider:      projectintegrations.ProviderJira,
		Kind:          projectintegrations.SyncKindIncremental,
		Status:        projectintegrations.SyncRunStatusNoOp,
		ItemsSeen:     0,
		ItemsUpserted: 0,
		EmptyPoll:     true,
		IdleSleep:     5 * time.Minute,
		StartedAt:     time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 5, 31, 10, 1, 0, 0, time.UTC),
	}
	if err := integrationStore.CreateSyncRun(t.Context(), run); err != nil {
		t.Fatalf("create sync run: %v", err)
	}
	if _, err := integrationStore.UpdateSyncState(t.Context(), projectintegrations.SyncStateInput{
		ProjectID:           "project-1",
		Provider:            projectintegrations.ProviderJira,
		LastRunID:           "run-1",
		LastSuccessfulRunID: "run-1",
		LastSuccessAt:       run.FinishedAt,
		LastEmptyPollAt:     run.FinishedAt,
		EmptyPollCount:      1,
		CurrentIdleSleep:    5 * time.Minute,
		Cursor:              "raw-provider-cursor-token",
		UpdatedAt:           run.FinishedAt,
	}); err != nil {
		t.Fatalf("update sync state: %v", err)
	}
	return mcpapi.NewHandlerWithResearchProjectsIngestionWorkspaceAndIntegrations(svc, nil, nil, nil, nil, nil, integrations, slog.Default())
}

type fakeIntegrationPollRunner struct {
	result projectintegrations.PollRunResult
	err    error
}

func (runner *fakeIntegrationPollRunner) RunProviderPoll(context.Context, string, projectintegrations.Provider, projectintegrations.SyncKind) (projectintegrations.PollRunResult, error) {
	if runner.err != nil {
		return projectintegrations.PollRunResult{}, runner.err
	}
	return runner.result, nil
}

func (runner *fakeIntegrationPollRunner) SubmitProviderPoll(context.Context, string, projectintegrations.Provider, projectintegrations.SyncKind) (projectintegrations.SyncRun, error) {
	if runner.err != nil {
		return projectintegrations.SyncRun{}, runner.err
	}
	return runner.result.Run, nil
}

func newHandlerWithProjectIngestion(t *testing.T) (http.Handler, string) {
	t.Helper()
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o700); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "main.go"), []byte("package main\n\nfunc helper() {}\n\nfunc Run() { helper() }\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example-service",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          projectregistry.UpdatePolicyManual,
		Include:               []string{"**/*.go"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: projectregistry.SensitiveMarkerPolicySkipFile,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(t.Context(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	ingestion := projectingestion.NewService(registry, projectingestion.NewGraphStore(graph), projectingestion.NewSQLiteStore(db.SQLDB()))
	scheduler := projectingestion.NewScheduler(ingestion, projectingestion.SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })
	return mcpapi.NewHandlerWithResearchProjectsAndIngestion(svc, nil, registry, digest, scheduler, slog.Default()), root
}

func TestProjectWorkspaceMCPToolsListWhenConfigured(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example-service",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          projectregistry.UpdatePolicyManual,
		WorkspaceMode:         projectregistry.WorkspaceModeEdit,
		Include:               []string{"**/*.go", "**/*.md"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: projectregistry.SensitiveMarkerPolicySkipFile,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	workspace := projectworkspace.NewService(registry, nil, projectworkspace.Options{Enabled: true})
	handler := mcpapi.NewHandlerWithResearchProjectsIngestionAndWorkspace(svc, nil, registry, digest, nil, workspace, slog.Default())

	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.workspace.git_status"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.workspace.git_worktree_create"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.workspace.file_read"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.workspace.file_create"`)) ||
		!bytes.Contains(list.Body.Bytes(), []byte(`"projects.workspace.file_delete"`)) {
		t.Fatalf("expected workspace tools, got %s", list.Body.String())
	}
	read := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects_workspace_file_read","arguments":{"id":"example-service","relative_path":"main.go"}}}`)
	if bytes.Contains(read.Body.Bytes(), []byte(`"error"`)) || bytes.Contains(read.Body.Bytes(), []byte(root)) || bytes.Contains(read.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("unexpected workspace read response: %s", read.Body.String())
	}
	var readResponse struct {
		Result struct {
			StructuredContent projectworkspace.WorkspaceFile `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &readResponse); err != nil {
		t.Fatalf("decode workspace read response: %v", err)
	}
	if readResponse.Result.StructuredContent.EditToken == "" {
		t.Fatalf("expected edit token in read response: %s", read.Body.String())
	}

	create := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects_workspace_file_create","arguments":{"id":"example-service","relative_path":"notes/new.md","text":"dry run only\n","dry_run":true,"create_parent_dirs":true}}}`)
	if bytes.Contains(create.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(create.Body.Bytes(), []byte(`"applied":false`)) || !bytes.Contains(create.Body.Bytes(), []byte(`"relative_path":"notes/new.md"`)) {
		t.Fatalf("unexpected workspace create dry-run response: %s", create.Body.String())
	}

	worktree := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects_workspace_git_worktree_create","arguments":{"id":"example-service","worktree_ref":"worktree/test","branch_ref":"codex/test","dry_run":true}}}`)
	if bytes.Contains(worktree.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(worktree.Body.Bytes(), []byte(`"applied":false`)) || bytes.Contains(worktree.Body.Bytes(), []byte(root)) {
		t.Fatalf("unexpected workspace worktree dry-run response: %s", worktree.Body.String())
	}

	deleteBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"projects.workspace.file_delete","arguments":{"id":"example-service","relative_path":"main.go","edit_token":%q,"dry_run":true}}}`, readResponse.Result.StructuredContent.EditToken)
	deleteRes := postMCP(t, handler, deleteBody)
	if bytes.Contains(deleteRes.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(deleteRes.Body.Bytes(), []byte(`"deleted":false`)) || !bytes.Contains(deleteRes.Body.Bytes(), []byte(`"relative_path":"main.go"`)) {
		t.Fatalf("unexpected workspace delete dry-run response: %s", deleteRes.Body.String())
	}
}

func TestProjectWorkspaceMCPGitUnavailableIsExplicit(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example-service",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          projectregistry.UpdatePolicyManual,
		WorkspaceMode:         projectregistry.WorkspaceModeReadOnly,
		Include:               []string{"**/*.go"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: projectregistry.SensitiveMarkerPolicySkipFile,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	workspace := projectworkspace.NewService(registry, nil, projectworkspace.Options{Enabled: true})
	workspace.SetGitRunner(failingGitRunner{})
	handler := mcpapi.NewHandlerWithResearchProjectsIngestionAndWorkspace(svc, nil, registry, digest, nil, workspace, slog.Default())

	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.workspace.git_status","arguments":{"id":"example-service"}}}`)
	if !bytes.Contains(res.Body.Bytes(), []byte(`"code":-32603`)) || !bytes.Contains(res.Body.Bytes(), []byte("git is not available")) {
		t.Fatalf("expected explicit git unavailable response, got %s", res.Body.String())
	}
}

func waitMCPIngestionRun(t *testing.T, handler http.Handler, runID string) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		status := postMCP(t, handler, `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"projects.ingestion_status","arguments":{"id":"example-service","run_id":"`+runID+`"}}}`)
		if bytes.Contains(status.Body.Bytes(), []byte(`"error"`)) {
			t.Fatalf("expected status success, got %s", status.Body.String())
		}
		var statusRPC rpcResponse
		if err := json.Unmarshal(status.Body.Bytes(), &statusRPC); err != nil {
			t.Fatalf("decode status response: %v", err)
		}
		statusValue := statusRPC.Result.StructuredContent["status"].(string)
		if statusValue == string(projectingestion.RunStatusCompleted) || statusValue == string(projectingestion.RunStatusFailed) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for ingestion run %s", runID)
		case <-ticker.C:
		}
	}
}

func postMCP(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpapi.ProtocolVersion)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

type fakeDiagnosticsSnapshotter struct {
	snapshot projectingestion.DiagnosticsSnapshot
}

func (fake fakeDiagnosticsSnapshotter) IngestionDiagnostics() projectingestion.DiagnosticsSnapshot {
	return fake.snapshot
}

type failingGitRunner struct{}

func (failingGitRunner) Run(context.Context, string, int, ...string) ([]byte, bool, error) {
	return nil, false, projectworkspace.ErrGitUnavailable
}

type topLevelWorkPlanAPI struct{}

func (topLevelWorkPlanAPI) CallWorkPlanTool(_ context.Context, name string, arguments json.RawMessage) (any, error) {
	var input map[string]any
	if err := json.Unmarshal(arguments, &input); err != nil {
		return nil, err
	}
	projectID, _ := input["id"].(string)
	result := map[string]any{
		"project_id":       projectID,
		"plan_id":          "plan-1",
		"task_id":          "task-1",
		"status":           "active",
		"updated_at":       "2026-06-03T00:00:00Z",
		"safe_next_action": "continue work plan workflow",
	}
	if strings.Contains(name, "work_tasks") {
		if taskID, _ := input["task_id"].(string); taskID != "" {
			result["task_id"] = taskID
		}
		result["evidence_needed"] = []string{"source evidence"}
		result["context_pack_refs"] = []string{}
		result["likely_files_affected"] = []string{"internal/agentcontrol/mcpapi/mcpapi.go"}
		result["dependency_task_ids"] = []string{}
		result["verification_requirement"] = "focused MCP routing test"
		result["resume_instructions"] = "continue from top-level route"
		result["claim_refs"] = []string{}
		result["evidence_refs"] = []string{}
		result["verifier_result_refs"] = []string{}
		result["knowledge_candidate_refs"] = []string{}
	}
	if name == "projects.work_plans.create" {
		result["plan_ref"] = input["plan_ref"]
	}
	return result, nil
}

var _ workplanmcpapi.API = topLevelWorkPlanAPI{}
