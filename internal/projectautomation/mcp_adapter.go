package projectautomation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func (svc *Service) CallAutomationTool(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "projects.automations.create":
		var input createAutomationMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.CreateAutomation(ctx, CreateAutomationInput{ProjectID: input.projectID(), AutomationRef: input.AutomationRef, Title: input.Title, Purpose: input.Purpose, Status: input.Status, AgentID: input.AgentID, PlanID: input.PlanID, AllowedTaskRefs: input.AllowedTaskRefs, RequiredReviewTaskIDs: input.RequiredReviewTaskIDs, TriggerKind: input.TriggerKind, SchedulePolicy: input.SchedulePolicy, PermissionRef: input.PermissionRef, CreatedByRunID: input.CreatedByRunID, TraceID: input.TraceID})
	case "projects.automations.get":
		var input automationIDInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.GetAutomation(ctx, input.projectID(), input.AutomationID)
	case "projects.automations.list":
		var input listAutomationsMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.ListAutomations(ctx, AutomationFilter{ProjectID: input.projectID(), Status: input.Status, AgentID: input.AgentID})
	case "projects.automations.run":
		var input submitRunMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.RunNow(ctx, input.run())
	case "projects.automations.run_parallel_batch":
		var input parallelBatchMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.ComputeParallelBatch(ctx, ComputeParallelBatchInput{ProjectID: input.projectID(), AutomationRunID: input.AutomationRunID, OrchestratorRunID: input.OrchestratorRunID, PlanID: input.PlanID, TaskIDs: input.TaskIDs, MaxTasks: input.MaxTasks})
	case "projects.automation_runs.get":
		var input runIDInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		projectID, runID, err := safeProjectObject(input.projectID(), input.RunID, "run_id")
		if err != nil {
			return nil, err
		}
		return svc.store.GetRun(ctx, projectID, runID)
	case "projects.automation_runs.list":
		var input listRunsMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		projectID, err := safeRef(input.projectID(), "project_id")
		if err != nil {
			return nil, err
		}
		return svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, AutomationID: input.AutomationID, Status: input.Status})
	case "projects.automation_runs.claim_next":
		var input claimNextMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: input.projectID(), AgentID: input.AgentID, RunnerKind: input.RunnerKind})
	case "projects.automation_runs.complete_attempt":
		var input completeAttemptMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid automation arguments", ErrInvalidInput)
		}
		return svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: input.projectID(), RunID: input.RunID, Status: input.Status, FailureCategory: input.FailureCategory, DurationMS: input.DurationMS, VerifierResultRefs: input.VerifierRefs, EvidenceRefs: input.EvidenceRefs, ClaimRefs: input.ClaimRefs, KnowledgeRefs: input.KnowledgeRefs})
	default:
		return nil, fmt.Errorf("%w: unknown automation tool", ErrInvalidInput)
	}
}

type createAutomationMCPInput struct {
	ID                    string   `json:"id"`
	ProjectID             string   `json:"project_id,omitempty"`
	AutomationRef         string   `json:"automation_ref"`
	Title                 string   `json:"title"`
	Purpose               string   `json:"purpose"`
	Status                string   `json:"status,omitempty"`
	AgentID               string   `json:"agent_id"`
	PlanID                string   `json:"plan_id,omitempty"`
	AllowedTaskRefs       []string `json:"allowed_task_refs,omitempty"`
	RequiredReviewTaskIDs []string `json:"required_review_task_ids,omitempty"`
	TriggerKind           string   `json:"trigger_kind,omitempty"`
	SchedulePolicy        string   `json:"schedule_policy,omitempty"`
	PermissionRef         string   `json:"permission_ref"`
	CreatedByRunID        string   `json:"created_by_run_id,omitempty"`
	TraceID               string   `json:"trace_id,omitempty"`
}

func (input createAutomationMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type automationIDInput struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id,omitempty"`
	AutomationID string `json:"automation_id"`
}

func (input automationIDInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type listAutomationsMCPInput struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	Status    string `json:"status,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

func (input listAutomationsMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type submitRunMCPInput struct {
	ID                string   `json:"id"`
	ProjectID         string   `json:"project_id,omitempty"`
	AutomationID      string   `json:"automation_id"`
	PlanID            string   `json:"plan_id,omitempty"`
	TaskID            string   `json:"task_id,omitempty"`
	OwnerAgent        string   `json:"owner_agent,omitempty"`
	RunnerKind        string   `json:"runner_kind,omitempty"`
	OrchestratorRunID string   `json:"orchestrator_run_id,omitempty"`
	ParentRunID       string   `json:"parent_run_id,omitempty"`
	EvidenceRefs      []string `json:"evidence_refs,omitempty"`
	VerifierRefs      []string `json:"verifier_result_refs,omitempty"`
	SafeNextAction    string   `json:"safe_next_action,omitempty"`
}

func (input submitRunMCPInput) run() SubmitRunInput {
	return SubmitRunInput{ProjectID: input.projectID(), AutomationID: input.AutomationID, PlanID: input.PlanID, TaskID: input.TaskID, OwnerAgent: input.OwnerAgent, RunnerKind: input.RunnerKind, OrchestratorRunID: input.OrchestratorRunID, ParentRunID: input.ParentRunID, EvidenceRefs: input.EvidenceRefs, VerifierRefs: input.VerifierRefs, SafeNextAction: input.SafeNextAction}
}

func (input submitRunMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type parallelBatchMCPInput struct {
	ID                string   `json:"id"`
	ProjectID         string   `json:"project_id,omitempty"`
	AutomationRunID   string   `json:"automation_run_id,omitempty"`
	OrchestratorRunID string   `json:"orchestrator_run_id"`
	PlanID            string   `json:"plan_id,omitempty"`
	TaskIDs           []string `json:"task_ids,omitempty"`
	MaxTasks          int      `json:"max_tasks,omitempty"`
}

func (input parallelBatchMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type runIDInput struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	RunID     string `json:"run_id"`
}

func (input runIDInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type listRunsMCPInput struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id,omitempty"`
	AutomationID string `json:"automation_id,omitempty"`
	Status       string `json:"status,omitempty"`
}

func (input listRunsMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type claimNextMCPInput struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	RunnerKind string `json:"runner_kind,omitempty"`
}

func (input claimNextMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

type completeAttemptMCPInput struct {
	ID              string   `json:"id"`
	ProjectID       string   `json:"project_id,omitempty"`
	RunID           string   `json:"run_id"`
	Status          string   `json:"status"`
	FailureCategory string   `json:"failure_category,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	VerifierRefs    []string `json:"verifier_result_refs,omitempty"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
	ClaimRefs       []string `json:"claim_refs,omitempty"`
	KnowledgeRefs   []string `json:"knowledge_candidate_refs,omitempty"`
}

func (input completeAttemptMCPInput) projectID() string {
	return projectIDAlias(input.ID, input.ProjectID)
}

func projectIDAlias(id, projectID string) string {
	if strings.TrimSpace(projectID) != "" {
		return projectID
	}
	return id
}

func decodeMCP(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: trailing json", ErrInvalidInput)
	}
	return nil
}
