package projectworkplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CallWorkPlanTool adapts MCP tool calls onto the service-owned validation and state machine.
func (svc *Service) CallWorkPlanTool(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "projects.work_plans.create":
		var input createPlanMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work plan", err)
		}
		return svc.CreateWorkPlan(ctx, CreateWorkPlanInput{ProjectID: input.projectID(), PlanRef: input.PlanRef, UserRequestRef: input.UserRequestRef, Title: input.Title, GoalSummary: input.GoalSummary, OwnerAgent: input.OwnerAgent, CreatedByRunID: input.CreatedByRunID, TraceID: input.TraceID, ResumeSummary: input.ResumeSummary, IsolationMode: input.IsolationMode, ParallelGroupRef: input.ParallelGroupRef, WorkspaceRef: input.WorkspaceRef, GitBaseRef: input.GitBaseRef, GitBranchRef: input.GitBranchRef, GitWorktreeRef: input.GitWorktreeRef})
	case "projects.work_plans.get":
		var input planIDInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work plan", err)
		}
		return svc.GetWorkPlan(ctx, input.projectID(), input.PlanID)
	case "projects.work_plans.list":
		var input listPlansInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work plan", err)
		}
		return svc.ListWorkPlans(ctx, WorkPlanFilter{ProjectID: input.projectID(), Status: input.Status, OwnerAgent: input.OwnerAgent, PageSize: input.PageSize, PageToken: input.PageToken})
	case "projects.work_plans.update_status":
		var input updatePlanStatusInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work plan", err)
		}
		return svc.UpdateWorkPlanStatus(ctx, UpdateWorkPlanStatusInput{ProjectID: input.projectID(), PlanID: input.PlanID, Status: input.Status, SafeNextAction: input.SafeNextAction, ResumeSummary: input.ResumeSummary, Outcome: input.Outcome, RunID: input.RunID, TraceID: input.TraceID})
	case "projects.work_plans.resume":
		var input resumePlanInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work plan", err)
		}
		return svc.ResumeWorkPlan(ctx, ResumeWorkPlanInput{ProjectID: input.projectID(), PlanID: input.PlanID})
	case "projects.work_tasks.create":
		var input createTaskMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		if input.FailureCriteria == "" {
			input.FailureCriteria = input.FailureBlockCriteria
		}
		return svc.CreateWorkTask(ctx, CreateWorkTaskInput{ProjectID: input.projectID(), PlanID: input.planID(), TaskRef: input.TaskRef, Title: input.Title, Description: input.description(), Status: input.Status, OwnerAgent: input.OwnerAgent, RunID: input.runID(), TraceID: input.TraceID, EvidenceNeeded: input.EvidenceNeeded, ContextPackRefs: input.ContextPackRefs, FilesToRead: input.FilesToRead, FilesToEdit: input.FilesToEdit, LikelyFilesAffected: input.LikelyFilesAffected, DependencyTaskIDs: input.DependencyTaskIDs, VerificationRequirement: input.VerificationRequirement, ExpectedOutput: input.ExpectedOutput, FailureCriteria: input.FailureCriteria, ReviewGate: input.ReviewGate, ResumeInstructions: input.ResumeInstructions, KnowledgeCandidateRefs: input.KnowledgeCandidateRefs, DecompositionQuality: input.DecompositionQuality})
	case "projects.work_tasks.get":
		var input taskIDInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.GetWorkTask(ctx, input.projectID(), input.TaskID)
	case "projects.work_tasks.update_status":
		var input taskStatusMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.UpdateWorkTaskStatus(ctx, input.status())
	case "projects.work_tasks.claim":
		var input taskActionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.ClaimWorkTask(ctx, input.action())
	case "projects.work_tasks.release":
		var input taskActionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.ReleaseWorkTask(ctx, input.action())
	case "projects.work_tasks.start":
		var input taskActionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.StartWorkTask(ctx, input.action())
	case "projects.work_tasks.complete":
		var input taskActionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.CompleteWorkTask(ctx, input.action())
	case "projects.work_tasks.fail":
		var input taskActionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.FailWorkTask(ctx, input.action())
	case "projects.work_tasks.block":
		var input taskActionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.BlockWorkTask(ctx, input.action())
	case "projects.work_tasks.list", "projects.work_tasks.list_open":
		var input listTasksMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.ListOpenWorkTasks(ctx, input.filter())
	case "projects.work_tasks.list_mine":
		var input listTasksMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.ListMineWorkTasks(ctx, input.filter())
	case "projects.work_tasks.list_blocked":
		var input listTasksMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.ListBlockedWorkTasks(ctx, input.filter())
	case "projects.work_tasks.get_next":
		var input getNextMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		return svc.GetNextWorkTask(ctx, GetNextWorkTaskInput{ProjectID: input.projectID(), PlanID: input.PlanID, OwnerAgent: input.OwnerAgent, RunID: input.RunID, TraceID: input.TraceID, IncludeClaimedByMe: input.IncludeClaimedByMe})
	case "projects.work_tasks.attach_evidence":
		var input attachMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		input.Ref = input.EvidenceRef
		return svc.AttachEvidence(ctx, input.attach())
	case "projects.work_tasks.attach_context_pack":
		var input attachMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		input.Ref = input.ContextPackRef
		return svc.AttachContextPack(ctx, input.attach())
	case "projects.work_tasks.attach_claim":
		var input attachMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		input.Ref = input.ClaimRef
		return svc.AttachClaim(ctx, input.attach())
	case "projects.work_tasks.attach_verifier_result":
		var input attachMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		input.Ref = input.VerifierResultRef
		return svc.AttachVerifierResult(ctx, input.attach())
	case "projects.work_tasks.attach_review_result":
		var input attachMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		input.Ref = input.ReviewResultRef
		return svc.AttachReviewResult(ctx, input.attach())
	case "projects.work_tasks.promote_knowledge_candidate":
		var input attachMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, mcpInvalidArgumentsError("work task", err)
		}
		input.Ref = input.KnowledgeCandidateRef
		return svc.AttachKnowledgeCandidate(ctx, input.attach())
	default:
		return nil, fmt.Errorf("%w: unknown work plan tool", ErrInvalidInput)
	}
}

