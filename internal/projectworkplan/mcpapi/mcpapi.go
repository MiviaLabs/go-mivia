package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrInvalidInput = errors.New("invalid project work plan input")
	ErrNotFound     = errors.New("project work plan resource not found")
)

type API interface {
	CallWorkPlanTool(ctx context.Context, name string, arguments json.RawMessage) (any, error)
}

var workPlanTools = []string{
	"projects.work_plans.create",
	"projects.work_plans.get",
	"projects.work_plans.list",
	"projects.work_plans.update_status",
	"projects.work_plans.resume",
	"projects.work_tasks.create",
	"projects.work_tasks.get",
	"projects.work_tasks.update_status",
	"projects.work_tasks.claim",
	"projects.work_tasks.release",
	"projects.work_tasks.start",
	"projects.work_tasks.complete",
	"projects.work_tasks.fail",
	"projects.work_tasks.block",
	"projects.work_tasks.list",
	"projects.work_tasks.list_open",
	"projects.work_tasks.list_mine",
	"projects.work_tasks.list_blocked",
	"projects.work_tasks.get_next",
	"projects.work_tasks.attach_evidence",
	"projects.work_tasks.attach_context_pack",
	"projects.work_tasks.attach_claim",
	"projects.work_tasks.attach_verifier_result",
	"projects.work_tasks.attach_review_result",
	"projects.work_tasks.promote_knowledge_candidate",
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
	longText := map[string]any{"type": "string", "minLength": 1, "maxLength": 1200}
	optionalText := map[string]any{"type": "string", "maxLength": 1200}
	reviewExemptReason := map[string]any{"type": "string", "maxLength": 300}
	refArray := map[string]any{"type": "array", "items": ref, "maxItems": 100}
	fileArray := map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 300}, "maxItems": 100}
	pageFields := map[string]any{
		"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"page_token": map[string]any{"type": "string", "maxLength": 20},
	}
	tools := []map[string]any{
		tool("projects.work_plans.create", "Create Work Plan", "MUST be used before multi-step project work when no active Work Plan exists. Prior state: project id and safe request summary refs are known. Required fields: id, plan_ref, title, and goal_summary, then create explicit Work Tasks. Safety: metadata-only; never store raw user prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, or PII. Next tool: projects.work_tasks.create. Must not be used as a loose narrative or AgentRun replacement.",
			schema(map[string]any{"id": ref, "plan_ref": ref, "user_request_ref": ref, "title": text, "goal_summary": longText, "owner_agent": ref, "created_by_run_id": ref, "trace_id": ref, "resume_summary": optionalText, "isolation_mode": map[string]any{"type": "string", "enum": []string{"shared", "dedicated_worktree", "unavailable"}}, "parallel_group_ref": ref, "workspace_ref": ref, "git_base_ref": ref, "git_branch_ref": ref, "git_worktree_ref": ref}, []string{"id", "plan_ref", "title", "goal_summary"})),
		tool("projects.work_plans.get", "Get Work Plan", "MUST be used to inspect one existing Work Plan before changing plan state or task state. Prior state: project id and plan_id are known. Required fields: id and plan_id. Safety: return metadata and safe next action only; no raw prompt/source/log/provider material. Next tool: projects.work_plans.resume or projects.work_tasks.get_next. Must not infer task ownership from chat history.",
			schema(map[string]any{"id": ref, "plan_id": ref}, []string{"id", "plan_id"})),
		tool("projects.work_plans.list", "List Work Plans", "MUST be used to find candidate Work Plans for a project before creating duplicates. Prior state: project id is known. Required fields: id; optional status, owner_agent, page_size, and page_token. Safety: metadata-only filters and summaries. Next tool: projects.work_plans.get or projects.work_plans.resume. Must not scan raw storage or external systems.",
			schema(merge(map[string]any{"id": ref, "status": statusSchema(planStatuses()), "owner_agent": ref}, pageFields), []string{"id"})),
		tool("projects.work_plans.update_status", "Update Work Plan Status", "MUST be used when a Work Plan lifecycle state changes after task evidence supports it. Prior state: projects.work_plans.get or resume has identified the plan. Required fields: id, plan_id, status, and safe_next_action. Normal lifecycle is planned -> active -> done; do not jump planned -> done. Safety: bounded metadata only; do not reopen terminal states outside service transition rules. Next tool: projects.work_tasks.get_next or projects.work_tasks.list_open. Must not complete AgentRun or Work Task implicitly.",
			schema(map[string]any{"id": ref, "plan_id": ref, "status": statusSchema(planStatuses()), "safe_next_action": text, "resume_summary": optionalText, "outcome": longText, "run_id": ref, "trace_id": ref}, []string{"id", "plan_id", "status", "safe_next_action"})),
		tool("projects.work_plans.resume", "Resume Work Plan", "MUST be used when an agent resumes a session, enters an existing project, or asks what was happening. Prior state: project id is known; plan_id is optional if service can select the active plan. Required fields: id. Safety: return current plan, current task, open mine, blocked summary, and next safe task hint as metadata only. Next tool: projects.work_tasks.get_next or projects.work_tasks.list_mine. Must not rely on prior chat memory.",
			schema(map[string]any{"id": ref, "plan_id": ref, "owner_agent": ref, "run_id": ref, "trace_id": ref}, []string{"id"})),
		tool("projects.work_tasks.create", "Create Work Task", "MUST create small dependency-aware tasks suitable for an isolated low-intelligence worker. Prior state: a Work Plan exists. Required fields: id, plan_id, task_ref, title, evidence_needed, likely_files_affected or discovery scope in description, verification_requirement, and resume_instructions. The task must be executable from its metadata and attached refs alone, without prior chat memory or hidden orchestrator context; verification must be written so the orchestrator can run it independently. Safety: refs and bounded metadata only; reject broad or vague work. Next tool: projects.work_tasks.claim after dependencies are ready. Must not store raw context pack contents or source dumps.",
			schema(map[string]any{"id": ref, "plan_id": ref, "task_ref": ref, "title": text, "description": optionalText, "status": statusSchema(nonTerminalTaskStatuses()), "owner_agent": ref, "evidence_needed": refArray, "context_pack_refs": refArray, "files_to_read": fileArray, "files_to_edit": fileArray, "likely_files_affected": fileArray, "dependency_task_ids": refArray, "verification_requirement": longText, "resume_instructions": longText, "expected_output": optionalText, "failure_criteria": optionalText, "failure_block_criteria": optionalText, "review_gate": optionalText, "knowledge_candidate_expectation": optionalText, "decomposition_quality": statusSchema(decompositionQualities()), "run_id": ref, "trace_id": ref}, []string{"id", "plan_id", "task_ref", "title", "evidence_needed", "verification_requirement", "resume_instructions"})),
		tool("projects.work_tasks.get", "Get Work Task", "MUST be used to inspect one existing Work Task before changing task state, attaching refs, or resuming task execution. Prior state: project id and task_id are known. Required fields: id and task_id. Safety: return bounded task metadata only; no raw prompt/source/log/provider material. Next tool: projects.work_tasks.claim, start, update_status, or complete according to lifecycle.",
			schema(map[string]any{"id": ref, "task_id": ref}, []string{"id", "task_id"})),
		tool("projects.work_tasks.update_status", "Update Work Task Status", "MUST be used when a Work Task lifecycle state changes outside claim/start/complete/fail/block helpers, especially to cancel or supersede stale planned metadata. Prior state: projects.work_tasks.get or list_open identified the task. Required fields: id, task_id, status, and safe_next_action. Normal lifecycle is planned -> ready -> claimed -> in_progress -> needs_review -> verifying -> done; do not jump planned -> done. Safety: bounded metadata only; do not bypass verifier, independent review, Evidence Graph, confidence, or knowledge-decision requirements for done tasks. Next tool: projects.work_tasks.get_next or list_open.",
			schema(map[string]any{"id": ref, "task_id": ref, "status": statusSchema(taskStatuses()), "safe_next_action": text, "outcome": longText, "blocked_reason": longText, "resume_instructions": longText, "blocked_by_task_ids": refArray, "verifier_result_refs": refArray, "review_result_refs": refArray, "review_exempt_reason": reviewExemptReason, "claim_refs": refArray, "evidence_refs": refArray, "knowledge_candidate_refs": refArray, "owner_agent": ref, "run_id": ref, "trace_id": ref}, []string{"id", "task_id", "status", "safe_next_action"})),
		tool("projects.work_tasks.claim", "Claim Work Task", "MUST be called before an agent edits files or executes a task. Prior state: task is ready and dependencies are satisfied. Required fields: id, task_id, owner_agent, and run_id. Safety: prevents duplicate agent work; metadata-only owner/run refs. Next tool: projects.work_tasks.start. Must not override another claim unless the service explicitly allows it.",
			schema(map[string]any{"id": ref, "task_id": ref, "owner_agent": ref, "run_id": ref, "trace_id": ref}, []string{"id", "task_id", "owner_agent", "run_id"})),
		tool("projects.work_tasks.release", "Release Work Task", "MUST be used when a claimed task is intentionally returned to the ready queue. Prior state: task is claimed by the caller or service permits release. Required fields: id, task_id, and owner_agent. Safety: bounded reason metadata only. Next tool: projects.work_tasks.get_next. Must not hide blocked or failed work.",
			schema(map[string]any{"id": ref, "task_id": ref, "owner_agent": ref, "reason": optionalText, "run_id": ref, "trace_id": ref}, []string{"id", "task_id", "owner_agent"})),
		tool("projects.work_tasks.start", "Start Work Task", "MUST be called when execution starts after claim. Prior state: projects.work_tasks.claim succeeded. Required fields: id and task_id; include run_id, trace_id, and context_pack_refs when indexed context is used. Safety: context packs are refs only. Next tool: attach evidence/context/claim as needed, then complete, block, or fail. Must not begin unclaimed execution.",
			schema(map[string]any{"id": ref, "task_id": ref, "run_id": ref, "trace_id": ref, "context_pack_refs": refArray}, []string{"id", "task_id"})),
		tool("projects.work_tasks.complete", "Complete Work Task", "MUST be used only after required verification, independent review, and attachments are recorded. Prior state: task is needs_review or verifying. Required fields: id, task_id, outcome, safe_next_action, verifier_result_refs, and either review_result_refs or review_exempt_reason. Review refs must come from a different run than the implementing claimed run; use review_exempt_reason only for tiny mechanical no-risk tasks. Before completion, make a reusable-knowledge decision: attach Evidence Graph claim/evidence refs plus confidence and knowledge candidate refs when the task produced durable knowledge, or state a short no-reusable-knowledge reason in outcome. Safety: metadata-only refs; task completion never implies AgentRun completion or knowledge promotion. Next tool: projects.work_tasks.get_next. Must not mark work done with unmet verification, review, Evidence Graph, confidence, or knowledge-decision requirements.",
			schema(map[string]any{"id": ref, "task_id": ref, "outcome": longText, "safe_next_action": text, "verifier_result_refs": refArray, "review_result_refs": refArray, "review_exempt_reason": reviewExemptReason, "claim_refs": refArray, "evidence_refs": refArray, "knowledge_candidate_refs": refArray, "run_id": ref, "trace_id": ref}, []string{"id", "task_id", "outcome", "safe_next_action"})),
		tool("projects.work_tasks.fail", "Fail Work Task", "MUST be used when execution reached a terminal failure. Prior state: task execution was started or verification failed. Required fields: id, task_id, outcome, and safe_next_action. Safety: bounded failure summary only; no raw stderr, provider payloads, source dumps, secrets, roots, or PII. Next tool: projects.work_tasks.get_next or projects.work_tasks.block for dependent work. Must not silently abandon failed work.",
			schema(map[string]any{"id": ref, "task_id": ref, "outcome": longText, "safe_next_action": text, "run_id": ref, "trace_id": ref}, []string{"id", "task_id", "outcome", "safe_next_action"})),
		tool("projects.work_tasks.block", "Block Work Task", "MUST be used instead of silently stopping when a task cannot proceed. Prior state: task exists and a blocker is known. Required fields: id, task_id, blocked_reason, resume_instructions, and safe_next_action. Safety: redacted blocker metadata only. Next tool: projects.work_tasks.get_next or projects.work_tasks.list_blocked. Must not use raw logs, raw source, secrets, roots, or PII as blocker text.",
			schema(map[string]any{"id": ref, "task_id": ref, "blocked_reason": longText, "resume_instructions": longText, "blocked_by_task_ids": refArray, "safe_next_action": text, "run_id": ref, "trace_id": ref}, []string{"id", "task_id", "blocked_reason", "resume_instructions", "safe_next_action"})),
		tool("projects.work_tasks.list", "List Work Tasks", "Alias for projects.work_tasks.list_open. MUST be used to inspect ready or in-progress project work before choosing a task. Prior state: project id is known. Required fields: id; plan_id is optional. Safety: metadata-only task summaries. Next tool: projects.work_tasks.get_next or projects.work_tasks.claim.",
			schema(merge(map[string]any{"id": ref, "plan_id": ref}, pageFields), []string{"id"})),
		tool("projects.work_tasks.list_open", "List Open Work Tasks", "MUST be used to inspect ready or in-progress project work before choosing a task. Prior state: project id is known. Required fields: id; plan_id is optional. Safety: metadata-only task summaries. Next tool: projects.work_tasks.get_next or projects.work_tasks.claim. Must not infer readiness from stale chat notes.",
			schema(merge(map[string]any{"id": ref, "plan_id": ref}, pageFields), []string{"id"})),
		tool("projects.work_tasks.list_mine", "List My Work Tasks", "MUST be used to recover tasks owned or claimed by the current agent/run. Prior state: owner_agent or run_id is known. Required fields: id and either owner_agent or run_id. Safety: metadata-only summaries. Next tool: projects.work_tasks.start, complete, block, fail, or release based on status. Must not expose other agents' raw work payloads.",
			schema(merge(map[string]any{"id": ref, "plan_id": ref, "owner_agent": ref, "run_id": ref}, pageFields), []string{"id"})),
		tool("projects.work_tasks.list_blocked", "List Blocked Work Tasks", "MUST be used when planning unblock work or answering what is blocked. Prior state: project id is known. Required fields: id; plan_id is optional. Safety: bounded blocker summaries and refs only. Next tool: projects.work_tasks.get_next or unblock through service-approved transition. Must not paste raw stderr, source, provider payloads, roots, secrets, or PII.",
			schema(merge(map[string]any{"id": ref, "plan_id": ref}, pageFields), []string{"id"})),
		tool("projects.work_tasks.get_next", "Get Next Work Task", "MUST be used after resume, after task completion, and whenever an agent is unsure what to do next. Prior state: project id is known and optional plan/owner filters may be supplied. Required fields: id. Safety: only return ready tasks with satisfied dependencies and adequate decomposition metadata. Next tool: projects.work_tasks.claim. Must not return blocked or dependency-incomplete tasks as safe to execute.",
			schema(map[string]any{"id": ref, "plan_id": ref, "owner_agent": ref, "run_id": ref, "include_claimed_by_me": map[string]any{"type": "boolean"}}, []string{"id"})),
		tool("projects.work_tasks.attach_evidence", "Attach Work Task Evidence", "MUST be used when a task relies on Evidence Graph evidence. Prior state: task exists and evidence was recorded elsewhere. Required fields: id, task_id, and evidence_ref. Safety: link refs only; no evidence blobs. Next tool: projects.work_tasks.attach_claim or projects.work_tasks.attach_verifier_result when appropriate. Must not create Evidence Graph claims itself.",
			schema(map[string]any{"id": ref, "task_id": ref, "evidence_ref": ref, "attached_by_run_id": ref, "trace_id": ref, "note": optionalText}, []string{"id", "task_id", "evidence_ref"})),
		tool("projects.work_tasks.attach_context_pack", "Attach Work Task Context Pack", "MUST be used before executing context-dependent tasks. Prior state: task exists and a context pack ref exists. Required fields: id, task_id, and context_pack_ref. Safety: link refs only; never paste context pack contents. Next tool: projects.work_tasks.start or evidence/claim attachment. Must not store source chunks or raw diffs.",
			schema(map[string]any{"id": ref, "task_id": ref, "context_pack_ref": ref, "attached_by_run_id": ref, "trace_id": ref, "note": optionalText}, []string{"id", "task_id", "context_pack_ref"})),
		tool("projects.work_tasks.attach_claim", "Attach Work Task Claim", "MUST be used when task output depends on or creates an Evidence Graph claim. Prior state: task exists and claim_ref exists. Required fields: id, task_id, and claim_ref. Safety: claim refs only. Subagents must attach candidate claim refs or explicitly report no reusable knowledge. Next tool: confidence assessment when the claim may become knowledge, or verifier attachment. Must not store claim chains or raw evidence.",
			schema(map[string]any{"id": ref, "task_id": ref, "claim_ref": ref, "attached_by_run_id": ref, "trace_id": ref, "note": optionalText}, []string{"id", "task_id", "claim_ref"})),
		tool("projects.work_tasks.attach_verifier_result", "Attach Work Task Verifier Result", "MUST be used before completing tasks with verification requirements. Prior state: verifier ran and has a safe verifier/result ref. Required fields: id, task_id, verifier_result_ref, and status. Safety: link refs/results only; no raw stderr or logs. Next tool: projects.work_tasks.complete if verification passed. Must not mark verification passed without a safe ref.",
			schema(map[string]any{"id": ref, "task_id": ref, "verifier_result_ref": ref, "status": map[string]any{"type": "string", "enum": []string{"passed", "failed", "blocked", "unknown"}}, "attached_by_run_id": ref, "trace_id": ref, "note": optionalText}, []string{"id", "task_id", "verifier_result_ref", "status"})),
		tool("projects.work_tasks.attach_review_result", "Attach Work Task Review Result", "MUST be used before completing write-capable or non-trivial Work Tasks unless a bounded review_exempt_reason is recorded. Prior state: an independent reviewer run has reviewed the task diff/evidence and produced a safe review_result_ref. Required fields: id, task_id, review_result_ref, and status. Safety: link refs/results only; no raw comments, source dumps, or logs. The attached_by_run_id must differ from the task claimed run when both are known. Next tool: projects.work_tasks.attach_verifier_result or projects.work_tasks.complete. Must not review your own implementation run.",
			schema(map[string]any{"id": ref, "task_id": ref, "review_result_ref": ref, "status": map[string]any{"type": "string", "enum": []string{"passed", "failed", "blocked", "unknown"}}, "attached_by_run_id": ref, "trace_id": ref, "note": optionalText}, []string{"id", "task_id", "review_result_ref", "status"})),
		tool("projects.work_tasks.promote_knowledge_candidate", "Create Work Task Knowledge Candidate Link", "MUST only create or link a Knowledge Promotion candidate for a Work Task. Prior state: evidence, claim, confidence, and verifier refs needed for the candidate are attached or supplied. Required fields: id, task_id, knowledge_candidate_ref, claim_refs, evidence_refs, confidence_ref, and verifier_result_refs. Safety: metadata-only candidate refs; never bypass projectknowledge validation, project promotion, or org promotion gates. Next tool: projectknowledge validation/promotion tools. Must not imply knowledge promotion or Work Task completion.",
			schema(map[string]any{"id": ref, "task_id": ref, "knowledge_candidate_ref": ref, "claim_refs": refArray, "evidence_refs": refArray, "confidence_ref": ref, "verifier_result_refs": refArray, "attached_by_run_id": ref, "trace_id": ref, "note": optionalText}, []string{"id", "task_id", "knowledge_candidate_ref", "claim_refs", "evidence_refs", "confidence_ref", "verifier_result_refs"})),
	}
	return tools
}

