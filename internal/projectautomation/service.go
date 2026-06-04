package projectautomation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

var ErrInvalidInput = errors.New("invalid project automation input")

var (
	refPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
	emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)
)

type Store interface {
	CreateAutomation(context.Context, Automation) (Automation, error)
	GetAutomation(context.Context, string, string) (Automation, error)
	ListAutomations(context.Context, AutomationFilter) ([]Automation, error)
	UpdateAutomation(context.Context, Automation) (Automation, error)
	CreateRun(context.Context, AutomationRun) (AutomationRun, error)
	GetRun(context.Context, string, string) (AutomationRun, error)
	ListRuns(context.Context, RunFilter) ([]AutomationRun, error)
	UpdateRun(context.Context, AutomationRun) (AutomationRun, error)
	CreateAttempt(context.Context, AutomationAttempt) (AutomationAttempt, error)
	CreateParallelBatch(context.Context, AutomationParallelBatch) (AutomationParallelBatch, error)
	GetParallelBatch(context.Context, string, string) (AutomationParallelBatch, error)
}

type WorkTaskAPI interface {
	GetWorkTask(context.Context, string, string) (projectworkplan.WorkTask, error)
	ListOpenWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)
	ClaimWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	StartWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	AttachEvidence(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
	AttachVerifierResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
	CompleteWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	FailWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	BlockWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
}

type Service struct {
	store          Store
	workTasks      WorkTaskAPI
	options        Options
	now            func() time.Time
	newID          func(string) string
	codexAvailable func() bool
	codexPath      func() (string, bool)
	codexRunner    func(context.Context, CodexCommand, int64) (CodexRunResult, error)
}

func New(store Store, workTasks WorkTaskAPI, options Options) *Service {
	if options.MaxParallelTasks <= 0 {
		options.MaxParallelTasks = 1
	}
	if strings.TrimSpace(options.RunnerExecution) == "" {
		options.RunnerExecution = RunnerExecutionInProcess
	}
	agents := append([]AutomationAgent(nil), options.Agents...)
	options.Agents = agents
	return &Service{
		store:     store,
		workTasks: workTasks,
		options:   options,
		now:       func() time.Time { return time.Now().UTC() },
		newID:     newID,
		codexRunner: func(ctx context.Context, command CodexCommand, maxOutputBytes int64) (CodexRunResult, error) {
			return RunCodexCommand(ctx, command, maxOutputBytes)
		},
		codexPath: func() (string, bool) {
			return DetectCodex(options.CodexBinaryPath)
		},
		codexAvailable: func() bool {
			_, ok := DetectCodex(options.CodexBinaryPath)
			return ok
		},
	}
}

