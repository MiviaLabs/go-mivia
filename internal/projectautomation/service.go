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
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

var ErrInvalidInput = errors.New("invalid project automation input")

const defaultAutomationMaxRetries = 3
const defaultAutomationMaxReplacementRunsPerTask = 3
const automationReplacementRetryLimitCategory = "automation_replacement_retry_limit_reached"
const defaultExternalRunLeaseTTL = 90 * time.Second

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
	GetWorkPlan(context.Context, string, string) (projectworkplan.WorkPlan, error)
	GetWorkTask(context.Context, string, string) (projectworkplan.WorkTask, error)
	ListOpenWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)
	ClaimWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	StartWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	AttachEvidence(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
	AttachVerifierResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
	AttachReviewResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error)
	CompleteWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	FailWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
	BlockWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error)
}

type workTaskStatusUpdater interface {
	UpdateWorkTaskStatus(context.Context, projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error)
}

type workTaskScopeExpander interface {
	ExpandWorkTaskScope(context.Context, projectworkplan.ExpandWorkTaskScopeInput) (projectworkplan.WorkTask, error)
}

type workPlanLister interface {
	ListWorkPlans(context.Context, projectworkplan.WorkPlanFilter) ([]projectworkplan.WorkPlan, error)
}

type remediationWorkPlanAPI interface {
	CreateWorkPlan(context.Context, projectworkplan.CreateWorkPlanInput) (projectworkplan.WorkPlan, error)
	ListWorkPlans(context.Context, projectworkplan.WorkPlanFilter) ([]projectworkplan.WorkPlan, error)
	CreateWorkTask(context.Context, projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error)
	UpdateWorkPlanStatus(context.Context, projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error)
}

type Service struct {
	store          Store
	workTasks      WorkTaskAPI
	options        Options
	claimMu        sync.Mutex
	startedAt      time.Time
	now            func() time.Time
	newID          func(string) string
	codexAvailable func() bool
	codexPath      func() (string, bool)
	codexRunner    func(context.Context, CodexCommand, int64) (CodexRunResult, error)
}

func (svc *Service) CreateRemediationFromFinding(ctx context.Context, input CreateRemediationFromFindingInput) (CreateRemediationFromFindingResult, error) {
	if svc.store == nil {
		return CreateRemediationFromFindingResult{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	workPlans, ok := svc.workTasks.(remediationWorkPlanAPI)
	if !ok || workPlans == nil {
		return CreateRemediationFromFindingResult{}, fmt.Errorf("%w: work plan API is required", ErrInvalidInput)
	}
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	findingRef, err := safeRef(input.FindingRef, "finding_ref")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	if strings.TrimSpace(input.FindingStatus) != "confirmed" {
		return CreateRemediationFromFindingResult{}, fmt.Errorf("%w: finding_status must be confirmed", ErrInvalidInput)
	}
	title, err := safeRequiredText(input.Title, "title", 200)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	summary, err := safeRequiredText(input.Summary, "summary", 500)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	severity, err := safeOptionalRef(input.Severity, "severity")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	ownerAgent, err := safeOptionalRef(input.OwnerAgent, "owner_agent")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	if ownerAgent == "" {
		ownerAgent = "orchestrator"
	}
	implementationAgentID, err := safeOptionalRef(input.ImplementationAgentID, "implementation_agent_id")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	if implementationAgentID == "" {
		implementationAgentID = "codex-worker"
	}
	reviewerAgentID := independentReviewerAgent(implementationAgentID)
	runID, err := safeOptionalRef(input.CreatedByRunID, "created_by_run_id")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	gitBaseRef, err := safeOptionalRef(input.GitBaseRef, "git_base_ref")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	if gitBaseRef == "" {
		gitBaseRef, err = svc.remediationBaseRefFromCreatorRun(ctx, workPlans, projectID, runID)
		if err != nil {
			return CreateRemediationFromFindingResult{}, err
		}
	}
	gitBranchRef, err := safeOptionalRef(input.GitBranchRef, "git_branch_ref")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	gitWorktreeRef, err := safeOptionalRef(input.GitWorktreeRef, "git_worktree_ref")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	verification, err := safeRequiredText(input.VerificationRequirement, "verification_requirement", 500)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	reviewGate, err := safeOptionalText(input.ReviewGate, "review_gate", 500)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	filesToRead, err := safeFileList(input.FilesToRead, "files_to_read")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	filesToEdit, err := safeFileList(input.FilesToEdit, "files_to_edit")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	likelyFiles, err := safeFileList(input.LikelyFilesAffected, "likely_files_affected")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	if len(likelyFiles) == 0 {
		likelyFiles = append([]string(nil), filesToEdit...)
	}
	filesToEdit = remediationEditScope(filesToEdit, likelyFiles)
	evidenceRefs, err := safeRefList(input.EvidenceRefs, "evidence_refs")
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	findingToken := safeBranchToken(findingRef)
	findingDisplay := safeDisplayRef(findingRef)
	workerEvidenceRefs := safeWorkerEvidenceRefs(append([]string{"confirmed-finding-" + findingToken}, evidenceRefs...))
	planRef := "remediate-" + findingRef
	taskRef := "fix-" + findingRef
	reviewTaskRef := "review-" + taskRef
	automationRef := "auto-remediate-" + findingRef
	reviewAutomationRef := "auto-review-remediation-" + findingRef
	gitBranchRef = remediationFindingScopedGitRef(gitBranchRef, "mivia/remediate-", findingToken)
	gitWorktreeRef = remediationFindingScopedGitRef(gitWorktreeRef, "wt-remediate-", findingToken)
	goal := "Fix confirmed finding " + findingDisplay + ": " + summary
	if severity != "" {
		goal = "Fix " + severity + " confirmed finding " + findingDisplay + ": " + summary
	}
	planInput := projectworkplan.CreateWorkPlanInput{
		ProjectID:        projectID,
		PlanRef:          planRef,
		UserRequestRef:   findingRef,
		Title:            "Remediate confirmed finding " + findingDisplay,
		GoalSummary:      goal,
		OwnerAgent:       ownerAgent,
		CreatedByRunID:   runID,
		TraceID:          traceID,
		ResumeSummary:    "Use the ready remediation task generated from confirmed finding metadata.",
		IsolationMode:    projectworkplan.WorkPlanIsolationDedicatedWorktree,
		ParallelGroupRef: "finding-" + findingRef,
		GitBaseRef:       gitBaseRef,
		GitBranchRef:     gitBranchRef,
		GitWorktreeRef:   gitWorktreeRef,
	}
	plan, err := svc.getOrCreateRemediationWorkPlan(ctx, workPlans, planInput)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	taskInput := projectworkplan.CreateWorkTaskInput{
		ProjectID:               projectID,
		PlanID:                  plan.ID,
		TaskRef:                 taskRef,
		Title:                   title,
		Description:             summary,
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              implementationAgentID,
		RunID:                   runID,
		TraceID:                 traceID,
		EvidenceNeeded:          workerEvidenceRefs,
		FilesToRead:             filesToRead,
		FilesToEdit:             filesToEdit,
		LikelyFilesAffected:     likelyFiles,
		VerificationRequirement: verification + " Include a focused regression test for the confirmed bug when feasible; if not feasible, record the concrete reason in the task outcome.",
		ExpectedOutput:          "Implementation that fixes confirmed finding " + findingDisplay + ", includes a focused regression test when feasible, and records safe review and verifier refs.",
		FailureCriteria:         "Fail if the finding is not fixed, scope expands beyond the listed files without a new plan, verification cannot be performed, or a feasible regression test is omitted.",
		ReviewGate:              reviewGate,
		ResumeInstructions:      "Resume from the confirmed finding ref and the generated remediation Work Plan.",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
	task, err := svc.getOrCreateOpenRemediationWorkTask(ctx, workPlans, taskInput)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	taskDisplay := safeDisplayRef(task.ID)
	reviewTaskInput := projectworkplan.CreateWorkTaskInput{
		ProjectID:               projectID,
		PlanID:                  plan.ID,
		TaskRef:                 reviewTaskRef,
		Title:                   "Review remediation " + findingDisplay,
		Description:             "Independently review implementation task " + taskDisplay + " for confirmed finding " + findingDisplay + ".",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              reviewerAgentID,
		RunID:                   runID,
		TraceID:                 traceID,
		EvidenceNeeded:          safeWorkerEvidenceRefs(append(append([]string{"review-target-" + task.ID, "implementation-task-" + task.ID}, workerEvidenceRefs...), "implementation-output-refs")),
		FilesToRead:             uniqueRefs(append(append([]string{}, filesToRead...), filesToEdit...)),
		LikelyFilesAffected:     likelyFiles,
		VerificationRequirement: "Attach an independent review_result_ref to the implementation task before completion.",
		ExpectedOutput:          "Independent review decision for implementation task " + taskDisplay + " with review refs attached to the implementation task.",
		FailureCriteria:         "Block on self-review, missing implementation evidence, missing verifier refs, unsafe payloads, or unclear approval decision.",
		ReviewGate:              "independent-reviewer-must-not-be-" + implementationAgentID,
		ResumeInstructions:      "Review the implementation task only. Attach review_result_ref to that implementation task, then complete this review task.",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
	reviewTask, err := svc.getOrCreateOpenRemediationWorkTask(ctx, workPlans, reviewTaskInput)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	automationInput := CreateAutomationInput{
		ProjectID:       projectID,
		AutomationRef:   automationRef,
		Title:           "Implement remediation " + findingDisplay,
		Purpose:         "Execute confirmed finding remediation task " + taskDisplay + ".",
		Status:          AutomationStatusEnabled,
		AgentID:         implementationAgentID,
		PlanID:          plan.ID,
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "work_plan_status_trigger",
		PermissionRef:   "permission-remediation-" + findingRef,
		SourceKind:      AutomationSourceManual,
		CreatedByRunID:  runID,
		TraceID:         traceID,
	}
	automation, err := svc.getOrCreateRemediationAutomation(ctx, automationInput)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	reviewAutomationInput := CreateAutomationInput{
		ProjectID:       projectID,
		AutomationRef:   reviewAutomationRef,
		Title:           "Review remediation " + findingDisplay,
		Purpose:         "Independently review remediation task " + taskDisplay + " through the generated review task.",
		Status:          AutomationStatusEnabled,
		AgentID:         reviewerAgentID,
		PlanID:          plan.ID,
		AllowedTaskRefs: []string{reviewTask.ID, reviewTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission-remediation-review-" + findingRef,
		SourceKind:      AutomationSourceManual,
		CreatedByRunID:  runID,
		TraceID:         traceID,
	}
	reviewAutomation, err := svc.getOrCreateRemediationAutomation(ctx, reviewAutomationInput)
	if err != nil {
		return CreateRemediationFromFindingResult{}, err
	}
	result := CreateRemediationFromFindingResult{WorkPlan: plan, WorkTask: task, ReviewTask: reviewTask, Automation: automation, ReviewAutomation: reviewAutomation}
	if input.ActivatePlan && plan.Status != projectworkplan.WorkPlanStatusActive {
		activated, err := workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:     projectID,
			PlanID:        plan.ID,
			Status:        projectworkplan.WorkPlanStatusActive,
			ResumeSummary: "Automatic remediation queued through Work Plan status trigger.",
		})
		if err != nil {
			return result, err
		}
		result.WorkPlan = activated
		result.Activated = true
	}
	return result, nil
}

func (svc *Service) remediationBaseRefFromCreatorRun(ctx context.Context, workPlans remediationWorkPlanAPI, projectID string, runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", nil
	}
	run, err := svc.store.GetRun(ctx, projectID, runID)
	if err != nil || strings.TrimSpace(run.PlanID) == "" {
		return "", nil
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: projectID})
	if err != nil {
		return "", err
	}
	for _, plan := range plans {
		if plan.ID != run.PlanID {
			continue
		}
		return safeOptionalRef(plan.GitBaseRef, "creator_plan.git_base_ref")
	}
	return "", nil
}

func (svc *Service) getOrCreateRemediationWorkPlan(ctx context.Context, workPlans remediationWorkPlanAPI, input projectworkplan.CreateWorkPlanInput) (projectworkplan.WorkPlan, error) {
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: input.ProjectID})
	if err != nil {
		return projectworkplan.WorkPlan{}, err
	}
	for _, plan := range plans {
		if plan.PlanRef == input.PlanRef {
			return plan, nil
		}
	}
	return workPlans.CreateWorkPlan(ctx, input)
}

func (svc *Service) getOrCreateOpenRemediationWorkTask(ctx context.Context, workPlans remediationWorkPlanAPI, input projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error) {
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: input.ProjectID, PlanID: input.PlanID})
	if err != nil {
		return projectworkplan.WorkTask{}, err
	}
	for _, task := range tasks {
		if task.TaskRef == input.TaskRef {
			return task, nil
		}
	}
	return workPlans.CreateWorkTask(ctx, input)
}

func (svc *Service) getOrCreateRemediationAutomation(ctx context.Context, input CreateAutomationInput) (Automation, error) {
	automations, err := svc.store.ListAutomations(ctx, AutomationFilter{ProjectID: input.ProjectID})
	if err != nil {
		return Automation{}, err
	}
	for _, automation := range automations {
		if automation.AutomationRef == input.AutomationRef {
			return automation, nil
		}
	}
	return svc.CreateAutomation(ctx, input)
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
	options.DirtyScopeRecovery.AllowedSupportPathspecs = safeTaskPathspecs(options.DirtyScopeRecovery.AllowedSupportPathspecs)
	startedAt := time.Now().UTC()
	return &Service{
		store:     store,
		workTasks: workTasks,
		options:   options,
		startedAt: startedAt,
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
	if svc.workTasks != nil && strings.TrimSpace(planID) != "" {
		if err := svc.validatePlanExecutable(ctx, projectID, planID); err != nil {
			return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, runPlanFailureCategory(err))
		}
	}
	if err := svc.ensureRequiredAutomationReviewRuns(ctx, automation, runnerKind, input); err != nil {
		return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, err.Error())
	}
	if !svc.requiredAutomationReviewsDone(ctx, automation) {
		return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, "automation_review_gate_open")
	}
	var resolvedTask projectworkplan.WorkTask
	if svc.workTasks != nil {
		if taskID == "" && automation.TriggerKind == TriggerKindAutomatic {
			task, err := svc.resolveTask(ctx, AutomationRun{ProjectID: projectID, AutomationID: automation.ID, AgentID: owner, PlanID: planID, RunnerKind: runnerKind}, automation)
			if err != nil {
				return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, "task_unavailable")
			}
			taskID = task.ID
			resolvedTask = task
		} else if taskID != "" {
			task, err := svc.workTasks.GetWorkTask(ctx, projectID, taskID)
			if err != nil {
				return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, "task_unavailable")
			}
			resolvedTask = task
		}
		if strings.TrimSpace(resolvedTask.ID) != "" {
			if err := svc.validateRunPlanExecutable(ctx, AutomationRun{ProjectID: projectID, PlanID: planID}, resolvedTask); err != nil {
				return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusBlocked, runPlanFailureCategory(err))
			}
		}
	}
	if err := svc.validateAutomationPolicy(ctx, automation, runnerKind, taskID, owner); err != nil {
		return svc.createRejectedRun(ctx, automation, planID, taskID, owner, input, RunStatusPolicyDenied, err.Error())
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
		ParentRunID: parentRunID, TraceID: automation.TraceID, CreatedAt: now, UpdatedAt: now,
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
	_ = svc.reconcileRunningRuns(ctx, projectID)
	_ = svc.reconcileVerifyingRuns(ctx, projectID)
	statusFilter := strings.TrimSpace(filter.Status)
	filter.Status = ""
	runs, err := svc.store.ListRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := runs[:0]
	for _, run := range runs {
		if statusFilter != "" && run.Status != statusFilter {
			continue
		}
		out = append(out, run)
	}
	return out, nil
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
	if err := svc.blockReadyTasksWithoutEnabledAutomation(ctx, event.ProjectID, event.PlanID, automations); err != nil {
		return err
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

func (svc *Service) blockReadyTasksWithoutEnabledAutomation(ctx context.Context, projectID string, planID string, automations []Automation) error {
	if svc == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(planID) == "" {
		return nil
	}
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Status != projectworkplan.WorkTaskStatusReady {
			continue
		}
		if hasEnabledAutomationForReadyTask(automations, task) {
			continue
		}
		blocked, err := svc.workTasks.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			SafeNextAction:     "no_enabled_matching_automation",
			TraceID:            "no-enabled-matching-automation",
			BlockedReason:      "No enabled automatic automation matches this ready Work Task.",
			ResumeInstructions: "Enable or create an automatic automation for this task_ref and plan before resuming the Work Plan.",
		})
		if err != nil {
			return err
		}
		if err := svc.updatePlanAfterTerminalTask(ctx, blocked); err != nil {
			return err
		}
	}
	return nil
}

func hasEnabledAutomationForReadyTask(automations []Automation, task projectworkplan.WorkTask) bool {
	for _, automation := range automations {
		if automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || automation.PlanID != task.PlanID {
			continue
		}
		if containsRef(automation.RequiredReviewTaskIDs, task.ID) {
			return true
		}
		if validateAllowedTaskRef(automation, task) == nil {
			return true
		}
	}
	return false
}

func (svc *Service) RunNow(ctx context.Context, input SubmitRunInput) (AutomationRun, error) {
	run, err := svc.SubmitRun(ctx, input)
	if err != nil {
		return run, err
	}
	if svc.options.RunnerExecution == RunnerExecutionExternal {
		if strings.TrimSpace(input.SafeNextAction) == RunSafeSummaryGitOpsPostTaskRecovery {
			return svc.prepareExternalGitOpsPostTaskRecoveryRun(ctx, run)
		}
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
	run, task, err := svc.prepareRunForExecution(ctx, run, "")
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

func (svc *Service) prepareExternalGitOpsPostTaskRecoveryRun(ctx context.Context, run AutomationRun) (AutomationRun, error) {
	if svc.workTasks == nil || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		run.Status = RunStatusPolicyDenied
		run.FailureCategory = "work_task_api_unavailable"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil || !taskHasGitOpsRecoveryCloseout(task) {
		run.Status = RunStatusPolicyDenied
		run.FailureCategory = "gitops_recovery_closeout_missing"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	if updater, ok := svc.workTasks.(workTaskStatusUpdater); ok && updater != nil {
		updatedTask, updateErr := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
				ProjectID:      task.ProjectID,
				TaskID:         task.ID,
				RunID:          run.ID,
				TraceID:        firstNonEmpty(run.TraceID, run.ID),
				SafeNextAction: "explicit_gitops_post_task_recovery",
			},
			Status: task.Status,
		})
		if updateErr != nil {
			return AutomationRun{}, updateErr
		}
		task = updatedTask
	}
	run.SafeSummary = RunSafeSummaryGitOpsPostTaskRecovery
	if !taskOwnsGitOpsRecoveryRun(task, run) {
		run.Status = RunStatusPolicyDenied
		run.FailureCategory = "gitops_recovery_run_not_attached"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	now := svc.now()
	run.Status = RunStatusFailed
	run.WorkTaskStatus = task.Status
	run.FailureCategory = "gitops_post_task_failed"
	run.UpdatedAt = now
	return svc.store.UpdateRun(ctx, run)
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
	run, task, err := svc.prepareRunForExecution(ctx, run, "")
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
	svc.claimMu.Lock()
	defer svc.claimMu.Unlock()
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
	runnerID, err := safeOptionalRef(input.RunnerID, "runner_id")
	if err != nil {
		return ClaimedRun{}, err
	}
	if err := svc.reconcileQueuedRunsFromTerminalPlans(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_queued_terminal_plans", err)
	}
	if claimed, ok, err := svc.claimInterruptedStartingRun(ctx, projectID, agentID, runnerID); err != nil || ok {
		return claimed, claimNextStepError("claim_interrupted_starting", err)
	}
	if err := svc.reconcileRunningRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_running", err)
	}
	if err := svc.reconcileVerifyingRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_verifying", err)
	}
	if err := svc.reconcileInterruptedRunsWithProgressedTasks(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_interrupted_progressed", err)
	}
	if err := svc.reconcileRecoverablePreExecutionRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_recoverable_pre_execution", err)
	}
	if err := svc.reconcileExhaustedPreExecutionRecoveryRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_exhausted_pre_execution", err)
	}
	if err := svc.reconcileExhaustedGitOpsRecoveryRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_exhausted_gitops", err)
	}
	if err := svc.reconcileRecoveryRunsWithStaleReadyTasks(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_stale_ready_recovery", err)
	}
	if claimed, ok, err := svc.claimPreExecutionRecovery(ctx, projectID, agentID, runnerID); err != nil || ok {
		return claimed, claimNextStepError("claim_pre_execution_recovery", err)
	}
	if claimed, ok, err := svc.claimGitOpsPostTaskRecovery(ctx, projectID, agentID, runnerID); err != nil || ok {
		return claimed, claimNextStepError("claim_gitops_post_task_recovery", err)
	}
	if claimed, ok, err := svc.claimPostImplementationReviewRecovery(ctx, projectID, agentID, runnerID); err != nil || ok {
		return claimed, claimNextStepError("claim_post_implementation_review_recovery", err)
	}
	if claimed, ok, err := svc.claimInterruptedStartingRun(ctx, projectID, agentID, runnerID); err != nil || ok {
		return claimed, claimNextStepError("claim_interrupted_starting_after_recovery", err)
	}
	if err := svc.reconcileReadyAutomationsForProject(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_ready_automations", err)
	}
	claimed, ok, skippedReason, err := svc.claimFirstQueuedRun(ctx, projectID, agentID, runnerID)
	if err != nil || ok {
		return claimed, err
	}
	if err := svc.reconcileRunningRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_running_after_claim", err)
	}
	if err := svc.reconcileVerifyingRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_verifying_after_claim", err)
	}
	if err := svc.reconcileInterruptedRunsWithProgressedTasks(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_interrupted_progressed_after_claim", err)
	}
	if err := svc.reconcileRecoverablePreExecutionRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_recoverable_pre_execution_after_claim", err)
	}
	if err := svc.reconcileExhaustedPreExecutionRecoveryRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_exhausted_pre_execution_after_claim", err)
	}
	if err := svc.reconcileExhaustedGitOpsRecoveryRuns(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_exhausted_gitops_after_claim", err)
	}
	if err := svc.reconcileRecoveryRunsWithStaleReadyTasks(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_stale_ready_recovery_after_claim", err)
	}
	if err := svc.queueOutstandingPostImplementationReviews(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("queue_outstanding_post_implementation_reviews", err)
	}
	if claimed, ok, err := svc.claimInterruptedStartingRun(ctx, projectID, agentID, runnerID); err != nil || ok {
		return claimed, claimNextStepError("claim_interrupted_starting_after_review_queue", err)
	}
	if err := svc.reconcileReadyAutomationsForProject(ctx, projectID); err != nil {
		return ClaimedRun{}, claimNextStepError("reconcile_ready_automations_after_review_queue", err)
	}
	claimed, ok, skippedReasonAfterReconcile, err := svc.claimFirstQueuedRun(ctx, projectID, agentID, runnerID)
	if err != nil || ok {
		return claimed, err
	}
	if skippedReasonAfterReconcile != "" {
		skippedReason = skippedReasonAfterReconcile
	}
	if skippedReason != "" {
		return ClaimedRun{}, fmt.Errorf("%w: queued automation runs not claimable: %s", ErrInvalidInput, skippedReason)
	}
	return ClaimedRun{}, fmt.Errorf("%w: no queued automation run", ErrInvalidInput)
}