type planIDInput struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	PlanID    string `json:"plan_id"`
}

type taskIDInput struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id"`
}

type createPlanMCPInput struct {
	ID               string `json:"id"`
	ProjectID        string `json:"project_id,omitempty"`
	PlanRef          string `json:"plan_ref"`
	UserRequestRef   string `json:"user_request_ref,omitempty"`
	Title            string `json:"title"`
	GoalSummary      string `json:"goal_summary"`
	OwnerAgent       string `json:"owner_agent,omitempty"`
	CreatedByRunID   string `json:"created_by_run_id,omitempty"`
	TraceID          string `json:"trace_id,omitempty"`
	ResumeSummary    string `json:"resume_summary,omitempty"`
	IsolationMode    string `json:"isolation_mode,omitempty"`
	ParallelGroupRef string `json:"parallel_group_ref,omitempty"`
	WorkspaceRef     string `json:"workspace_ref,omitempty"`
	GitBaseRef       string `json:"git_base_ref,omitempty"`
	GitBranchRef     string `json:"git_branch_ref,omitempty"`
	GitWorktreeRef   string `json:"git_worktree_ref,omitempty"`
}

type listPlansInput struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id,omitempty"`
	Status     string `json:"status,omitempty"`
	OwnerAgent string `json:"owner_agent,omitempty"`
	PageSize   int    `json:"page_size,omitempty"`
	PageToken  string `json:"page_token,omitempty"`
}