func (svc *Service) CreateAutomation(ctx context.Context, input CreateAutomationInput) (Automation, error) {
	if svc.store == nil {
		return Automation{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return Automation{}, err
	}
	automationRef, err := safeRef(input.AutomationRef, "automation_ref")
	if err != nil {
		return Automation{}, err
	}
	title, err := safeRequiredText(input.Title, "title", 200)
	if err != nil {
		return Automation{}, err
	}
	purpose, err := safeRequiredText(input.Purpose, "purpose", 500)
	if err != nil {
		return Automation{}, err
	}
	agentID, err := safeRef(input.AgentID, "agent_id")
	if err != nil {
		return Automation{}, err
	}
	planID, err := safeOptionalRef(input.PlanID, "plan_id")
	if err != nil {
		return Automation{}, err
	}
	taskRefs, err := safeRefList(input.AllowedTaskRefs, "allowed_task_refs")
	if err != nil {
		return Automation{}, err
	}
	requiredReviewTaskIDs, err := safeRefList(input.RequiredReviewTaskIDs, "required_review_task_ids")
	if err != nil {
		return Automation{}, err
	}
	sourceKind, err := safeAutomationSource(input.SourceKind)
	if err != nil {
		return Automation{}, err
	}
	status, err := safeOptionalAutomationStatus(input.Status)
	if err != nil {
		return Automation{}, err
	}
	if status == "" {
		status = AutomationStatusDraft
	}
	triggerKind, err := safeAutomationTrigger(input.TriggerKind)
	if err != nil {
		return Automation{}, err
	}
	schedulePolicy, err := safeOptionalRef(input.SchedulePolicy, "schedule_policy")
	if err != nil {
		return Automation{}, err
	}
	permissionRef, err := safeOptionalRef(input.PermissionRef, "permission_ref")
	if err != nil {
		return Automation{}, err
	}
	if sourceKind == AutomationSourceWorkflow {
		if err := validatePermissionSnapshotRef(permissionRef); err != nil {
			return Automation{}, err
		}
	}
	runID, err := safeOptionalRef(input.CreatedByRunID, "created_by_run_id")
	if err != nil {
		return Automation{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return Automation{}, err
	}
	now := svc.now()
	value := Automation{
		ID: svc.newID("automation"), ProjectID: projectID, AutomationRef: automationRef, Title: title, Purpose: purpose,
		Status: status, AgentID: agentID, PlanID: planID, AllowedTaskRefs: taskRefs, RequiredReviewTaskIDs: requiredReviewTaskIDs, TriggerKind: triggerKind,
		SourceKind: sourceKind, SchedulePolicy: schedulePolicy, PermissionRef: permissionRef, CreatedByRunID: runID, TraceID: traceID, CreatedAt: now, UpdatedAt: now,
	}
	return svc.store.CreateAutomation(ctx, value)
}

func (svc *Service) GetAutomation(ctx context.Context, projectID, automationID string) (Automation, error) {
	projectID, automationID, err := safeProjectObject(projectID, automationID, "automation_id")
	if err != nil {
		return Automation{}, err
	}
	return svc.store.GetAutomation(ctx, projectID, automationID)
}

func (svc *Service) UpdateAutomationStatus(ctx context.Context, input UpdateAutomationStatusInput) (Automation, error) {
	if svc.store == nil {
		return Automation{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, automationID, err := safeProjectObject(input.ProjectID, input.AutomationID, "automation_id")
	if err != nil {
		return Automation{}, err
	}
	status, err := safeAutomationStatus(input.Status)
	if err != nil {
		return Automation{}, err
	}
	if _, err := safeOptionalRef(input.RunID, "run_id"); err != nil {
		return Automation{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return Automation{}, err
	}
	automation, err := svc.store.GetAutomation(ctx, projectID, automationID)
	if err != nil {
		return Automation{}, err
	}
	automation.Status = status
	if traceID != "" {
		automation.TraceID = traceID
	}
	automation.UpdatedAt = svc.now()
	return svc.store.UpdateAutomation(ctx, automation)
}

func (svc *Service) ListAutomations(ctx context.Context, filter AutomationFilter) ([]Automation, error) {
	projectID, err := safeRef(filter.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter.ProjectID = projectID
	if filter.Status != "" {
		if _, err := safeAutomationStatus(filter.Status); err != nil {
			return nil, err
		}
	}
	if filter.AgentID != "" {
		if filter.AgentID, err = safeOptionalRef(filter.AgentID, "agent_id"); err != nil {
			return nil, err
		}
	}
	return svc.store.ListAutomations(ctx, filter)
}

func (svc *Service) SubmitRun(ctx context.Context, input SubmitRunInput) (AutomationRun, error) {
	if svc.store == nil {
		return AutomationRun{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, automationID, err := safeProjectObject(input.ProjectID, input.AutomationID, "automation_id")
	if err != nil {
		return AutomationRun{}, err
	}
	automation, err := svc.store.GetAutomation(ctx, projectID, automationID)
	if err != nil {
		return AutomationRun{}, err
	}
	taskID, err := safeOptionalRef(input.TaskID, "task_id")
	if err != nil {
		return AutomationRun{}, err
	}
	planID, err := safeOptionalRef(firstNonEmpty(input.PlanID, automation.PlanID), "plan_id")
	if err != nil {
		return AutomationRun{}, err
	}
	owner, err := safeOptionalRef(firstNonEmpty(input.OwnerAgent, automation.AgentID), "owner_agent")
	if err != nil {
		return AutomationRun{}, err
	}
	runnerKind, err := svc.resolveRunnerKind(input.RunnerKind)
	if err != nil {
		return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusPolicyDenied, err.Error())
	}
	if taskID == "" && automation.TriggerKind == TriggerKindAutomatic && svc.workTasks != nil {
		task, err := svc.resolveTask(ctx, AutomationRun{ProjectID: projectID, AutomationID: automation.ID, AgentID: owner, PlanID: planID, RunnerKind: runnerKind}, automation)
		if err != nil {
			return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, "task_unavailable")
		}
		taskID = task.ID
	}
	if err := svc.validateAutomationPolicy(ctx, automation, runnerKind, taskID); err != nil {
		return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusPolicyDenied, err.Error())
	}
	if err := svc.validateRequiredAutomationReviews(ctx, automation); err != nil {
		return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, err.Error())
	}
	orchestratorRunID, err := safeOptionalRef(input.OrchestratorRunID, "orchestrator_run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	parentRunID, err := safeOptionalRef(input.ParentRunID, "parent_run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	now := svc.now()
	run := AutomationRun{
		ID: svc.newID("automation_run"), ProjectID: projectID, AutomationID: automation.ID, AgentID: owner, PlanID: planID,
		TaskID: taskID, Status: RunStatusQueued, RunnerKind: runnerKind, AttemptCount: 0, OrchestratorRunID: orchestratorRunID,
		ParentRunID: parentRunID, CreatedAt: now, UpdatedAt: now,
	}
	return svc.store.CreateRun(ctx, run)
}

func (svc *Service) GetRun(ctx context.Context, projectID, runID string) (AutomationRun, error) {
	projectID, runID, err := safeProjectObject(projectID, runID, "run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	return svc.store.GetRun(ctx, projectID, runID)
}

func (svc *Service) ListRuns(ctx context.Context, filter RunFilter) ([]AutomationRun, error) {
	projectID, err := safeRef(filter.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter.ProjectID = projectID
	if filter.AutomationID != "" {
		if filter.AutomationID, err = safeOptionalRef(filter.AutomationID, "automation_id"); err != nil {
			return nil, err
		}
	}
	if filter.PlanID != "" {
		if filter.PlanID, err = safeOptionalRef(filter.PlanID, "plan_id"); err != nil {
			return nil, err
		}
	}
	if filter.OrchestratorRunID != "" {
		if filter.OrchestratorRunID, err = safeOptionalRef(filter.OrchestratorRunID, "orchestrator_run_id"); err != nil {
			return nil, err
		}
	}
	return svc.store.ListRuns(ctx, filter)
}

func (svc *Service) HandleWorkPlanStatusChanged(ctx context.Context, event projectworkplan.WorkPlanStatusChange) error {
	if svc == nil || svc.store == nil || !svc.options.Enabled || !svc.options.WorkPlanStatusTrigger.Enabled {
		return nil
	}
	if !svc.workPlanStatusTriggers(event.NewStatus) {
		return nil
	}
	automations, err := svc.store.ListAutomations(ctx, AutomationFilter{ProjectID: event.ProjectID, Status: AutomationStatusEnabled})
	if err != nil {
		return err
	}
	for _, automation := range automations {
		if automation.TriggerKind != TriggerKindAutomatic || automation.PlanID != event.PlanID {
			continue
		}
		if !svc.hasReadyAutomaticTask(ctx, automation) {
			continue
		}
		triggerRunID := workPlanStatusTriggerRunID(event, automation)
		existing, err := svc.store.ListRuns(ctx, RunFilter{
			ProjectID:         event.ProjectID,
			AutomationID:      automation.ID,
			PlanID:            event.PlanID,
			OrchestratorRunID: triggerRunID,
		})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			continue
		}
		if _, err := svc.SubmitRun(ctx, SubmitRunInput{
			ProjectID:         event.ProjectID,
			AutomationID:      automation.ID,
			PlanID:            event.PlanID,
			OwnerAgent:        automation.AgentID,
			OrchestratorRunID: triggerRunID,
			SafeNextAction:    "work_plan_status_trigger",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) workPlanStatusTriggers(status string) bool {
	statuses := svc.options.WorkPlanStatusTrigger.Statuses
	if len(statuses) == 0 {
		statuses = []string{projectworkplan.WorkPlanStatusActive}
	}
	for _, candidate := range statuses {
		if strings.TrimSpace(candidate) == status {
			return true
		}
	}
	return false
}

func workPlanStatusTriggerRunID(event projectworkplan.WorkPlanStatusChange, automation Automation) string {
	return "workplan-status:" + event.PlanID + ":" + event.NewStatus + ":" + automation.ID
}

func (svc *Service) RunNow(ctx context.Context, input SubmitRunInput) (AutomationRun, error) {
	run, err := svc.SubmitRun(ctx, input)
	if err != nil {
		return run, err
	}
	if svc.options.RunnerExecution == RunnerExecutionExternal {
		run.SafeSummary = "external_runner_queued"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	if svc.workTasks == nil {
		return svc.failRun(ctx, run, RunStatusRunnerUnavailable, "work_task_api_unavailable")
	}
	if !svc.options.Enabled || !svc.options.RunnerEnabled {
		return svc.failRun(ctx, run, RunStatusPolicyDenied, "automation_runner_disabled")
	}
	if run.RunnerKind == RunnerKindCodexCLI && !svc.codexAvailable() {
		return svc.failRun(ctx, run, RunStatusRunnerUnavailable, "codex_cli_unavailable")
	}
	run, task, err := svc.prepareRunForExecution(ctx, run)
	if err != nil {
		return run, err
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		attempt := AutomationAttempt{ID: svc.newID("automation_attempt"), ProjectID: run.ProjectID, AutomationRunID: run.ID, AttemptNumber: 1, RunnerKind: run.RunnerKind, Status: RunStatusCompleted, CreatedAt: svc.now(), FinishedAt: svc.now()}
		if _, err := svc.store.CreateAttempt(ctx, attempt); err != nil {
			return svc.failRun(ctx, run, RunStatusFailed, "attempt_record_failed")
		}
		return svc.failRun(ctx, run, RunStatusVerifying, "verification_required")
	}
	return svc.runCodexTask(ctx, run, task)
}

func (svc *Service) ExecuteQueuedRun(ctx context.Context, projectID, runID string) (AutomationRun, error) {
	if svc.store == nil {
		return AutomationRun{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, runID, err := safeProjectObject(projectID, runID, "run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	run, err := svc.store.GetRun(ctx, projectID, runID)
	if err != nil {
		return AutomationRun{}, err
	}
	if run.Status != RunStatusQueued {
		return run, nil
	}
	if svc.options.RunnerExecution == RunnerExecutionExternal {
		run.SafeSummary = "external_runner_queued"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	if svc.workTasks == nil {
		return svc.failRun(ctx, run, RunStatusRunnerUnavailable, "work_task_api_unavailable")
	}
	if !svc.options.Enabled || !svc.options.RunnerEnabled {
		return svc.failRun(ctx, run, RunStatusPolicyDenied, "automation_runner_disabled")
	}
	if run.RunnerKind == RunnerKindCodexCLI && !svc.codexAvailable() {
		return svc.failRun(ctx, run, RunStatusRunnerUnavailable, "codex_cli_unavailable")
	}
	run, task, err := svc.prepareRunForExecution(ctx, run)
	if err != nil {
		return run, err
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		attempt := AutomationAttempt{ID: svc.newID("automation_attempt"), ProjectID: run.ProjectID, AutomationRunID: run.ID, AttemptNumber: 1, RunnerKind: run.RunnerKind, Status: RunStatusCompleted, CreatedAt: svc.now(), FinishedAt: svc.now()}
		if _, err := svc.store.CreateAttempt(ctx, attempt); err != nil {
			return svc.failRun(ctx, run, RunStatusFailed, "attempt_record_failed")
		}
		run.Status = RunStatusVerifying
		run.FailureCategory = "verification_required"
		run.FinishedAt = svc.now()
		run.UpdatedAt = run.FinishedAt
		return svc.store.UpdateRun(ctx, run)
	}
	return svc.runCodexTask(ctx, run, task)
}

func (svc *Service) ClaimNextRun(ctx context.Context, input ClaimNextRunInput) (ClaimedRun, error) {
	if svc.store == nil || svc.workTasks == nil {
		return ClaimedRun{}, fmt.Errorf("%w: store and work task api are required", ErrInvalidInput)
	}
	if !svc.options.Enabled || !svc.options.RunnerEnabled {
		return ClaimedRun{}, fmt.Errorf("%w: automation_runner_disabled", ErrInvalidInput)
	}
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return ClaimedRun{}, err
	}
	agentID, err := safeOptionalRef(input.AgentID, "agent_id")
	if err != nil {
		return ClaimedRun{}, err
	}
	runnerKind := strings.TrimSpace(input.RunnerKind)
	if runnerKind == "" {
		runnerKind = RunnerKindCodexCLI
	}
	if runnerKind != RunnerKindCodexCLI {
		return ClaimedRun{}, fmt.Errorf("%w: external runner supports codex_cli only", ErrInvalidInput)
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusQueued})
	if err != nil {
		return ClaimedRun{}, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI {
			continue
		}
		if agentID != "" && run.AgentID != "" && run.AgentID != agentID {
			continue
		}
		claimed, task, err := svc.prepareRunForExecution(ctx, run)
		if err != nil {
			continue
		}
		timeout := automationMaxRuntime(svc.options.Agents, claimed.AgentID, svc.options.DefaultMaxRuntime)
		return ClaimedRun{Run: claimed, CodexInput: codexInputForRun(claimed, task), TimeoutMS: timeout.Milliseconds()}, nil
	}
	return ClaimedRun{}, fmt.Errorf("%w: no queued automation run", ErrInvalidInput)
}

func (svc *Service) hasAnyRun(ctx context.Context, automation Automation) bool {
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID})
	if err != nil {
		return true
	}
	return len(runs) > 0
}

func (svc *Service) hasReadyAutomaticTask(ctx context.Context, automation Automation) bool {
	if svc.workTasks == nil {
		return false
	}
	if svc.validateRequiredAutomationReviews(ctx, automation) != nil {
		return false
	}
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: automation.ProjectID, PlanID: automation.PlanID})
	if err != nil {
		return false
	}
	for _, task := range tasks {
		if validateAllowedTaskRef(automation, task) == nil && validateExecutableTask(task) == nil && svc.dependenciesDone(ctx, task) {
			return true
		}
	}
	return false
}

func (svc *Service) validateRequiredAutomationReviews(ctx context.Context, automation Automation) error {
	if len(automation.RequiredReviewTaskIDs) == 0 {
		return nil
	}
	if svc.workTasks == nil {
		return fmt.Errorf("%w: automation_review_gate_unavailable", ErrInvalidInput)
	}
	for _, taskID := range automation.RequiredReviewTaskIDs {
		task, err := svc.workTasks.GetWorkTask(ctx, automation.ProjectID, taskID)
		if err != nil {
			return fmt.Errorf("%w: automation_review_gate_open", ErrInvalidInput)
		}
		if task.Status != projectworkplan.WorkTaskStatusDone {
			return fmt.Errorf("%w: automation_review_gate_open", ErrInvalidInput)
		}
	}
	return nil
}

func (svc *Service) CompleteAttempt(ctx context.Context, input CompleteAttemptInput) (AutomationRun, error) {
	projectID, runID, err := safeProjectObject(input.ProjectID, input.RunID, "run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	run, err := svc.store.GetRun(ctx, projectID, runID)
	if err != nil {
		return AutomationRun{}, err
	}
	if run.Status != RunStatusRunning || run.RunnerKind != RunnerKindCodexCLI {
		return AutomationRun{}, fmt.Errorf("%w: automation run is not externally claimed", ErrInvalidInput)
	}
	status, err := safeAttemptStatus(input.Status)
	if err != nil {
		return AutomationRun{}, err
	}
	failureCategory, err := safeText(input.FailureCategory, "failure_category", 200)
	if err != nil {
		return AutomationRun{}, err
	}
	verifierRefs, err := safeRefList(input.VerifierResultRefs, "verifier_result_refs")
	if err != nil {
		return AutomationRun{}, err
	}
	evidenceRefs, err := safeRefList(input.EvidenceRefs, "evidence_refs")
	if err != nil {
		return AutomationRun{}, err
	}
	claimRefs, err := safeRefList(input.ClaimRefs, "claim_refs")
	if err != nil {
		return AutomationRun{}, err
	}
	reviewRefs, err := safeRefList(input.ReviewRefs, "review_result_refs")
	if err != nil {
		return AutomationRun{}, err
	}
	knowledgeRefs, err := safeRefList(input.KnowledgeRefs, "knowledge_candidate_refs")
	if err != nil {
		return AutomationRun{}, err
	}
	now := svc.now()
	attempt := AutomationAttempt{
		ID:                 svc.newID("automation_attempt"),
		ProjectID:          projectID,
		AutomationRunID:    run.ID,
		AttemptNumber:      run.AttemptCount,
		RunnerKind:         run.RunnerKind,
		CommandRef:         "external_codex_cli:" + run.ID,
		Status:             status,
		FailureCategory:    failureCategory,
		DurationMS:         input.DurationMS,
		VerifierResultRefs: verifierRefs,
		EvidenceRefs:       append([]string{"automation_run:" + run.ID}, evidenceRefs...),
		ClaimRefs:          claimRefs,
		KnowledgeRefs:      knowledgeRefs,
		CreatedAt:          now,
		FinishedAt:         now,
	}
	knowledgeRefs, err = svc.attachAttemptGovernance(ctx, run, attempt, attemptGovernanceRefs{
		VerifierRefs:  verifierRefs,
		EvidenceRefs:  append([]string{"automation_run:" + run.ID}, evidenceRefs...),
		ClaimRefs:     claimRefs,
		ReviewRefs:    reviewRefs,
		KnowledgeRefs: knowledgeRefs,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	attempt.KnowledgeRefs = knowledgeRefs
	if _, err := svc.store.CreateAttempt(ctx, attempt); err != nil {
		return svc.failRun(ctx, run, RunStatusFailed, "attempt_record_failed")
	}
	run.FailureCategory = failureCategory
	run.FinishedAt = now
	run.UpdatedAt = now
	switch status {
	case RunStatusCompleted:
		run.Status = RunStatusVerifying
		run.SafeSummary = "external_codex_cli_completed_verification_required"
	case RunStatusTimeout:
		run.Status = RunStatusTimeout
	case RunStatusFailed:
		run.Status = RunStatusFailed
	default:
		run.Status = status
	}
	return svc.store.UpdateRun(ctx, run)
}

func (svc *Service) ComputeParallelBatch(ctx context.Context, input ComputeParallelBatchInput) (AutomationParallelBatch, error) {
	if svc.store == nil || svc.workTasks == nil {
		return AutomationParallelBatch{}, fmt.Errorf("%w: store and work task api are required", ErrInvalidInput)
	}
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return AutomationParallelBatch{}, err
	}
	planID, err := safeOptionalRef(input.PlanID, "plan_id")
	if err != nil {
		return AutomationParallelBatch{}, err
	}
	orchestratorRunID, err := safeRef(input.OrchestratorRunID, "orchestrator_run_id")
	if err != nil {
		return AutomationParallelBatch{}, err
	}
	automationRunID, err := safeOptionalRef(input.AutomationRunID, "automation_run_id")
	if err != nil {
		return AutomationParallelBatch{}, err
	}
	maxTasks := input.MaxTasks
	if maxTasks <= 0 || maxTasks > svc.options.MaxParallelTasks {
		maxTasks = svc.options.MaxParallelTasks
	}
	tasks, err := svc.candidateTasks(ctx, projectID, planID, input.TaskIDs)
	if err != nil {
		return AutomationParallelBatch{}, err
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskRef < tasks[j].TaskRef })
	selected := make([]projectworkplan.WorkTask, 0, maxTasks)
	usedFiles := map[string]string{}
	for _, task := range tasks {
		if len(selected) == maxTasks {
			break
		}
		if err := validateExecutableTask(task); err != nil {
			continue
		}
		if !svc.dependenciesDone(ctx, task) {
			continue
		}
		if conflict := firstFileConflict(task, usedFiles); conflict != "" {
			continue
		}
		for _, file := range task.LikelyFilesAffected {
			usedFiles[strings.ToLower(file)] = task.ID
		}
		selected = append(selected, task)
	}
	if len(selected) == 0 {
		return AutomationParallelBatch{}, fmt.Errorf("%w: no parallel-safe tasks", ErrInvalidInput)
	}
	taskIDs := make([]string, 0, len(selected))
	for _, task := range selected {
		taskIDs = append(taskIDs, task.ID)
	}
	now := svc.now()
	batch := AutomationParallelBatch{
		ID: svc.newID("automation_batch"), ProjectID: projectID, AutomationRunID: automationRunID, OrchestratorRunID: orchestratorRunID,
		PlanID: planID, TaskIDs: taskIDs, Status: BatchStatusPlanned, SafetyReason: "ready_tasks_with_done_dependencies_and_disjoint_file_scope", CreatedAt: now, UpdatedAt: now,
	}
	return svc.store.CreateParallelBatch(ctx, batch)
}

func (svc *Service) prepareRunForExecution(ctx context.Context, run AutomationRun) (AutomationRun, projectworkplan.WorkTask, error) {
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "automation_unavailable")
		return updated, projectworkplan.WorkTask{}, err
	}
	if err := svc.validateAutomationPolicy(ctx, automation, run.RunnerKind, run.TaskID); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusPolicyDenied, err.Error())
		return updated, projectworkplan.WorkTask{}, err
	}
	if err := svc.validateRequiredAutomationReviews(ctx, automation); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "automation_review_gate_open")
		return updated, projectworkplan.WorkTask{}, err
	}
	task, err := svc.resolveTask(ctx, run, automation)
	if err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "task_unavailable")
		return updated, projectworkplan.WorkTask{}, err
	}
	if err := validateExecutableTask(task); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusPolicyDenied, err.Error())
		return updated, projectworkplan.WorkTask{}, err
	}
	if !svc.dependenciesDone(ctx, task) {
		err := fmt.Errorf("%w: task_dependencies_not_done", ErrInvalidInput)
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "task_dependencies_not_done")
		return updated, projectworkplan.WorkTask{}, err
	}
	run.TaskID = task.ID
	run.PlanID = task.PlanID
	run.WorkTaskStatus = task.Status
	run.Status = RunStatusClaiming
	run.UpdatedAt = svc.now()
	if run, err = svc.store.UpdateRun(ctx, run); err != nil {
		return run, projectworkplan.WorkTask{}, err
	}
	if _, err := svc.workTasks.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: run.ProjectID, TaskID: task.ID, OwnerAgent: run.AgentID, RunID: run.ID, TraceID: run.TraceID}); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "claim_failed")
		return updated, projectworkplan.WorkTask{}, err
	}
	run.Status = RunStatusStarting
	run.UpdatedAt = svc.now()
	if run, err = svc.store.UpdateRun(ctx, run); err != nil {
		return run, projectworkplan.WorkTask{}, err
	}
	if _, err := svc.workTasks.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: run.ProjectID, TaskID: task.ID, OwnerAgent: run.AgentID, RunID: run.ID, TraceID: run.TraceID}); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "start_failed")
		return updated, projectworkplan.WorkTask{}, err
	}
	run.Status = RunStatusRunning
	run.AttemptCount++
	run.StartedAt = svc.now()
	run.UpdatedAt = run.StartedAt
	run, err = svc.store.UpdateRun(ctx, run)
	if err != nil {
		return run, task, err
	}
	if err := svc.attachRunStartGovernance(ctx, run, task); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusFailed, "governance_action_failed")
		return updated, projectworkplan.WorkTask{}, err
	}
	return run, task, nil
}