func claimNextStepError(step string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: claim_next_%s_failed:%s", ErrInvalidInput, step, safeFailure(err.Error()))
}

func (svc *Service) claimFirstQueuedRun(ctx context.Context, projectID string, agentID string, runnerID string) (ClaimedRun, bool, string, error) {
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusQueued})
	if err != nil {
		return ClaimedRun{}, false, "", err
	}
	svc.sortQueuedRunsForClaim(ctx, runs)
	skippedReason := ""
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI {
			continue
		}
		if agentID != "" && run.AgentID != "" && run.AgentID != agentID {
			continue
		}
		claimed, task, err := svc.prepareRunForExecution(ctx, run, runnerID)
		if err != nil {
			if skippedReason == "" {
				skippedReason = claimSkipReason(claimed, err)
			}
			continue
		}
		timeout := automationMaxRuntime(svc.options.Agents, claimed.AgentID, svc.options.DefaultMaxRuntime)
		return ClaimedRun{Run: claimed, CodexInput: svc.codexInputForClaim(ctx, claimed, task), TimeoutMS: timeout.Milliseconds()}, true, "", nil
	}
	return ClaimedRun{}, false, skippedReason, nil
}

func claimSkipReason(run AutomationRun, err error) string {
	if strings.TrimSpace(run.FailureCategory) != "" {
		return safeFailure(run.FailureCategory)
	}
	if err == nil {
		return "unknown"
	}
	reason := strings.TrimSpace(err.Error())
	prefix := ErrInvalidInput.Error() + ":"
	if strings.HasPrefix(reason, prefix) {
		reason = strings.TrimSpace(strings.TrimPrefix(reason, prefix))
	} else if idx := strings.LastIndex(reason, ":"); idx >= 0 && idx+1 < len(reason) {
		reason = strings.TrimSpace(reason[idx+1:])
	}
	return safeFailure(reason)
}

func (svc *Service) reconcileQueuedRunsFromTerminalPlans(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil {
		return nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusQueued})
	if err != nil {
		return err
	}
	for _, run := range runs {
		if strings.TrimSpace(run.PlanID) == "" {
			continue
		}
		err := svc.validatePlanExecutable(ctx, run.ProjectID, run.PlanID)
		if err == nil || runPlanFailureCategory(err) != "work_plan_terminal" {
			continue
		}
		if _, err := svc.failRun(ctx, run, RunStatusBlocked, "work_plan_terminal"); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) claimInterruptedStartingRun(ctx context.Context, projectID string, agentID string, runnerID string) (ClaimedRun, bool, error) {
	runs := make([]AutomationRun, 0)
	for _, status := range []string{RunStatusClaiming, RunStatusStarting} {
		statusRuns, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: status})
		if err != nil {
			return ClaimedRun{}, false, err
		}
		runs = append(runs, statusRuns...)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	skippedReason := ""
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || run.TaskID == "" {
			continue
		}
		if agentID != "" && run.AgentID != "" && run.AgentID != agentID {
			continue
		}
		if !run.ClaimedAt.IsZero() || !run.LastHeartbeatAt.IsZero() || !run.LeaseExpiresAt.IsZero() {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		reportUnclaimable := strings.TrimSpace(run.SafeSummary) == RunSafeSummaryPostImplementationReviewQueued
		if err != nil || strings.TrimSpace(task.ClaimedByRunID) != run.ID {
			if reportUnclaimable && skippedReason == "" {
				skippedReason = "starting_run_task_not_owned"
			}
			continue
		}
		switch task.Status {
		case projectworkplan.WorkTaskStatusReady, projectworkplan.WorkTaskStatusClaimed, projectworkplan.WorkTaskStatusInProgress:
		default:
			continue
		}
		automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
		if err != nil {
			if reportUnclaimable && skippedReason == "" {
				skippedReason = "starting_run_automation_unavailable"
			}
			continue
		}
		if err := svc.validateAutomationPolicy(ctx, automation, run.RunnerKind, run.TaskID, run.AgentID); err != nil {
			if reportUnclaimable && skippedReason == "" {
				skippedReason = "starting_run_policy_denied:" + claimSkipReason(run, err)
			}
			continue
		}
		if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil {
			_, _ = svc.failRun(ctx, run, RunStatusBlocked, runPlanFailureCategory(err))
			if reportUnclaimable && skippedReason == "" {
				skippedReason = "starting_run_plan_not_executable:" + runPlanFailureCategory(err)
			}
			continue
		}
		if !svc.isAutomationReviewTask(automation, run.TaskID) {
			if err := svc.validateRequiredAutomationReviews(ctx, automation); err != nil {
				if reportUnclaimable && skippedReason == "" {
					skippedReason = "starting_run_review_gate_open:" + claimSkipReason(run, err)
				}
				continue
			}
		}
		if task.Status != projectworkplan.WorkTaskStatusInProgress {
			startedTask, err := svc.workTasks.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: run.ProjectID, TaskID: task.ID, OwnerAgent: run.AgentID, RunID: run.ID, TraceID: run.TraceID})
			if err != nil {
				if reportUnclaimable && skippedReason == "" {
					skippedReason = "starting_run_task_start_failed"
				}
				continue
			}
			task = startedTask
		}
		now := svc.now()
		run.Status = RunStatusRunning
		run.WorkTaskStatus = task.Status
		run.AttemptCount++
		run.StartedAt = now
		run.FinishedAt = time.Time{}
		run.FailureCategory = ""
		svc.applyExternalClaim(&run, runnerID, now)
		run.UpdatedAt = now
		claimed, err := svc.store.UpdateRun(ctx, run)
		if err != nil {
			return ClaimedRun{}, false, err
		}
		if claimed.Status != RunStatusRunning || !claimed.StartedAt.Equal(now) {
			continue
		}
		timeout := automationMaxRuntime(svc.options.Agents, claimed.AgentID, svc.options.DefaultMaxRuntime)
		return ClaimedRun{Run: claimed, CodexInput: svc.codexInputForClaim(ctx, claimed, task), TimeoutMS: timeout.Milliseconds()}, true, nil
	}
	if skippedReason != "" {
		return ClaimedRun{}, false, fmt.Errorf("%w: interrupted_starting_run_unclaimable:%s", ErrInvalidInput, skippedReason)
	}
	return ClaimedRun{}, false, nil
}

func (svc *Service) claimPreExecutionRecovery(ctx context.Context, projectID string, agentID string, runnerID string) (ClaimedRun, bool, error) {
	runs, err := svc.recoverablePreExecutionRuns(ctx, projectID)
	if err != nil {
		return ClaimedRun{}, false, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || !isRecoverablePreExecutionFailure(run.FailureCategory) {
			continue
		}
		if !svc.canRetryRun(run) {
			continue
		}
		automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
		if err != nil {
			continue
		}
		if err := svc.validateAutomationPolicy(ctx, automation, run.RunnerKind, run.TaskID, run.AgentID); err != nil {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || !preExecutionRecoveryTaskMatchesRun(task, run) || !svc.dependenciesDone(ctx, task) {
			continue
		}
		if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil {
			_, _ = svc.failRun(ctx, run, RunStatusBlocked, runPlanFailureCategory(err))
			continue
		}
		task, _, blocked, err := svc.expandOrBlockPreExecutionDirtyScope(ctx, run, task, dirtyPathsFromEvidenceRefs(task.EvidenceRefs))
		if err != nil {
			return ClaimedRun{}, false, err
		}
		if blocked {
			continue
		}
		task, err = svc.prepareTaskForPreExecutionRecovery(ctx, run, task)
		if err != nil {
			continue
		}
		now := svc.now()
		run.Status = RunStatusRunning
		run.WorkTaskStatus = task.Status
		run.AttemptCount++
		run.SafeSummary = "pre_execution_recovery"
		run.StartedAt = now
		run.FinishedAt = time.Time{}
		run.FailureCategory = ""
		svc.applyExternalClaim(&run, runnerID, now)
		run.UpdatedAt = now
		claimed, err := svc.store.UpdateRun(ctx, run)
		if err != nil {
			return ClaimedRun{}, false, err
		}
		if !sameRecoveryClaim(claimed, now, "pre_execution_recovery") {
			continue
		}
		timeout := automationMaxRuntime(svc.options.Agents, claimed.AgentID, svc.options.DefaultMaxRuntime)
		return ClaimedRun{Run: claimed, CodexInput: svc.codexInputForClaim(ctx, claimed, task), TimeoutMS: timeout.Milliseconds()}, true, nil
	}
	return ClaimedRun{}, false, nil
}

func (svc *Service) recoverablePreExecutionRuns(ctx context.Context, projectID string) ([]AutomationRun, error) {
	failed, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusFailed})
	if err != nil {
		return nil, err
	}
	blocked, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusBlocked})
	if err != nil {
		return nil, err
	}
	return append(failed, blocked...), nil
}

func (svc *Service) sortQueuedRunsForClaim(ctx context.Context, runs []AutomationRun) {
	if len(runs) < 2 || svc == nil || svc.store == nil {
		sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
		return
	}
	tracePriority := make(map[string]int, len(runs))
	for _, run := range runs {
		priority := 1
		if strings.TrimSpace(run.TraceID) != "" {
			priority = 0
		} else if automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID); err == nil && strings.TrimSpace(automation.TraceID) != "" {
			priority = 0
		}
		tracePriority[run.ID] = priority
	}
	sort.SliceStable(runs, func(i, j int) bool {
		left := tracePriority[runs[i].ID]
		right := tracePriority[runs[j].ID]
		if left != right {
			return left < right
		}
		if left == 0 {
			return runs[i].CreatedAt.After(runs[j].CreatedAt)
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}

func (svc *Service) reconcileRecoverablePreExecutionRuns(ctx context.Context, projectID string) error {
	runs, err := svc.recoverablePreExecutionRuns(ctx, projectID)
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || !isRecoverablePreExecutionFailure(run.FailureCategory) || run.TaskID == "" {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || strings.TrimSpace(task.ClaimedByRunID) != run.ID {
			continue
		}
		if task.Status == projectworkplan.WorkTaskStatusDone {
			if _, err := svc.completeRunAfterTaskDone(ctx, run, task); err != nil {
				return err
			}
			continue
		}
		if isTerminalIncompleteTaskStatus(task.Status) {
			if _, err := svc.finishRunAfterTaskTerminal(ctx, run, task); err != nil {
				return err
			}
			continue
		}
		if task.Status != projectworkplan.WorkTaskStatusNeedsReview && task.Status != projectworkplan.WorkTaskStatusVerifying {
			continue
		}
		now := svc.now()
		run.Status = RunStatusVerifying
		run.WorkTaskStatus = task.Status
		run.SafeSummary = "pre_execution_recovery_progressed_task_verifying"
		run.FailureCategory = ""
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.UpdatedAt = now
		updated, err := svc.store.UpdateRun(ctx, run)
		if err != nil {
			return err
		}
		if _, err := svc.reconcileVerifyingRun(ctx, updated); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) prepareTaskForPreExecutionRecovery(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (projectworkplan.WorkTask, error) {
	if strings.TrimSpace(task.ClaimedByRunID) != run.ID {
		if strings.TrimSpace(task.ClaimedByRunID) != "" {
			return projectworkplan.WorkTask{}, fmt.Errorf("%w: task_claimed_by_other_run", ErrInvalidInput)
		}
		claimed, err := svc.workTasks.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{
			ProjectID:  run.ProjectID,
			TaskID:     task.ID,
			OwnerAgent: run.AgentID,
			RunID:      run.ID,
			TraceID:    firstNonEmpty(run.TraceID, run.ID),
		})
		if err != nil {
			return projectworkplan.WorkTask{}, err
		}
		task = claimed
	}
	if task.Status == projectworkplan.WorkTaskStatusInProgress {
		return task, nil
	}
	return svc.workTasks.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:  run.ProjectID,
		TaskID:     task.ID,
		OwnerAgent: run.AgentID,
		RunID:      run.ID,
		TraceID:    firstNonEmpty(run.TraceID, run.ID),
	})
}

func (svc *Service) claimGitOpsPostTaskRecovery(ctx context.Context, projectID string, agentID string, runnerID string) (ClaimedRun, bool, error) {
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusFailed})
	if err != nil {
		return ClaimedRun{}, false, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || !isRecoverableGitOpsPostTaskFailure(run.FailureCategory) {
			continue
		}
		if !svc.canRetryRun(run) {
			continue
		}
		automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
		if err != nil {
			continue
		}
		if err := svc.validateAutomationPolicy(ctx, automation, run.RunnerKind, run.TaskID, run.AgentID); err != nil {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || !taskHasGitOpsRecoveryCloseout(task) {
			continue
		}
		if !taskOwnsGitOpsRecoveryRun(task, run) {
			continue
		}
		if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil {
			_, _ = svc.failRun(ctx, run, RunStatusBlocked, runPlanFailureCategory(err))
			continue
		}
		now := svc.now()
		run.Status = RunStatusRunning
		run.WorkTaskStatus = task.Status
		run.AttemptCount++
		run.SafeSummary = RunSafeSummaryGitOpsPostTaskRecovery
		run.StartedAt = now
		run.FinishedAt = time.Time{}
		svc.applyExternalClaim(&run, runnerID, now)
		run.UpdatedAt = now
		claimed, err := svc.store.UpdateRun(ctx, run)
		if err != nil {
			return ClaimedRun{}, false, err
		}
		if !sameRecoveryClaim(claimed, now, RunSafeSummaryGitOpsPostTaskRecovery) {
			continue
		}
		timeout := automationMaxRuntime(svc.options.Agents, claimed.AgentID, svc.options.DefaultMaxRuntime)
		return ClaimedRun{Run: claimed, CodexInput: svc.codexInputForClaim(ctx, claimed, task), TimeoutMS: timeout.Milliseconds()}, true, nil
	}
	return ClaimedRun{}, false, nil
}

func (svc *Service) reconcileExhaustedGitOpsRecoveryRuns(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil {
		return nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusFailed})
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || (!isRecoverableGitOpsPostTaskFailure(run.FailureCategory) && !isRecoverableRecoveryFailure(run.FailureCategory)) {
			continue
		}
		if svc.canRetryRun(run) {
			continue
		}
		if strings.TrimSpace(run.SafeSummary) != RunSafeSummaryGitOpsPostTaskRecovery {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || (!taskHasGitOpsRecoveryCloseout(task) && !isRecoverableRecoveryFailure(run.FailureCategory)) {
			continue
		}
		if isTerminalAutomationTaskStatus(task.Status) {
			continue
		}
		if !taskOwnsGitOpsRecoveryRun(task, run) {
			continue
		}
		if countTerminalReplacementFailures(runs, task) >= defaultAutomationMaxReplacementRunsPerTask {
			if _, err := svc.blockTaskAfterReplacementRetryLimit(ctx, task, latestTerminalReplacementFailureCategory(runs, task)); err != nil {
				return err
			}
			continue
		}
		if _, err := svc.requeueTaskAfterGitOpsRecoveryFailure(ctx, run, run.FailureCategory, nil); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) reconcileExhaustedPreExecutionRecoveryRuns(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil {
		return nil
	}
	runs, err := svc.recoverablePreExecutionRuns(ctx, projectID)
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || !isRecoverablePreExecutionFailure(run.FailureCategory) || svc.canRetryRun(run) {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || strings.TrimSpace(task.ClaimedByRunID) != run.ID {
			continue
		}
		switch task.Status {
		case projectworkplan.WorkTaskStatusReady, projectworkplan.WorkTaskStatusClaimed, projectworkplan.WorkTaskStatusInProgress:
		default:
			continue
		}
		if _, err := svc.requeueTaskAfterPreExecutionRecoveryFailure(ctx, run, run.FailureCategory, task.EvidenceRefs); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) claimPostImplementationReviewRecovery(ctx context.Context, projectID string, agentID string, runnerID string) (ClaimedRun, bool, error) {
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusFailed})
	if err != nil {
		return ClaimedRun{}, false, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || strings.TrimSpace(run.SafeSummary) != RunSafeSummaryPostImplementationReviewQueued || !isRecoverableReviewGitOpsFailure(run.FailureCategory) {
			continue
		}
		if !svc.canRetryRun(run) {
			continue
		}
		if agentID != "" && run.AgentID != "" && run.AgentID != agentID {
			continue
		}
		automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
		if err != nil {
			continue
		}
		if err := svc.validateAutomationPolicy(ctx, automation, run.RunnerKind, run.TaskID, run.AgentID); err != nil {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || task.Status == projectworkplan.WorkTaskStatusDone {
			continue
		}
		if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil {
			_, _ = svc.failRun(ctx, run, RunStatusBlocked, runPlanFailureCategory(err))
			continue
		}
		now := svc.now()
		run.Status = RunStatusRunning
		run.WorkTaskStatus = task.Status
		run.AttemptCount++
		run.StartedAt = now
		run.FinishedAt = time.Time{}
		run.FailureCategory = ""
		svc.applyExternalClaim(&run, runnerID, now)
		run.UpdatedAt = now
		claimed, err := svc.store.UpdateRun(ctx, run)
		if err != nil {
			return ClaimedRun{}, false, err
		}
		if !sameRecoveryClaim(claimed, now, RunSafeSummaryPostImplementationReviewQueued) {
			continue
		}
		timeout := automationMaxRuntime(svc.options.Agents, claimed.AgentID, svc.options.DefaultMaxRuntime)
		return ClaimedRun{Run: claimed, CodexInput: svc.codexInputForClaim(ctx, claimed, task), TimeoutMS: timeout.Milliseconds()}, true, nil
	}
	return ClaimedRun{}, false, nil
}