func IsWorkPlanTool(name string) bool {
	for _, tool := range workPlanTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return true
		}
	}
	return false
}

func CallTool(ctx context.Context, api API, name string, arguments json.RawMessage) (map[string]any, error) {
	if api == nil {
		return nil, ErrNotFound
	}
	canonical := canonicalToolName(name)
	if canonical == "" {
		return nil, ErrNotFound
	}
	arguments = normalizeProjectIDAlias(arguments)
	if err := validateArguments(canonical, arguments); err != nil {
		return nil, err
	}
	value, err := api.CallWorkPlanTool(ctx, canonical, arguments)
	if err != nil {
		return nil, err
	}
	return toolResult(value), nil
}

func validateArguments(name string, arguments json.RawMessage) error {
	var value any
	switch name {
	case "projects.work_plans.create":
		value = &createPlanInput{}
	case "projects.work_plans.get":
		value = &planIDInput{}
	case "projects.work_plans.list":
		value = &listPlansInput{}
	case "projects.work_plans.update_status":
		value = &updatePlanStatusInput{}
	case "projects.work_plans.resume":
		value = &resumePlanInput{}
	case "projects.work_tasks.create":
		value = &createTaskInput{}
	case "projects.work_tasks.get":
		value = &taskIDInput{}
	case "projects.work_tasks.update_status":
		value = &updateTaskStatusInput{}
	case "projects.work_tasks.claim":
		value = &claimTaskInput{}
	case "projects.work_tasks.release":
		value = &releaseTaskInput{}
	case "projects.work_tasks.start":
		value = &startTaskInput{}
	case "projects.work_tasks.complete":
		value = &completeTaskInput{}
	case "projects.work_tasks.fail":
		value = &failTaskInput{}
	case "projects.work_tasks.block":
		value = &blockTaskInput{}
	case "projects.work_tasks.list", "projects.work_tasks.list_open", "projects.work_tasks.list_blocked":
		value = &listTasksInput{}
	case "projects.work_tasks.list_mine":
		value = &listMineTasksInput{}
	case "projects.work_tasks.get_next":
		value = &getNextTaskInput{}
	case "projects.work_tasks.attach_evidence":
		value = &attachEvidenceInput{}
	case "projects.work_tasks.attach_context_pack":
		value = &attachContextPackInput{}
	case "projects.work_tasks.attach_claim":
		value = &attachClaimInput{}
	case "projects.work_tasks.attach_verifier_result":
		value = &attachVerifierInput{}
	case "projects.work_tasks.attach_review_result":
		value = &attachReviewInput{}
	case "projects.work_tasks.promote_knowledge_candidate":
		value = &promoteKnowledgeCandidateInput{}
	default:
		return ErrNotFound
	}
	if err := decodeRaw(arguments, value); err != nil {
		return fmt.Errorf("%w: invalid %s arguments", ErrInvalidInput, workPlanToolKind(name))
	}
	if hasUnsafeValue(value) {
		return fmt.Errorf("%w: unsafe %s metadata", ErrInvalidInput, workPlanToolKind(name))
	}
	return nil
}