func (svc *Service) candidateTasks(ctx context.Context, projectID string, planID string, taskIDs []string) ([]projectworkplan.WorkTask, error) {
	if len(taskIDs) > 0 {
		refs, err := safeRefList(taskIDs, "task_ids")
		if err != nil {
			return nil, err
		}
		out := make([]projectworkplan.WorkTask, 0, len(refs))
		for _, taskID := range refs {
			task, err := svc.workTasks.GetWorkTask(ctx, projectID, taskID)
			if err != nil {
				return nil, err
			}
			out = append(out, task)
		}
		return out, nil
	}
	return svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
}

func (svc *Service) resolveRunnerKind(requested string) (string, error) {
	kind := strings.TrimSpace(requested)
	external := svc.options.RunnerExecution == RunnerExecutionExternal
	if kind == "" {
		if svc.options.RunnerEnabled && (external || svc.codexAvailable()) {
			return RunnerKindCodexCLI, nil
		}
		if svc.options.AllowManualRunner {
			return RunnerKindManual, nil
		}
		return "", fmt.Errorf("%w: codex_cli_unavailable", ErrInvalidInput)
	}
	switch kind {
	case RunnerKindCodexCLI:
		if !svc.options.RunnerEnabled {
			return "", fmt.Errorf("%w: runner_disabled", ErrInvalidInput)
		}
		if !external && !svc.codexAvailable() {
			return "", fmt.Errorf("%w: codex_cli_unavailable", ErrInvalidInput)
		}
		return kind, nil
	case RunnerKindManual:
		if !svc.options.AllowManualRunner {
			return "", fmt.Errorf("%w: manual_runner_denied", ErrInvalidInput)
		}
		if svc.options.RequireCodexWhenAvailable && svc.options.RunnerEnabled && svc.codexAvailable() {
			return "", fmt.Errorf("%w: codex_cli_required", ErrInvalidInput)
		}
		return kind, nil
	default:
		return "", fmt.Errorf("%w: unknown_runner_kind", ErrInvalidInput)
	}
}