func sameRecoveryClaim(run AutomationRun, startedAt time.Time, safeSummary string) bool {
	return run.Status == RunStatusRunning && run.SafeSummary == safeSummary && run.StartedAt.Equal(startedAt)
}

func (svc *Service) applyExternalClaim(run *AutomationRun, runnerID string, now time.Time) {
	if run == nil {
		return
	}
	run.ClaimID = svc.newID("claim")
	run.RunnerID = runnerID
	run.ClaimedAt = now
	run.LastHeartbeatAt = now
	run.LeaseExpiresAt = now.Add(defaultExternalRunLeaseTTL)
}

func (svc *Service) canRetryRun(run AutomationRun) bool {
	limit := automationMaxRetries(svc.options.Agents, run.AgentID)
	if limit <= 0 {
		return true
	}
	return run.AttemptCount < limit
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
	if svc.hasPendingAutomationReviewTask(ctx, automation) {
		return true
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

func (svc *Service) hasPendingAutomationReviewTask(ctx context.Context, automation Automation) bool {
	for _, taskID := range automation.RequiredReviewTaskIDs {
		task, err := svc.workTasks.GetWorkTask(ctx, automation.ProjectID, taskID)
		if err != nil || task.Status == projectworkplan.WorkTaskStatusDone {
			continue
		}
		if task.Status == projectworkplan.WorkTaskStatusPlanned && task.DecompositionQuality == projectworkplan.DecompositionReady {
			return true
		}
		if validateExecutableTask(task) == nil && svc.dependenciesDone(ctx, task) {
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
	svc.claimMu.Lock()
	defer svc.claimMu.Unlock()

	projectID, runID, err := safeProjectObject(input.ProjectID, input.RunID, "run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	run, err := svc.store.GetRun(ctx, projectID, runID)
	if err != nil {
		return AutomationRun{}, err
	}
	status, err := safeAttemptStatus(input.Status)
	if err != nil {
		return AutomationRun{}, err
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		return AutomationRun{}, fmt.Errorf("%w: automation run is not externally claimed", ErrInvalidInput)
	}
	claimID, err := safeOptionalRef(input.ClaimID, "claim_id")
	if err != nil {
		return AutomationRun{}, err
	}
	runnerID, err := safeOptionalRef(input.RunnerID, "runner_id")
	if err != nil {
		return AutomationRun{}, err
	}
	if run.Status != RunStatusRunning {
		if terminalAttemptAlreadyRecorded(run, status) && (run.ClaimID == "" || claimID == "" || run.ClaimID == claimID) {
			return run, nil
		}
		if status == RunStatusFailed && svc.failedAttemptMatchesAdvancedTask(ctx, run) {
			return run, nil
		}
		if status == RunStatusCompleted && terminalAuditRemediationFailureAlreadyRecorded(run) && claimMatchesTerminalRun(run, claimID, runnerID) {
			return run, nil
		}
		if svc.externallyClaimedTaskOwnsRun(ctx, run) && (run.Status == RunStatusClaiming || run.Status == RunStatusStarting) {
			// The external runner may fail while resolving its worktree or GitOps
			// setup. At that point the Work Task is already owned by this run,
			// so accepting the report prevents a stuck starting run.
		} else if !(status == RunStatusCompleted && (run.Status == RunStatusVerifying || svc.completedAttemptMatchesRecoveredTask(ctx, run))) {
			return AutomationRun{}, fmt.Errorf("%w: automation run is not externally claimed", ErrInvalidInput)
		}
	}
	if run.Status == RunStatusRunning && run.ClaimID != "" && run.RunnerID != "" {
		if claimID == "" || claimID != run.ClaimID {
			return AutomationRun{}, fmt.Errorf("%w: claim_id does not match automation run", ErrInvalidInput)
		}
		if run.RunnerID != "" && runnerID != "" && runnerID != run.RunnerID {
			return AutomationRun{}, fmt.Errorf("%w: runner_id does not match automation run", ErrInvalidInput)
		}
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
	if runnerID != "" {
		run.RunnerID = runnerID
	}
	run.FinishedAt = now
	run.UpdatedAt = now
	switch status {
	case RunStatusCompleted:
		run.Status = RunStatusVerifying
		run.SafeSummary = "external_codex_cli_completed_verification_required"
		if err := svc.queuePostImplementationReview(ctx, run); err != nil {
			return svc.failRun(ctx, run, RunStatusFailed, "post_implementation_review_queue_failed")
		}
	case RunStatusTimeout:
		if isRecoverableCodexExecutionFailure(failureCategory) {
			return svc.requeueTaskAfterCodexExecutionFailure(ctx, run, failureCategory)
		}
		run.Status = RunStatusTimeout
	case RunStatusFailed:
		if strings.TrimSpace(run.SafeSummary) == RunSafeSummaryGitOpsPostTaskRecovery && isGitOpsRecoveryFailure(failureCategory) {
			return svc.requeueTaskAfterGitOpsRecoveryFailure(ctx, run, failureCategory, evidenceRefs)
		}
		if isRecoverableGitOpsPostTaskFailure(failureCategory) {
			run.Status = RunStatusFailed
			run.SafeSummary = RunSafeSummaryGitOpsPostTaskRecovery
			break
		}
		if isRecoverableCodexExecutionFailure(failureCategory) {
			return svc.requeueTaskAfterCodexExecutionFailure(ctx, run, failureCategory)
		}
		if isNonRetryableCodexExecutionFailure(failureCategory) {
			return svc.blockTaskAfterNonRetryableCodexExecutionFailure(ctx, run, failureCategory)
		}
		if isRecoverableGovernedCloseoutFailure(failureCategory) {
			return svc.requeueTaskAfterGovernedCloseoutFailure(ctx, run, failureCategory)
		}
		return svc.failTaskAfterTerminalAttempt(ctx, run, failureCategory)
	case RunStatusBlocked:
		return svc.blockTaskAfterTerminalAttempt(ctx, run, failureCategory)
	default:
		run.Status = status
	}
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	durable, err := svc.store.GetRun(ctx, updated.ProjectID, updated.ID)
	if err != nil {
		return AutomationRun{}, err
	}
	if durable.Status != updated.Status || durable.FailureCategory != updated.FailureCategory || durable.ClaimID != updated.ClaimID || (isTerminalRunStatus(updated.Status) && durable.FinishedAt.IsZero()) {
		return AutomationRun{}, fmt.Errorf("%w: automation run durable completion mismatch", ErrInvalidInput)
	}
	if status == RunStatusCompleted {
		return svc.reconcileVerifyingRun(ctx, durable)
	}
	return durable, nil
}

func (svc *Service) blockTaskAfterTerminalAttempt(ctx context.Context, run AutomationRun, category string) (AutomationRun, error) {
	run.Status = RunStatusBlocked
	run.FailureCategory = category
	run.SafeSummary = "external_codex_cli_blocked"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	if strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return updated, nil
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return updated, nil
	}
	if claimedBy := strings.TrimSpace(task.ClaimedByRunID); claimedBy != "" && claimedBy != run.ID {
		return updated, nil
	}
	reason := "External Codex runner blocked task."
	if strings.TrimSpace(category) != "" {
		reason = "External Codex runner blocked task with " + safeFailure(category) + "."
	}
	blocked, err := svc.workTasks.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          task.ProjectID,
		TaskID:             task.ID,
		SafeNextAction:     "external_runner_blocked",
		RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
		TraceID:            firstNonEmpty(run.TraceID, run.ID),
		BlockedReason:      reason,
		ResumeInstructions: "Inspect the automation run evidence and resume only after the blocker is resolved.",
	})
	if err != nil {
		return AutomationRun{}, err
	}
	updated.WorkTaskStatus = blocked.Status
	if err := svc.updatePlanAfterTerminalTask(ctx, blocked); err != nil {
		return updated, err
	}
	return svc.store.UpdateRun(ctx, updated)
}

func (svc *Service) failTaskAfterTerminalAttempt(ctx context.Context, run AutomationRun, category string) (AutomationRun, error) {
	run.Status = RunStatusFailed
	run.FailureCategory = category
	run.SafeSummary = "external_codex_cli_failed"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	if strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return updated, nil
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return updated, nil
	}
	if claimedBy := strings.TrimSpace(task.ClaimedByRunID); claimedBy != "" && claimedBy != run.ID {
		return updated, nil
	}
	outcome := "External Codex runner failed task."
	if strings.TrimSpace(category) != "" {
		outcome = "External Codex runner failed task with " + safeFailure(category) + "."
	}
	failed, err := svc.workTasks.FailWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:      task.ProjectID,
		TaskID:         task.ID,
		SafeNextAction: "external_runner_failed",
		RunID:          firstNonEmpty(task.ClaimedByRunID, run.ID),
		TraceID:        firstNonEmpty(run.TraceID, run.ID),
		Outcome:        outcome,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	updated.WorkTaskStatus = failed.Status
	if err := svc.updatePlanAfterTerminalTask(ctx, failed); err != nil {
		return updated, err
	}
	return svc.store.UpdateRun(ctx, updated)
}

func terminalAuditRemediationFailureAlreadyRecorded(run AutomationRun) bool {
	return run.Status == RunStatusFailed && strings.TrimSpace(run.FailureCategory) == "confirmed_finding_remediation_missing"
}

func claimMatchesTerminalRun(run AutomationRun, claimID string, runnerID string) bool {
	if run.ClaimID != "" && claimID != "" && run.ClaimID != claimID {
		return false
	}
	if run.RunnerID != "" && runnerID != "" && run.RunnerID != runnerID {
		return false
	}
	if run.ClaimID != "" {
		return claimID == run.ClaimID
	}
	return true
}

func (svc *Service) HeartbeatRun(ctx context.Context, input HeartbeatRunInput) (AutomationRun, error) {
	svc.claimMu.Lock()
	defer svc.claimMu.Unlock()
	projectID, runID, err := safeProjectObject(input.ProjectID, input.RunID, "run_id")
	if err != nil {
		return AutomationRun{}, err
	}
	claimID, err := safeRef(input.ClaimID, "claim_id")
	if err != nil {
		return AutomationRun{}, err
	}
	runnerID, err := safeOptionalRef(input.RunnerID, "runner_id")
	if err != nil {
		return AutomationRun{}, err
	}
	run, err := svc.store.GetRun(ctx, projectID, runID)
	if err != nil {
		return AutomationRun{}, err
	}
	if run.Status != RunStatusRunning && run.Status != RunStatusClaiming && run.Status != RunStatusStarting {
		return AutomationRun{}, fmt.Errorf("%w: automation run is not active", ErrInvalidInput)
	}
	if run.ClaimID == "" || run.ClaimID != claimID {
		return AutomationRun{}, fmt.Errorf("%w: claim_id does not match automation run", ErrInvalidInput)
	}
	if run.RunnerID != "" && runnerID != "" && run.RunnerID != runnerID {
		return AutomationRun{}, fmt.Errorf("%w: runner_id does not match automation run", ErrInvalidInput)
	}
	now := svc.now()
	if runnerID != "" {
		run.RunnerID = runnerID
	}
	run.LastHeartbeatAt = now
	run.LeaseExpiresAt = now.Add(defaultExternalRunLeaseTTL)
	run.UpdatedAt = now
	return svc.store.UpdateRun(ctx, run)
}

func (svc *Service) completedAttemptMatchesRecoveredTask(ctx context.Context, run AutomationRun) bool {
	if run.RunnerKind != RunnerKindCodexCLI || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return false
	}
	if run.Status == RunStatusFailed && isRecoverableGitOpsPostTaskFailure(run.FailureCategory) {
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		return err == nil && taskOwnsGitOpsRecoveryRun(task, run) && taskHasGitOpsRecoveryCloseout(task)
	}
	if run.Status != RunStatusBlocked || !isRecoverablePreExecutionFailure(run.FailureCategory) {
		return false
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return false
	}
	if strings.TrimSpace(task.ClaimedByRunID) != run.ID {
		return false
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusNeedsReview, projectworkplan.WorkTaskStatusVerifying, projectworkplan.WorkTaskStatusDone:
		return true
	default:
		return false
	}
}

func (svc *Service) failedAttemptMatchesAdvancedTask(ctx context.Context, run AutomationRun) bool {
	if svc == nil || svc.workTasks == nil || run.RunnerKind != RunnerKindCodexCLI || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return false
	}
	if run.Status != RunStatusVerifying && run.Status != RunStatusCompleted {
		return false
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil || strings.TrimSpace(task.ClaimedByRunID) != run.ID {
		return false
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusNeedsReview, projectworkplan.WorkTaskStatusVerifying, projectworkplan.WorkTaskStatusDone:
		return true
	default:
		return false
	}
}

func (svc *Service) externallyClaimedTaskOwnsRun(ctx context.Context, run AutomationRun) bool {
	if svc == nil || svc.workTasks == nil || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" || strings.TrimSpace(run.ID) == "" {
		return false
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return false
	}
	return strings.TrimSpace(task.ClaimedByRunID) == run.ID
}

func terminalAttemptAlreadyRecorded(run AutomationRun, status string) bool {
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusTimeout, RunStatusBlocked, RunStatusCancelled:
		return run.Status == status
	default:
		return false
	}
}

func isTerminalRunStatus(status string) bool {
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusTimeout, RunStatusBlocked, RunStatusCancelled, RunStatusPolicyDenied, RunStatusRunnerUnavailable:
		return true
	default:
		return false
	}
}

func isGitOpsRecoveryFailure(category string) bool {
	category = strings.TrimSpace(category)
	return strings.HasPrefix(category, "gitops_") || category == "automation_task_closeout_missing"
}

func taskOwnsGitOpsRecoveryRun(task projectworkplan.WorkTask, run AutomationRun) bool {
	if strings.TrimSpace(task.ClaimedByRunID) == run.ID {
		return true
	}
	if strings.TrimSpace(run.SafeSummary) != RunSafeSummaryGitOpsPostTaskRecovery {
		return false
	}
	return containsRef(task.AgentRunIDs, run.ID)
}

func recoveryResumeInstructions(sentence string) string {
	sentence = strings.TrimSpace(sentence)
	if len(sentence) <= projectworkplan.MaxResumeInstructionsLength {
		return sentence
	}
	return strings.TrimSpace(sentence[:projectworkplan.MaxResumeInstructionsLength])
}

func (svc *Service) requeueTaskAfterGitOpsRecoveryFailure(ctx context.Context, run AutomationRun, category string, evidenceRefs []string) (AutomationRun, error) {
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok || updater == nil || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		run.Status = RunStatusFailed
		run.FailureCategory = category
		run.SafeSummary = gitOpsRecoveryRequeueSummary(category)
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		run.Status = RunStatusFailed
		run.FailureCategory = category
		run.SafeSummary = RunSafeSummaryGitOpsRecoveryRequeuedImplementation
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	dirtyPaths := dirtyPathsFromEvidenceRefs(evidenceRefs)
	if strings.TrimSpace(category) == "gitops_dirty_worktree_scope" && len(dirtyPaths) > 0 {
		expandScopes, outsidePaths := svc.classifyDirtyScopePaths(run.ProjectID, task, dirtyPaths)
		if len(outsidePaths) > 0 {
			return svc.blockTaskAfterOutOfScopeDirtyPaths(ctx, updater, run, task, outsidePaths)
		}
		if len(expandScopes) > 0 {
			if expander, ok := svc.workTasks.(workTaskScopeExpander); ok && expander != nil {
				expanded, err := expander.ExpandWorkTaskScope(ctx, projectworkplan.ExpandWorkTaskScopeInput{
					ProjectID:          task.ProjectID,
					TaskID:             task.ID,
					FilesToEdit:        expandScopes,
					RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
					TraceID:            firstNonEmpty(run.TraceID, run.ID),
					ResumeInstructions: recoveryResumeInstructions("GitOps dirty paths were inside likely_files_affected and files_to_edit was expanded for retry: " + strings.Join(dirtyPaths, ", ")),
				})
				if err == nil {
					task = expanded
				}
			}
		}
	}
	readyTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			SafeNextAction:     "gitops_recovery_failed_requeue_implementation",
			RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
			TraceID:            firstNonEmpty(run.TraceID, run.ID),
			ResumeInstructions: gitOpsRecoveryResumeInstructions(category, dirtyPaths),
		},
		Status: projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	run.Status = RunStatusFailed
	run.WorkTaskStatus = readyTask.Status
	run.SafeSummary = gitOpsRecoveryRequeueSummary(category)
	run.FailureCategory = "gitops_recovery_failed_requires_implementation"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil || automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || validateAllowedTaskRef(automation, readyTask) != nil {
		return updated, nil
	}
	if err := svc.queueReadyDependentAutomation(ctx, automation, readyTask); err != nil {
		return updated, nil
	}
	return updated, nil
}

func gitOpsRecoveryRequeueSummary(category string) string {
	category = safeFailure(category)
	if category == "" {
		return RunSafeSummaryGitOpsRecoveryRequeuedImplementation
	}
	return RunSafeSummaryGitOpsRecoveryRequeuedImplementation + "_after_" + category
}

func (svc *Service) blockTaskAfterOutOfScopeDirtyPaths(ctx context.Context, updater workTaskStatusUpdater, run AutomationRun, task projectworkplan.WorkTask, dirtyPaths []string) (AutomationRun, error) {
	reason := dirtyPathsBlockedReason(dirtyPaths)
	blockedTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			SafeNextAction:     "gitops_dirty_scope_requires_new_plan",
			RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
			TraceID:            firstNonEmpty(run.TraceID, run.ID),
			BlockedReason:      reason,
			ResumeInstructions: recoveryResumeInstructions(reason + "; create a new Work Plan or update likely_files_affected before rerunning."),
		},
		Status: projectworkplan.WorkTaskStatusBlocked,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	run.Status = RunStatusFailed
	run.WorkTaskStatus = blockedTask.Status
	run.FailureCategory = "gitops_dirty_worktree_scope_requires_plan"
	run.SafeSummary = "gitops_recovery_blocked_after_out_of_scope_dirty_paths"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	return svc.store.UpdateRun(ctx, run)
}

func dirtyPathsBlockedReason(dirtyPaths []string) string {
	const maxBlockedReasonLength = 500
	prefix := "GitOps dirty paths outside likely_files_affected require a new plan: "
	if len(dirtyPaths) == 0 {
		return strings.TrimSpace(prefix)
	}
	paths := uniqueRefs(dirtyPaths)
	reason := prefix
	added := 0
	for i, path := range paths {
		remaining := len(paths) - i
		suffix := ""
		if remaining > 1 {
			suffix = fmt.Sprintf(", and %d more", remaining-1)
		}
		separator := ""
		if added > 0 {
			separator = ", "
		}
		candidate := reason + separator + path + suffix
		if len(candidate) > maxBlockedReasonLength {
			break
		}
		reason += separator + path
		added++
	}
	omitted := len(paths) - added
	if omitted > 0 {
		suffix := fmt.Sprintf(", and %d more", omitted)
		if len(reason)+len(suffix) <= maxBlockedReasonLength {
			reason += suffix
		}
	}
	if len(reason) > maxBlockedReasonLength {
		return strings.TrimSpace(reason[:maxBlockedReasonLength])
	}
	return reason
}

func gitOpsRecoveryResumeInstructions(category string, dirtyPaths []string) string {
	if len(dirtyPaths) > 0 {
		return recoveryResumeInstructions("GitOps recovery failed with " + safeFailure(category) + "; dirty paths: " + strings.Join(dirtyPaths, ", ") + ". Rerun implementation after files_to_edit scope is corrected.")
	}
	return recoveryResumeInstructions("GitOps recovery failed with " + safeFailure(category) + "; rerun implementation to fix verification, generated artifacts, commit scope, or PR readiness before GitOps post-task is retried.")
}

func preExecutionRecoveryRequeueSummary(category string) string {
	category = safeFailure(category)
	if category == "" {
		return "pre_execution_recovery_requeued_implementation"
	}
	return "pre_execution_recovery_requeued_implementation_after_" + category
}