func workPlanToolKind(name string) string {
	if strings.HasPrefix(name, "projects.work_tasks.") {
		return "work task"
	}
	return "work plan"
}

type createPlanInput struct {
	ID               string          `json:"id"`
	PlanRef          string          `json:"plan_ref"`
	UserRequestRef   string          `json:"user_request_ref,omitempty"`
	Title            string          `json:"title"`
	GoalSummary      string          `json:"goal_summary"`
	OwnerAgent       string          `json:"owner_agent,omitempty"`
	CreatedByRunID   string          `json:"created_by_run_id,omitempty"`
	TraceID          string          `json:"trace_id,omitempty"`
	ResumeSummary    string          `json:"resume_summary,omitempty"`
	IsolationMode    string          `json:"isolation_mode,omitempty"`
	ParallelGroupRef string          `json:"parallel_group_ref,omitempty"`
	WorkspaceRef     string          `json:"workspace_ref,omitempty"`
	GitBaseRef       string          `json:"git_base_ref,omitempty"`
	GitBranchRef     string          `json:"git_branch_ref,omitempty"`
	GitWorktreeRef   string          `json:"git_worktree_ref,omitempty"`
	Meta             json.RawMessage `json:"_meta,omitempty"`
}

type planIDInput struct {
	ID     string          `json:"id"`
	PlanID string          `json:"plan_id"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

type listPlansInput struct {
	ID         string          `json:"id"`
	Status     string          `json:"status,omitempty"`
	OwnerAgent string          `json:"owner_agent,omitempty"`
	PageSize   int             `json:"page_size,omitempty"`
	PageToken  string          `json:"page_token,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type updatePlanStatusInput struct {
	ID             string          `json:"id"`
	PlanID         string          `json:"plan_id"`
	Status         string          `json:"status"`
	SafeNextAction string          `json:"safe_next_action"`
	ResumeSummary  string          `json:"resume_summary,omitempty"`
	Outcome        string          `json:"outcome,omitempty"`
	RunID          string          `json:"run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type resumePlanInput struct {
	ID         string          `json:"id"`
	PlanID     string          `json:"plan_id,omitempty"`
	OwnerAgent string          `json:"owner_agent,omitempty"`
	RunID      string          `json:"run_id,omitempty"`
	TraceID    string          `json:"trace_id,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type createTaskInput struct {
	ID                            string          `json:"id"`
	PlanID                        string          `json:"plan_id"`
	TaskRef                       string          `json:"task_ref"`
	Title                         string          `json:"title"`
	Description                   string          `json:"description,omitempty"`
	Status                        string          `json:"status,omitempty"`
	OwnerAgent                    string          `json:"owner_agent,omitempty"`
	EvidenceNeeded                []string        `json:"evidence_needed"`
	ContextPackRefs               []string        `json:"context_pack_refs,omitempty"`
	FilesToRead                   []string        `json:"files_to_read,omitempty"`
	FilesToEdit                   []string        `json:"files_to_edit,omitempty"`
	LikelyFilesAffected           []string        `json:"likely_files_affected,omitempty"`
	DependencyTaskIDs             []string        `json:"dependency_task_ids,omitempty"`
	VerificationRequirement       string          `json:"verification_requirement"`
	ResumeInstructions            string          `json:"resume_instructions"`
	ExpectedOutput                string          `json:"expected_output,omitempty"`
	FailureCriteria               string          `json:"failure_criteria,omitempty"`
	FailureBlockCriteria          string          `json:"failure_block_criteria,omitempty"`
	ReviewGate                    string          `json:"review_gate,omitempty"`
	KnowledgeCandidateExpectation string          `json:"knowledge_candidate_expectation,omitempty"`
	DecompositionQuality          string          `json:"decomposition_quality,omitempty"`
	RunID                         string          `json:"run_id,omitempty"`
	TraceID                       string          `json:"trace_id,omitempty"`
	Meta                          json.RawMessage `json:"_meta,omitempty"`
}