type updatePlanStatusInput struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id,omitempty"`
	PlanID         string `json:"plan_id"`
	Status         string `json:"status"`
	SafeNextAction string `json:"safe_next_action"`
	ResumeSummary  string `json:"resume_summary,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
}

type resumePlanInput struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
}

type createTaskMCPInput struct {
	ID                      string   `json:"id"`
	ProjectID               string   `json:"project_id,omitempty"`
	PlanID                  string   `json:"plan_id"`
	WorkPlanID              string   `json:"work_plan_id,omitempty"`
	TaskRef                 string   `json:"task_ref"`
	Title                   string   `json:"title"`
	Objective               string   `json:"objective,omitempty"`
	Description             string   `json:"description,omitempty"`
	Status                  string   `json:"status,omitempty"`
	OwnerAgent              string   `json:"owner_agent,omitempty"`
	TraceID                 string   `json:"trace_id,omitempty"`
	EvidenceNeeded          []string `json:"evidence_needed,omitempty"`
	ContextPackRefs         []string `json:"context_pack_refs,omitempty"`
	FilesToRead             []string `json:"files_to_read,omitempty"`
	FilesToEdit             []string `json:"files_to_edit,omitempty"`
	LikelyFilesAffected     []string `json:"likely_files_affected,omitempty"`
	DependencyTaskIDs       []string `json:"dependency_task_ids,omitempty"`
	VerificationRequirement string   `json:"verification_requirement"`
	ExpectedOutput          string   `json:"expected_output,omitempty"`
	FailureCriteria         string   `json:"failure_criteria,omitempty"`
	FailureBlockCriteria    string   `json:"failure_block_criteria,omitempty"`
	ReviewGate              string   `json:"review_gate,omitempty"`
	ResumeInstructions      string   `json:"resume_instructions,omitempty"`
	KnowledgeCandidateRefs  []string `json:"knowledge_candidate_refs,omitempty"`
	KnowledgeExpectation    string   `json:"knowledge_candidate_expectation,omitempty"`
	DecompositionQuality    string   `json:"decomposition_quality,omitempty"`
	RunID                   string   `json:"run_id,omitempty"`
	CreatedByRunID          string   `json:"created_by_run_id,omitempty"`
}

type taskActionMCPInput struct {
	ID                 string   `json:"id"`
	ProjectID          string   `json:"project_id,omitempty"`
	TaskID             string   `json:"task_id"`
	OwnerAgent         string   `json:"owner_agent,omitempty"`
	RunID              string   `json:"run_id,omitempty"`
	TraceID            string   `json:"trace_id,omitempty"`
	ContextPackRefs    []string `json:"context_pack_refs,omitempty"`
	Outcome            string   `json:"outcome,omitempty"`
	SafeNextAction     string   `json:"safe_next_action,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	BlockedReason      string   `json:"blocked_reason,omitempty"`
	BlockedByTaskIDs   []string `json:"blocked_by_task_ids,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ClaimRefs          []string `json:"claim_refs,omitempty"`
	KnowledgeRefs      []string `json:"knowledge_candidate_refs,omitempty"`
	ResumeInstructions string   `json:"resume_instructions,omitempty"`
	VerifierResultRefs []string `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs   []string `json:"review_result_refs,omitempty"`
	ReviewExemptReason string   `json:"review_exempt_reason,omitempty"`
}

func (input taskActionMCPInput) action() WorkTaskActionInput {
	return WorkTaskActionInput{ProjectID: input.projectID(), TaskID: input.TaskID, OwnerAgent: input.OwnerAgent, RunID: input.RunID, TraceID: input.TraceID, ContextPackRefs: input.ContextPackRefs, EvidenceRefs: input.EvidenceRefs, ClaimRefs: input.ClaimRefs, KnowledgeRefs: input.KnowledgeRefs, Outcome: input.Outcome, SafeNextAction: input.SafeNextAction, BlockedReason: input.BlockedReason, BlockedByTaskIDs: input.BlockedByTaskIDs, ResumeInstructions: input.ResumeInstructions, VerifierResultRefs: input.VerifierResultRefs, ReviewResultRefs: input.ReviewResultRefs, ReviewExemptReason: input.ReviewExemptReason}
}

type taskStatusMCPInput struct {
	taskActionMCPInput
	Status string `json:"status"`
}

func (input taskStatusMCPInput) status() UpdateWorkTaskStatusInput {
	return UpdateWorkTaskStatusInput{WorkTaskActionInput: input.action(), Status: input.Status}
}