func preExecutionRecoveryResumeInstructions(category string, dirtyPaths []string) string {
	if len(dirtyPaths) > 0 {
		return recoveryResumeInstructions("Pre-execution recovery failed with " + safeFailure(category) + "; dirty paths: " + strings.Join(dirtyPaths, ", ") + ". Rerun implementation after files_to_edit scope is corrected.")
	}
	return recoveryResumeInstructions("Pre-execution recovery failed with " + safeFailure(category) + "; rerun implementation after resolving worktree, GitOps pre-task, or dirty-worktree scope setup before execution is retried.")
}

func (svc *Service) expandOrBlockPreExecutionDirtyScope(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask, dirtyPaths []string) (projectworkplan.WorkTask, AutomationRun, bool, error) {
	if strings.TrimSpace(run.FailureCategory) != "gitops_dirty_worktree_scope" || len(dirtyPaths) == 0 {
		return task, run, false, nil
	}
	expandScopes, outsidePaths := svc.classifyDirtyScopePaths(run.ProjectID, task, dirtyPaths)
	if len(outsidePaths) > 0 {
		updater, ok := svc.workTasks.(workTaskStatusUpdater)
		if !ok || updater == nil {
			return task, run, false, nil
		}
		blockedRun, err := svc.blockTaskAfterOutOfScopeDirtyPaths(ctx, updater, run, task, outsidePaths)
		if err != nil {
			return task, run, false, err
		}
		return task, blockedRun, true, nil
	}
	if len(expandScopes) == 0 {
		return task, run, false, nil
	}
	expander, ok := svc.workTasks.(workTaskScopeExpander)
	if !ok || expander == nil {
		return task, run, false, nil
	}
	expanded, err := expander.ExpandWorkTaskScope(ctx, projectworkplan.ExpandWorkTaskScopeInput{
		ProjectID:          task.ProjectID,
		TaskID:             task.ID,
		FilesToEdit:        expandScopes,
		RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
		TraceID:            firstNonEmpty(run.TraceID, run.ID),
		ResumeInstructions: recoveryResumeInstructions("Pre-execution dirty paths were inside likely_files_affected and files_to_edit was expanded for retry: " + strings.Join(dirtyPaths, ", ")),
	})
	if err != nil {
		return task, run, false, err
	}
	return expanded, run, false, nil
}

func (svc *Service) requeueTaskAfterPreExecutionRecoveryFailure(ctx context.Context, run AutomationRun, category string, evidenceRefs []string) (AutomationRun, error) {
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok || updater == nil || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		run.Status = RunStatusFailed
		run.FailureCategory = "pre_execution_recovery_failed_requires_implementation"
		run.SafeSummary = preExecutionRecoveryRequeueSummary(category)
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		run.Status = RunStatusFailed
		run.FailureCategory = "pre_execution_recovery_failed_requires_implementation"
		run.SafeSummary = preExecutionRecoveryRequeueSummary(category)
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	dirtyPaths := dirtyPathsFromEvidenceRefs(evidenceRefs)
	task, blockedRun, blocked, err := svc.expandOrBlockPreExecutionDirtyScope(ctx, run, task, dirtyPaths)
	if err != nil {
		return AutomationRun{}, err
	}
	if blocked {
		return blockedRun, nil
	}
	readyTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			SafeNextAction:     "pre_execution_recovery_failed_requeue_implementation",
			RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
			TraceID:            firstNonEmpty(run.TraceID, run.ID),
			ResumeInstructions: preExecutionRecoveryResumeInstructions(category, dirtyPaths),
		},
		Status: projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	run.Status = RunStatusFailed
	run.WorkTaskStatus = readyTask.Status
	run.SafeSummary = preExecutionRecoveryRequeueSummary(category)
	run.FailureCategory = "pre_execution_recovery_failed_requires_implementation"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil || automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || validateAllowedTaskRef(automation, readyTask) != nil {
		return updated, nil
	}
	if err := svc.queueReadyDependentAutomation(ctx, automation, readyTask); err != nil {
		return updated, nil
	}
	return updated, nil
}

func (svc *Service) requeueTaskAfterGovernedCloseoutFailure(ctx context.Context, run AutomationRun, category string) (AutomationRun, error) {
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok || updater == nil || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		run.Status = RunStatusFailed
		run.FailureCategory = category
		run.SafeSummary = "governed_closeout_failed_requeue_unavailable"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		run.Status = RunStatusFailed
		run.FailureCategory = category
		run.SafeSummary = "governed_closeout_failed_requeue_unavailable"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusReady, projectworkplan.WorkTaskStatusClaimed, projectworkplan.WorkTaskStatusInProgress:
	default:
		run.Status = RunStatusFailed
		run.WorkTaskStatus = task.Status
		run.FailureCategory = category
		run.SafeSummary = "governed_closeout_failed_task_advanced"
		now := svc.now()
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.UpdatedAt = now
		return svc.store.UpdateRun(ctx, run)
	}
	if claimedBy := strings.TrimSpace(task.ClaimedByRunID); claimedBy != "" && claimedBy != run.ID {
		run.Status = RunStatusFailed
		run.WorkTaskStatus = task.Status
		run.FailureCategory = category
		run.SafeSummary = "governed_closeout_failed_task_claimed_by_other_run"
		now := svc.now()
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.UpdatedAt = now
		return svc.store.UpdateRun(ctx, run)
	}
	readyTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			SafeNextAction:     "governed_closeout_failed_requeue_implementation",
			RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
			TraceID:            firstNonEmpty(run.TraceID, run.ID),
			ResumeInstructions: governedCloseoutRecoveryResumeInstructions(category),
		},
		Status: projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	run.Status = RunStatusFailed
	run.WorkTaskStatus = readyTask.Status
	run.FailureCategory = category
	run.SafeSummary = "governed_closeout_failed_requeue_implementation"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil || automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || validateAllowedTaskRef(automation, readyTask) != nil {
		return updated, nil
	}
	if err := svc.queueReadyDependentAutomation(ctx, automation, readyTask); err != nil {
		return updated, nil
	}
	return updated, nil
}

func (svc *Service) requeueTaskAfterCodexExecutionFailure(ctx context.Context, run AutomationRun, category string) (AutomationRun, error) {
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok || updater == nil || strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		run.Status = RunStatusFailed
		run.FailureCategory = category
		run.SafeSummary = "codex_execution_failed_requeue_unavailable"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		run.Status = RunStatusFailed
		run.FailureCategory = category
		run.SafeSummary = "codex_execution_failed_requeue_unavailable"
		run.UpdatedAt = svc.now()
		return svc.store.UpdateRun(ctx, run)
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusReady, projectworkplan.WorkTaskStatusClaimed, projectworkplan.WorkTaskStatusInProgress:
	default:
		run.Status = RunStatusFailed
		run.WorkTaskStatus = task.Status
		run.FailureCategory = category
		run.SafeSummary = "codex_execution_failed_task_advanced"
		now := svc.now()
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.UpdatedAt = now
		return svc.store.UpdateRun(ctx, run)
	}
	if claimedBy := strings.TrimSpace(task.ClaimedByRunID); claimedBy != "" && claimedBy != run.ID {
		run.Status = RunStatusFailed
		run.WorkTaskStatus = task.Status
		run.FailureCategory = category
		run.SafeSummary = "codex_execution_failed_task_claimed_by_other_run"
		now := svc.now()
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.UpdatedAt = now
		return svc.store.UpdateRun(ctx, run)
	}
	readyTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			SafeNextAction:     "codex_execution_failed_requeue_implementation",
			RunID:              firstNonEmpty(task.ClaimedByRunID, run.ID),
			TraceID:            firstNonEmpty(run.TraceID, run.ID),
			ResumeInstructions: codexExecutionFailureResumeInstructions(category),
		},
		Status: projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		return AutomationRun{}, err
	}
	run.Status = RunStatusFailed
	run.WorkTaskStatus = readyTask.Status
	run.FailureCategory = category
	run.SafeSummary = "codex_execution_failed_requeue_implementation"
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil || automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || validateAllowedTaskRef(automation, readyTask) != nil {
		return updated, nil
	}
	if err := svc.queueReadyDependentAutomation(ctx, automation, readyTask); err != nil {
		return updated, nil
	}
	return updated, nil
}

func (svc *Service) blockTaskAfterNonRetryableCodexExecutionFailure(ctx context.Context, run AutomationRun, category string) (AutomationRun, error) {
	run.Status = RunStatusFailed
	run.FailureCategory = category
	run.SafeSummary = category
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	if strings.TrimSpace(run.ProjectID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return updated, nil
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return updated, nil
	}
	if claimedBy := strings.TrimSpace(task.ClaimedByRunID); claimedBy != "" && claimedBy != run.ID {
		return updated, nil
	}
	blocked, err := svc.workTasks.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          task.ProjectID,
		TaskID:             task.ID,
		SafeNextAction:     category,
		TraceID:            category,
		BlockedReason:      nonRetryableCodexBlockedReason(category),
		ResumeInstructions: nonRetryableCodexResumeInstructions(category),
	})
	if err != nil {
		return AutomationRun{}, err
	}
	if err := svc.updatePlanAfterTerminalTask(ctx, blocked); err != nil {
		return updated, err
	}
	return updated, nil
}

func (svc *Service) reconcileVerifyingRuns(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusVerifying})
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if _, err := svc.reconcileVerifyingRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) reconcileRunningRuns(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	runs := make([]AutomationRun, 0)
	for _, status := range []string{RunStatusClaiming, RunStatusStarting, RunStatusRunning} {
		statusRuns, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: status})
		if err != nil {
			return err
		}
		runs = append(runs, statusRuns...)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if _, err := svc.reconcileRunningRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) reconcileRunningRun(ctx context.Context, run AutomationRun) (AutomationRun, error) {
	if svc == nil || svc.store == nil || svc.workTasks == nil || run.RunnerKind != RunnerKindCodexCLI || run.ProjectID == "" || run.TaskID == "" {
		return run, nil
	}
	if run.Status != RunStatusClaiming && run.Status != RunStatusStarting && run.Status != RunStatusRunning {
		return run, nil
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return run, nil
	}
	if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil && runPlanFailureCategory(err) == "work_plan_terminal" {
		return svc.blockRunFromTerminalPlan(ctx, run, task)
	}
	if run.Status == RunStatusClaiming || run.Status == RunStatusStarting {
		if task.Status == projectworkplan.WorkTaskStatusDone {
			return svc.completeRunAfterTaskDone(ctx, run, task)
		}
		if isTerminalIncompleteTaskStatus(task.Status) {
			return svc.finishRunAfterTaskTerminal(ctx, run, task)
		}
		if task.Status == projectworkplan.WorkTaskStatusNeedsReview || task.Status == projectworkplan.WorkTaskStatusVerifying {
			now := svc.now()
			run.Status = RunStatusVerifying
			run.WorkTaskStatus = task.Status
			run.SafeSummary = "external_codex_cli_completed_verification_required"
			run.FailureCategory = ""
			if run.FinishedAt.IsZero() {
				run.FinishedAt = now
			}
			run.UpdatedAt = now
			updated, err := svc.store.UpdateRun(ctx, run)
			if err != nil {
				return AutomationRun{}, err
			}
			return svc.reconcileVerifyingRun(ctx, updated)
		}
		if (svc.externalRunLeaseExpired(run) || svc.runStartedBeforeService(run)) && (task.Status == projectworkplan.WorkTaskStatusReady || task.Status == projectworkplan.WorkTaskStatusClaimed || task.Status == projectworkplan.WorkTaskStatusInProgress) {
			return svc.requeueAbandonedRunningRun(ctx, run, task)
		}
		return run, nil
	}
	if task.Status == projectworkplan.WorkTaskStatusDone {
		return svc.completeRunAfterTaskDone(ctx, run, task)
	}
	if isTerminalIncompleteTaskStatus(task.Status) {
		return svc.finishRunAfterTaskTerminal(ctx, run, task)
	}
	if (svc.externalRunLeaseExpired(run) || svc.runStartedBeforeService(run)) && (task.Status == projectworkplan.WorkTaskStatusReady || task.Status == projectworkplan.WorkTaskStatusClaimed || task.Status == projectworkplan.WorkTaskStatusInProgress) {
		return svc.requeueAbandonedRunningRun(ctx, run, task)
	}
	if task.Status != projectworkplan.WorkTaskStatusNeedsReview && task.Status != projectworkplan.WorkTaskStatusVerifying {
		if run.WorkTaskStatus != task.Status {
			run.WorkTaskStatus = task.Status
			run.UpdatedAt = svc.now()
			return svc.store.UpdateRun(ctx, run)
		}
		return run, nil
	}
	if auditTaskHasConfirmedFindingWithoutRemediation(task) {
		if svc.externalClaimStillActive(run) {
			if run.WorkTaskStatus != task.Status {
				run.WorkTaskStatus = task.Status
				run.UpdatedAt = svc.now()
				return svc.store.UpdateRun(ctx, run)
			}
			return run, nil
		}
		return svc.failRunningAuditWithoutRemediation(ctx, run, task)
	}
	now := svc.now()
	run.Status = RunStatusVerifying
	run.WorkTaskStatus = task.Status
	run.SafeSummary = "external_codex_cli_completed_verification_required"
	run.FailureCategory = ""
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	return svc.reconcileVerifyingRun(ctx, updated)
}

func (svc *Service) failRunningAuditWithoutRemediation(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	action := projectworkplan.WorkTaskActionInput{
		ProjectID:          run.ProjectID,
		TaskID:             task.ID,
		RunID:              firstNonEmpty(run.ID, task.ClaimedByRunID),
		TraceID:            firstNonEmpty(run.TraceID, run.ID),
		Outcome:            "confirmed finding did not create remediation handoff",
		SafeNextAction:     "create_remediation_from_finding_required",
		VerifierResultRefs: append([]string(nil), task.VerifierResultRefs...),
		ReviewResultRefs:   append([]string(nil), task.ReviewResultRefs...),
		ClaimRefs:          append([]string(nil), task.ClaimRefs...),
		EvidenceRefs:       append([]string(nil), task.EvidenceRefs...),
	}
	if _, err := svc.workTasks.FailWorkTask(ctx, action); err != nil {
		return run, err
	}
	run.WorkTaskStatus = projectworkplan.WorkTaskStatusFailed
	return svc.failRun(ctx, run, RunStatusFailed, "confirmed_finding_remediation_missing")
}

func (svc *Service) runStartedBeforeService(run AutomationRun) bool {
	if svc == nil || svc.startedAt.IsZero() {
		return false
	}
	marker := run.UpdatedAt
	if marker.IsZero() {
		marker = run.StartedAt
	}
	return !marker.IsZero() && marker.Before(svc.startedAt)
}

func (svc *Service) externalRunLeaseExpired(run AutomationRun) bool {
	if svc == nil || run.LeaseExpiresAt.IsZero() {
		return false
	}
	return !svc.now().Before(run.LeaseExpiresAt)
}

func (svc *Service) externalClaimStillActive(run AutomationRun) bool {
	if svc == nil || run.ClaimID == "" || run.LeaseExpiresAt.IsZero() {
		return false
	}
	return svc.now().Before(run.LeaseExpiresAt)
}

func (svc *Service) requeueAbandonedRunningRun(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok {
		return run, nil
	}
	current, err := svc.store.GetRun(ctx, run.ProjectID, run.ID)
	if err != nil {
		return AutomationRun{}, err
	}
	if current.Status != run.Status || current.ClaimID != run.ClaimID || current.RunnerID != run.RunnerID || !current.LeaseExpiresAt.Equal(run.LeaseExpiresAt) {
		return current, nil
	}
	if current.Status != RunStatusClaiming && current.Status != RunStatusStarting && current.Status != RunStatusRunning {
		return current, nil
	}
	run = current
	now := svc.now()
	run.Status = RunStatusTimeout
	run.WorkTaskStatus = task.Status
	run.SafeSummary = "external_codex_cli_abandoned_after_restart"
	run.FailureCategory = "external_runner_interrupted"
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	readyTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:      task.ProjectID,
			TaskID:         task.ID,
			SafeNextAction: "external_runner_restart_requeue",
			RunID:          run.ID,
			TraceID:        firstNonEmpty(run.TraceID, run.ID),
		},
		Status: projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		return updated, nil
	}
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil {
		return updated, nil
	}
	if automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || validateAllowedTaskRef(automation, readyTask) != nil {
		return updated, nil
	}
	if err := svc.queueReadyDependentAutomation(ctx, automation, readyTask); err != nil {
		return updated, nil
	}
	return updated, nil
}

func (svc *Service) reconcileInterruptedRunsWithProgressedTasks(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusTimeout})
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || strings.TrimSpace(run.FailureCategory) != "external_runner_interrupted" || run.TaskID == "" {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || strings.TrimSpace(task.ClaimedByRunID) != run.ID {
			continue
		}
		if task.Status == projectworkplan.WorkTaskStatusDone {
			if _, err := svc.completeRunAfterTaskDone(ctx, run, task); err != nil {
				return err
			}
			continue
		}
		if isTerminalIncompleteTaskStatus(task.Status) {
			if _, err := svc.finishRunAfterTaskTerminal(ctx, run, task); err != nil {
				return err
			}
			continue
		}
		if task.Status != projectworkplan.WorkTaskStatusNeedsReview && task.Status != projectworkplan.WorkTaskStatusVerifying {
			continue
		}
		now := svc.now()
		run.Status = RunStatusVerifying
		run.WorkTaskStatus = task.Status
		run.SafeSummary = "external_runner_timeout_task_progressed_verification_required"
		run.FailureCategory = ""
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.UpdatedAt = now
		updated, err := svc.store.UpdateRun(ctx, run)
		if err != nil {
			return err
		}
		if _, err := svc.reconcileVerifyingRun(ctx, updated); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) reconcileRecoveryRunsWithStaleReadyTasks(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok || updater == nil {
		return nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusFailed})
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || run.TaskID == "" {
			continue
		}
		if run.WorkTaskStatus != projectworkplan.WorkTaskStatusReady && !isRecoverableCodexExecutionFailure(run.FailureCategory) && !isRecoverableGovernedCloseoutFailure(run.FailureCategory) {
			continue
		}
		if !isRecoverableRecoveryFailure(run.FailureCategory) && !isRecoverableCodexExecutionFailure(run.FailureCategory) && !isRecoverableGovernedCloseoutFailure(run.FailureCategory) {
			continue
		}
		automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
		if err != nil {
			continue
		}
		if automation.SourceKind == AutomationSourceWorkflow && strings.TrimSpace(automation.TraceID) == "" {
			continue
		}
		task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || strings.TrimSpace(task.ClaimedByRunID) != run.ID || task.Status == projectworkplan.WorkTaskStatusReady {
			continue
		}
		switch task.Status {
		case projectworkplan.WorkTaskStatusDone, projectworkplan.WorkTaskStatusFailed, projectworkplan.WorkTaskStatusBlocked, projectworkplan.WorkTaskStatusCancelled, projectworkplan.WorkTaskStatusSuperseded:
			continue
		}
		readyTask, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
				ProjectID:          task.ProjectID,
				TaskID:             task.ID,
				SafeNextAction:     "recovery_run_ready_status_repair",
				RunID:              firstNonEmpty(run.ID, task.ClaimedByRunID),
				TraceID:            firstNonEmpty(run.TraceID, run.ID),
				ResumeInstructions: task.ResumeInstructions,
			},
			Status: projectworkplan.WorkTaskStatusReady,
		})
		if err != nil {
			return err
		}
		if err != nil || automation.Status != AutomationStatusEnabled || automation.TriggerKind != TriggerKindAutomatic || validateAllowedTaskRef(automation, readyTask) != nil {
			continue
		}
		if err := svc.queueReadyDependentAutomation(ctx, automation, readyTask); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) reconcileVerifyingRun(ctx context.Context, run AutomationRun) (AutomationRun, error) {
	if svc == nil || svc.store == nil || svc.workTasks == nil || run.Status != RunStatusVerifying || run.ProjectID == "" || run.TaskID == "" {
		return run, nil
	}
	task, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return run, nil
	}
	if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil && runPlanFailureCategory(err) == "work_plan_terminal" {
		return svc.blockRunFromTerminalPlan(ctx, run, task)
	}
	if task.Status == projectworkplan.WorkTaskStatusDone {
		return svc.completeRunAfterTaskDone(ctx, run, task)
	}
	if isTerminalIncompleteTaskStatus(task.Status) {
		return svc.finishRunAfterTaskTerminal(ctx, run, task)
	}
	if taskNeedsGitOpsPostTaskRecovery(run, task) {
		return svc.markRunForGitOpsPostTaskRecovery(ctx, run, task)
	}
	if !taskReadyForAutomationCloseout(task) {
		if run.WorkTaskStatus != task.Status {
			run.WorkTaskStatus = task.Status
			run.UpdatedAt = svc.now()
			return svc.store.UpdateRun(ctx, run)
		}
		return run, nil
	}
	action := projectworkplan.WorkTaskActionInput{
		ProjectID:          run.ProjectID,
		TaskID:             task.ID,
		RunID:              firstNonEmpty(run.ID, task.ClaimedByRunID),
		TraceID:            firstNonEmpty(run.TraceID, run.ID),
		Outcome:            automationCloseoutOutcome(task),
		SafeNextAction:     "automation_closeout",
		ClaimRefs:          append([]string(nil), task.ClaimRefs...),
		VerifierResultRefs: automationCloseoutVerifierRefs(task),
		ReviewResultRefs:   append([]string(nil), task.ReviewResultRefs...),
	}
	if reason := automationReviewExemptReason(task); reason != "" {
		action.ReviewResultRefs = nil
		action.ReviewExemptReason = reason
	}
	if isNoConfirmedBugPlannerTask(task) && len(action.ClaimRefs) == 0 {
		action.ClaimRefs = []string{"claim.no-confirmed-bug-remediation-not-required"}
	}
	completed, err := svc.workTasks.CompleteWorkTask(ctx, action)
	if err != nil {
		return run, err
	}
	updated, err := svc.completeRunAfterTaskDone(ctx, run, completed)
	if err != nil {
		return AutomationRun{}, err
	}
	if err := svc.reconcileReadyDependentAutomations(ctx, completed.ProjectID, completed.PlanID, completed.ID); err != nil {
		return updated, nil
	}
	if err := svc.completePlanIfNoOpenTasks(ctx, completed.ProjectID, completed.PlanID, completed.ID); err != nil {
		return updated, nil
	}
	return updated, nil
}