func (svc *Service) runCodexTask(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	binaryPath, ok := svc.codexPath()
	if !ok {
		return svc.failRun(ctx, run, RunStatusRunnerUnavailable, "codex_cli_unavailable")
	}
	inputPath, cleanup, err := svc.writeCodexInput(run, task)
	if err != nil {
		return svc.failRun(ctx, run, RunStatusFailed, "codex_input_create_failed")
	}
	defer cleanup()

	timeout := automationMaxRuntime(svc.options.Agents, run.AgentID, svc.options.DefaultMaxRuntime)
	command, err := BuildCodexCommand(CodexCommandInput{
		BinaryPath: binaryPath,
		InputPath:  inputPath,
		Timeout:    timeout,
		EnvAllow:   map[string]string{},
	})
	if err != nil {
		return svc.failRun(ctx, run, RunStatusPolicyDenied, err.Error())
	}

	started := svc.now()
	result, err := svc.codexRunner(ctx, command, 64*1024)
	finished := svc.now()
	attemptStatus := RunStatusCompleted
	failureCategory := ""
	if err != nil {
		if result.TimedOut {
			attemptStatus = RunStatusTimeout
			failureCategory = "codex_cli_timeout"
		} else {
			attemptStatus = RunStatusFailed
			failureCategory = "codex_cli_failed"
		}
	}
	attempt := AutomationAttempt{
		ID:                 svc.newID("automation_attempt"),
		ProjectID:          run.ProjectID,
		AutomationRunID:    run.ID,
		AttemptNumber:      1,
		RunnerKind:         run.RunnerKind,
		CommandRef:         "codex_cli:" + run.ID,
		Status:             attemptStatus,
		FailureCategory:    failureCategory,
		DurationMS:         result.Duration.Milliseconds(),
		EvidenceRefs:       []string{"automation_run:" + run.ID},
		ClaimRefs:          nil,
		KnowledgeRefs:      nil,
		CreatedAt:          started,
		FinishedAt:         finished,
		VerifierResultRefs: nil,
	}
	if _, err := svc.attachAttemptGovernance(ctx, run, attempt, attemptGovernanceRefs{EvidenceRefs: []string{"automation_run:" + run.ID}}); err != nil {
		return svc.failRun(ctx, run, RunStatusFailed, "governance_outcome_failed")
	}
	if _, createErr := svc.store.CreateAttempt(ctx, attempt); createErr != nil {
		return svc.failRun(ctx, run, RunStatusFailed, "attempt_record_failed")
	}
	if err != nil {
		return svc.failRun(ctx, run, attemptStatus, failureCategory)
	}
	run.Status = RunStatusVerifying
	run.SafeSummary = "codex_cli_completed_verification_required"
	run.FinishedAt = finished
	run.UpdatedAt = finished
	return svc.store.UpdateRun(ctx, run)
}