type listTasksMCPInput struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
	OwnerAgent string `json:"owner_agent,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	PageSize   int    `json:"page_size,omitempty"`
	PageToken  string `json:"page_token,omitempty"`
}

func (input listTasksMCPInput) filter() WorkTaskFilter {
	return WorkTaskFilter{ProjectID: input.projectID(), PlanID: input.PlanID, OwnerAgent: input.OwnerAgent, ClaimedByRunID: input.RunID, PageSize: input.PageSize, PageToken: input.PageToken}
}

type getNextMCPInput struct {
	ID                 string `json:"id"`
	ProjectID          string `json:"project_id,omitempty"`
	PlanID             string `json:"plan_id,omitempty"`
	OwnerAgent         string `json:"owner_agent,omitempty"`
	RunID              string `json:"run_id,omitempty"`
	TraceID            string `json:"trace_id,omitempty"`
	IncludeClaimedByMe bool   `json:"include_claimed_by_me,omitempty"`
}

type attachMCPInput struct {
	ID                    string   `json:"id"`
	ProjectID             string   `json:"project_id,omitempty"`
	TaskID                string   `json:"task_id"`
	Ref                   string   `json:"ref,omitempty"`
	EvidenceRef           string   `json:"evidence_ref,omitempty"`
	ContextPackRef        string   `json:"context_pack_ref,omitempty"`
	ClaimRef              string   `json:"claim_ref,omitempty"`
	VerifierResultRef     string   `json:"verifier_result_ref,omitempty"`
	ReviewResultRef       string   `json:"review_result_ref,omitempty"`
	KnowledgeCandidateRef string   `json:"knowledge_candidate_ref,omitempty"`
	Status                string   `json:"status,omitempty"`
	ConfidenceRef         string   `json:"confidence_ref,omitempty"`
	ClaimRefs             []string `json:"claim_refs,omitempty"`
	EvidenceRefs          []string `json:"evidence_refs,omitempty"`
	VerifierResultRefs    []string `json:"verifier_result_refs,omitempty"`
	AttachedByRunID       string   `json:"attached_by_run_id,omitempty"`
	TraceID               string   `json:"trace_id,omitempty"`
	Note                  string   `json:"note,omitempty"`
}

func (input attachMCPInput) attach() AttachInput {
	return AttachInput{ProjectID: input.projectID(), TaskID: input.TaskID, Ref: input.Ref, AttachedByRunID: input.AttachedByRunID, TraceID: input.TraceID, Note: input.Note}
}

func (input planIDInput) projectID() string        { return firstNonEmpty(input.ID, input.ProjectID) }
func (input taskIDInput) projectID() string        { return firstNonEmpty(input.ID, input.ProjectID) }
func (input createPlanMCPInput) projectID() string { return firstNonEmpty(input.ID, input.ProjectID) }
func (input listPlansInput) projectID() string     { return firstNonEmpty(input.ID, input.ProjectID) }
func (input updatePlanStatusInput) projectID() string {
	return firstNonEmpty(input.ID, input.ProjectID)
}
func (input resumePlanInput) projectID() string    { return firstNonEmpty(input.ID, input.ProjectID) }
func (input createTaskMCPInput) projectID() string { return firstNonEmpty(input.ID, input.ProjectID) }
func (input createTaskMCPInput) planID() string    { return firstNonEmpty(input.PlanID, input.WorkPlanID) }
func (input createTaskMCPInput) runID() string {
	return firstNonEmpty(input.RunID, input.CreatedByRunID)
}
func (input createTaskMCPInput) description() string {
	if strings.TrimSpace(input.Description) != "" {
		return input.Description
	}
	return input.Objective
}
func (input taskActionMCPInput) projectID() string { return firstNonEmpty(input.ID, input.ProjectID) }
func (input listTasksMCPInput) projectID() string  { return firstNonEmpty(input.ID, input.ProjectID) }
func (input getNextMCPInput) projectID() string    { return firstNonEmpty(input.ID, input.ProjectID) }
func (input attachMCPInput) projectID() string     { return firstNonEmpty(input.ID, input.ProjectID) }

func firstNonEmpty(primary, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return primary
}

func mcpInvalidArgumentsError(kind string, err error) error {
	if field, ok := mcpRejectedJSONFieldLabel(err); ok {
		return fmt.Errorf("%w: %s is not accepted for %s", ErrInvalidInput, field, kind)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if field := mcpSafeErrorFieldName(typeErr.Field); field != "" {
			return fmt.Errorf("%w: %s has invalid type for %s", ErrInvalidInput, field, kind)
		}
		return fmt.Errorf("%w: %s arguments must be a JSON object", ErrInvalidInput, kind)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("%w: malformed %s JSON", ErrInvalidInput, kind)
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s arguments are required", ErrInvalidInput, kind)
	}
	if strings.Contains(err.Error(), "unexpected trailing JSON") {
		return fmt.Errorf("%w: %s arguments must contain one JSON value", ErrInvalidInput, kind)
	}
	return fmt.Errorf("%w: invalid %s arguments", ErrInvalidInput, kind)
}

func mcpRejectedJSONFieldLabel(err error) (string, bool) {
	const prefix = "json: unknown field "
	message := err.Error()
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	var field string
	if decodeErr := json.Unmarshal([]byte(strings.TrimPrefix(message, prefix)), &field); decodeErr != nil {
		return "unknown field", true
	}
	if field = mcpSafeErrorFieldName(field); field == "" {
		return "unknown field", true
	}
	return "field " + field, true
}

func mcpSafeErrorFieldName(field string) string {
	field = strings.TrimSpace(field)
	if field == "" || len(field) > 80 || mcpUnsafeErrorText(field) {
		return ""
	}
	for _, char := range field {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return ""
	}
	return field
}

func mcpUnsafeErrorText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"raw prompt",
		"raw completion",
		"source dump",
		"raw stderr",
		"provider payload",
		"token=",
		"secret=",
		"credential",
		"api_key",
		"password=",
		"/home/",
		"wsl.localhost",
		"c:\\",
		"\\\\",
		"package main",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func decodeMCP(raw json.RawMessage, dst any) error {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = json.RawMessage(encoded)
	}
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