func (svc *Service) markRunForGitOpsPostTaskRecovery(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	now := svc.now()
	run.Status = RunStatusFailed
	run.WorkTaskStatus = task.Status
	run.SafeSummary = RunSafeSummaryGitOpsPostTaskRecovery
	run.FailureCategory = "gitops_post_task_failed"
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	return svc.store.UpdateRun(ctx, run)
}

func auditTaskHasConfirmedFindingWithoutRemediation(task projectworkplan.WorkTask) bool {
	if !isRemediationPlanningTask(task) || !taskHasConfirmedFinding(task) {
		return false
	}
	return !taskHasRemediationHandoff(task)
}

func isRemediationPlanningTask(task projectworkplan.WorkTask) bool {
	text := strings.ToLower(strings.Join([]string{task.TaskRef, task.Title, task.Description}, " "))
	return strings.Contains(text, "create-confirmed-bug") || strings.Contains(text, "remediation planning") || strings.Contains(text, "create remediation")
}

func taskHasConfirmedFinding(task projectworkplan.WorkTask) bool {
	for _, ref := range task.ClaimRefs {
		value := strings.ToLower(strings.TrimSpace(ref))
		if refIndicatesNoConfirmedBug(value) {
			continue
		}
		if strings.Contains(value, ".confirmed.") || strings.Contains(value, "-confirmed-") || strings.HasPrefix(value, "confirmed.") || strings.HasSuffix(value, ".confirmed") {
			return true
		}
	}
	return false
}

func taskHasRemediationHandoff(task projectworkplan.WorkTask) bool {
	refs := append([]string{task.Outcome, task.ResumeInstructions}, task.ClaimRefs...)
	refs = append(refs, task.EvidenceRefs...)
	refs = append(refs, task.ArtifactRefs...)
	refs = append(refs, task.KnowledgeCandidateRefs...)
	for _, ref := range refs {
		value := strings.ToLower(strings.TrimSpace(ref))
		if refIndicatesNoConfirmedBug(value) {
			continue
		}
		if strings.Contains(value, "remediation-work-plan") ||
			strings.Contains(value, "remediation work plan") ||
			strings.Contains(value, "remediation-work-task") ||
			strings.Contains(value, "remediation work task") ||
			strings.Contains(value, "remediation-automation") ||
			strings.Contains(value, "remediation automation") ||
			strings.Contains(value, "create-remediation") ||
			strings.Contains(value, "created remediation") ||
			strings.Contains(value, "bug-work-plan") ||
			strings.Contains(value, "bug work plan") ||
			strings.Contains(value, "bug-work-task") ||
			strings.Contains(value, "bug work task") ||
			strings.Contains(value, "auto-remediate-") ||
			strings.Contains(value, "auto-review-remediation-") {
			return true
		}
	}
	return false
}

func (svc *Service) completeRunAfterTaskDone(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	run.Status = RunStatusCompleted
	run.WorkTaskStatus = task.Status
	run.SafeSummary = RunSafeSummaryVerifiedTaskDone
	run.FailureCategory = ""
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	if err := svc.reconcilePostImplementationReviewParent(ctx, updated); err != nil {
		return updated, err
	}
	if err := svc.reconcileReadyDependentAutomations(ctx, task.ProjectID, task.PlanID, task.ID); err != nil {
		return updated, nil
	}
	if err := svc.completePlanIfNoOpenTasks(ctx, task.ProjectID, task.PlanID, task.ID); err != nil {
		return updated, nil
	}
	return updated, nil
}

func (svc *Service) reconcilePostImplementationReviewParent(ctx context.Context, reviewRun AutomationRun) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || strings.TrimSpace(reviewRun.ParentRunID) == "" {
		return nil
	}
	if strings.TrimSpace(reviewRun.SafeSummary) != RunSafeSummaryVerifiedTaskDone {
		return nil
	}
	parent, err := svc.store.GetRun(ctx, reviewRun.ProjectID, reviewRun.ParentRunID)
	if err != nil || parent.Status != RunStatusVerifying || strings.TrimSpace(parent.TaskID) == "" {
		return nil
	}
	parentTask, err := svc.workTasks.GetWorkTask(ctx, parent.ProjectID, parent.TaskID)
	if err != nil || !taskNeedsPostImplementationReview(parentTask) {
		return nil
	}
	reviewRef := "review_result_" + safeBranchToken(parentTask.ID) + "_approved"
	if _, err := svc.workTasks.AttachReviewResult(ctx, projectworkplan.AttachInput{
		ProjectID:       parentTask.ProjectID,
		TaskID:          parentTask.ID,
		Ref:             reviewRef,
		AttachedByRunID: reviewRun.ID,
		TraceID:         firstNonEmpty(reviewRun.TraceID, reviewRun.ID),
		Note:            "post-implementation review completed",
	}); err != nil {
		return err
	}
	_, err = svc.reconcileVerifyingRun(ctx, parent)
	return err
}

func (svc *Service) finishRunAfterTaskTerminal(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	run.WorkTaskStatus = task.Status
	run.SafeSummary = "external_codex_cli_task_terminal"
	switch task.Status {
	case projectworkplan.WorkTaskStatusBlocked:
		run.Status = RunStatusBlocked
		run.FailureCategory = "work_task_blocked"
	case projectworkplan.WorkTaskStatusFailed:
		run.Status = RunStatusFailed
		run.FailureCategory = "work_task_failed"
	case projectworkplan.WorkTaskStatusCancelled:
		run.Status = RunStatusCancelled
		run.FailureCategory = "work_task_cancelled"
	case projectworkplan.WorkTaskStatusSuperseded:
		run.Status = RunStatusCancelled
		run.FailureCategory = "work_task_superseded"
	default:
		return run, nil
	}
	now := svc.now()
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	updated, err := svc.store.UpdateRun(ctx, run)
	if err != nil {
		return AutomationRun{}, err
	}
	return updated, svc.updatePlanAfterTerminalTask(ctx, task)
}

func (svc *Service) blockRunFromTerminalPlan(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	run.WorkTaskStatus = task.Status
	run.SafeSummary = "work_plan_terminal"
	return svc.failRun(ctx, run, RunStatusBlocked, "work_plan_terminal")
}

func isTerminalIncompleteTaskStatus(status string) bool {
	switch status {
	case projectworkplan.WorkTaskStatusBlocked, projectworkplan.WorkTaskStatusFailed, projectworkplan.WorkTaskStatusCancelled, projectworkplan.WorkTaskStatusSuperseded:
		return true
	default:
		return false
	}
}

func isTerminalAutomationTaskStatus(status string) bool {
	return status == projectworkplan.WorkTaskStatusDone || isTerminalIncompleteTaskStatus(status)
}

func (svc *Service) updatePlanAfterTerminalTask(ctx context.Context, task projectworkplan.WorkTask) error {
	if task.ProjectID == "" || task.PlanID == "" {
		return nil
	}
	workPlans, ok := svc.workTasks.(remediationWorkPlanAPI)
	if !ok || workPlans == nil {
		return nil
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusBlocked:
		_, err := workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:     task.ProjectID,
			PlanID:        task.PlanID,
			Status:        projectworkplan.WorkPlanStatusBlocked,
			Outcome:       firstNonEmpty(task.BlockedReason, "automation Work Task blocked"),
			ResumeSummary: firstNonEmpty(task.ResumeInstructions, "resolve blocked Work Task before resuming automation"),
			CurrentTaskID: task.ID,
		})
		return err
	case projectworkplan.WorkTaskStatusFailed:
		_, err := workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:     task.ProjectID,
			PlanID:        task.PlanID,
			Status:        projectworkplan.WorkPlanStatusFailed,
			Outcome:       firstNonEmpty(task.Outcome, "automation Work Task failed"),
			ResumeSummary: firstNonEmpty(task.ResumeInstructions, "inspect failed Work Task before resuming automation"),
			CurrentTaskID: task.ID,
		})
		return err
	case projectworkplan.WorkTaskStatusCancelled:
		_, err := workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:     task.ProjectID,
			PlanID:        task.PlanID,
			Status:        projectworkplan.WorkPlanStatusCancelled,
			Outcome:       firstNonEmpty(task.Outcome, "automation Work Task cancelled"),
			ResumeSummary: firstNonEmpty(task.ResumeInstructions, "cancelled Work Task terminated automation Work Plan"),
			CurrentTaskID: task.ID,
		})
		return err
	case projectworkplan.WorkTaskStatusSuperseded:
		_, err := workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:     task.ProjectID,
			PlanID:        task.PlanID,
			Status:        projectworkplan.WorkPlanStatusSuperseded,
			Outcome:       firstNonEmpty(task.Outcome, "automation Work Task superseded"),
			ResumeSummary: firstNonEmpty(task.ResumeInstructions, "superseded Work Task terminated automation Work Plan"),
			CurrentTaskID: task.ID,
		})
		return err
	default:
		return nil
	}
}

func (svc *Service) completePlanIfNoOpenTasks(ctx context.Context, projectID string, planID string, currentTaskID string) error {
	if projectID == "" || planID == "" {
		return nil
	}
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return err
	}
	if len(tasks) > 0 {
		return nil
	}
	workPlans, ok := svc.workTasks.(remediationWorkPlanAPI)
	if !ok || workPlans == nil {
		return nil
	}
	_, err = workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:     projectID,
		PlanID:        planID,
		Status:        projectworkplan.WorkPlanStatusDone,
		Outcome:       "automation closeout completed all Work Tasks",
		ResumeSummary: "automation closeout completed all Work Tasks",
		CurrentTaskID: currentTaskID,
	})
	return err
}

func (svc *Service) reconcileReadyDependentAutomations(ctx context.Context, projectID string, planID string, completedTaskID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || projectID == "" || planID == "" {
		return nil
	}
	return svc.reconcileReadyAutomationsForPlan(ctx, projectID, planID, completedTaskID)
}

func (svc *Service) reconcileReadyAutomationsForProject(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	workPlans, ok := svc.workTasks.(remediationWorkPlanAPI)
	if !ok || workPlans == nil {
		return nil
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: projectID, Status: projectworkplan.WorkPlanStatusActive})
	if err != nil {
		return err
	}
	automations, err := svc.store.ListAutomations(ctx, AutomationFilter{ProjectID: projectID, Status: AutomationStatusEnabled})
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if err := svc.reconcileReadyAutomationsForPlan(ctx, projectID, plan.ID, ""); err != nil {
			return err
		}
		if err := svc.blockReadyTasksWithoutEnabledAutomation(ctx, projectID, plan.ID, automations); err != nil {
			return err
		}
		if err := svc.blockActivePlanFromStaleBlockedTasks(ctx, projectID, plan.ID); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) blockActivePlanFromStaleBlockedTasks(ctx context.Context, projectID string, planID string) error {
	if svc == nil || svc.workTasks == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(planID) == "" {
		return nil
	}
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Status != projectworkplan.WorkTaskStatusBlocked {
			continue
		}
		return svc.updatePlanAfterTerminalTask(ctx, task)
	}
	return nil
}

func (svc *Service) reconcileReadyAutomationsForPlan(ctx context.Context, projectID string, planID string, completedTaskID string) error {
	readyTasks, err := svc.readyPlannedDependentTasks(ctx, projectID, planID, completedTaskID)
	if err != nil {
		return err
	}
	existingReadyTasks, err := svc.readyOpenTasks(ctx, projectID, planID)
	if err != nil {
		return err
	}
	readyTasks = appendUniqueTasks(readyTasks, existingReadyTasks...)
	if len(readyTasks) == 0 {
		return nil
	}
	automations, err := svc.store.ListAutomations(ctx, AutomationFilter{ProjectID: projectID, Status: AutomationStatusEnabled})
	if err != nil {
		return err
	}
	for _, task := range readyTasks {
		for _, automation := range automations {
			if automation.PlanID != planID || automation.TriggerKind != TriggerKindAutomatic {
				continue
			}
			if validateAllowedTaskRef(automation, task) != nil {
				continue
			}
			if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
				return err
			}
		}
	}
	return nil
}

func (svc *Service) readyOpenTasks(ctx context.Context, projectID string, planID string) ([]projectworkplan.WorkTask, error) {
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return nil, err
	}
	ready := make([]projectworkplan.WorkTask, 0)
	for _, task := range tasks {
		if task.Status != projectworkplan.WorkTaskStatusReady {
			continue
		}
		if task.DecompositionQuality != projectworkplan.DecompositionReady || strings.TrimSpace(task.VerificationRequirement) == "" {
			continue
		}
		if !svc.dependenciesDone(ctx, task) {
			continue
		}
		ready = append(ready, task)
	}
	return ready, nil
}

func appendUniqueTasks(tasks []projectworkplan.WorkTask, additions ...projectworkplan.WorkTask) []projectworkplan.WorkTask {
	seen := make(map[string]struct{}, len(tasks)+len(additions))
	out := make([]projectworkplan.WorkTask, 0, len(tasks)+len(additions))
	for _, task := range tasks {
		if task.ID == "" {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		seen[task.ID] = struct{}{}
		out = append(out, task)
	}
	for _, task := range additions {
		if task.ID == "" {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		seen[task.ID] = struct{}{}
		out = append(out, task)
	}
	return out
}

func (svc *Service) readyPlannedDependentTasks(ctx context.Context, projectID string, planID string, completedTaskID string) ([]projectworkplan.WorkTask, error) {
	updater, ok := svc.workTasks.(workTaskStatusUpdater)
	if !ok {
		return nil, nil
	}
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		return nil, err
	}
	ready := make([]projectworkplan.WorkTask, 0)
	for _, task := range tasks {
		if task.Status != projectworkplan.WorkTaskStatusPlanned {
			continue
		}
		if len(task.DependencyTaskIDs) == 0 {
			continue
		}
		if completedTaskID != "" && !containsRef(task.DependencyTaskIDs, completedTaskID) {
			continue
		}
		if task.DecompositionQuality != projectworkplan.DecompositionReady || strings.TrimSpace(task.VerificationRequirement) == "" {
			continue
		}
		if !svc.dependenciesDone(ctx, task) {
			continue
		}
		updated, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
				ProjectID:      projectID,
				TaskID:         task.ID,
				SafeNextAction: "dependency_ready_automation",
				RunID:          "dependency-ready",
				TraceID:        "dependency-ready",
			},
			Status: projectworkplan.WorkTaskStatusReady,
		})
		if err != nil {
			return nil, err
		}
		ready = append(ready, updated)
	}
	return ready, nil
}

func (svc *Service) queueReadyDependentAutomation(ctx context.Context, automation Automation, task projectworkplan.WorkTask) error {
	if err := svc.validateRunPlanExecutable(ctx, AutomationRun{ProjectID: task.ProjectID, PlanID: task.PlanID}, task); err != nil {
		return nil
	}
	existing, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: task.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		return err
	}
	for _, run := range existing {
		if run.TaskID == task.ID && isActiveAutomationRunStatus(run.Status) && !isQueuedReplacementRun(run) {
			return nil
		}
	}
	task, err = svc.markTaskAfterRecoverableDirtyScopeConfig(ctx, task, existing)
	if err != nil {
		return err
	}
	if countTerminalReplacementFailures(existing, task) >= defaultAutomationMaxReplacementRunsPerTask {
		_, err := svc.blockTaskAfterReplacementRetryLimit(ctx, task, latestTerminalReplacementFailureCategory(existing, task))
		return err
	}
	for _, run := range existing {
		if run.TaskID != task.ID {
			continue
		}
		if svc.shouldQueueReplacementRunForTask(ctx, automation, run, task) {
			continue
		}
		return nil
	}
	now := svc.now()
	run := AutomationRun{
		ID:                svc.newID("automation_run"),
		ProjectID:         task.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           firstNonEmpty(task.OwnerAgent, automation.AgentID),
		PlanID:            task.PlanID,
		TaskID:            task.ID,
		Status:            RunStatusQueued,
		RunnerKind:        RunnerKindCodexCLI,
		AttemptCount:      0,
		OrchestratorRunID: dependencyReadyRunID(task, automation),
		SafeSummary:       "dependency_ready_automation_queued",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	_, err = svc.store.CreateRun(ctx, run)
	return err
}

const dirtyScopeConfigRecoveryRunID = "orchestrator-system-fix-dirty-scope-config"

func (svc *Service) markTaskAfterRecoverableDirtyScopeConfig(ctx context.Context, task projectworkplan.WorkTask, existing []AutomationRun) (projectworkplan.WorkTask, error) {
	if svc == nil || svc.workTasks == nil || task.Status != projectworkplan.WorkTaskStatusReady || taskHasSystemFixRecoveryMarker(task) {
		return task, nil
	}
	if countTerminalReplacementFailures(existing, task) < defaultAutomationMaxReplacementRunsPerTask {
		return task, nil
	}
	dirtyPaths := dirtyPathsFromEvidenceRefs(task.EvidenceRefs)
	if len(dirtyPaths) == 0 {
		return task, nil
	}
	expandScopes, outsidePaths := svc.classifyDirtyScopePaths(task.ProjectID, task, dirtyPaths)
	if len(outsidePaths) > 0 || len(expandScopes) == 0 {
		return task, nil
	}
	if expander, ok := svc.workTasks.(workTaskScopeExpander); ok && expander != nil {
		expanded, err := expander.ExpandWorkTaskScope(ctx, projectworkplan.ExpandWorkTaskScopeInput{
			ProjectID:          task.ProjectID,
			TaskID:             task.ID,
			FilesToEdit:        expandScopes,
			RunID:              dirtyScopeConfigRecoveryRunID,
			TraceID:            dirtyScopeConfigRecoveryRunID,
			ResumeInstructions: recoveryResumeInstructions("Configured dirty-scope recovery now covers previous dirty paths: " + strings.Join(dirtyPaths, ", ") + ". Requeue implementation once under the refreshed scope."),
		})
		if err != nil {
			return projectworkplan.WorkTask{}, err
		}
		return expanded, nil
	}
	return task, nil
}

func (svc *Service) replacementRetryLimitReached(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (bool, error) {
	if !isQueuedReplacementRun(run) {
		return false, nil
	}
	if run.ProjectID == "" || run.AutomationID == "" || task.PlanID == "" || task.ID == "" {
		return false, nil
	}
	existing, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: run.ProjectID, AutomationID: run.AutomationID, PlanID: task.PlanID})
	if err != nil {
		return false, err
	}
	return countTerminalReplacementFailures(existing, task) >= defaultAutomationMaxReplacementRunsPerTask, nil
}