type CodexTaskInput struct {
	SchemaVersion           int      `json:"schema_version"`
	ProjectID               string   `json:"project_id"`
	AutomationRunID         string   `json:"automation_run_id"`
	TraceID                 string   `json:"trace_id,omitempty"`
	PlanID                  string   `json:"plan_id"`
	TaskID                  string   `json:"task_id"`
	TaskRef                 string   `json:"task_ref"`
	Title                   string   `json:"title"`
	Description             string   `json:"description,omitempty"`
	EvidenceNeeded          []string `json:"evidence_needed,omitempty"`
	ContextPackRefs         []string `json:"context_pack_refs,omitempty"`
	LikelyFilesAffected     []string `json:"likely_files_affected,omitempty"`
	VerificationRequirement string   `json:"verification_requirement"`
	ExpectedOutput          string   `json:"expected_output,omitempty"`
	FailureCriteria         string   `json:"failure_criteria,omitempty"`
	ResumeInstructions      string   `json:"resume_instructions,omitempty"`
	RunnerInstructions      []string `json:"runner_instructions"`
}

func (svc *Service) writeCodexInput(run AutomationRun, task projectworkplan.WorkTask) (string, func(), error) {
	payload := codexInputForRun(run, task)
	data := []byte(RenderCodexTaskPrompt(payload))
	dir, err := os.MkdirTemp("", "mivia-automation-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "codex-input.txt")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func RenderCodexTaskPrompt(input CodexTaskInput) string {
	var builder strings.Builder
	builder.WriteString("You are a Mivia automation worker executing one governed Work Task.\n\n")
	builder.WriteString("Perform the task now in the current repository workspace. Keep the change limited to the likely affected files. Do not run full test suites. Do not complete the Work Task; the orchestrator will verify and complete it.\n\n")
	builder.WriteString("Identifiers:\n")
	writePromptLine(&builder, "- Project ID", input.ProjectID)
	writePromptLine(&builder, "- Automation run ID", input.AutomationRunID)
	writePromptLine(&builder, "- Trace ID", input.TraceID)
	writePromptLine(&builder, "- Work Plan ID", input.PlanID)
	writePromptLine(&builder, "- Work Task ID", input.TaskID)
	writePromptLine(&builder, "- Work Task ref", input.TaskRef)
	builder.WriteString("\nTask:\n")
	writePromptLine(&builder, "- Title", input.Title)
	writePromptLine(&builder, "- Description", input.Description)
	writePromptList(&builder, "- Evidence needed", input.EvidenceNeeded)
	writePromptList(&builder, "- Context pack refs", input.ContextPackRefs)
	writePromptList(&builder, "- Likely files affected", input.LikelyFilesAffected)
	writePromptLine(&builder, "- Verification requirement", input.VerificationRequirement)
	writePromptLine(&builder, "- Expected output", input.ExpectedOutput)
	writePromptLine(&builder, "- Failure criteria", input.FailureCriteria)
	writePromptLine(&builder, "- Resume instructions", input.ResumeInstructions)
	builder.WriteString("\nRules:\n")
	for _, instruction := range input.RunnerInstructions {
		writePromptLine(&builder, "-", instruction)
	}
	builder.WriteString("- Return a concise summary of changed files, evidence, risks, and tests not run.\n")
	return builder.String()
}

func writePromptLine(builder *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	builder.WriteString(label)
	builder.WriteString(": ")
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func writePromptList(builder *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	builder.WriteString(label)
	builder.WriteString(":\n")
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		builder.WriteString("  - ")
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
}

func codexInputForRun(run AutomationRun, task projectworkplan.WorkTask) CodexTaskInput {
	return CodexTaskInput{
		SchemaVersion:           1,
		ProjectID:               run.ProjectID,
		AutomationRunID:         run.ID,
		TraceID:                 run.TraceID,
		PlanID:                  task.PlanID,
		TaskID:                  task.ID,
		TaskRef:                 task.TaskRef,
		Title:                   task.Title,
		Description:             task.Description,
		EvidenceNeeded:          append([]string(nil), task.EvidenceNeeded...),
		ContextPackRefs:         append([]string(nil), task.ContextPackRefs...),
		LikelyFilesAffected:     append([]string(nil), task.LikelyFilesAffected...),
		VerificationRequirement: task.VerificationRequirement,
		ExpectedOutput:          task.ExpectedOutput,
		FailureCriteria:         task.FailureCriteria,
		ResumeInstructions:      task.ResumeInstructions,
		RunnerInstructions: []string{
			"Use the Mivia MCP project id from this input.",
			"Do not store raw prompts, completions, source dumps, raw stderr, secrets, roots, provider payloads, or PII.",
			"Use only the bounded task scope and likely affected files unless current source proves a narrower necessary change.",
			"Do not run verifier commands unless this task explicitly allows worker verification.",
			"Leave verifier execution and task completion to the orchestrator.",
		},
	}
}

func automationMaxRuntime(agents []AutomationAgent, agentID string, fallback time.Duration) time.Duration {
	for _, agent := range agents {
		if agent.ID == agentID && agent.MaxRuntime > 0 {
			return agent.MaxRuntime
		}
	}
	if fallback > 0 {
		return fallback
	}
	return 10 * time.Minute
}

func (svc *Service) createRejectedRun(ctx context.Context, automation Automation, planID string, taskID string, owner string, input SubmitRunInput, status string, reason string) (AutomationRun, error) {
	now := svc.now()
	run := AutomationRun{ID: svc.newID("automation_run"), ProjectID: automation.ProjectID, AutomationID: automation.ID, AgentID: firstNonEmpty(owner, automation.AgentID), PlanID: planID, TaskID: taskID, Status: status, RunnerKind: strings.TrimSpace(input.RunnerKind), FailureCategory: safeFailure(reason), CreatedAt: now, UpdatedAt: now, FinishedAt: now}
	return svc.store.CreateRun(ctx, run)
}

func (svc *Service) failRun(ctx context.Context, run AutomationRun, status string, reason string) (AutomationRun, error) {
	run.Status = status
	run.FailureCategory = safeFailure(reason)
	run.FinishedAt = svc.now()
	run.UpdatedAt = run.FinishedAt
	return svc.store.UpdateRun(ctx, run)
}

func (svc *Service) resolveTask(ctx context.Context, run AutomationRun, automation Automation) (projectworkplan.WorkTask, error) {
	if run.TaskID != "" {
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil {
			return projectworkplan.WorkTask{}, err
		}
		if err := validateAllowedTaskRef(automation, task); err != nil {
			return projectworkplan.WorkTask{}, err
		}
		return task, nil
	}
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: run.ProjectID, PlanID: run.PlanID})
	if err != nil {
		return projectworkplan.WorkTask{}, err
	}
	for _, task := range tasks {
		if validateAllowedTaskRef(automation, task) != nil {
			continue
		}
		if err := validateExecutableTask(task); err == nil && svc.dependenciesDone(ctx, task) {
			return task, nil
		}
	}
	return projectworkplan.WorkTask{}, fmt.Errorf("%w: no ready task", ErrInvalidInput)
}