type taskIDInput struct {
	ID     string          `json:"id"`
	TaskID string          `json:"task_id"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

type claimTaskInput struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"task_id"`
	OwnerAgent string          `json:"owner_agent"`
	RunID      string          `json:"run_id,omitempty"`
	TraceID    string          `json:"trace_id,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type releaseTaskInput struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"task_id"`
	OwnerAgent string          `json:"owner_agent"`
	Reason     string          `json:"reason,omitempty"`
	RunID      string          `json:"run_id,omitempty"`
	TraceID    string          `json:"trace_id,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type startTaskInput struct {
	ID              string          `json:"id"`
	TaskID          string          `json:"task_id"`
	RunID           string          `json:"run_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	ContextPackRefs []string        `json:"context_pack_refs,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
}

type completeTaskInput struct {
	ID                     string          `json:"id"`
	TaskID                 string          `json:"task_id"`
	Outcome                string          `json:"outcome"`
	SafeNextAction         string          `json:"safe_next_action"`
	VerifierResultRefs     []string        `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs       []string        `json:"review_result_refs,omitempty"`
	ReviewExemptReason     string          `json:"review_exempt_reason,omitempty"`
	ClaimRefs              []string        `json:"claim_refs,omitempty"`
	EvidenceRefs           []string        `json:"evidence_refs,omitempty"`
	KnowledgeCandidateRefs []string        `json:"knowledge_candidate_refs,omitempty"`
	RunID                  string          `json:"run_id,omitempty"`
	TraceID                string          `json:"trace_id,omitempty"`
	Meta                   json.RawMessage `json:"_meta,omitempty"`
}

type updateTaskStatusInput struct {
	ID                     string          `json:"id"`
	TaskID                 string          `json:"task_id"`
	Status                 string          `json:"status"`
	SafeNextAction         string          `json:"safe_next_action"`
	Outcome                string          `json:"outcome,omitempty"`
	BlockedReason          string          `json:"blocked_reason,omitempty"`
	ResumeInstructions     string          `json:"resume_instructions,omitempty"`
	BlockedByTaskIDs       []string        `json:"blocked_by_task_ids,omitempty"`
	VerifierResultRefs     []string        `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs       []string        `json:"review_result_refs,omitempty"`
	ClaimRefs              []string        `json:"claim_refs,omitempty"`
	EvidenceRefs           []string        `json:"evidence_refs,omitempty"`
	KnowledgeCandidateRefs []string        `json:"knowledge_candidate_refs,omitempty"`
	OwnerAgent             string          `json:"owner_agent,omitempty"`
	RunID                  string          `json:"run_id,omitempty"`
	TraceID                string          `json:"trace_id,omitempty"`
	Meta                   json.RawMessage `json:"_meta,omitempty"`
}