func (svc *Service) blockRunAfterReplacementRetryLimit(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (AutomationRun, error) {
	lastFailureCategory, err := svc.latestTerminalReplacementFailureCategory(ctx, run, task)
	if err != nil {
		return run, err
	}
	blockedTask, err := svc.blockTaskAfterReplacementRetryLimit(ctx, task, lastFailureCategory)
	if err != nil {
		return run, err
	}
	now := svc.now()
	run.Status = RunStatusBlocked
	run.WorkTaskStatus = blockedTask.Status
	run.FailureCategory = automationReplacementRetryLimitCategory
	run.SafeSummary = automationReplacementRetryLimitCategory
	if run.FinishedAt.IsZero() {
		run.FinishedAt = now
	}
	run.UpdatedAt = now
	return svc.store.UpdateRun(ctx, run)
}

func (svc *Service) latestTerminalReplacementFailureCategory(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) (string, error) {
	if run.ProjectID == "" || run.AutomationID == "" || task.PlanID == "" || task.ID == "" {
		return "", nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: run.ProjectID, AutomationID: run.AutomationID, PlanID: task.PlanID})
	if err != nil {
		return "", err
	}
	return latestTerminalReplacementFailureCategory(runs, task), nil
}

func latestTerminalReplacementFailureCategory(runs []AutomationRun, task projectworkplan.WorkTask) string {
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for i := len(runs) - 1; i >= 0; i-- {
		candidate := runs[i]
		if candidate.TaskID != task.ID || !isTerminalReplacementFailure(candidate) {
			continue
		}
		if category := strings.TrimSpace(candidate.FailureCategory); category != "" {
			return category
		}
	}
	return ""
}

func (svc *Service) blockTaskAfterReplacementRetryLimit(ctx context.Context, task projectworkplan.WorkTask, lastFailureCategory string) (projectworkplan.WorkTask, error) {
	blockedReason := replacementRetryLimitBlockedReason(lastFailureCategory)
	blocked, err := svc.workTasks.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          task.ProjectID,
		TaskID:             task.ID,
		SafeNextAction:     automationReplacementRetryLimitCategory,
		TraceID:            "automation-replacement-limit",
		BlockedReason:      blockedReason,
		ResumeInstructions: replacementRetryLimitResumeInstructions(lastFailureCategory),
	})
	if err != nil {
		return projectworkplan.WorkTask{}, err
	}
	if err := svc.updatePlanAfterTerminalTask(ctx, blocked); err != nil {
		return blocked, err
	}
	return blocked, nil
}

func replacementRetryLimitBlockedReason(lastFailureCategory string) string {
	category := strings.TrimSpace(lastFailureCategory)
	if category == "" {
		return "Automation replacement retry limit reached; last concrete failure category is unavailable."
	}
	return "Automation replacement retry limit reached after repeated " + category + " failures."
}

func replacementRetryLimitResumeInstructions(lastFailureCategory string) string {
	category := strings.TrimSpace(lastFailureCategory)
	if isRecoverableGovernedCloseoutFailure(category) {
		return governedCloseoutRecoveryResumeInstructions(category)
	}
	if isRecoverableCodexExecutionFailure(category) {
		return codexExecutionFailureResumeInstructions(category)
	}
	return "Inspect the last failed automation run and correct " + safeFailure(category) + " before requeueing. Do not create another replacement run until the concrete blocker is corrected."
}

func countTerminalReplacementFailures(runs []AutomationRun, task projectworkplan.WorkTask) int {
	count := 0
	recoveryAfter := time.Time{}
	if taskHasSystemFixRecoveryMarker(task) {
		recoveryAfter = task.UpdatedAt
	}
	for _, run := range runs {
		if !recoveryAfter.IsZero() && !run.UpdatedAt.After(recoveryAfter) {
			continue
		}
		if run.TaskID == task.ID && isTerminalReplacementFailure(run) {
			count++
		}
	}
	return count
}

func taskHasSystemFixRecoveryMarker(task projectworkplan.WorkTask) bool {
	for _, ref := range task.AgentRunIDs {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, "orchestrator-system-fix-") || strings.HasPrefix(ref, "orchestrator-requeue-") {
			return true
		}
	}
	return false
}

func isQueuedReplacementRun(run AutomationRun) bool {
	return strings.TrimSpace(run.SafeSummary) == "dependency_ready_automation_queued" ||
		strings.HasPrefix(strings.TrimSpace(run.OrchestratorRunID), "dependency-ready:")
}

func isActiveAutomationRunStatus(status string) bool {
	switch status {
	case RunStatusQueued, RunStatusClaiming, RunStatusStarting, RunStatusRunning, RunStatusVerifying:
		return true
	default:
		return false
	}
}

func isTerminalReplacementFailure(run AutomationRun) bool {
	switch run.Status {
	case RunStatusFailed:
		if isRecoverableRecoveryFailure(run.FailureCategory) || isRecoverableCodexExecutionFailure(run.FailureCategory) || isRecoverableGovernedCloseoutFailure(run.FailureCategory) {
			return true
		}
		return false
	case RunStatusTimeout:
		return isRecoverableCodexExecutionFailure(run.FailureCategory)
	default:
		return false
	}
}

func shouldQueueReplacementRun(run AutomationRun) bool {
	switch run.Status {
	case RunStatusFailed:
		return isRecoverablePreExecutionFailure(run.FailureCategory) ||
			isRecoverableGitOpsPostTaskFailure(run.FailureCategory) ||
			isRecoverableReviewGitOpsFailure(run.FailureCategory) ||
			isRecoverableRecoveryFailure(run.FailureCategory) ||
			isRecoverableCodexExecutionFailure(run.FailureCategory) ||
			isRecoverableGovernedCloseoutFailure(run.FailureCategory)
	case RunStatusTimeout:
		return strings.TrimSpace(run.FailureCategory) == "external_runner_interrupted"
	default:
		return false
	}
}

func isRecoverableRecoveryFailure(category string) bool {
	switch strings.TrimSpace(category) {
	case "gitops_recovery_failed_requires_implementation", "pre_execution_recovery_failed_requires_implementation":
		return true
	default:
		return false
	}
}

func isRecoverableCodexExecutionFailure(category string) bool {
	switch strings.TrimSpace(category) {
	case "codex_cli_failed", "codex_cli_timeout":
		return true
	default:
		return false
	}
}

func isNonRetryableCodexExecutionFailure(category string) bool {
	switch strings.TrimSpace(category) {
	case "codex_output_schema_invalid", "codex_usage_limit_reached":
		return true
	default:
		return false
	}
}

func isRecoverableGovernedCloseoutFailure(category string) bool {
	category = strings.TrimSpace(category)
	for _, prefix := range []string{
		"automation_task_closeout_failed",
		"automation_task_closeout_missing",
		"governed_closeout_apply_failed",
		"governed_closeout_invalid_json",
		"governed_closeout_output_missing",
		"governed_closeout_readback_failed",
		"governed_closeout_schema_create_failed",
		"governed_closeout_validation_failed",
	} {
		if category == prefix || strings.HasPrefix(category, prefix+"_") {
			return true
		}
	}
	return false
}

func governedCloseoutRecoveryResumeInstructions(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		category = "governed_closeout_failed"
	}
	return "Governed closeout failed with " + category + ". Inspect the automation run evidence and retry the same bounded task; do not bypass child task, review gate, verifier, or stop-condition requirements."
}

func codexExecutionFailureResumeInstructions(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		category = "codex_cli_failed"
	}
	return recoveryResumeInstructions("Codex execution failed with " + safeFailure(category) + ". Inspect the automation run evidence, runner worktree, Codex binary/config, and generated closeout before retrying the same bounded task.")
}

func nonRetryableCodexBlockedReason(category string) string {
	switch strings.TrimSpace(category) {
	case "codex_output_schema_invalid":
		return "Codex output schema is invalid for the configured runner; automation cannot continue until the schema or runner invocation is corrected."
	case "codex_usage_limit_reached":
		return "Codex usage limit reached; automation cannot continue until quota or credits are available."
	default:
		return "Codex execution failed with a non-retryable runtime condition."
	}
}

func nonRetryableCodexResumeInstructions(category string) string {
	switch strings.TrimSpace(category) {
	case "codex_output_schema_invalid":
		return "Correct the runner output-schema configuration, then explicitly requeue this Work Task; do not retry automatically while the schema is invalid."
	case "codex_usage_limit_reached":
		return "Restore Codex quota or credits, then explicitly requeue this Work Task; do not retry automatically while the usage limit is active."
	default:
		return "Resolve the non-retryable Codex runtime condition, then explicitly requeue this Work Task."
	}
}

func (svc *Service) shouldQueueReplacementRunForTask(ctx context.Context, automation Automation, run AutomationRun, task projectworkplan.WorkTask) bool {
	if shouldQueueReplacementRun(run) {
		return true
	}
	if run.Status == RunStatusPolicyDenied &&
		task.Status == projectworkplan.WorkTaskStatusReady &&
		strings.Contains(strings.TrimSpace(run.FailureCategory), "task_not_ready") {
		return true
	}
	if run.Status == RunStatusFailed &&
		task.Status == projectworkplan.WorkTaskStatusReady &&
		strings.TrimSpace(run.FailureCategory) == "gitops_dirty_worktree_scope_requires_plan" {
		return true
	}
	if run.Status == RunStatusBlocked &&
		task.Status == projectworkplan.WorkTaskStatusReady &&
		strings.TrimSpace(run.FailureCategory) == "automation_review_gate_open" {
		return len(automation.RequiredReviewTaskIDs) > 0 && svc.validateRequiredAutomationReviews(ctx, automation) == nil
	}
	return run.Status == RunStatusBlocked &&
		strings.TrimSpace(run.FailureCategory) == "work_task_blocked" &&
		task.Status == projectworkplan.WorkTaskStatusReady
}

func dependencyReadyRunID(task projectworkplan.WorkTask, automation Automation) string {
	return "dependency-ready:" + task.PlanID + ":" + task.ID + ":" + automation.ID
}

func containsRef(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func (svc *Service) queuePostImplementationReview(ctx context.Context, run AutomationRun) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil || run.ProjectID == "" || run.PlanID == "" || run.TaskID == "" {
		return nil
	}
	target, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
	if err != nil || !taskNeedsPostImplementationReview(target) {
		return nil
	}
	return svc.queueReviewForImplementationTask(ctx, run, target)
}

func (svc *Service) queueOutstandingPostImplementationReviews(ctx context.Context, projectID string) error {
	if svc == nil || svc.store == nil || svc.workTasks == nil {
		return nil
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: projectID, Status: RunStatusVerifying})
	if err != nil {
		return err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt.Before(runs[j].UpdatedAt) })
	for _, run := range runs {
		if run.RunnerKind != RunnerKindCodexCLI || run.TaskID == "" || run.PlanID == "" {
			continue
		}
		target, err := svc.workTasks.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if err != nil || !taskNeedsPostImplementationReview(target) {
			continue
		}
		if err := svc.queueReviewForImplementationTask(ctx, run, target); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) queueReviewForImplementationTask(ctx context.Context, run AutomationRun, target projectworkplan.WorkTask) error {
	tasks, err := svc.workTasks.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: run.ProjectID, PlanID: run.PlanID})
	if err != nil {
		return err
	}
	reviewTaskRef := "review-" + target.TaskRef
	for _, task := range tasks {
		if task.TaskRef != reviewTaskRef {
			continue
		}
		if task.Status == projectworkplan.WorkTaskStatusDone {
			return nil
		}
		if task.Status == projectworkplan.WorkTaskStatusPlanned {
			updater, ok := svc.workTasks.(workTaskStatusUpdater)
			if !ok {
				return fmt.Errorf("%w: post_implementation_review_status_unavailable", ErrInvalidInput)
			}
			updated, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
				WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
					ProjectID:      run.ProjectID,
					TaskID:         task.ID,
					SafeNextAction: "post_implementation_review",
					RunID:          run.ID,
					TraceID:        firstNonEmpty(run.TraceID, run.ID),
				},
				Status: projectworkplan.WorkTaskStatusReady,
			})
			if err != nil {
				return err
			}
			task = updated
		}
		if task.Status != projectworkplan.WorkTaskStatusReady {
			return nil
		}
		return svc.queuePostImplementationReviewRun(ctx, run, task)
	}
	task, err := svc.createRecoveryPostImplementationReviewTask(ctx, run, target, reviewTaskRef)
	if err != nil {
		return err
	}
	return svc.queuePostImplementationReviewRun(ctx, run, task)
}

func (svc *Service) queuePostImplementationReviewRun(ctx context.Context, parent AutomationRun, reviewTask projectworkplan.WorkTask) error {
	if err := svc.validateRunPlanExecutable(ctx, parent, reviewTask); err != nil {
		return nil
	}
	automations, err := svc.store.ListAutomations(ctx, AutomationFilter{ProjectID: parent.ProjectID, Status: AutomationStatusEnabled})
	if err != nil {
		return err
	}
	for _, automation := range automations {
		if automation.PlanID != parent.PlanID || automation.TriggerKind != TriggerKindAutomatic || automation.SchedulePolicy != "post_implementation_review" {
			continue
		}
		if validateAllowedTaskRef(automation, reviewTask) != nil {
			continue
		}
		existing, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: parent.ProjectID, AutomationID: automation.ID, PlanID: parent.PlanID})
		if err != nil {
			return err
		}
		for _, run := range existing {
			if run.TaskID == reviewTask.ID && (run.Status == RunStatusQueued || run.Status == RunStatusClaiming || run.Status == RunStatusStarting || run.Status == RunStatusRunning || run.Status == RunStatusVerifying) {
				return nil
			}
		}
		now := svc.now()
		reviewRun := AutomationRun{
			ID: svc.newID("automation_run"), ProjectID: parent.ProjectID, AutomationID: automation.ID,
			AgentID: firstNonEmpty(reviewTask.OwnerAgent, automation.AgentID), PlanID: parent.PlanID, TaskID: reviewTask.ID,
			Status: RunStatusQueued, RunnerKind: firstNonEmpty(parent.RunnerKind, RunnerKindCodexCLI), AttemptCount: 0,
			OrchestratorRunID: "post-review:" + parent.ID, ParentRunID: parent.ID,
			SafeSummary: RunSafeSummaryPostImplementationReviewQueued, CreatedAt: now, UpdatedAt: now,
		}
		_, err = svc.store.CreateRun(ctx, reviewRun)
		return err
	}
	automation, err := svc.createRecoveryPostImplementationReviewAutomation(ctx, parent, reviewTask)
	if err != nil {
		return err
	}
	return svc.queuePostImplementationReviewRun(ctx, AutomationRun{
		ID: parent.ID, ProjectID: parent.ProjectID, PlanID: parent.PlanID, RunnerKind: parent.RunnerKind,
	}, reviewTaskForAutomation(reviewTask, automation))
}

func taskNeedsPostImplementationReview(task projectworkplan.WorkTask) bool {
	if isReviewTask(task) || len(task.FilesToEdit) == 0 {
		return false
	}
	if len(task.ReviewResultRefs) > 0 {
		return false
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusNeedsReview:
		return true
	case projectworkplan.WorkTaskStatusVerifying:
		return strings.TrimSpace(task.ReviewGate) != ""
	default:
		return false
	}
}

func (svc *Service) createRecoveryPostImplementationReviewTask(ctx context.Context, run AutomationRun, target projectworkplan.WorkTask, reviewTaskRef string) (projectworkplan.WorkTask, error) {
	workPlans, ok := svc.workTasks.(remediationWorkPlanAPI)
	if !ok || workPlans == nil {
		return projectworkplan.WorkTask{}, fmt.Errorf("%w: post_implementation_review_task_missing", ErrInvalidInput)
	}
	reviewerAgentID := independentReviewerAgent(target.OwnerAgent)
	files := uniqueRefs(append(append(append([]string{}, target.FilesToRead...), target.FilesToEdit...), target.LikelyFilesAffected...))
	return workPlans.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               run.ProjectID,
		PlanID:                  run.PlanID,
		TaskRef:                 reviewTaskRef,
		Title:                   "Review remediation " + target.TaskRef,
		Description:             "Independently review implementation task " + target.ID + " after automation runner completion.",
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              reviewerAgentID,
		RunID:                   run.ID,
		TraceID:                 firstNonEmpty(run.TraceID, run.ID),
		EvidenceNeeded:          safeWorkerEvidenceRefs([]string{"review-target-" + target.ID, "implementation-task-" + target.ID, "implementation-output-refs"}),
		FilesToRead:             files,
		LikelyFilesAffected:     files,
		VerificationRequirement: "Attach an independent review_result_ref to implementation task " + target.ID + " before completion.",
		ExpectedOutput:          "Independent review decision for implementation task " + target.ID + " with review refs attached to the implementation task.",
		FailureCriteria:         "Block on self-review, missing implementation evidence, missing verifier refs, unsafe payloads, or unclear approval decision.",
		ReviewGate:              "independent-reviewer-must-not-be-" + target.OwnerAgent,
		ResumeInstructions:      "Review implementation task " + target.ID + " only. Attach review_result_ref to that implementation task, then complete this review task.",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
}

func (svc *Service) createRecoveryPostImplementationReviewAutomation(ctx context.Context, parent AutomationRun, reviewTask projectworkplan.WorkTask) (Automation, error) {
	automationRef := "auto-review-" + safeBranchToken(reviewTask.TaskRef) + "-" + safeBranchToken(firstNonEmpty(parent.PlanID, reviewTask.PlanID, parent.ID))
	if automationRef == "auto-review-" {
		automationRef += reviewTask.ID
	}
	return svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       parent.ProjectID,
		AutomationRef:   automationRef,
		Title:           "Review remediation " + reviewTask.TaskRef,
		Purpose:         "Independently review implementation output for task " + reviewTask.TaskRef + ".",
		Status:          AutomationStatusEnabled,
		AgentID:         firstNonEmpty(reviewTask.OwnerAgent, "codex-reviewer"),
		PlanID:          parent.PlanID,
		AllowedTaskRefs: []string{reviewTask.ID, reviewTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission-remediation-review-" + safeBranchToken(reviewTask.TaskRef),
		SourceKind:      AutomationSourceManual,
		CreatedByRunID:  parent.ID,
		TraceID:         firstNonEmpty(parent.TraceID, parent.ID),
	})
}

func reviewTaskForAutomation(reviewTask projectworkplan.WorkTask, automation Automation) projectworkplan.WorkTask {
	if reviewTask.OwnerAgent == "" {
		reviewTask.OwnerAgent = automation.AgentID
	}
	return reviewTask
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

func (svc *Service) prepareRunForExecution(ctx context.Context, run AutomationRun, runnerID string) (AutomationRun, projectworkplan.WorkTask, error) {
	automation, err := svc.store.GetAutomation(ctx, run.ProjectID, run.AutomationID)
	if err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "automation_unavailable")
		return updated, projectworkplan.WorkTask{}, err
	}
	if err := svc.validateAutomationPolicy(ctx, automation, run.RunnerKind, run.TaskID, run.AgentID); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusPolicyDenied, err.Error())
		return updated, projectworkplan.WorkTask{}, err
	}
	if !svc.isAutomationReviewTask(automation, run.TaskID) {
		if err := svc.validateRequiredAutomationReviews(ctx, automation); err != nil {
			updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "automation_review_gate_open")
			return updated, projectworkplan.WorkTask{}, err
		}
	}
	task, err := svc.resolveTask(ctx, run, automation)
	if err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "task_unavailable")
		return updated, projectworkplan.WorkTask{}, err
	}
	if err := svc.validateRunPlanExecutable(ctx, run, task); err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, runPlanFailureCategory(err))
		return updated, projectworkplan.WorkTask{}, err
	}
	if reached, err := svc.replacementRetryLimitReached(ctx, run, task); err != nil {
		return run, projectworkplan.WorkTask{}, err
	} else if reached {
		updated, updateErr := svc.blockRunAfterReplacementRetryLimit(ctx, run, task)
		if updateErr != nil {
			return updated, projectworkplan.WorkTask{}, updateErr
		}
		return updated, projectworkplan.WorkTask{}, fmt.Errorf("%w: %s", ErrInvalidInput, automationReplacementRetryLimitCategory)
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
	claimedTask, err := svc.workTasks.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: run.ProjectID, TaskID: task.ID, OwnerAgent: run.AgentID, RunID: run.ID, TraceID: run.TraceID})
	if err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "claim_failed")
		return updated, projectworkplan.WorkTask{}, err
	}
	task = claimedTask
	run.Status = RunStatusStarting
	run.WorkTaskStatus = task.Status
	run.UpdatedAt = svc.now()
	if run, err = svc.store.UpdateRun(ctx, run); err != nil {
		return run, projectworkplan.WorkTask{}, err
	}
	startedTask, err := svc.workTasks.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: run.ProjectID, TaskID: task.ID, OwnerAgent: run.AgentID, RunID: run.ID, TraceID: run.TraceID})
	if err != nil {
		updated, _ := svc.failRun(ctx, run, RunStatusBlocked, "start_failed")
		return updated, projectworkplan.WorkTask{}, err
	}
	task = startedTask
	run.Status = RunStatusRunning
	run.WorkTaskStatus = task.Status
	run.AttemptCount++
	run.StartedAt = svc.now()
	svc.applyExternalClaim(&run, runnerID, run.StartedAt)
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