func (svc *Service) validateAutomationPolicy(ctx context.Context, automation Automation, runnerKind string, taskID string) error {
	if automation.SourceKind == AutomationSourceWorkflow {
		if err := validatePermissionSnapshotRef(automation.PermissionRef); err != nil {
			return err
		}
	}
	if strings.HasPrefix(automation.PermissionRef, PermissionSnapshotRefPrefix) {
		if svc.options.PermissionResolver == nil {
			return fmt.Errorf("%w: permission_resolver_unavailable", ErrInvalidInput)
		}
		metadata, err := svc.options.PermissionResolver.CheckAutomationPermission(ctx, PermissionCheckInput{
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AutomationRef:   automation.AutomationRef,
			AgentID:         automation.AgentID,
			PermissionRef:   automation.PermissionRef,
			RunnerKind:      runnerKind,
			RunnerExecution: svc.options.RunnerExecution,
		})
		if err != nil {
			return err
		}
		if metadata.AgentID != automation.AgentID {
			return fmt.Errorf("%w: permission_agent_mismatch", ErrInvalidInput)
		}
		if !permissionAllowsRunner(metadata, runnerKind) {
			return fmt.Errorf("%w: permission_runner_denied", ErrInvalidInput)
		}
	}
	if taskID != "" && len(automation.AllowedTaskRefs) > 0 && svc.workTasks != nil {
		task, err := svc.workTasks.GetWorkTask(ctx, automation.ProjectID, taskID)
		if err != nil {
			return fmt.Errorf("%w: task_unavailable", ErrInvalidInput)
		}
		if err := validateAllowedTaskRef(automation, task); err != nil {
			return err
		}
	}
	return nil
}