type failTaskInput struct {
	ID             string          `json:"id"`
	TaskID         string          `json:"task_id"`
	Outcome        string          `json:"outcome"`
	SafeNextAction string          `json:"safe_next_action"`
	RunID          string          `json:"run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type blockTaskInput struct {
	ID                 string          `json:"id"`
	TaskID             string          `json:"task_id"`
	BlockedReason      string          `json:"blocked_reason"`
	ResumeInstructions string          `json:"resume_instructions"`
	BlockedByTaskIDs   []string        `json:"blocked_by_task_ids,omitempty"`
	SafeNextAction     string          `json:"safe_next_action"`
	RunID              string          `json:"run_id,omitempty"`
	TraceID            string          `json:"trace_id,omitempty"`
	Meta               json.RawMessage `json:"_meta,omitempty"`
}

type listTasksInput struct {
	ID        string          `json:"id"`
	PlanID    string          `json:"plan_id,omitempty"`
	PageSize  int             `json:"page_size,omitempty"`
	PageToken string          `json:"page_token,omitempty"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

type listMineTasksInput struct {
	ID         string          `json:"id"`
	PlanID     string          `json:"plan_id,omitempty"`
	OwnerAgent string          `json:"owner_agent,omitempty"`
	RunID      string          `json:"run_id,omitempty"`
	PageSize   int             `json:"page_size,omitempty"`
	PageToken  string          `json:"page_token,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type getNextTaskInput struct {
	ID                 string          `json:"id"`
	PlanID             string          `json:"plan_id,omitempty"`
	OwnerAgent         string          `json:"owner_agent,omitempty"`
	RunID              string          `json:"run_id,omitempty"`
	IncludeClaimedByMe bool            `json:"include_claimed_by_me,omitempty"`
	Meta               json.RawMessage `json:"_meta,omitempty"`
}

type attachEvidenceInput struct {
	ID              string          `json:"id"`
	TaskID          string          `json:"task_id"`
	EvidenceRef     string          `json:"evidence_ref"`
	AttachedByRunID string          `json:"attached_by_run_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	Note            string          `json:"note,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
}

type attachContextPackInput struct {
	ID              string          `json:"id"`
	TaskID          string          `json:"task_id"`
	ContextPackRef  string          `json:"context_pack_ref"`
	AttachedByRunID string          `json:"attached_by_run_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	Note            string          `json:"note,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
}

type attachClaimInput struct {
	ID              string          `json:"id"`
	TaskID          string          `json:"task_id"`
	ClaimRef        string          `json:"claim_ref"`
	AttachedByRunID string          `json:"attached_by_run_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	Note            string          `json:"note,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
}

type attachVerifierInput struct {
	ID                string          `json:"id"`
	TaskID            string          `json:"task_id"`
	VerifierResultRef string          `json:"verifier_result_ref"`
	Status            string          `json:"status"`
	AttachedByRunID   string          `json:"attached_by_run_id,omitempty"`
	TraceID           string          `json:"trace_id,omitempty"`
	Note              string          `json:"note,omitempty"`
	Meta              json.RawMessage `json:"_meta,omitempty"`
}

type attachReviewInput struct {
	ID              string          `json:"id"`
	TaskID          string          `json:"task_id"`
	ReviewResultRef string          `json:"review_result_ref"`
	Status          string          `json:"status"`
	AttachedByRunID string          `json:"attached_by_run_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	Note            string          `json:"note,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
}

type promoteKnowledgeCandidateInput struct {
	ID                    string          `json:"id"`
	TaskID                string          `json:"task_id"`
	KnowledgeCandidateRef string          `json:"knowledge_candidate_ref"`
	ClaimRefs             []string        `json:"claim_refs"`
	EvidenceRefs          []string        `json:"evidence_refs"`
	ConfidenceRef         string          `json:"confidence_ref"`
	VerifierResultRefs    []string        `json:"verifier_result_refs"`
	AttachedByRunID       string          `json:"attached_by_run_id,omitempty"`
	TraceID               string          `json:"trace_id,omitempty"`
	Note                  string          `json:"note,omitempty"`
	Meta                  json.RawMessage `json:"_meta,omitempty"`
}

func tool(name, title, description string, inputSchema map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"title":       title,
		"description": description,
		"inputSchema": inputSchema,
	}
}