func (svc *Service) validateRunPlanExecutable(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) error {
	planID := firstNonEmpty(task.PlanID, run.PlanID)
	return svc.validatePlanExecutable(ctx, run.ProjectID, planID)
}

func (svc *Service) validatePlanExecutable(ctx context.Context, projectID string, planID string) error {
	if strings.TrimSpace(planID) == "" {
		return nil
	}
	plan, err := svc.workTasks.GetWorkPlan(ctx, projectID, planID)
	if err != nil {
		return fmt.Errorf("%w: work_plan_unavailable", ErrInvalidInput)
	}
	if isTerminalWorkPlanStatus(plan.Status) {
		return fmt.Errorf("%w: work_plan_terminal", ErrInvalidInput)
	}
	return nil
}

func runPlanFailureCategory(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "work_plan_unavailable") {
		return "work_plan_unavailable"
	}
	return "work_plan_terminal"
}

func isTerminalWorkPlanStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case projectworkplan.WorkPlanStatusDone,
		projectworkplan.WorkPlanStatusBlocked,
		projectworkplan.WorkPlanStatusFailed,
		projectworkplan.WorkPlanStatusCancelled,
		projectworkplan.WorkPlanStatusSuperseded:
		return true
	default:
		return false
	}
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
		} else if result.SafeFailureCategory != "" {
			attemptStatus = RunStatusFailed
			failureCategory = result.SafeFailureCategory
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
	MCPServerURL            string   `json:"mcp_server_url,omitempty"`
	AutomationRunID         string   `json:"automation_run_id"`
	TraceID                 string   `json:"trace_id,omitempty"`
	PlanID                  string   `json:"plan_id"`
	TaskID                  string   `json:"task_id"`
	TaskRef                 string   `json:"task_ref"`
	Title                   string   `json:"title"`
	Description             string   `json:"description,omitempty"`
	EvidenceNeeded          []string `json:"evidence_needed,omitempty"`
	ContextPackRefs         []string `json:"context_pack_refs,omitempty"`
	WorkPlanContext         []string `json:"work_plan_context,omitempty"`
	DependencyContext       []string `json:"dependency_context,omitempty"`
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
	builder.WriteString("Perform the task now in the current repository workspace. Keep the change limited to the likely affected files. Do not run full test suites. Do not mark the Work Task done; the orchestrator will verify and complete it.\n\n")
	builder.WriteString("Identifiers:\n")
	writePromptLine(&builder, "- Project ID", input.ProjectID)
	writePromptLine(&builder, "- Mivia server URL", input.MCPServerURL)
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
	writePromptList(&builder, "- Work Plan context", input.WorkPlanContext)
	writePromptList(&builder, "- Completed dependency context", input.DependencyContext)
	writePromptList(&builder, "- Likely files affected", input.LikelyFilesAffected)
	writePromptLine(&builder, "- Verification requirement", input.VerificationRequirement)
	writePromptLine(&builder, "- Expected output", input.ExpectedOutput)
	writePromptLine(&builder, "- Failure criteria", input.FailureCriteria)
	writePromptLine(&builder, "- Resume instructions", input.ResumeInstructions)
	if !isGovernedWorkflowTaskRef(input.TaskRef) {
		writeConcreteRESTCloseoutEndpoints(&builder, input)
	}
	builder.WriteString("\nRules:\n")
	builder.WriteString("- Do not call projects.automation_runs.complete_attempt. The external runner owns automation run attempt reporting after your process exits.\n")
	builder.WriteString("- Do not commit, push, or create pull requests when supervised runner GitOps is enabled. Modify task files only; the runner commits, pushes, and opens draft PRs after governed task closeout.\n")
	builder.WriteString("- When attaching MCP evidence, claim, verifier, and knowledge refs, use short safe refs with only letters, numbers, dots, underscores, and hyphens. Do not use colons, slashes, paths, commands, raw logs, or source snippets as refs.\n")
	builder.WriteString("- Do not attach review_result refs unless this task explicitly says you are the independent reviewer. Implementation workers must not self-review.\n")
	builder.WriteString("- For confirmed bug fixes, add a focused regression test when feasible. If a regression test is not feasible in the task scope, record the concrete reason in the task outcome.\n")
	builder.WriteString("- Before exiting successfully on non-governed tasks, record governed system closeout: attach bounded evidence and verifier refs, then move this Work Task out of in_progress, normally to needs_review. For governed wrapper tasks, return the required JSON object instead; the runner owns lifecycle mutation.\n")
	builder.WriteString("- If you confirm a real bug and the task asks for automatic remediation, call projects.automations.create_remediation_from_finding with finding_status=confirmed and activate_plan=true. Do not call projects.automations.run.\n")
	builder.WriteString("- If no bug is confirmed, attach a no-confirmed-bug evidence ref and move this Work Task to needs_review with that outcome.\n")
	builder.WriteString("- If native MCP tools are unavailable and the Mivia server URL is present, use direct HTTP REST against that exact runtime URL only when this task is not a governed wrapper task. Do not hard-code hostnames or ports and do not depend on Codex MCP harness configuration.\n")
	for _, instruction := range input.RunnerInstructions {
		writePromptLine(&builder, "-", instruction)
	}
	builder.WriteString("- Return a concise summary of changed files, evidence, risks, and tests not run.\n")
	return builder.String()
}

func writeConcreteRESTCloseoutEndpoints(builder *strings.Builder, input CodexTaskInput) {
	baseURL := strings.TrimRight(strings.TrimSpace(input.MCPServerURL), "/")
	projectID := strings.TrimSpace(input.ProjectID)
	planID := strings.TrimSpace(input.PlanID)
	taskID := strings.TrimSpace(input.TaskID)
	if baseURL == "" || projectID == "" || taskID == "" {
		return
	}
	builder.WriteString("\nConcrete REST closeout endpoints:\n")
	if planID != "" {
		writePromptLine(builder, "- Create child Work Task", baseURL+"/api/v1/projects/"+projectID+"/work-plans/"+planID+"/tasks")
	}
	writePromptLine(builder, "- Attach wrapper evidence", baseURL+"/api/v1/projects/"+projectID+"/work-tasks/"+taskID+"/evidence")
	writePromptLine(builder, "- Move wrapper status", baseURL+"/api/v1/projects/"+projectID+"/work-tasks/"+taskID+"/status")
	writePromptLine(builder, "- Block wrapper", baseURL+"/api/v1/projects/"+projectID+"/work-tasks/"+taskID+"/block")
	writePromptLine(builder, "- Fail wrapper", baseURL+"/api/v1/projects/"+projectID+"/work-tasks/"+taskID+"/fail")
	writePromptLine(builder, "- Read wrapper back", baseURL+"/api/v1/projects/"+projectID+"/work-tasks/"+taskID)
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

func (svc *Service) codexInputForClaim(ctx context.Context, run AutomationRun, task projectworkplan.WorkTask) CodexTaskInput {
	input := codexInputForRun(run, task)
	input.WorkPlanContext = svc.workPlanContextLines(ctx, task)
	input.DependencyContext = svc.dependencyContextLines(ctx, task)
	return input
}

func (svc *Service) workPlanContextLines(ctx context.Context, task projectworkplan.WorkTask) []string {
	if svc == nil || svc.workTasks == nil || strings.TrimSpace(task.ProjectID) == "" || strings.TrimSpace(task.PlanID) == "" {
		return nil
	}
	lister, ok := svc.workTasks.(workPlanLister)
	if !ok || lister == nil {
		return nil
	}
	plans, err := lister.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: task.ProjectID})
	if err != nil {
		return nil
	}
	for _, plan := range plans {
		if plan.ID != task.PlanID {
			continue
		}
		parts := make([]string, 0, 8)
		appendPromptField(&parts, "plan_ref", plan.PlanRef)
		appendPromptField(&parts, "user_request_ref", plan.UserRequestRef)
		appendPromptField(&parts, "title", plan.Title)
		appendPromptField(&parts, "goal_summary", plan.GoalSummary)
		appendPromptField(&parts, "resume_summary", plan.ResumeSummary)
		appendPromptField(&parts, "git_base_ref", plan.GitBaseRef)
		appendPromptField(&parts, "git_branch_ref", plan.GitBranchRef)
		if len(parts) == 0 {
			return nil
		}
		return []string{strings.Join(parts, "; ")}
	}
	return nil
}

func (svc *Service) dependencyContextLines(ctx context.Context, task projectworkplan.WorkTask) []string {
	if svc == nil || svc.workTasks == nil || strings.TrimSpace(task.ProjectID) == "" || len(task.DependencyTaskIDs) == 0 {
		return nil
	}
	lines := make([]string, 0, len(task.DependencyTaskIDs))
	for _, dependencyID := range task.DependencyTaskIDs {
		dependencyID = strings.TrimSpace(dependencyID)
		if dependencyID == "" {
			continue
		}
		dependency, err := svc.workTasks.GetWorkTask(ctx, task.ProjectID, dependencyID)
		if err != nil {
			continue
		}
		parts := make([]string, 0, 12)
		appendPromptField(&parts, "task_ref", dependency.TaskRef)
		appendPromptField(&parts, "task_id", dependency.ID)
		appendPromptField(&parts, "status", dependency.Status)
		appendPromptField(&parts, "outcome", dependency.Outcome)
		appendPromptRefs(&parts, "context_pack_refs", dependency.ContextPackRefs)
		appendPromptRefs(&parts, "evidence_refs", dependency.EvidenceRefs)
		appendPromptRefs(&parts, "claim_refs", dependency.ClaimRefs)
		appendPromptRefs(&parts, "verifier_result_refs", dependency.VerifierResultRefs)
		appendPromptRefs(&parts, "review_result_refs", dependency.ReviewResultRefs)
		appendPromptField(&parts, "review_exempt_reason", dependency.ReviewExemptReason)
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, "; "))
		}
	}
	return lines
}

func appendPromptField(parts *[]string, name string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*parts = append(*parts, name+"="+value)
}

func appendPromptRefs(parts *[]string, name string, refs []string) {
	refs = uniqueRefs(refs)
	if len(refs) == 0 {
		return
	}
	*parts = append(*parts, name+"=["+strings.Join(refs, ", ")+"]")
}

func codexInputForRun(run AutomationRun, task projectworkplan.WorkTask) CodexTaskInput {
	instructions := []string{
		"Use the Mivia MCP project id from this input.",
		"Do not store raw prompts, completions, source dumps, raw stderr, secrets, roots, provider payloads, or PII.",
		"Use only the bounded task scope and likely affected files unless current source proves a narrower necessary change.",
		"Do not run verifier commands unless this task explicitly allows worker verification.",
	}
	governedInstructions := governedWorkflowStepInstructions(task.TaskRef)
	if strings.HasPrefix(task.TaskRef, "review-") && len(governedInstructions) == 0 {
		instructions = append(instructions,
			"This is an independent review task. Do not edit implementation files.",
			"Review the implementation task named in the task description or evidence refs.",
			"Attach a review_result_ref to the implementation task before completing this review task.",
		)
	} else if len(governedInstructions) == 0 {
		instructions = append(instructions, "Leave verifier execution and task completion to the orchestrator.")
	}
	instructions = append(instructions, governedInstructions...)
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
		RunnerInstructions:      instructions,
	}
}

func governedWorkflowStepInstructions(taskRef string) []string {
	switch strings.TrimSpace(taskRef) {
	case "decompose-work-plan":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must produce concrete child Work Task metadata for the requested ticket in the final child_tasks JSON array. Do not exit successfully with only metadata on this wrapper task.",
			"Each generated child Work Task must be implementation-ready: one objective, files_to_read, files_to_edit or explicit discovery scope, downstream impact, regression-test applicability, acceptance criteria, stop conditions, verifier ladder, dependencies, and resume instructions.",
			"If no concrete implementation task can be derived from the ticket and source evidence, block this task with the exact missing evidence instead of marking it ready or done.",
		})
	case "mark-ready-after-review":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must inspect child Work Tasks from decomposition and return needs_review only when reviewed implementation-ready child tasks exist. Do not exit successfully if no child implementation Work Tasks exist.",
			"If no child implementation task is ready, block this task and state whether decomposition produced no tasks, review refs are missing, or task metadata is incomplete.",
		})
	case "select-ready-tasks":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must select actual ready child implementation Work Tasks from the previous decomposition output. Do not exit successfully if the only ready tasks are workflow wrapper tasks.",
			"If no concrete child implementation task is selected, block this task with a missing-ready-implementation-task reason.",
		})
	case "run-implementation-batch":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must dispatch or execute concrete selected child implementation tasks. Do not exit successfully with no repository diff, no child task evidence, and no explicit blocked reason.",
			"If selected implementation tasks cannot be claimed or executed, block this task and attach the selected task refs and blocker category.",
		})
	case "review-implementation-batch":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must independently review concrete child implementation task outputs. Do not exit successfully if there are no child implementation tasks, no implementation evidence refs, or no diff/branch evidence to review.",
			"If implementation output is missing or unreviewable, block this task with missing-implementation-output instead of approving the batch.",
		})
	case "orchestrator-verification":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must verify concrete implementation outputs against the ticket, downstream impact map, regression-test decision, and verifier ladder. Do not exit successfully if there are no implementation outputs to verify.",
			"If verification cannot run or required implementation evidence is missing, block this task with missing-verification-evidence or verifier-unavailable.",
		})
	case "pr-gitops-readiness":
		return governedWorkflowCloseoutInstructions([]string{
			"This governance step must not approve GitOps readiness when there is no implementation diff and no existing branch/PR evidence for the ticket.",
			"If no diff exists after implementation, block this task with no-implementation-diff instead of approving PR readiness.",
		})
	case "collect-final-scope":
		return governedWorkflowCloseoutInstructions([]string{
			"This post-validation step must collect bounded final scope refs: changed files, implementation task refs, downstream impact refs, regression-test refs, generated artifact refs, and local agentic evidence.",
			"If final scope evidence is missing, stale, too broad, or contains unreviewed changes, block with the exact missing final-scope ref.",
		})
	case "validate-regression-and-downstream":
		return governedWorkflowCloseoutInstructions([]string{
			"This post-validation step must independently validate regression-test evidence, downstream reachability, affected targets, contracts, generated artifacts, and sensitive-risk negative coverage.",
			"If validation evidence is missing or not feasible, block with the exact missing regression or downstream ref.",
		})
	case "run-final-verification":
		return governedWorkflowCloseoutInstructions([]string{
			"This post-validation step must verify concrete implementation outputs with feasible regression checks first, then affected lint/typecheck/test/policy/generated-artifact/diff checks.",
			"If a required verifier cannot run or fails, block with the first failed verifier ref.",
		})
	case "final-pr-readiness":
		return governedWorkflowCloseoutInstructions([]string{
			"This post-validation step must approve GitOps only after validation review refs, verifier refs, regression evidence, generated artifact checks, branch policy, PR metadata, and metadata redaction checks are complete.",
			"If GitOps readiness evidence is incomplete or unsafe, block with the exact missing readiness ref.",
		})
	case "smoke-draft-pr":
		return governedWorkflowCloseoutInstructions([]string{
			"This smoke GitOps step must create or update only the bounded smoke marker file requested by the task, then return governed closeout JSON so the runner can exercise commit, push, and draft PR creation.",
			"If the bounded smoke marker cannot be created safely, block with smoke-marker-unavailable.",
		})
	default:
		return nil
	}
}

func governedWorkflowCloseoutInstructions(stepInstructions []string) []string {
	return append(stepInstructions,
		"For this governed workflow wrapper, do not call MCP or REST for Work Task lifecycle closeout. The runner owns governed state transitions.",
		"Your final response must be a single JSON object. Do not wrap it in prose. Required top-level fields: closeout_action, outcome, safe_next_action, evidence_refs, verifier_result_refs, child_tasks, block_reason, failure_reason.",
		"closeout_action must be one of needs_review, block, or fail. Use needs_review only when the requested wrapper output is complete. Use block when required source or ticket evidence is missing. Use fail only for terminal execution failure.",
		"child_tasks must contain concrete implementation Work Tasks when this wrapper decomposes or selects implementation work. Each child task must include task_ref, title, description, status, owner_agent, evidence_needed, context_pack_refs, files_to_read or explicit discovery scope, files_to_edit when known, likely_files_affected, dependency_task_ids, verification_requirement, expected_output, failure_criteria, review_gate, resume_instructions, decomposition_quality, acceptance_criteria, stop_conditions, verifier_ladder, regression_test_applicability, downstream_impact_refs, and output_contract. Use empty arrays or empty strings only when a field is genuinely not applicable; runner validation still rejects missing required safe metadata.",
		"Use only bounded safe metadata in JSON: no raw source, raw logs, absolute paths, roots, secrets, external URLs, provider payloads, or PII.",
		"The runner will validate this JSON, create child tasks, attach evidence, and move/block/fail this wrapper task. If the JSON is missing or invalid, the automation fails deterministically.",
	)
}

func isGovernedWorkflowTaskRef(taskRef string) bool {
	switch strings.TrimSpace(taskRef) {
	case "decompose-work-plan",
		"mark-ready-after-review",
		"select-ready-tasks",
		"run-implementation-batch",
		"review-implementation-batch",
		"orchestrator-verification",
		"pr-gitops-readiness",
		"collect-final-scope",
		"validate-regression-and-downstream",
		"run-final-verification",
		"final-pr-readiness",
		"smoke-draft-pr":
		return true
	default:
		return false
	}
}