func validateAllowedTaskRef(automation Automation, task projectworkplan.WorkTask) error {
	if len(automation.AllowedTaskRefs) == 0 {
		return nil
	}
	for _, ref := range automation.AllowedTaskRefs {
		if ref == task.TaskRef || ref == task.ID {
			return nil
		}
	}
	return fmt.Errorf("%w: task_ref_not_allowed", ErrInvalidInput)
}

func validatePermissionSnapshotRef(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: missing_permission_snapshot_ref", ErrInvalidInput)
	}
	if !strings.HasPrefix(value, PermissionSnapshotRefPrefix) {
		return fmt.Errorf("%w: malformed_permission_snapshot_ref", ErrInvalidInput)
	}
	if _, err := safeRef(strings.TrimPrefix(value, PermissionSnapshotRefPrefix), "permission_snapshot_id"); err != nil {
		return fmt.Errorf("%w: malformed_permission_snapshot_ref", ErrInvalidInput)
	}
	return nil
}

func permissionAllowsRunner(metadata PermissionSnapshotMetadata, runnerKind string) bool {
	if runnerKind == "" {
		return true
	}
	for _, denied := range metadata.DeniedCommands {
		normalized := strings.ToLower(strings.TrimSpace(denied))
		if runnerKind == RunnerKindCodexCLI && (normalized == "codex" || normalized == "codex_cli" || strings.Contains(normalized, "codex cli")) {
			return false
		}
		if runnerKind == RunnerKindManual && normalized == "manual" {
			return false
		}
	}
	if len(metadata.AllowedRunnerKinds) == 0 {
		return true
	}
	for _, allowed := range metadata.AllowedRunnerKinds {
		if strings.TrimSpace(allowed) == runnerKind {
			return true
		}
	}
	return false
}