func schema(properties map[string]any, required []string) map[string]any {
	if idSchema, ok := properties["id"]; ok {
		properties = merge(properties, map[string]any{"project_id": idSchema})
	}
	out := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	required, _ = optionalizeIDRequired(required)
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func optionalizeIDRequired(required []string) ([]string, bool) {
	out := make([]string, 0, len(required))
	hasID := false
	for _, field := range required {
		if field == "id" {
			hasID = true
			continue
		}
		out = append(out, field)
	}
	return out, hasID
}

func statusSchema(statuses []string) map[string]any {
	return map[string]any{"type": "string", "enum": statuses}
}

func planStatuses() []string {
	return []string{"planned", "active", "blocked", "needs_review", "done", "failed", "cancelled", "superseded"}
}

func taskStatuses() []string {
	return []string{"planned", "ready", "claimed", "in_progress", "blocked", "needs_review", "verifying", "done", "failed", "cancelled", "superseded"}
}

func nonTerminalTaskStatuses() []string {
	return []string{"planned", "ready", "claimed", "in_progress", "blocked", "needs_review", "verifying"}
}

func decompositionQualities() []string {
	return []string{"draft", "ready", "too_broad", "missing_evidence", "missing_context", "missing_verification", "missing_resume"}
}

func merge(first map[string]any, second map[string]any) map[string]any {
	out := make(map[string]any, len(first)+len(second))
	for key, value := range first {
		out[key] = value
	}
	for key, value := range second {
		out[key] = value
	}
	return out
}

func canonicalToolName(name string) string {
	for _, tool := range workPlanTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return tool
		}
	}
	return ""
}

func toolResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(encoded)},
		},
		"structuredContent": value,
		"isError":           false,
	}
}

func decodeRaw(raw json.RawMessage, dst any) error {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = json.RawMessage(encoded)
	}
	raw = normalizeProjectIDAlias(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func normalizeProjectIDAlias(raw json.RawMessage) json.RawMessage {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw
	}
	projectID, hasProjectID := payload["project_id"]
	if !hasProjectID {
		return raw
	}
	if _, hasID := payload["id"]; !hasID {
		payload["id"] = projectID
	}
	delete(payload, "project_id")
	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return normalized
}

func hasUnsafeValue(value any) bool {
	encoded, _ := json.Marshal(value)
	lower := redactSafeProhibitionPhrases(strings.ToLower(string(encoded)))
	for _, marker := range []string{
		"raw prompt",
		"raw completion",
		"source dump",
		"raw stderr",
		"provider payload",
		"package main",
		"token=",
		"secret=",
		"credential",
		"api_key",
		"password=",
		"/home/",
		"wsl.localhost",
		"c:\\",
		"\\\\",
		"..",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func redactSafeProhibitionPhrases(value string) string {
	if strings.Contains(value, "no raw prompt") || strings.Contains(value, "never store") || strings.Contains(value, "must not store") || strings.Contains(value, "do not store") || strings.Contains(value, "would store") || strings.Contains(value, "must not expose") || strings.Contains(value, "do not expose") || strings.Contains(value, "would expose") || strings.Contains(value, "must not include") || strings.Contains(value, "do not include") || strings.Contains(value, "would include") || strings.Contains(value, "prohibited") {
		for _, marker := range []string{
			"raw prompts",
			"raw prompt",
			"raw completions",
			"raw completion",
			"source dumps",
			"source dump",
			"raw stderr",
			"provider payloads",
			"provider payload",
			"secrets",
			"secret",
			"credentials",
			"credential",
			"roots",
			"root",
			"paths",
			"path",
		} {
			value = strings.ReplaceAll(value, marker, "")
		}
	}
	for _, prefix := range []string{"no", "never store", "must not store", "do not store", "would store", "must not expose", "do not expose", "would expose", "must not include", "do not include", "would include"} {
		for _, marker := range []string{"raw prompts", "raw prompt", "raw completions", "raw completion", "source dumps", "source dump", "raw stderr", "provider payloads", "provider payload", "credentials", "credential", "secrets", "secret", "roots", "root", "paths", "path"} {
			value = strings.ReplaceAll(value, prefix+" "+marker, "")
		}
	}
	return value
}

var _ = taskIDInput{}
var _ = taskStatuses