func safeWorkerEvidenceRefs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		ref := safeDisplayRef(value)
		if ref == "" {
			continue
		}
		if len(ref) > 160 {
			ref = strings.Trim(ref[:160], "-")
		}
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func independentReviewerAgent(implementationAgentID string) string {
	switch strings.TrimSpace(implementationAgentID) {
	case "", "codex-reviewer":
		return "codex-independent-reviewer"
	default:
		return "codex-reviewer"
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

func automationMaxRetries(agents []AutomationAgent, agentID string) int {
	for _, agent := range agents {
		if agent.ID == agentID {
			if agent.MaxRetries > 0 {
				return agent.MaxRetries
			}
			return defaultAutomationMaxRetries
		}
	}
	return defaultAutomationMaxRetries
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
		if !svc.isAutomationReviewTask(automation, task.ID) && !svc.isAutomationReviewTask(automation, task.TaskRef) {
			if err := validateAllowedTaskRef(automation, task); err != nil {
				return projectworkplan.WorkTask{}, err
			}
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

func (svc *Service) validateAutomationPolicy(ctx context.Context, automation Automation, runnerKind string, taskID string, effectiveAgentID string) error {
	if strings.TrimSpace(effectiveAgentID) == "" {
		effectiveAgentID = automation.AgentID
	}
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
			AgentID:         effectiveAgentID,
			PermissionRef:   automation.PermissionRef,
			RunnerKind:      runnerKind,
			RunnerExecution: svc.options.RunnerExecution,
		})
		if err != nil {
			return err
		}
		if metadata.AgentID != effectiveAgentID {
			return fmt.Errorf("%w: permission_agent_mismatch", ErrInvalidInput)
		}
		if !permissionAllowsRunner(metadata, runnerKind) {
			return fmt.Errorf("%w: permission_runner_denied", ErrInvalidInput)
		}
	}
	if taskID != "" && len(automation.AllowedTaskRefs) > 0 && svc.workTasks != nil && !svc.isAutomationReviewTask(automation, taskID) {
		task, err := svc.workTasks.GetWorkTask(ctx, automation.ProjectID, taskID)
		if err != nil {
			return fmt.Errorf("%w: task_unavailable", ErrInvalidInput)
		}
		if !svc.isAutomationReviewTask(automation, task.ID) && !svc.isAutomationReviewTask(automation, task.TaskRef) {
			if err := validateAllowedTaskRef(automation, task); err != nil {
				return err
			}
		}
	}
	return nil
}

func (svc *Service) ensureRequiredAutomationReviewRuns(ctx context.Context, automation Automation, runnerKind string, input SubmitRunInput) error {
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
		if task.Status == projectworkplan.WorkTaskStatusDone {
			continue
		}
		if task.Status == projectworkplan.WorkTaskStatusPlanned {
			updater, ok := svc.workTasks.(workTaskStatusUpdater)
			if !ok {
				return fmt.Errorf("%w: automation_review_gate_open", ErrInvalidInput)
			}
			updated, err := updater.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
				WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
					ProjectID:      automation.ProjectID,
					TaskID:         task.ID,
					SafeNextAction: "automation_review_gate",
					RunID:          input.OrchestratorRunID,
					TraceID:        input.OrchestratorRunID,
				},
				Status: projectworkplan.WorkTaskStatusReady,
			})
			if err != nil {
				return err
			}
			task = updated
		}
		if task.Status != projectworkplan.WorkTaskStatusReady {
			continue
		}
		if err := svc.validateRunPlanExecutable(ctx, AutomationRun{ProjectID: automation.ProjectID, PlanID: automation.PlanID}, task); err != nil {
			return err
		}
		reviewAutomation, err := svc.reviewAutomationForTask(ctx, automation, task)
		if err != nil {
			return err
		}
		existing, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: reviewAutomation.ID, PlanID: automation.PlanID})
		if err != nil {
			return err
		}
		alreadyQueued := false
		for _, run := range existing {
			if run.TaskID == task.ID && (run.Status == RunStatusQueued || run.Status == RunStatusClaiming || run.Status == RunStatusStarting || run.Status == RunStatusRunning || run.Status == RunStatusVerifying) {
				alreadyQueued = true
				break
			}
		}
		if alreadyQueued {
			continue
		}
		now := svc.now()
		reviewRun := AutomationRun{
			ID: svc.newID("automation_run"), ProjectID: automation.ProjectID, AutomationID: reviewAutomation.ID,
			AgentID: firstNonEmpty(task.OwnerAgent, reviewAutomation.AgentID), PlanID: task.PlanID, TaskID: task.ID,
			Status: RunStatusQueued, RunnerKind: runnerKind, AttemptCount: 0, OrchestratorRunID: input.OrchestratorRunID,
			ParentRunID: input.ParentRunID, SafeSummary: "automation_review_gate_queued", CreatedAt: now, UpdatedAt: now,
		}
		if _, err := svc.store.CreateRun(ctx, reviewRun); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) reviewAutomationForTask(ctx context.Context, fallback Automation, task projectworkplan.WorkTask) (Automation, error) {
	if strings.TrimSpace(task.ID) == "" {
		return fallback, nil
	}
	automations, err := svc.store.ListAutomations(ctx, AutomationFilter{ProjectID: fallback.ProjectID, Status: AutomationStatusEnabled})
	if err != nil {
		return Automation{}, err
	}
	for _, candidate := range automations {
		if candidate.ID == fallback.ID || candidate.PlanID != fallback.PlanID {
			continue
		}
		if automationAllowsTask(candidate, task) {
			return candidate, nil
		}
	}
	return fallback, nil
}

func automationAllowsTask(automation Automation, task projectworkplan.WorkTask) bool {
	for _, allowed := range automation.AllowedTaskRefs {
		switch strings.TrimSpace(allowed) {
		case task.ID, task.TaskRef:
			return true
		}
	}
	return false
}

func (svc *Service) requiredAutomationReviewsDone(ctx context.Context, automation Automation) bool {
	if len(automation.RequiredReviewTaskIDs) == 0 {
		return true
	}
	if svc.workTasks == nil {
		return false
	}
	for _, taskID := range automation.RequiredReviewTaskIDs {
		task, err := svc.workTasks.GetWorkTask(ctx, automation.ProjectID, taskID)
		if err != nil || task.Status != projectworkplan.WorkTaskStatusDone {
			return false
		}
	}
	return true
}

func (svc *Service) isAutomationReviewTask(automation Automation, taskIDOrRef string) bool {
	if taskIDOrRef == "" {
		return false
	}
	for _, reviewTaskID := range automation.RequiredReviewTaskIDs {
		if reviewTaskID == taskIDOrRef {
			return true
		}
	}
	return false
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
	if implementationTaskRequiresGovernanceContract(task) {
		if len(task.AcceptanceCriteria) == 0 {
			return fmt.Errorf("%w: missing_acceptance_criteria", ErrInvalidInput)
		}
		if len(task.StopConditions) == 0 {
			return fmt.Errorf("%w: missing_stop_conditions", ErrInvalidInput)
		}
		if len(task.VerifierLadder) == 0 {
			return fmt.Errorf("%w: missing_verifier_ladder", ErrInvalidInput)
		}
		if strings.TrimSpace(task.RegressionApplicability) == "" {
			return fmt.Errorf("%w: missing_regression_test_applicability", ErrInvalidInput)
		}
		if len(task.DownstreamImpactRefs) == 0 {
			return fmt.Errorf("%w: missing_downstream_impact_refs", ErrInvalidInput)
		}
		if strings.TrimSpace(task.OutputContract) == "" {
			return fmt.Errorf("%w: missing_output_contract", ErrInvalidInput)
		}
	}
	return nil
}

func implementationTaskRequiresGovernanceContract(task projectworkplan.WorkTask) bool {
	if isGovernedWorkflowTaskRef(task.TaskRef) || isReviewTask(task) || isReadOnlyScannerTask(task) || len(task.FilesToEdit) == 0 {
		return false
	}
	owner := strings.ToLower(strings.TrimSpace(task.OwnerAgent))
	return strings.Contains(owner, "implementation")
}

func isRecoverableGitOpsPostTaskFailure(category string) bool {
	switch strings.TrimSpace(category) {
	case "gitops_post_task_failed", "gitops_branch_policy_failed", "gitops_command_failed", "gitops_invalid_input", "gitops_verification_failed", "gitops_dirty_worktree_scope":
		return true
	default:
		return false
	}
}

func isRecoverablePreExecutionFailure(category string) bool {
	switch strings.TrimSpace(category) {
	case "worktree_resolve_failed", "worktree_prepare_failed", "claim_failed", "start_failed", "gitops_pre_task_failed", "gitops_dirty_worktree", "gitops_dirty_worktree_scope":
		return true
	default:
		return false
	}
}

func preExecutionRecoveryTaskMatchesRun(task projectworkplan.WorkTask, run AutomationRun) bool {
	if claimedBy := strings.TrimSpace(task.ClaimedByRunID); claimedBy != "" && claimedBy != run.ID {
		return false
	}
	if validateExecutableTask(task) == nil {
		return true
	}
	switch task.Status {
	case projectworkplan.WorkTaskStatusClaimed, projectworkplan.WorkTaskStatusInProgress:
		return strings.TrimSpace(task.ClaimedByRunID) == run.ID && task.DecompositionQuality == projectworkplan.DecompositionReady && strings.TrimSpace(task.VerificationRequirement) != ""
	default:
		return false
	}
}

func isRecoverableReviewGitOpsFailure(category string) bool {
	switch strings.TrimSpace(category) {
	case "gitops_dirty_worktree", "gitops_dirty_worktree_scope", "gitops_pre_task_failed":
		return true
	default:
		return false
	}
}

func taskHasGitOpsRecoveryCloseout(task projectworkplan.WorkTask) bool {
	switch task.Status {
	case projectworkplan.WorkTaskStatusNeedsReview, projectworkplan.WorkTaskStatusVerifying, projectworkplan.WorkTaskStatusDone:
	default:
		return false
	}
	return len(task.EvidenceRefs) > 0 || len(task.ClaimRefs) > 0 || len(task.VerifierResultRefs) > 0 || len(task.ReviewResultRefs) > 0
}

func taskNeedsGitOpsPostTaskRecovery(run AutomationRun, task projectworkplan.WorkTask) bool {
	if run.Status != RunStatusVerifying || strings.TrimSpace(run.FailureCategory) != "" {
		return false
	}
	if isReadOnlyScannerTask(task) || isReviewTask(task) || len(task.FilesToEdit) == 0 {
		return false
	}
	if task.Status != projectworkplan.WorkTaskStatusNeedsReview && task.Status != projectworkplan.WorkTaskStatusVerifying {
		return false
	}
	if len(task.ReviewResultRefs) == 0 && strings.TrimSpace(task.ReviewExemptReason) == "" {
		return false
	}
	for _, ref := range append(append([]string{}, task.EvidenceRefs...), task.ClaimRefs...) {
		normalized := strings.ToLower(strings.TrimSpace(ref))
		if strings.HasPrefix(normalized, "gitops-commit") ||
			strings.HasPrefix(normalized, "gitops-push") ||
			strings.HasPrefix(normalized, "gitops-pr") ||
			strings.HasPrefix(normalized, "git-commit") ||
			strings.HasPrefix(normalized, "git-push") ||
			strings.HasPrefix(normalized, "draft-pr") ||
			strings.Contains(normalized, "git-no-changes") ||
			strings.Contains(normalized, "draft-pr-ready") {
			return false
		}
	}
	return true
}

func taskReadyForAutomationCloseout(task projectworkplan.WorkTask) bool {
	switch task.Status {
	case projectworkplan.WorkTaskStatusNeedsReview, projectworkplan.WorkTaskStatusVerifying:
	default:
		return false
	}
	if isReadOnlyScannerTask(task) && readOnlyScannerTaskHasCloseoutOutput(task) {
		return true
	}
	if !isReadOnlyScannerTask(task) && len(task.FilesToEdit) == 0 && metadataOnlyTaskHasCloseoutEvidence(task) {
		return true
	}
	if len(automationCloseoutVerifierRefs(task)) == 0 {
		return false
	}
	return len(task.ReviewResultRefs) > 0 || strings.TrimSpace(task.ReviewExemptReason) != "" || automationReviewExemptReason(task) != ""
}

func isReviewTask(task projectworkplan.WorkTask) bool {
	return strings.HasPrefix(strings.TrimSpace(task.TaskRef), "review-") || strings.Contains(strings.ToLower(task.ReviewGate), "independent-reviewer")
}

func isReadOnlyScannerTask(task projectworkplan.WorkTask) bool {
	if len(task.FilesToEdit) > 0 {
		return false
	}
	ref := strings.ToLower(strings.TrimSpace(task.TaskRef))
	owner := strings.ToLower(strings.TrimSpace(task.OwnerAgent))
	return owner == "code-review-scanner" || strings.HasPrefix(ref, "scan-") || strings.HasPrefix(ref, "collect-review-scope") || strings.HasPrefix(ref, "research-")
}

func automationReviewExemptReason(task projectworkplan.WorkTask) string {
	switch {
	case isReviewTask(task):
		return "independent review task; secondary review not required"
	case isReadOnlyScannerTask(task):
		return "read-only automation task; downstream review remains dependency-gated"
	case isNoConfirmedBugPlannerTask(task):
		return "no confirmed bug remediation planner; upstream independent review found no bug"
	case len(task.FilesToEdit) == 0 && metadataOnlyTaskHasCloseoutEvidence(task):
		return "metadata-only automation task; no repository writes require secondary review"
	default:
		return ""
	}
}

func metadataOnlyTaskHasCloseoutEvidence(task projectworkplan.WorkTask) bool {
	return len(task.EvidenceRefs) > 0 ||
		len(task.ClaimRefs) > 0 ||
		len(task.ArtifactRefs) > 0 ||
		len(task.KnowledgeCandidateRefs) > 0 ||
		strings.TrimSpace(task.Outcome) != ""
}

func readOnlyScannerTaskHasCloseoutOutput(task projectworkplan.WorkTask) bool {
	if len(task.VerifierResultRefs) > 0 ||
		len(task.ClaimRefs) > 0 ||
		len(task.ArtifactRefs) > 0 ||
		len(task.KnowledgeCandidateRefs) > 0 ||
		strings.TrimSpace(task.Outcome) != "" {
		return true
	}
	for _, ref := range task.EvidenceRefs {
		if isMeaningfulScannerEvidenceRef(ref) {
			return true
		}
	}
	return false
}

func automationCloseoutVerifierRefs(task projectworkplan.WorkTask) []string {
	refs := append([]string(nil), task.VerifierResultRefs...)
	if len(refs) == 0 && isReadOnlyScannerTask(task) && readOnlyScannerTaskHasCloseoutOutput(task) {
		refs = append(refs, "verifier.automation.read-only-scanner-output")
	}
	if len(refs) == 0 && !isReadOnlyScannerTask(task) && len(task.FilesToEdit) == 0 && metadataOnlyTaskHasCloseoutEvidence(task) {
		refs = append(refs, "verifier.automation.metadata-only-output")
	}
	if len(refs) == 0 && len(task.FilesToEdit) > 0 && gitOpsTaskHasCloseoutEvidence(task) {
		refs = append(refs, "verifier.automation.gitops-output")
	}
	return refs
}

func gitOpsTaskHasCloseoutEvidence(task projectworkplan.WorkTask) bool {
	for _, ref := range task.EvidenceRefs {
		value := strings.ToLower(strings.TrimSpace(ref))
		if strings.HasPrefix(value, "gitops-commit") ||
			strings.HasPrefix(value, "gitops-push") ||
			strings.HasPrefix(value, "gitops-pr") ||
			strings.HasPrefix(value, "git-commit") ||
			strings.HasPrefix(value, "git-push") ||
			strings.HasPrefix(value, "draft-pr") ||
			strings.Contains(value, "draft-pr-ready") {
			return true
		}
	}
	return false
}

func isMeaningfulScannerEvidenceRef(ref string) bool {
	value := strings.ToLower(strings.TrimSpace(ref))
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "automation_run") ||
		strings.Contains(value, "automation-run") ||
		strings.Contains(value, "action-start") ||
		strings.Contains(value, "run-start") {
		return false
	}
	return true
}

func automationCloseoutOutcome(task projectworkplan.WorkTask) string {
	if isNoConfirmedBugPlannerTask(task) && strings.TrimSpace(task.Outcome) != "" {
		return task.Outcome
	}
	if isReviewTask(task) {
		return "automation closeout completed independent review task; no secondary review required"
	}
	if isReadOnlyScannerTask(task) && len(task.ReviewResultRefs) == 0 && strings.TrimSpace(task.ReviewExemptReason) == "" {
		return "automation closeout completed read-only task after verifier refs; downstream review remains gated"
	}
	return "automation closeout completed after required verifier and independent review refs were attached"
}

func isNoConfirmedBugPlannerTask(task projectworkplan.WorkTask) bool {
	if !isRemediationPlanningTask(task) || taskHasConfirmedFinding(task) {
		return false
	}
	text := strings.ToLower(strings.Join(append(append([]string{task.Outcome, task.ResumeInstructions}, task.EvidenceRefs...), task.ClaimRefs...), " "))
	return refIndicatesNoConfirmedBug(text)
}

func refIndicatesNoConfirmedBug(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "no confirmed") ||
		strings.Contains(value, "confirmed-bug-refs-none") ||
		strings.Contains(value, "confirmed-bug_refs-none") ||
		strings.Contains(value, "confirmed_bug_refs_none") ||
		strings.Contains(value, "no-confirmed-bug") ||
		strings.Contains(value, "no-confirmed-bugs") ||
		strings.Contains(value, "no_confirmed_bug") ||
		strings.Contains(value, "no_confirmed_bugs") ||
		strings.Contains(value, "no-remediation-work-plan") ||
		strings.Contains(value, "no-remediation-work-plans")
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

func safeFileList(values []string, field string) ([]string, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("%w: %s has too many values", ErrInvalidInput, field)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 300 || strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, "\x00\r\n\\") {
			return nil, fmt.Errorf("%w: %s contains unsafe path", ErrInvalidInput, field)
		}
		out = append(out, trimmed)
	}
	return out, nil
}

func remediationEditScope(filesToEdit []string, likelyFiles []string) []string {
	return uniqueRefs(append(append([]string{}, filesToEdit...), likelyFiles...))
}

func dirtyPathsFromEvidenceRefs(refs []string) []string {
	const prefix = "gitops-dirty-path:"
	out := make([]string, 0)
	for _, ref := range refs {
		path := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(ref, prefix)))
		if path == ref || !isSafeTaskPath(path) {
			continue
		}
		out = append(out, path)
		if len(out) >= 20 {
			break
		}
	}
	return uniqueRefs(out)
}

func (svc *Service) classifyDirtyScopePaths(projectID string, task projectworkplan.WorkTask, dirtyPaths []string) ([]string, []string) {
	likely := safeTaskPathspecs(task.LikelyFilesAffected)
	supportScopes := svc.automationSupportPathspecs(projectID)
	if len(likely) == 0 && len(supportScopes) == 0 {
		return nil, dirtyPaths
	}
	current := safeTaskPathspecs(task.FilesToEdit)
	var expand []string
	var outside []string
	for _, path := range dirtyPaths {
		if !isSafeTaskPath(path) {
			outside = append(outside, "unsafe-path")
			continue
		}
		if taskPathMatchesAny(path, current) {
			continue
		}
		scope := firstMatchingTaskScope(path, likely)
		if scope == "" {
			scope = firstMatchingTaskScope(path, supportScopes)
		}
		if scope == "" {
			outside = append(outside, path)
			continue
		}
		expand = append(expand, scope)
	}
	return uniqueRefs(expand), uniqueRefs(outside)
}

func (svc *Service) automationSupportPathspecs(projectID string) []string {
	scopes := append([]string(nil), svc.options.DirtyScopeRecovery.AllowedSupportPathspecs...)
	if svc.options.DirtyScopeRecovery.PathspecResolver != nil {
		scopes = append(scopes, svc.options.DirtyScopeRecovery.PathspecResolver(projectID)...)
	}
	return safeTaskPathspecs(scopes)
}

func safeTaskPathspecs(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		path := filepath.ToSlash(strings.TrimSpace(value))
		if isSafeTaskPath(path) {
			out = append(out, strings.TrimSuffix(path, "/"))
		}
	}
	return uniqueRefs(out)
}

func taskPathMatchesAny(path string, scopes []string) bool {
	for _, scope := range scopes {
		if taskPathMatchesScope(path, scope) {
			return true
		}
	}
	return false
}

func firstMatchingTaskScope(path string, scopes []string) string {
	for _, scope := range scopes {
		if taskPathMatchesScope(path, scope) {
			return scope
		}
	}
	return ""
}

func taskPathMatchesScope(path string, scope string) bool {
	path = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(path)), "/")
	scope = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(scope)), "/")
	return path == scope || strings.HasPrefix(path, scope+"/")
}

func isSafeTaskPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	return path != "" && len(path) <= 300 && !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "\\") && !strings.Contains(path, "..") && !strings.Contains(path, ":") && !strings.ContainsAny(path, "\x00\r\n\\")
}

func safeRequiredText(value, field string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	return safeText(value, field, max)
}

func safeOptionalText(value, field string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
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

func safeBranchToken(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	token := strings.Trim(builder.String(), ".-_")
	if token == "" {
		return "finding"
	}
	if len(token) > 80 {
		return token[:80]
	}
	return token
}

func remediationFindingScopedGitRef(value string, fallbackPrefix string, findingToken string) string {
	value = strings.TrimSpace(value)
	findingToken = safeBranchToken(findingToken)
	if value == "" {
		return fallbackPrefix + findingToken
	}
	if strings.Contains(value, findingToken) {
		return value
	}
	suffix := "-" + findingToken
	maxBase := 200 - len(suffix)
	if maxBase < 1 {
		return findingToken[:minInt(len(findingToken), 200)]
	}
	if len(value) > maxBase {
		value = value[:maxBase]
	}
	value = strings.TrimRight(value, ".:/@+-")
	if value == "" {
		return fallbackPrefix + findingToken
	}
	return value + suffix
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func safeDisplayRef(value string) string {
	token := safeBranchToken(value)
	if token == "" {
		return "finding"
	}
	var builder strings.Builder
	digits := strings.Builder{}
	flushDigits := func() {
		if digits.Len() == 0 {
			return
		}
		if digits.Len() >= 8 {
			builder.WriteString("ref")
		} else {
			builder.WriteString(digits.String())
		}
		digits.Reset()
	}
	for _, r := range token {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		flushDigits()
		builder.WriteRune(r)
	}
	flushDigits()
	out := strings.Trim(builder.String(), ".-_")
	if out == "" {
		return "finding"
	}
	return out
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