func (svc *Service) dependenciesDone(ctx context.Context, task projectworkplan.WorkTask) bool {
	for _, dependencyID := range task.DependencyTaskIDs {
		dependency, err := svc.workTasks.GetWorkTask(ctx, task.ProjectID, dependencyID)
		if err != nil || dependency.Status != projectworkplan.WorkTaskStatusDone {
			return false
		}
	}
	return true
}

func validateExecutableTask(task projectworkplan.WorkTask) error {
	if task.Status != projectworkplan.WorkTaskStatusReady {
		return fmt.Errorf("%w: task_not_ready", ErrInvalidInput)
	}
	if task.DecompositionQuality != projectworkplan.DecompositionReady {
		return fmt.Errorf("%w: decomposition_not_ready", ErrInvalidInput)
	}
	if strings.TrimSpace(task.VerificationRequirement) == "" {
		return fmt.Errorf("%w: missing_verification", ErrInvalidInput)
	}
	return nil
}

func firstFileConflict(task projectworkplan.WorkTask, used map[string]string) string {
	for _, file := range task.LikelyFilesAffected {
		key := strings.ToLower(strings.TrimSpace(file))
		if key == "" {
			continue
		}
		if used[key] != "" {
			return key
		}
	}
	return ""
}

func safeProjectObject(projectID, objectID, field string) (string, string, error) {
	projectID, err := safeRef(projectID, "project_id")
	if err != nil {
		return "", "", err
	}
	objectID, err = safeRef(objectID, field)
	if err != nil {
		return "", "", err
	}
	return projectID, objectID, nil
}

func safeRef(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if !refPattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s must be a safe ref", ErrInvalidInput, field)
	}
	return value, nil
}

func safeOptionalRef(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeRef(value, field)
}

func safeRefList(values []string, field string) ([]string, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("%w: %s has too many values", ErrInvalidInput, field)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		safe, err := safeRef(value, field)
		if err != nil {
			return nil, err
		}
		out = append(out, safe)
	}
	return out, nil
}

func safeRequiredText(value, field string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	return safeText(value, field, max)
}

func safeText(value, field string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidInput, field)
	}
	unsafeMarkers := []string{"BEGIN PRIVATE KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "provider_payload", "raw_prompt", "raw_completion", "raw_stderr", "ghp_", "sk-"}
	lower := strings.ToLower(value)
	for _, marker := range unsafeMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return "", fmt.Errorf("%w: %s contains unsafe marker", ErrInvalidInput, field)
		}
	}
	if emailPattern.MatchString(value) || phonePattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s contains pii-like content", ErrInvalidInput, field)
	}
	return value, nil
}

func safeAutomationStatus(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case AutomationStatusDraft, AutomationStatusEnabled, AutomationStatusDisabled, AutomationStatusRunning, AutomationStatusPaused, AutomationStatusFailed, AutomationStatusCancelled, AutomationStatusSuperseded:
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("%w: unknown automation status", ErrInvalidInput)
	}
}

func safeOptionalAutomationStatus(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeAutomationStatus(value)
}

func safeAutomationTrigger(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return TriggerKindManual, nil
	}
	switch value {
	case TriggerKindManual, TriggerKindAutomatic:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unknown automation trigger", ErrInvalidInput)
	}
}

func safeAutomationSource(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return AutomationSourceManual, nil
	}
	switch value {
	case AutomationSourceManual, AutomationSourceWorkflow:
		return value, nil
	default:
		return "", fmt.Errorf("%w: unknown automation source", ErrInvalidInput)
	}
}

func safeAttemptStatus(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case RunStatusCompleted, RunStatusFailed, RunStatusTimeout, RunStatusBlocked, RunStatusCancelled:
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("%w: unknown attempt status", ErrInvalidInput)
	}
}

func safeFailure(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "unknown"
	}
	if len(value) > 100 {
		return value[:100]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func newID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}
