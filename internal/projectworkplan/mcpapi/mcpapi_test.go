package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolDefinitionsExposeAllWorkPlanTools(t *testing.T) {
	encoded, err := json.Marshal(ToolDefinitions())
	if err != nil {
		t.Fatalf("marshal definitions: %v", err)
	}
	for _, definition := range ToolDefinitions() {
		name, _ := definition["name"].(string)
		schema, ok := definition["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("missing schema for %v", name)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("missing schema properties for %v", name)
		}
		if _, ok := properties["project_id"]; !ok {
			t.Fatalf("%v schema does not expose project_id alias", name)
		}
		for _, forbidden := range []string{"anyOf", "oneOf", "allOf", "not"} {
			if _, ok := schema[forbidden]; ok {
				t.Fatalf("%v schema exposes top-level %s", name, forbidden)
			}
		}
		if schema["type"] != "object" {
			t.Fatalf("%v schema must be a top-level object, got %#v", name, schema["type"])
		}
		if _, ok := schema["enum"]; ok {
			t.Fatalf("%v schema exposes top-level enum", name)
		}
		required, _ := schema["required"].([]string)
		if required == nil {
			if raw, ok := schema["required"].([]any); ok {
				for _, value := range raw {
					if text, ok := value.(string); ok {
						required = append(required, text)
					}
				}
			}
		}
		if !containsString(required, "id") {
			t.Fatalf("%v schema must require id for project-scoped calls, required=%#v", name, required)
		}
	}
	for _, name := range workPlanTools {
		if !bytes.Contains(encoded, []byte(`"name":"`+name+`"`)) {
			t.Fatalf("missing tool %s in %s", name, string(encoded))
		}
		if !IsWorkPlanTool(name) || !IsWorkPlanTool(strings.ReplaceAll(name, ".", "_")) {
			t.Fatalf("expected dotted and underscore aliases for %s", name)
		}
	}
	for _, required := range []string{"MUST", "Safety:", "Next tool:", "Must not", `"additionalProperties":false`} {
		if !bytes.Contains(encoded, []byte(required)) {
			t.Fatalf("tool descriptions/schemas missing %q in %s", required, string(encoded))
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCallToolPlanLifecycleReturnsMetadataOnlyShape(t *testing.T) {
	api := newFakeWorkPlanAPI()
	plan := call(t, api, "projects.work_plans.create", `{"project_id":"example-service","plan_ref":"plan/ref","title":"MCP surface","goal_summary":"Expose metadata-only work plan tools","owner_agent":"worker-4","isolation_mode":"dedicated_worktree","parallel_group_ref":"parallel/ref","workspace_ref":"workspace/ref","git_base_ref":"main","git_branch_ref":"codex/mcp-plan","git_worktree_ref":"worktree/mcp-plan"}`)
	planID := structuredString(t, plan, "plan_id")
	list := call(t, api, "projects.work_plans.list", `{"project_id":"example-service"}`)
	got := call(t, api, "projects.work_plans.get", `{"project_id":"example-service","plan_id":"`+planID+`"}`)
	updated := call(t, api, "projects.work_plans.update_status", `{"project_id":"example-service","plan_id":"`+planID+`","status":"active","safe_next_action":"create bounded tasks","outcome":"plan ready for implementation"}`)

	for _, result := range []map[string]any{plan, list, got, updated} {
		requireCommonOutput(t, result)
		for _, forbidden := range []string{"raw prompt", "package main", "provider payload", "/home/mac", "token=secret"} {
			if bytes.Contains(jsonText(t, result), []byte(forbidden)) {
				t.Fatalf("response leaked forbidden marker %q: %s", forbidden, jsonText(t, result))
			}
		}
	}
	if structuredString(t, updated, "status") != "active" {
		t.Fatalf("expected active status, got %#v", updated)
	}
	if structuredString(t, updated, "outcome") != "plan ready for implementation" {
		t.Fatalf("expected outcome to persist, got %#v", updated)
	}
	if structuredString(t, plan, "git_worktree_ref") != "worktree/mcp-plan" || structuredString(t, plan, "isolation_mode") != "dedicated_worktree" {
		t.Fatalf("expected worktree isolation metadata, got %s", jsonText(t, plan))
	}
}

func TestCallToolTaskLifecycleAttachmentsResumeAndNext(t *testing.T) {
	api := newFakeWorkPlanAPI()
	plan := call(t, api, "projects.work_plans.create", `{"id":"example-service","plan_ref":"plan/ref","title":"MCP surface","goal_summary":"Expose metadata-only work plan tools"}`)
	planID := structuredString(t, plan, "plan_id")
	taskBody := `{"id":"example-service","plan_id":"` + planID + `","task_ref":"task/ref","title":"Wire MCP","description":"Files to read include internal/projectworkplan/mcpapi/mcpapi.go.","status":"ready","evidence_needed":["source patterns"],"files_to_read":["internal/projectworkplan/mcpapi/mcpapi.go"],"files_to_edit":["internal/projectworkplan/mcpapi/mcpapi.go"],"likely_files_affected":["internal/agentcontrol/mcpapi/mcpapi.go"],"review_gate":"independent review required before completion","decomposition_quality":"ready","verification_requirement":"focused MCP tests","resume_instructions":"continue from tool routing"}`
	task := call(t, api, "projects.work_tasks.create", taskBody)
	taskID := structuredString(t, task, "task_id")
	gotTask := call(t, api, "projects.work_tasks.get", `{"id":"example-service","task_id":"`+taskID+`"}`)
	stale := call(t, api, "projects.work_tasks.create", `{"id":"example-service","plan_id":"`+planID+`","task_ref":"task/stale","title":"Stale metadata","evidence_needed":["workplan-list"],"likely_files_affected":["mcp-workplan-metadata"],"verification_requirement":"confirm cancellation","resume_instructions":"no execution needed","expected_output":"stale task cancelled","failure_block_criteria":"status update rejected"}`)
	staleID := structuredString(t, stale, "task_id")
	cancelled := call(t, api, "projects.work_tasks.update_status", `{"id":"example-service","task_id":"`+staleID+`","status":"cancelled","safe_next_action":"stale metadata retained"}`)

	claim := call(t, api, "projects_work_tasks_claim", `{"id":"example-service","task_id":"`+taskID+`","owner_agent":"worker-4","run_id":"run-1"}`)
	start := call(t, api, "projects.work_tasks.start", `{"id":"example-service","task_id":"`+taskID+`","run_id":"run-1","trace_id":"trace-1","context_pack_refs":["context-pack:manifest:68c3ee2ad1556459"]}`)
	evidence := call(t, api, "projects.work_tasks.attach_evidence", `{"id":"example-service","task_id":"`+taskID+`","evidence_ref":"evidence-1"}`)
	contextPack := call(t, api, "projects.work_tasks.attach_context_pack", `{"id":"example-service","task_id":"`+taskID+`","context_pack_ref":"context-pack-2"}`)
	claimRef := call(t, api, "projects.work_tasks.attach_claim", `{"id":"example-service","task_id":"`+taskID+`","claim_ref":"claim-1"}`)
	verifier := call(t, api, "projects.work_tasks.attach_verifier_result", `{"id":"example-service","task_id":"`+taskID+`","verifier_result_ref":"go-test-focused","status":"passed"}`)
	review := call(t, api, "projects.work_tasks.attach_review_result", `{"id":"example-service","task_id":"`+taskID+`","review_result_ref":"review-focused","status":"passed","attached_by_run_id":"agent_run_review"}`)
	candidate := call(t, api, "projects.work_tasks.promote_knowledge_candidate", `{"id":"example-service","task_id":"`+taskID+`","knowledge_candidate_ref":"knowledge-candidate-1","claim_refs":["claim-1"],"evidence_refs":["evidence-1"],"confidence_ref":"confidence-1","verifier_result_refs":["go-test-focused"]}`)
	complete := call(t, api, "projects.work_tasks.complete", `{"id":"example-service","task_id":"`+taskID+`","outcome":"MCP tools wired","safe_next_action":"get next task","verifier_result_refs":["go-test-focused"],"review_result_refs":["review-focused"],"claim_refs":["claim-1"],"evidence_refs":["evidence-1"],"knowledge_candidate_refs":["knowledge-candidate-1"]}`)
	next := call(t, api, "projects.work_tasks.get_next", `{"project_id":"example-service","plan_id":"`+planID+`"}`)
	resume := call(t, api, "projects.work_plans.resume", `{"project_id":"example-service","plan_id":"`+planID+`","owner_agent":"worker-4"}`)

	for _, result := range []map[string]any{task, gotTask, stale, cancelled, claim, start, evidence, contextPack, claimRef, verifier, review, candidate, complete, next, resume} {
		requireCommonOutput(t, result)
	}
	if structuredString(t, gotTask, "task_id") != taskID {
		t.Fatalf("expected get task %s, got %s", taskID, jsonText(t, gotTask))
	}
	for _, want := range []string{"files_to_read", "files_to_edit", "review_gate", "internal/projectworkplan/mcpapi/mcpapi.go"} {
		if !bytes.Contains(jsonText(t, gotTask), []byte(want)) {
			t.Fatalf("expected task metadata %q in get response: %s", want, jsonText(t, gotTask))
		}
	}
	if structuredString(t, cancelled, "status") != "cancelled" {
		t.Fatalf("expected cancelled stale task, got %#v", cancelled)
	}
	requireTaskOutput(t, complete)
	if structuredString(t, complete, "status") != "done" {
		t.Fatalf("expected completed task, got %#v", complete)
	}
	if !bytes.Contains(jsonText(t, candidate), []byte("knowledge-candidate-1")) || bytes.Contains(jsonText(t, candidate), []byte(`"promotion_state":"promoted"`)) {
		t.Fatalf("candidate tool must link candidate only without promotion: %s", jsonText(t, candidate))
	}
	if !bytes.Contains(jsonText(t, resume), []byte(`"current_task"`)) || !bytes.Contains(jsonText(t, next), []byte(`"safe_next_action"`)) {
		t.Fatalf("resume/get_next missing workflow metadata: resume=%s next=%s", jsonText(t, resume), jsonText(t, next))
	}
}

func TestCallToolBlockAndLists(t *testing.T) {
	api := newFakeWorkPlanAPI()
	plan := call(t, api, "projects.work_plans.create", `{"id":"example-service","plan_ref":"plan/ref","title":"MCP surface","goal_summary":"Expose metadata-only work plan tools"}`)
	planID := structuredString(t, plan, "plan_id")
	task := call(t, api, "projects.work_tasks.create", `{"id":"example-service","plan_id":"`+planID+`","task_ref":"task/ref","title":"Wire MCP","evidence_needed":["source patterns"],"verification_requirement":"focused MCP tests","resume_instructions":"continue from tool routing"}`)
	taskID := structuredString(t, task, "task_id")

	blocked := call(t, api, "projects.work_tasks.block", `{"id":"example-service","task_id":"`+taskID+`","blocked_reason":"waiting for service package","resume_instructions":"re-run after service worker lands","safe_next_action":"list blocked tasks"}`)
	listBlocked := call(t, api, "projects_work_tasks_list_blocked", `{"id":"example-service","plan_id":"`+planID+`"}`)
	listAlias := call(t, api, "projects.work_tasks.list", `{"id":"example-service","plan_id":"`+planID+`"}`)
	listOpen := call(t, api, "projects.work_tasks.list_open", `{"id":"example-service","plan_id":"`+planID+`"}`)
	listMine := call(t, api, "projects.work_tasks.list_mine", `{"id":"example-service","owner_agent":"worker-4"}`)

	for _, result := range []map[string]any{blocked, listBlocked, listAlias, listOpen, listMine} {
		requireCommonOutput(t, result)
	}
	if structuredString(t, blocked, "status") != "blocked" || !bytes.Contains(jsonText(t, listBlocked), []byte(taskID)) {
		t.Fatalf("expected blocked task in list: blocked=%s list=%s", jsonText(t, blocked), jsonText(t, listBlocked))
	}
}

func TestCallToolCreateTaskAllowsRichMetadataAndSafetyGuidance(t *testing.T) {
	api := newFakeWorkPlanAPI()
	plan := call(t, api, "projects.work_plans.create", `{"id":"example-service","plan_ref":"plan/ref","title":"MCP surface","goal_summary":"Expose metadata-only work plan tools"}`)
	planID := structuredString(t, plan, "plan_id")
	task := call(t, api, "projects.work_tasks.create", `{"id":"example-service","plan_id":"`+planID+`","task_ref":"task/rich","title":"Prepare isolated fixture","description":"Metadata-only task for data/dashboard-operations-redesign-plan-2026-06-04.md.","status":"ready","owner_agent":"gpt-5.5-low-worker","evidence_needed":["current source and focused verifier refs"],"context_pack_refs":["context-pack:manifest:68c3ee2ad1556459"],"files_to_read":["data/dashboard-operations-redesign-plan-2026-06-04.md"],"files_to_edit":["internal/dashboard/httpapi/assets/app.js"],"likely_files_affected":["tmp/mivia-workplan-smoke"],"verification_requirement":"orchestrator runs focused tests after worker output is reviewed","resume_instructions":"resume by claiming the next ready task and reading attached refs","expected_output":"safe metadata-only task output","failure_criteria":"block if metadata would expose credentials, roots, paths, raw prompts, source dumps, or provider payloads","review_gate":"independent reviewer must pass before done","knowledge_candidate_expectation":"none for smoke test","decomposition_quality":"ready","run_id":"agent_run_1","trace_id":"trace_1"}`)
	if structuredString(t, task, "task_id") == "" {
		t.Fatalf("expected task id in rich create response: %s", jsonText(t, task))
	}
	for _, want := range []string{"files_to_read", "files_to_edit", "review_gate", "data/dashboard-operations-redesign-plan-2026-06-04.md"} {
		if !bytes.Contains(jsonText(t, task), []byte(want)) {
			t.Fatalf("expected rich metadata %q in response: %s", want, jsonText(t, task))
		}
	}
}

func TestCallToolCreateTaskAllowsHiddenDirectoryPathMetadata(t *testing.T) {
	api := newFakeWorkPlanAPI()
	plan := call(t, api, "projects.work_plans.create", `{"id":"example-service","plan_ref":"plan/ref","title":"MCP surface","goal_summary":"Expose metadata-only work plan tools"}`)
	planID := structuredString(t, plan, "plan_id")
	task := call(t, api, "projects.work_tasks.create", `{"id":"example-service","plan_id":"`+planID+`","task_ref":"task/docs","title":"Update docs","description":"Docs-only metadata task with project-relative paths.","status":"ready","evidence_needed":["current docs"],"files_to_read":[".ai/skills/mivia-mcp/SKILL.md",".devcontainer/docker-compose.mivia.example.yml","configs/mivia-server.example.toml"],"files_to_edit":[".ai/skills/mivia-mcp/SKILL.md",".devcontainer/docker-compose.mivia.example.yml","configs/mivia-server.example.toml"],"likely_files_affected":[".ai/skills/mivia-mcp/SKILL.md",".devcontainer/docker-compose.mivia.example.yml","configs/mivia-server.example.toml"],"verification_requirement":"diff check","resume_instructions":"update docs and verify","decomposition_quality":"ready"}`)
	if structuredString(t, task, "task_id") == "" {
		t.Fatalf("expected task id in hidden-path create response: %s", jsonText(t, task))
	}
	for _, want := range []string{".ai/skills/mivia-mcp/SKILL.md", ".devcontainer/docker-compose.mivia.example.yml", "configs/mivia-server.example.toml"} {
		if !bytes.Contains(jsonText(t, task), []byte(want)) {
			t.Fatalf("expected hidden path metadata %q in response: %s", want, jsonText(t, task))
		}
	}
}

func TestCallToolRejectsUnknownFieldsAndUnsafePayloads(t *testing.T) {
	api := newFakeWorkPlanAPI()
	if _, err := CallTool(context.Background(), api, "projects.work_plans.create", json.RawMessage(`{"id":"example-service","plan_ref":"plan/ref","title":"MCP surface","goal_summary":"bounded metadata","query":"MATCH (n)"}`)); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if _, err := CallTool(context.Background(), api, "projects.work_tasks.create", json.RawMessage(`{"id":"example-service","plan_id":"plan-1","task_ref":"task/ref","title":"Unsafe","evidence_needed":["raw prompt: token=secret"],"verification_requirement":"focused tests","resume_instructions":"continue"}`)); err == nil {
		t.Fatal("expected unsafe payload rejection")
	}
}

func call(t *testing.T, api API, name string, body string) map[string]any {
	t.Helper()
	result, err := CallTool(context.Background(), api, name, json.RawMessage(body))
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func requireCommonOutput(t *testing.T, result map[string]any) {
	t.Helper()
	for _, field := range []string{"project_id", "status", "updated_at", "safe_next_action"} {
		if structuredString(t, result, field) == "" {
			t.Fatalf("missing common field %s in %s", field, jsonText(t, result))
		}
	}
	content := result["content"].([]map[string]string)
	if len(content) != 1 || content[0]["type"] != "text" || !json.Valid([]byte(content[0]["text"])) {
		t.Fatalf("expected JSON text content, got %#v", result["content"])
	}
}

func requireTaskOutput(t *testing.T, result map[string]any) {
	t.Helper()
	encoded := jsonText(t, result)
	for _, field := range []string{"evidence_needed", "context_pack_refs", "likely_files_affected", "dependency_task_ids", "verification_requirement", "resume_instructions", "claim_refs", "evidence_refs", "verifier_result_refs", "review_result_refs", "knowledge_candidate_refs"} {
		if !bytes.Contains(encoded, []byte(`"`+field+`"`)) {
			t.Fatalf("missing task field %s in %s", field, encoded)
		}
	}
}

func structuredString(t *testing.T, result map[string]any, field string) string {
	t.Helper()
	var object map[string]any
	if err := remarshal(result["structuredContent"], &object); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	value, _ := object[field].(string)
	return value
}

func jsonText(t *testing.T, result map[string]any) []byte {
	t.Helper()
	content := result["content"].([]map[string]string)
	return []byte(content[0]["text"])
}

func remarshal(source any, target any) error {
	encoded, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

type fakeWorkPlanAPI struct {
	now     string
	plans   map[string]map[string]any
	tasks   map[string]map[string]any
	nextID  int
	lastErr error
}

func newFakeWorkPlanAPI() *fakeWorkPlanAPI {
	return &fakeWorkPlanAPI{now: "2026-06-03T00:00:00Z", plans: map[string]map[string]any{}, tasks: map[string]map[string]any{}}
}

func (api *fakeWorkPlanAPI) CallWorkPlanTool(_ context.Context, name string, arguments json.RawMessage) (any, error) {
	var input map[string]any
	if err := json.Unmarshal(arguments, &input); err != nil {
		return nil, err
	}
	projectID, _ := input["id"].(string)
	switch name {
	case "projects.work_plans.create":
		api.nextID++
		planID := "plan-1"
		plan := api.common(projectID, planID, "", "planned", "create bounded work tasks")
		plan["id"] = planID
		plan["plan_ref"] = input["plan_ref"]
		plan["title"] = input["title"]
		plan["goal_summary"] = input["goal_summary"]
		plan["current_task_id"] = ""
		for _, key := range []string{"isolation_mode", "parallel_group_ref", "workspace_ref", "git_base_ref", "git_branch_ref", "git_worktree_ref"} {
			if value, ok := input[key]; ok {
				plan[key] = value
			}
		}
		api.plans[planID] = plan
		return plan, nil
	case "projects.work_plans.get":
		return api.plan(input)
	case "projects.work_plans.list":
		return api.commonList(projectID, "listed", "resume a plan", "plans", maps(api.plans)), nil
	case "projects.work_plans.update_status":
		plan, err := api.plan(input)
		if err != nil {
			return nil, err
		}
		plan["status"] = input["status"]
		plan["safe_next_action"] = input["safe_next_action"]
		plan["outcome"] = input["outcome"]
		return plan, nil
	case "projects.work_plans.resume":
		plan, _ := api.plan(input)
		return map[string]any{"project_id": projectID, "plan_id": plan["plan_id"], "status": plan["status"], "updated_at": api.now, "safe_next_action": "claim next ready task", "current_plan": plan, "current_task": firstMap(api.tasks), "open_mine": maps(api.tasks), "blocked_summary": mapsByStatus(api.tasks, "blocked"), "next_task": firstReady(api.tasks)}, nil
	case "projects.work_tasks.create":
		taskID := fmtID("task", len(api.tasks)+1)
		task := api.common(projectID, stringValue(input, "plan_id"), taskID, "ready", "claim task before editing")
		task["id"] = taskID
		task["task_ref"] = input["task_ref"]
		task["title"] = input["title"]
		task["description"] = input["description"]
		task["evidence_needed"] = arrayValue(input, "evidence_needed")
		task["context_pack_refs"] = arrayValue(input, "context_pack_refs")
		task["files_to_read"] = arrayValue(input, "files_to_read")
		task["files_to_edit"] = arrayValue(input, "files_to_edit")
		task["likely_files_affected"] = arrayValue(input, "likely_files_affected")
		task["dependency_task_ids"] = arrayValue(input, "dependency_task_ids")
		task["verification_requirement"] = input["verification_requirement"]
		task["resume_instructions"] = input["resume_instructions"]
		task["expected_output"] = input["expected_output"]
		task["failure_criteria"] = input["failure_criteria"]
		task["review_gate"] = input["review_gate"]
		task["decomposition_quality"] = input["decomposition_quality"]
		task["claim_refs"] = []string{}
		task["evidence_refs"] = []string{}
		task["verifier_result_refs"] = []string{}
		task["review_result_refs"] = []string{}
		task["knowledge_candidate_refs"] = []string{}
		api.tasks[taskID] = task
		if plan := api.plans[stringValue(input, "plan_id")]; plan != nil {
			plan["current_task_id"] = taskID
		}
		return task, nil
	case "projects.work_tasks.get":
		task := api.tasks[stringValue(input, "task_id")]
		if task == nil {
			return nil, ErrNotFound
		}
		return task, nil
	case "projects.work_tasks.claim":
		return api.updateTask(input, "claimed", "start task execution", map[string]any{"owner_agent": input["owner_agent"], "claimed_by_run_id": input["run_id"]})
	case "projects.work_tasks.release":
		return api.updateTask(input, "ready", "get next task", nil)
	case "projects.work_tasks.start":
		return api.updateTask(input, "in_progress", "attach evidence or verifier refs", map[string]any{"agent_run_ids": []any{input["run_id"]}, "context_pack_refs": arrayValue(input, "context_pack_refs")})
	case "projects.work_tasks.update_status":
		return api.updateTask(input, stringValue(input, "status"), stringValue(input, "safe_next_action"), map[string]any{"outcome": input["outcome"], "verifier_result_refs": arrayValue(input, "verifier_result_refs"), "review_result_refs": arrayValue(input, "review_result_refs"), "review_exempt_reason": input["review_exempt_reason"], "claim_refs": arrayValue(input, "claim_refs"), "evidence_refs": arrayValue(input, "evidence_refs"), "knowledge_candidate_refs": arrayValue(input, "knowledge_candidate_refs")})
	case "projects.work_tasks.complete":
		return api.updateTask(input, "done", stringValue(input, "safe_next_action"), map[string]any{"outcome": input["outcome"], "verifier_result_refs": arrayValue(input, "verifier_result_refs"), "review_result_refs": arrayValue(input, "review_result_refs"), "review_exempt_reason": input["review_exempt_reason"], "claim_refs": arrayValue(input, "claim_refs"), "evidence_refs": arrayValue(input, "evidence_refs"), "knowledge_candidate_refs": arrayValue(input, "knowledge_candidate_refs")})
	case "projects.work_tasks.fail":
		return api.updateTask(input, "failed", stringValue(input, "safe_next_action"), map[string]any{"outcome": input["outcome"]})
	case "projects.work_tasks.block":
		return api.updateTask(input, "blocked", stringValue(input, "safe_next_action"), map[string]any{"blocked_reason": input["blocked_reason"], "resume_instructions": input["resume_instructions"], "blocked_by_task_ids": arrayValue(input, "blocked_by_task_ids")})
	case "projects.work_tasks.list", "projects.work_tasks.list_open":
		return api.commonList(projectID, "listed", "claim next ready task", "tasks", maps(api.tasks)), nil
	case "projects.work_tasks.list_mine":
		return api.commonList(projectID, "listed", "continue claimed task", "tasks", maps(api.tasks)), nil
	case "projects.work_tasks.list_blocked":
		return api.commonList(projectID, "listed", "unblock or get next safe task", "tasks", mapsByStatus(api.tasks, "blocked")), nil
	case "projects.work_tasks.get_next":
		next := firstReady(api.tasks)
		return map[string]any{"project_id": projectID, "plan_id": stringValue(input, "plan_id"), "task_id": stringValue(next, "task_id"), "status": "ready", "updated_at": api.now, "safe_next_action": "claim returned task", "task": next}, nil
	case "projects.work_tasks.attach_evidence":
		return api.appendTaskRef(input, "evidence_refs", "evidence_ref")
	case "projects.work_tasks.attach_context_pack":
		return api.appendTaskRef(input, "context_pack_refs", "context_pack_ref")
	case "projects.work_tasks.attach_claim":
		return api.appendTaskRef(input, "claim_refs", "claim_ref")
	case "projects.work_tasks.attach_verifier_result":
		return api.appendTaskRef(input, "verifier_result_refs", "verifier_result_ref")
	case "projects.work_tasks.attach_review_result":
		return api.appendTaskRef(input, "review_result_refs", "review_result_ref")
	case "projects.work_tasks.promote_knowledge_candidate":
		return api.appendTaskRef(input, "knowledge_candidate_refs", "knowledge_candidate_ref")
	default:
		return nil, ErrNotFound
	}
}

func (api *fakeWorkPlanAPI) common(projectID string, planID string, taskID string, status string, next string) map[string]any {
	return map[string]any{"project_id": projectID, "plan_id": planID, "task_id": taskID, "status": status, "updated_at": api.now, "safe_next_action": next}
}

func (api *fakeWorkPlanAPI) commonList(projectID string, status string, next string, key string, values []map[string]any) map[string]any {
	return map[string]any{"project_id": projectID, "status": status, "updated_at": api.now, "safe_next_action": next, key: values}
}

func (api *fakeWorkPlanAPI) plan(input map[string]any) (map[string]any, error) {
	plan := api.plans[stringValue(input, "plan_id")]
	if plan == nil {
		return nil, ErrNotFound
	}
	return plan, nil
}

func (api *fakeWorkPlanAPI) updateTask(input map[string]any, status string, next string, extra map[string]any) (map[string]any, error) {
	task := api.tasks[stringValue(input, "task_id")]
	if task == nil {
		return nil, ErrNotFound
	}
	task["status"] = status
	task["safe_next_action"] = next
	for key, value := range extra {
		task[key] = value
	}
	return task, nil
}

func (api *fakeWorkPlanAPI) appendTaskRef(input map[string]any, field string, inputField string) (map[string]any, error) {
	task := api.tasks[stringValue(input, "task_id")]
	if task == nil {
		return nil, ErrNotFound
	}
	refs, _ := task[field].([]string)
	refs = append(refs, stringValue(input, inputField))
	task[field] = refs
	task["safe_next_action"] = "continue workflow"
	return task, nil
}

func maps(values map[string]map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func mapsByStatus(values map[string]map[string]any, status string) []map[string]any {
	out := []map[string]any{}
	for _, value := range values {
		if value["status"] == status {
			out = append(out, value)
		}
	}
	return out
}

func firstMap(values map[string]map[string]any) map[string]any {
	for _, value := range values {
		return value
	}
	return map[string]any{}
}

func firstReady(values map[string]map[string]any) map[string]any {
	for _, value := range values {
		if value["status"] == "ready" {
			return value
		}
	}
	return map[string]any{}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func arrayValue(values map[string]any, key string) []any {
	raw, ok := values[key].([]any)
	if !ok {
		return []any{}
	}
	return raw
}

func fmtID(prefix string, number int) string {
	return prefix + "-" + string(rune('0'+number))
}
