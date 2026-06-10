package projectworkflowchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectgitops"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestValidateConfigRejectsUnsafeAndInvalidChains(t *testing.T) {
	base := testConfig()
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"duplicate stage", func(cfg *Config) { cfg.Stages[1].StageRef = cfg.Stages[0].StageRef }},
		{"cycle", func(cfg *Config) { cfg.Stages[0].DependsOn = []string{"implementation"} }},
		{"unsafe pattern", func(cfg *Config) { cfg.InputPattern = "^GENERIC-.*$" }},
		{"missing post validation", func(cfg *Config) {
			cfg.Stages = cfg.Stages[:2]
			cfg.GitOpsMode = GitOpsModeDraftPRAfterValidation
		}},
		{"gitops disabled", func(cfg *Config) { cfg.GitOpsEnabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.Stages = append([]StageConfig(nil), base.Stages...)
			for i := range cfg.Stages {
				cfg.Stages[i].DependsOn = append([]string(nil), base.Stages[i].DependsOn...)
			}
			tc.mutate(&cfg)
			if err := validateConfig(cfg); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid config, got %v", err)
			}
		})
	}
}

func TestStartDryRunRejectsUnsafeInputAndDoesNotCreateRun(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{testConfig()})

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "somebody@example.com", DryRun: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unsafe input rejection, got %v", err)
	}
	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run start: %v", err)
	}
	if !result.DryRun || result.InputRef != "jira:GENERIC-1044" || len(result.StageRuns) != 3 {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	result, err = svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "jira:GENERIC-1044", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run start with prefixed Jira input: %v", err)
	}
	if !result.DryRun || result.InputRef != "jira:GENERIC-1044" || len(result.StageRuns) != 3 {
		t.Fatalf("unexpected prefixed dry-run result: %#v", result)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("dry run persisted runs: %#v", runs)
	}
}

func TestStartDryRunPreflightsLocalJiraContextBeforePersistence(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("GENERIC-1044", true)})

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run start: %v", err)
	}
	if !containsString(result.ContextRefs, "jira-context:GENERIC-1044:summary") || !containsString(result.ContextRefs, "jira-context:GENERIC-1044:scope") {
		t.Fatalf("dry run missing verified context refs: %#v", result.ContextRefs)
	}
	if !containsString(result.ContextRefs, "jira-context:GENERIC-1044:implementation-evidence") || !containsString(result.ContextRefs, "jira-context:GENERIC-1044:source-anchors") || !containsString(result.ContextRefs, "jira-context:GENERIC-1044:verifier-scope") {
		t.Fatalf("dry run missing implementation context refs: %#v", result.ContextRefs)
	}
	if len(workflows.compileInputs) != 3 {
		t.Fatalf("expected dry run to compile all stages, got %d", len(workflows.compileInputs))
	}
	if !containsString(workflows.compileInputs[0].ContextPackRefs, "jira-context:GENERIC-1044:scope") || !containsString(workflows.compileInputs[0].ContextPackRefs, "jira-context:GENERIC-1044:implementation-evidence") {
		t.Fatalf("compile input missing context refs: %#v", workflows.compileInputs[0])
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("dry run persisted runs: %#v", runs)
	}
}

func TestStartRejectsLocalJiraContextMissingScopeBeforeRunCreation(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("GENERIC-1044", false)})

	_, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "description_or_acceptance_criteria") {
		t.Fatalf("expected missing scope rejection, got %v", err)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("invalid context must not create chain runs: %#v", runs)
	}
	if len(workflows.compileInputs) != 0 {
		t.Fatalf("invalid context must not compile workflows: %#v", workflows.compileInputs)
	}
}

func TestStartRejectsLocalJiraContextMissingImplementationEvidenceBeforeRunCreation(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContextWithoutImplementationEvidence("GENERIC-1044")})

	_, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "implementation_evidence") {
		t.Fatalf("expected missing implementation evidence rejection, got %v", err)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("invalid context must not create chain runs: %#v", runs)
	}
	if len(workflows.compileInputs) != 0 {
		t.Fatalf("invalid context must not compile workflows: %#v", workflows.compileInputs)
	}
}

func TestStartCreatesFirstStageAndAdvancesAfterPlanDone(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Status != ChainStatusQueued || len(result.WorkPlanIDs) != 1 || result.StageRuns[0].Status != StageStatusQueued {
		t.Fatalf("unexpected start result: %#v", result)
	}
	if len(workPlans.activations) != 1 || workPlans.activations[0] != "plan-decomposition" {
		t.Fatalf("expected first plan activation, got %#v", workPlans.activations)
	}
	if len(workPlans.released) != 1 || workPlans.released[0] != "task-decomposition" {
		t.Fatalf("expected first stage task release, got %#v", workPlans.released)
	}
	if got, want := strings.Join(workPlans.events[:2], ","), "release:task-decomposition,activate:plan-decomposition"; got != want {
		t.Fatalf("stage activation must release tasks before plan active event, got %s", got)
	}
	if len(workflows.compileInputs) != 1 {
		t.Fatalf("expected first-stage compile input, got %#v", workflows.compileInputs)
	}
	firstCompile := workflows.compileInputs[0]
	if firstCompile.UserRequestRef != "jira:GENERIC-1044" || firstCompile.CreatedByRunID != "run-1" || firstCompile.TraceID != "" {
		t.Fatalf("first-stage compile must preserve input and caller refs, got %#v", firstCompile)
	}
	if !containsString(firstCompile.ContextPackRefs, "jira:GENERIC-1044") {
		t.Fatalf("first-stage compile missing input context ref, got %#v", firstCompile.ContextPackRefs)
	}
	if !strings.Contains(firstCompile.TitleOverride, "jira:GENERIC-1044") || !strings.Contains(firstCompile.TitleOverride, "decomposition") {
		t.Fatalf("first-stage compile title must include input and stage, got %q", firstCompile.TitleOverride)
	}

	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err != nil {
		t.Fatalf("advance implementation: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.StageRuns[0].Status != StageStatusCompleted || run.StageRuns[1].Status != StageStatusQueued || len(run.WorkPlanIDs) != 2 {
		t.Fatalf("expected implementation queued after decomposition done: %#v", run)
	}
	if workPlans.activations[1] != "plan-implementation" {
		t.Fatalf("expected implementation plan activation, got %#v", workPlans.activations)
	}
	if got, want := strings.Join(workPlans.events[2:4], ","), "release:task-implementation,activate:plan-implementation"; got != want {
		t.Fatalf("next stage activation must release tasks before plan active event, got %s", got)
	}
	if len(workflows.compileInputs) != 2 {
		t.Fatalf("expected implementation compile input, got %#v", workflows.compileInputs)
	}
	nextCompile := workflows.compileInputs[1]
	if nextCompile.UserRequestRef != "jira:GENERIC-1044" || nextCompile.CreatedByRunID != "run-1" {
		t.Fatalf("next-stage compile must preserve input and creator run refs, got %#v", nextCompile)
	}
	if !containsString(nextCompile.ContextPackRefs, "jira:GENERIC-1044") || !strings.Contains(nextCompile.TitleOverride, "implementation") {
		t.Fatalf("next-stage compile must preserve context and stage title, got %#v", nextCompile)
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-implementation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance post-validation: %v", err)
	}
	run, err = svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run after implementation: %v", err)
	}
	if run.StageRuns[1].Status != StageStatusCompleted || run.StageRuns[2].Status != StageStatusQueued || len(run.WorkPlanIDs) != 3 {
		t.Fatalf("expected post-validation queued after implementation done: %#v", run)
	}
	if workPlans.activations[2] != "plan-post-validation" {
		t.Fatalf("expected post-validation plan activation, got %#v", workPlans.activations)
	}
	if got, want := strings.Join(workPlans.events[4:6], ","), "release:task-post-validation,activate:plan-post-validation"; got != want {
		t.Fatalf("post-validation activation must release tasks before plan active event, got %s", got)
	}
}

func TestAdvancingStageCarriesGeneratedImplementationTasksToNextPlan(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	generated := projectworkplan.WorkTask{
		ID:                      "generated-task-1",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "implement-ticket-slice",
		Title:                   "Implement Ticket Slice",
		Description:             "generated implementation packet",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              "developer",
		EvidenceNeeded:          []string{"evidence-ref"},
		ContextPackRefs:         []string{"context-ref"},
		FilesToRead:             []string{"internal/input.go"},
		FilesToEdit:             []string{"internal/output.go"},
		LikelyFilesAffected:     []string{"internal/output.go"},
		VerificationRequirement: "focused verifier",
		ExpectedOutput:          "code diff",
		FailureCriteria:         "stop on scope drift",
		ReviewGate:              "independent-review",
		ResumeInstructions:      "resume from packet",
		EvidenceRefs:            []string{"evidence:planning-output"},
		ClaimRefs:               []string{"claim:planning-worker"},
		VerifierResultRefs:      []string{"verifier:planning-readiness"},
		ReviewResultRefs:        []string{"review:planning-readiness-approved"},
		ReviewExemptReason:      "planning stage exemption must not carry",
		ArtifactRefs:            []string{"artifact:decomposition-packet"},
		AgentRunIDs:             []string{"automation_run_planning"},
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"works"},
		StopConditions:          []string{"blocked"},
		VerifierLadder:          []string{"unit"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"impact-ref"},
		OutputContract:          "diff plus evidence",
	}
	workPlans := &fakeWorkPlans{openTasksByPlan: map[string][]projectworkplan.WorkTask{
		"plan-decomposition": {
			{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "decompose-work-plan", Status: projectworkplan.WorkTaskStatusDone},
			generated,
		},
		"plan-implementation": {
			{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
		},
	}}
	automations := &fakeAutomationAPI{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetAutomationAPI(automations)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(workPlans.createdTasks) != 1 {
		t.Fatalf("expected one carried implementation task, got %#v", workPlans.createdTasks)
	}
	carried := workPlans.createdTasks[0]
	if carried.PlanID != "plan-implementation" || carried.TaskRef != generated.TaskRef || carried.Status != projectworkplan.WorkTaskStatusPlanned || carried.OwnerAgent != "implementation-worker" {
		t.Fatalf("unexpected carried task: %#v", carried)
	}
	if !containsString(carried.FilesToEdit, "internal/output.go") || carried.VerificationRequirement != generated.VerificationRequirement {
		t.Fatalf("carried task lost implementation metadata: %#v", carried)
	}
	if !containsString(carried.AcceptanceCriteria, "works") ||
		!containsString(carried.StopConditions, "blocked") ||
		!containsString(carried.VerifierLadder, "unit") ||
		carried.RegressionApplicability != generated.RegressionApplicability ||
		!containsString(carried.DownstreamImpactRefs, "impact-ref") ||
		carried.OutputContract != generated.OutputContract {
		t.Fatalf("carried task lost executable governance metadata: %#v", carried)
	}
	if len(carried.ReviewResultRefs) != 0 || len(carried.VerifierResultRefs) != 0 || carried.ReviewExemptReason != "" {
		t.Fatalf("carried executable task must not inherit planning review/verifier refs: %#v", carried)
	}
	if !containsString(carried.EvidenceRefs, "evidence:planning-output") || !containsString(carried.EvidenceRefs, "review:planning-readiness-approved") || !containsString(carried.EvidenceRefs, "verifier:planning-readiness") || !containsString(carried.ClaimRefs, "claim:planning-worker") || !containsString(carried.ArtifactRefs, "artifact:decomposition-packet") || !containsString(carried.AgentRunIDs, "automation_run_planning") {
		t.Fatalf("carried task lost planning handoff refs: %#v", carried)
	}
	if len(automations.created) != 1 {
		t.Fatalf("expected carried implementation automation, got %#v", automations.created)
	}
	createdAutomation := automations.created[0]
	if createdAutomation.PlanID != "plan-implementation" || createdAutomation.AgentID != "implementation-worker" || createdAutomation.Status != projectautomation.AutomationStatusEnabled || createdAutomation.TriggerKind != projectautomation.TriggerKindAutomatic || createdAutomation.SchedulePolicy != "on-ready-task" {
		t.Fatalf("carried implementation automation lost live handoff metadata: %#v", createdAutomation)
	}
	if !containsString(createdAutomation.AllowedTaskRefs, "created-"+generated.TaskRef) || !containsString(createdAutomation.AllowedTaskRefs, generated.TaskRef) {
		t.Fatalf("carried implementation automation must allow task id and ref, got %#v", createdAutomation.AllowedTaskRefs)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil || run.StageRuns[1].Status != StageStatusQueued {
		t.Fatalf("expected queued implementation stage, run=%#v err=%v", run, err)
	}
	if !containsString(run.StageRuns[1].WorkTaskIDs, "created-"+generated.TaskRef) || !containsString(run.StageRuns[1].AutomationIDs, createdAutomation.ID) {
		t.Fatalf("implementation stage lost carried task or automation refs: %#v", run.StageRuns[1])
	}
}

func TestAdvancingStageCarriesGeneratedTasksFromCompletedPriorPlan(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	generated := projectworkplan.WorkTask{
		ID:                   "generated-task-1",
		ProjectID:            "project-1",
		PlanID:               "plan-decomposition",
		TaskRef:              "PROJ-1044-expired-booking-query",
		Title:                "Add Expired Booking Selection Query",
		Status:               projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:           "developer",
		FilesToEdit:          []string{"apps/domain-booking/src/infrastructure/database/repositories/booking.repository.ts"},
		ReviewResultRefs:     []string{"review:planning-readiness-approved"},
		VerifierResultRefs:   []string{"verifier:planning-readiness"},
		DecompositionQuality: projectworkplan.DecompositionReady,
	}
	workPlans := &fakeWorkPlans{
		allTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {
				{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "decompose-work-plan", Status: projectworkplan.WorkTaskStatusDone, DecompositionQuality: projectworkplan.DecompositionReady},
				generated,
			},
		},
		openTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {},
			"plan-implementation": {
				{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
			},
		},
	}
	automations := &fakeAutomationAPI{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetAutomationAPI(automations)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	if len(workPlans.createdTasks) != 1 {
		t.Fatalf("expected generated implementation task to be carried from completed prior plan, got %#v", workPlans.createdTasks)
	}
	carried := workPlans.createdTasks[0]
	if carried.PlanID != "plan-implementation" || carried.TaskRef != generated.TaskRef || carried.Status != projectworkplan.WorkTaskStatusPlanned || carried.OwnerAgent != "implementation-worker" {
		t.Fatalf("unexpected carried implementation task: %#v", carried)
	}
	if len(automations.created) != 1 || automations.created[0].PlanID != "plan-implementation" || !containsString(automations.created[0].AllowedTaskRefs, generated.TaskRef) {
		t.Fatalf("expected carried implementation worker automation, got %#v", automations.created)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if run.Status != ChainStatusQueued || chainStageRunByRef(t, run, "implementation").Status != StageStatusQueued {
		t.Fatalf("expected implementation stage queued after carrying generated task, got %#v", run)
	}
}

func TestAdvancingStageBlocksWhenDecompositionProducedNoImplementationChildren(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{
		allTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {
				{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "decompose-work-plan", Status: projectworkplan.WorkTaskStatusDone, DecompositionQuality: projectworkplan.DecompositionReady},
				{ID: "review-decompose-work-plan", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "review-decompose-work-plan-planning-readiness-review", Status: projectworkplan.WorkTaskStatusDone, DecompositionQuality: projectworkplan.DecompositionReady},
			},
		},
		openTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {},
			"plan-implementation": {
				{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
			},
		},
	}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil || !strings.Contains(err.Error(), "missing_carried_implementation_tasks") {
		t.Fatalf("expected explicit missing child handoff error, got %v", err)
	}
	run, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get chain: %v", getErr)
	}
	implementation := chainStageRunByRef(t, run, "implementation")
	if run.Status != ChainStatusBlocked || implementation.Status != StageStatusBlocked || implementation.BlockedCode != BlockedCodeMissingCarriedImplementationTasks || implementation.BlockedReason != "activate_next_stage_failed_missing_carried_implementation_tasks" {
		t.Fatalf("expected chain blocked before implementation selector can run, got %#v", run)
	}
}

func TestAdvancingStageStripsWrapperDependenciesFromCarriedImplementationTasks(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	generated := projectworkplan.WorkTask{
		ID:                      "generated-task-1",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "implement-PROJ-1044-slice",
		Title:                   "Implement PROJ-1044 Slice",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              "planning-worker",
		FilesToEdit:             []string{"internal/output.go"},
		DependencyTaskIDs:       []string{"work_task_selector_wrapper", "review-select-ready-tasks", "select-ready-tasks"},
		VerificationRequirement: "focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		ReviewResultRefs:        []string{"review:implementation-independent-review"},
	}
	workPlans := &fakeWorkPlans{openTasksByPlan: map[string][]projectworkplan.WorkTask{
		"plan-decomposition": {
			{ID: "work_task_selector_wrapper", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusReady, DecompositionQuality: projectworkplan.DecompositionReady},
			{ID: "review-select-ready-tasks", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "review-select-ready-tasks", Status: projectworkplan.WorkTaskStatusReady, DecompositionQuality: projectworkplan.DecompositionReady},
			generated,
		},
		"plan-implementation": {
			{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
		},
	}}
	automations := &fakeAutomationAPI{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetAutomationAPI(automations)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(workPlans.createdTasks) != 1 {
		t.Fatalf("expected one carried implementation task, got %#v", workPlans.createdTasks)
	}
	carried := workPlans.createdTasks[0]
	if carried.TaskRef != generated.TaskRef || carried.Status != projectworkplan.WorkTaskStatusPlanned || carried.OwnerAgent != "implementation-worker" {
		t.Fatalf("carried task must wait for implementation selector release, got %#v", carried)
	}
	if containsString(carried.DependencyTaskIDs, "select-ready-tasks") {
		t.Fatalf("carried implementation task must not depend on source-stage wrapper refs: %#v", carried.DependencyTaskIDs)
	}
	if containsString(carried.DependencyTaskIDs, "work_task_selector_wrapper") || containsString(carried.DependencyTaskIDs, "review-select-ready-tasks") {
		t.Fatalf("carried implementation task must not depend on source-stage wrapper ids: %#v", carried.DependencyTaskIDs)
	}
	if len(automations.created) != 1 || automations.created[0].PlanID != "plan-implementation" || automations.created[0].AgentID != "implementation-worker" {
		t.Fatalf("expected implementation-worker automation for carried task, got %#v", automations.created)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil || run.StageRuns[1].Status != StageStatusQueued {
		t.Fatalf("expected queued implementation stage, run=%#v err=%v", run, err)
	}
}

func TestAdvancingStageBlocksNonReadyCarriedImplementationTask(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	blockedGenerated := projectworkplan.WorkTask{
		ID:                   "generated-task-1",
		ProjectID:            "project-1",
		PlanID:               "plan-decomposition",
		TaskRef:              "implement-PROJ-1044-slice",
		Title:                "Implement PROJ-1044 Slice",
		Status:               projectworkplan.WorkTaskStatusBlocked,
		OwnerAgent:           "planning-worker",
		FilesToEdit:          []string{"internal/output.go"},
		DecompositionQuality: projectworkplan.DecompositionReady,
		ReviewResultRefs:     []string{"review:implementation-independent-review"},
	}
	workPlans := &fakeWorkPlans{openTasksByPlan: map[string][]projectworkplan.WorkTask{
		"plan-decomposition": {
			{ID: "select-ready-tasks", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusReady, DecompositionQuality: projectworkplan.DecompositionReady},
			blockedGenerated,
		},
		"plan-implementation": {
			{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
		},
	}}
	automations := &fakeAutomationAPI{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetAutomationAPI(automations)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil || !strings.Contains(err.Error(), "non_ready_carried_implementation_task_blocked") {
		t.Fatalf("expected non-ready carried task error, got %v", err)
	}
	if len(workPlans.createdTasks) != 0 || len(automations.created) != 0 {
		t.Fatalf("blocked generated task must not be carried or automated, tasks=%#v automations=%#v", workPlans.createdTasks, automations.created)
	}
	run, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get chain: %v", getErr)
	}
	implementation := chainStageRunByRef(t, run, "implementation")
	if run.Status != ChainStatusBlocked || implementation.Status != StageStatusBlocked || implementation.BlockedCode != BlockedCodeNonReadyCarriedImplementationTask || implementation.BlockedReason != "activate_next_stage_failed_non_ready_carried_implementation_task_blocked" {
		t.Fatalf("expected chain blocked before implementation stage, got %#v", run)
	}
}

func TestActivationBlockedCodeUsesStableMachineCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "missing carried implementation tasks",
			err:  fmt.Errorf("%w: missing_carried_implementation_tasks", ErrInvalidInput),
			want: BlockedCodeMissingCarriedImplementationTasks,
		},
		{
			name: "non ready carried implementation task",
			err:  fmt.Errorf("%w: non_ready_carried_implementation_task_blocked", ErrInvalidInput),
			want: BlockedCodeNonReadyCarriedImplementationTask,
		},
		{
			name: "unknown activation error",
			err:  fmt.Errorf("%w: dependency_backend_timeout", ErrInvalidInput),
			want: BlockedCodeActivationFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := activationBlockedCode(tc.err); got != tc.want {
				t.Fatalf("activation blocked code mismatch: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestChainStageOutputTaskRejectsUnreviewedDecompositionChild(t *testing.T) {
	task := projectworkplan.WorkTask{
		ID:                   "generated-task-1",
		TaskRef:              "implement-PROJ-1044-slice",
		Status:               projectworkplan.WorkTaskStatusPlanned,
		FilesToEdit:          []string{"internal/projectworkflowchain/service.go"},
		DecompositionQuality: projectworkplan.DecompositionReady,
	}
	if chainStageOutputTask(task, map[string]struct{}{}, carryForwardOptions{}) {
		t.Fatal("unreviewed decomposition child must not be eligible for implementation carry-forward")
	}
	task.ReviewResultRefs = []string{"review:planning-readiness-approved"}
	if !chainStageOutputTask(task, map[string]struct{}{}, carryForwardOptions{}) {
		t.Fatal("reviewed decomposition child should be eligible for implementation carry-forward")
	}
}

func TestAdvancingStagePreservesExternalDependenciesOnCarriedImplementationTasks(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	generated := projectworkplan.WorkTask{
		ID:                      "generated-task-1",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "implement-external-dependent-slice",
		Title:                   "Implement External Dependent Slice",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		FilesToEdit:             []string{"internal/output.go"},
		DependencyTaskIDs:       []string{"upstream-external-task"},
		VerificationRequirement: "focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		ReviewResultRefs:        []string{"review:planning-readiness-approved"},
	}
	workPlans := &fakeWorkPlans{openTasksByPlan: map[string][]projectworkplan.WorkTask{
		"plan-decomposition": {
			{ID: "select-ready-tasks", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusReady, DecompositionQuality: projectworkplan.DecompositionReady},
			generated,
		},
		"plan-implementation": {
			{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
		},
	}}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(workPlans.createdTasks) != 1 {
		t.Fatalf("expected one carried implementation task, got %#v", workPlans.createdTasks)
	}
	carried := workPlans.createdTasks[0]
	if carried.Status != projectworkplan.WorkTaskStatusPlanned || !containsString(carried.DependencyTaskIDs, "upstream-external-task") {
		t.Fatalf("external dependencies must be preserved and keep task planned, got %#v", carried)
	}
}

func TestAdvancingStageNormalizesCarriedTaskRefDependenciesToCreatedTaskIDs(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	queryTask := projectworkplan.WorkTask{
		ID:                      "source-query-task",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "PROJ-1044-expired-booking-repository-query",
		Title:                   "Add Expired Booking Selection Query",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		FilesToEdit:             []string{"apps/domain-booking/src/repository.ts"},
		VerificationRequirement: "focused repository verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		ReviewResultRefs:        []string{"review:planning-readiness-approved"},
	}
	triggerTask := projectworkplan.WorkTask{
		ID:                      "source-trigger-task",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "PROJ-1044-booking-expiry-background-trigger",
		Title:                   "Wire Expiry Cleanup Background Trigger",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		FilesToEdit:             []string{"apps/domain-booking/src/booking-expiry-reconciler.service.ts"},
		DependencyTaskIDs:       []string{"PROJ-1044-expired-booking-repository-query"},
		VerificationRequirement: "focused trigger verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		ReviewResultRefs:        []string{"review:planning-readiness-approved"},
	}
	workPlans := &fakeWorkPlans{openTasksByPlan: map[string][]projectworkplan.WorkTask{
		"plan-decomposition": {
			{ID: "select-ready-tasks", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusReady, DecompositionQuality: projectworkplan.DecompositionReady},
			triggerTask,
			queryTask,
		},
		"plan-implementation": {
			{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
		},
	}}
	automations := &fakeAutomationAPI{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetAutomationAPI(automations)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(workPlans.createdTasks) != 2 {
		t.Fatalf("expected two carried implementation tasks, got %#v", workPlans.createdTasks)
	}
	query := workPlans.createdTasks[0]
	trigger := workPlans.createdTasks[1]
	if query.TaskRef != queryTask.TaskRef || query.Status != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("query task should be first and planned until selector release, got %#v", query)
	}
	if trigger.TaskRef != triggerTask.TaskRef {
		t.Fatalf("trigger task should be second, got %#v", trigger)
	}
	if trigger.Status != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("dependent trigger must remain planned until query task completes, got %#v", trigger)
	}
	if got, want := trigger.DependencyTaskIDs, []string{"created-" + queryTask.TaskRef}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trigger dependency must be normalized to carried task id, got %#v want %#v", got, want)
	}
}

func TestReleaseCompiledTasksReleasesCarriedImplementationAfterExternalDependencyDone(t *testing.T) {
	ctx := context.Background()
	carried := projectworkplan.WorkTask{
		ID:                      "created-implement-external-dependent-slice",
		ProjectID:               "project-1",
		PlanID:                  "plan-implementation",
		TaskRef:                 "implement-external-dependent-slice",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              "implementation-worker",
		FilesToEdit:             []string{"internal/output.go"},
		DependencyTaskIDs:       []string{"upstream-external-task"},
		VerificationRequirement: "focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"works"},
		StopConditions:          []string{"blocked"},
		VerifierLadder:          []string{"unit"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"impact-ref"},
		OutputContract:          "diff plus evidence",
	}
	workPlans := &fakeWorkPlans{
		openTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-implementation": {carried},
		},
		tasksByID: map[string]projectworkplan.WorkTask{
			"upstream-external-task": {ID: "upstream-external-task", ProjectID: "project-1", Status: projectworkplan.WorkTaskStatusDone},
		},
	}
	svc := New(newTestChainStore(), &fakeWorkflowAPI{}, workPlans, []Config{testConfig()})

	err := svc.releaseCompiledTasks(ctx, "project-1", projectworkflow.WorkflowCompileResult{
		WorkPlanID:  "plan-implementation",
		WorkTaskIDs: []string{carried.ID},
	}, ChainRun{ID: "workflow_chain_run_1", CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("releaseCompiledTasks returned error: %v", err)
	}
	if len(workPlans.taskStatusUpdates) != 1 {
		t.Fatalf("expected carried task release after dependency done, got %#v", workPlans.taskStatusUpdates)
	}
	update := workPlans.taskStatusUpdates[0]
	if update.TaskID != carried.ID || update.Status != projectworkplan.WorkTaskStatusReady || update.RunID != "run-1" || update.TraceID != "trace-1" {
		t.Fatalf("unexpected carried task release update: %#v", update)
	}
}

func TestAdvancingStageBackfillsAutomationForExistingCarriedImplementationTask(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	generated := projectworkplan.WorkTask{
		ID:                      "generated-task-1",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "implement-ticket-slice",
		Title:                   "Implement Ticket Slice",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              "developer",
		FilesToEdit:             []string{"internal/output.go"},
		VerificationRequirement: "focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		ReviewResultRefs:        []string{"review:planning-readiness-approved"},
		AcceptanceCriteria:      []string{"works"},
		StopConditions:          []string{"blocked"},
		VerifierLadder:          []string{"unit"},
	}
	existing := generated
	existing.ID = "existing-implementation-task"
	existing.PlanID = "plan-implementation"
	existing.Status = projectworkplan.WorkTaskStatusReady
	workPlans := &fakeWorkPlans{openTasksByPlan: map[string][]projectworkplan.WorkTask{
		"plan-decomposition": {
			{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "decompose-work-plan", Status: projectworkplan.WorkTaskStatusDone},
			generated,
		},
		"plan-implementation": {
			existing,
		},
	}}
	automations := &fakeAutomationAPI{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetAutomationAPI(automations)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(workPlans.createdTasks) != 0 {
		t.Fatalf("existing carried task should not be recreated, got %#v", workPlans.createdTasks)
	}
	if len(automations.created) != 1 {
		t.Fatalf("expected backfilled carried implementation automation, got %#v", automations.created)
	}
	createdAutomation := automations.created[0]
	if createdAutomation.PlanID != "plan-implementation" || createdAutomation.AgentID != "implementation-worker" || !containsString(createdAutomation.AllowedTaskRefs, existing.ID) || !containsString(createdAutomation.AllowedTaskRefs, existing.TaskRef) {
		t.Fatalf("backfilled automation lost existing task refs: %#v", createdAutomation)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil || run.StageRuns[1].Status != StageStatusQueued {
		t.Fatalf("expected queued implementation stage, run=%#v err=%v", run, err)
	}
}

func TestProductionCarryForwardIgnoresDoneSourceOutputTasks(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	doneOutput := projectworkplan.WorkTask{
		ID:                      "done-output-task",
		ProjectID:               "project-1",
		PlanID:                  "plan-decomposition",
		TaskRef:                 "implementation-output",
		Title:                   "Implementation Output",
		Status:                  projectworkplan.WorkTaskStatusDone,
		OwnerAgent:              "developer",
		FilesToEdit:             []string{"internal/output.go"},
		VerificationRequirement: "focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		ReviewResultRefs:        []string{"review:done-output"},
		VerifierResultRefs:      []string{"verifier:done-output"},
		EvidenceRefs:            []string{"evidence:done-output"},
	}
	workPlans := &fakeWorkPlans{
		openTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {
				{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "decompose-work-plan", Status: projectworkplan.WorkTaskStatusDone},
				doneOutput,
			},
			"plan-implementation": {
				{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
			},
		},
	}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil || !strings.Contains(err.Error(), "missing_carried_implementation_tasks") {
		t.Fatalf("expected production path to ignore done source output tasks, got %v", err)
	}
	if len(workPlans.createdTasks) != 0 {
		t.Fatalf("production path must not carry done source output tasks, got %#v", workPlans.createdTasks)
	}
	run, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get chain: %v", getErr)
	}
	implementation := chainStageRunByRef(t, run, "implementation")
	if implementation.Status != StageStatusBlocked || implementation.BlockedCode != BlockedCodeMissingCarriedImplementationTasks || implementation.BlockedReason != "activate_next_stage_failed_missing_carried_implementation_tasks" {
		t.Fatalf("implementation stage should block without carrying done source tasks: %#v", implementation)
	}
}

func TestShadowWrappersStayBoundToConfiguredChainStages(t *testing.T) {
	ctx := context.Background()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(newTestChainStore(), workflows, &fakeWorkPlans{}, []Config{testConfig()})
	run := ChainRun{
		ID:             "workflow_chain_run_1",
		ProjectID:      "project-1",
		ChainRef:       "chain-1",
		InputRef:       "jira:GENERIC-1044",
		CreatedByRunID: "run-1",
		TraceID:        "trace-1",
	}

	stage, compiled, err := svc.CompileStageMetadataForShadow(ctx, run, testConfig().Stages[1])
	if err != nil {
		t.Fatalf("configured shadow stage compile failed: %v", err)
	}
	if stage.StageRef != "implementation" || compiled.WorkPlanID != "plan-implementation" || len(workflows.compileInputs) != 1 {
		t.Fatalf("configured shadow stage compile lost stage refs: stage=%#v compiled=%#v inputs=%#v", stage, compiled, workflows.compileInputs)
	}

	_, _, err = svc.CompileStageMetadataForShadow(ctx, run, StageConfig{StageRef: "implementation", WorkflowRef: "governed-post-implementation-validation"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected workflow-ref mismatch rejection, got %v", err)
	}
	_, _, err = svc.CompileStageMetadataForShadow(ctx, run, StageConfig{StageRef: "unconfigured", WorkflowRef: "governed-workplan-implementation"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unconfigured stage rejection, got %v", err)
	}
	if len(workflows.compileInputs) != 1 {
		t.Fatalf("rejected shadow compiles must not call compiler, got %#v", workflows.compileInputs)
	}
}

func TestShadowContextResolverUsesLocalIngestedJiraValidation(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{workflows: enabledWorkflows()}, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("GENERIC-1044", true)})

	refs, err := svc.ResolveContextRefsForShadow(ctx, "project-1", "chain-1", "jira:GENERIC-1044")
	if err != nil {
		t.Fatalf("resolve shadow context refs: %v", err)
	}
	for _, ref := range []string{
		"jira:GENERIC-1044",
		"jira-context:GENERIC-1044:summary",
		"jira-context:GENERIC-1044:scope",
		"jira-context:GENERIC-1044:implementation-evidence",
		"jira-context:GENERIC-1044:source-anchors",
		"jira-context:GENERIC-1044:verifier-scope",
	} {
		if !containsString(refs, ref) {
			t.Fatalf("shadow context refs missing %q: %#v", ref, refs)
		}
	}
}

func TestShadowContextResolverRejectsMissingLocalJiraContext(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{workflows: enabledWorkflows()}, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContextWithoutImplementationEvidence("GENERIC-1044")})

	if _, err := svc.ResolveContextRefsForShadow(ctx, "project-1", "chain-1", "jira:GENERIC-1044"); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "implementation_evidence") {
		t.Fatalf("expected local-ingested context rejection, got %v", err)
	}
}

func TestShadowContextResolverRejectsUnnormalizedJiraInputRef(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{workflows: enabledWorkflows()}, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("GENERIC-1044", true)})

	for _, inputRef := range []string{"GENERIC-1044", "input:GENERIC-1044", "objective:abcdef012345", "jira:generic-1044"} {
		if _, err := svc.ResolveContextRefsForShadow(ctx, "project-1", "chain-1", inputRef); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected unnormalized Jira ref %q rejected, got %v", inputRef, err)
		}
	}
}

func TestShadowWrappersRejectProjectAndChainDrift(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{workflows: enabledWorkflows()}, &fakeWorkPlans{}, []Config{testConfig()})
	run := ChainRun{ID: "workflow_chain_run_1", ProjectID: "project-1", ChainRef: "chain-1"}
	compiled := projectworkflow.WorkflowCompileResult{WorkPlanID: "plan-implementation"}

	if err := svc.CarryForwardStageOutputTasksForShadow(ctx, "project-2", run, compiled); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected carry-forward project mismatch rejection, got %v", err)
	}
	if err := svc.ReleaseCompiledTasksForShadow(ctx, "project-2", compiled, run); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected release project mismatch rejection, got %v", err)
	}
	badRun := run
	badRun.ChainRef = "other-chain"
	if err := svc.FinalizeGitOpsForShadow(ctx, &badRun); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unconfigured chain finalization rejection, got %v", err)
	}
}

func TestStartWithSameCorrelationReturnsExistingActiveChainRun(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1", "workflow_chain_run_2")

	first, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "operator-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	second, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "operator-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("retry start: %v", err)
	}
	if second.ChainRunID != first.ChainRunID || second.Status != first.Status || len(second.WorkPlanIDs) != 1 {
		t.Fatalf("retry must return existing active chain run, first=%#v second=%#v", first, second)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1", ChainRef: "chain-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("retry must not create duplicate chain runs: %#v", runs)
	}
	if len(workflows.compileInputs) != 1 || len(workPlans.activations) != 1 {
		t.Fatalf("retry must not compile or activate a second plan, compile=%d activations=%#v", len(workflows.compileInputs), workPlans.activations)
	}
}

func TestStartWithTracePropagatesHandoffMetadataToCompileAndActivation(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "orchestrator-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(workflows.compileInputs) != 1 {
		t.Fatalf("expected one compile input, got %#v", workflows.compileInputs)
	}
	if workflows.compileInputs[0].CreatedByRunID != "orchestrator-1" || workflows.compileInputs[0].TraceID != "trace-1" {
		t.Fatalf("compile input must propagate orchestrator and trace refs, got %#v", workflows.compileInputs[0])
	}
	if len(workPlans.statusUpdates) != 1 {
		t.Fatalf("expected first-stage activation update, got %#v", workPlans.statusUpdates)
	}
	update := workPlans.statusUpdates[0]
	if update.RunID != "orchestrator-1" || update.TraceID != "trace-1" || update.SafeNextAction == "" {
		t.Fatalf("plan activation must propagate run/trace/action metadata, got %#v", update)
	}
	if len(workPlans.taskStatusUpdates) != 1 {
		t.Fatalf("expected first-stage task release update, got %#v", workPlans.taskStatusUpdates)
	}
	taskUpdate := workPlans.taskStatusUpdates[0]
	if taskUpdate.RunID != "orchestrator-1" || taskUpdate.TraceID != "trace-1" || taskUpdate.SafeNextAction == "" {
		t.Fatalf("task release must propagate run/trace/action metadata, got %#v", taskUpdate)
	}
}

func TestReleaseCompiledTasksDoesNotReleaseUnmetDependencies(t *testing.T) {
	ctx := context.Background()
	workPlans := &fakeWorkPlans{
		openTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {
				{
					ID:                   "task-root",
					ProjectID:            "project-1",
					PlanID:               "plan-decomposition",
					TaskRef:              "root",
					Status:               projectworkplan.WorkTaskStatusPlanned,
					DecompositionQuality: projectworkplan.DecompositionReady,
				},
				{
					ID:                   "task-dependent",
					ProjectID:            "project-1",
					PlanID:               "plan-decomposition",
					TaskRef:              "dependent",
					Status:               projectworkplan.WorkTaskStatusPlanned,
					DependencyTaskIDs:    []string{"task-root"},
					DecompositionQuality: projectworkplan.DecompositionReady,
				},
			},
		},
	}
	svc := New(newTestChainStore(), &fakeWorkflowAPI{}, workPlans, []Config{testConfig()})

	err := svc.releaseCompiledTasks(ctx, "project-1", projectworkflow.WorkflowCompileResult{
		WorkPlanID:  "plan-decomposition",
		WorkTaskIDs: []string{"task-root", "task-dependent"},
	}, ChainRun{ID: "chain-run-1"})
	if err != nil {
		t.Fatalf("release compiled tasks: %v", err)
	}
	if got, want := strings.Join(workPlans.released, ","), "task-root"; got != want {
		t.Fatalf("expected only dependency-free task release, got %q", got)
	}
}

func TestStartDoesNotActivateFirstStageBeforeChainRunPersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	store.createErr = errors.New("chain store unavailable")
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"}); err == nil {
		t.Fatalf("expected start to fail when chain run persistence fails")
	}
	if len(workPlans.activations) != 0 || len(workPlans.released) != 0 {
		t.Fatalf("must not activate or release stage work before chain persistence, activations=%#v released=%#v", workPlans.activations, workPlans.released)
	}
	if len(store.runs) != 0 {
		t.Fatalf("failed create must not leave persisted chain runs: %#v", store.runs)
	}
}

func TestStartDoesNotActivateFirstStageBeforeStageMetadataUpdatePersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")
	store.updateErr = errors.New("chain update unavailable")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"}); err == nil {
		t.Fatalf("expected start to fail when first-stage metadata update fails")
	}
	if len(workPlans.activations) != 0 || len(workPlans.released) != 0 {
		t.Fatalf("must not activate or release first-stage work before chain metadata update, activations=%#v released=%#v", workPlans.activations, workPlans.released)
	}
}

func TestHandleWorkPlanStatusChangedCreatesDraftPRAfterPostValidationDone(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "github-pr-1044" {
		t.Fatalf("expected completed chain with draft PR ref, got %#v", run)
	}
	if run.StageRuns[2].Status != StageStatusCompleted || run.NextAction == "" {
		t.Fatalf("expected completed post-validation stage with next action: %#v", run)
	}
	if len(finalizer.inputs) != 1 || finalizer.inputs[0].WorkPlan.ID != "plan-implementation" {
		t.Fatalf("expected one GitOps finalization with implementation plan, got %#v", finalizer.inputs)
	}
	input := finalizer.inputs[0]
	if !containsString(input.AllowedPathspecs, "internal/projectworkflowchain/service.go") || containsString(input.AllowedPathspecs, "cmd/mivia-server") {
		t.Fatalf("expected only explicit implementation edit pathspecs in GitOps finalization, got %#v", input.AllowedPathspecs)
	}
	if !containsString(input.ReviewRefs, "review:task-post-validation") || !containsString(input.VerifierRefs, "verifier:task-post-validation") {
		t.Fatalf("expected stage review and verifier refs in GitOps finalization, got reviews=%#v verifiers=%#v", input.ReviewRefs, input.VerifierRefs)
	}
	if !containsString(input.TestResults, "task-post-validation verified by verifier:task-post-validation") {
		t.Fatalf("expected verifier-derived test result in GitOps finalization, got %#v", input.TestResults)
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("idempotent post-validation event: %v", err)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected no duplicate GitOps finalization, got %d", len(finalizer.inputs))
	}
}

func TestJiraTicketChainPreservesEveryAutomationHandoffThroughDraftPR(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{
		CommitRef:      "commit/GENERIC-1044",
		PushRef:        "push/GENERIC-1044",
		PullRequestRef: "github-pr-1044",
		EvidenceRefs:   []string{"gitops-evidence:GENERIC-1044"},
	}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "orchestrator-run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("start chain: %v", err)
	}
	assertChainStageHandoff(t, result.StageRuns[0], "decomposition", "workflow-decomposition", "plan-decomposition", "task-decomposition", "automation-decomposition", StageStatusQueued)
	if result.InputRef != "jira:GENERIC-1044" || result.NextAction != "decomposition automation will run when planned tasks transition to ready" {
		t.Fatalf("start result lost Jira input or next action: %#v", result)
	}
	if len(workflows.compileInputs) != 1 || workflows.compileInputs[0].CreatedByRunID != "orchestrator-run-1" || workflows.compileInputs[0].TraceID != "trace-1" {
		t.Fatalf("first compile lost run/trace refs: %#v", workflows.compileInputs)
	}
	if got, want := strings.Join(workPlans.events[:2], ","), "release:task-decomposition,activate:plan-decomposition"; got != want {
		t.Fatalf("decomposition activation handoff order mismatch: got %s want %s", got, want)
	}

	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "github-pr-1044" || run.NextAction != "workflow chain completed with draft PR GitOps output" {
		t.Fatalf("completed chain lost final status/action/PR handoff: %#v", run)
	}
	if run.CreatedByRunID != "orchestrator-run-1" || run.TraceID != "trace-1" || run.InputRef != "jira:GENERIC-1044" {
		t.Fatalf("completed chain lost root refs: %#v", run)
	}
	if len(run.WorkPlanIDs) != 3 || len(run.AutomationIDs) != 3 || len(run.StageRuns) != 3 {
		t.Fatalf("completed chain lost plan or automation refs: %#v", run)
	}
	assertChainStageHandoff(t, run.StageRuns[0], "decomposition", "workflow-decomposition", "plan-decomposition", "task-decomposition", "automation-decomposition", StageStatusCompleted)
	assertChainStageHandoff(t, run.StageRuns[1], "implementation", "workflow-implementation", "plan-implementation", "task-implementation", "automation-implementation", StageStatusCompleted)
	assertChainStageHandoff(t, run.StageRuns[2], "post-validation", "workflow-validation", "plan-post-validation", "task-post-validation", "automation-post-validation", StageStatusCompleted)
	if got, want := strings.Join(workPlans.events, ","), "release:task-decomposition,activate:plan-decomposition,release:task-implementation,activate:plan-implementation,release:task-post-validation,activate:plan-post-validation"; got != want {
		t.Fatalf("stage activation handoff order mismatch:\n got: %s\nwant: %s", got, want)
	}
	if len(workflows.compileInputs) != 3 {
		t.Fatalf("expected all three stages compiled, got %#v", workflows.compileInputs)
	}
	for i, input := range workflows.compileInputs {
		if input.UserRequestRef != "jira:GENERIC-1044" || input.CreatedByRunID != "orchestrator-run-1" || input.TraceID != "trace-1" {
			t.Fatalf("compile input %d lost Jira/run/trace refs: %#v", i, input)
		}
		if len(input.ContextPackRefs) == 0 || !containsString(input.ContextPackRefs, "jira:GENERIC-1044") {
			t.Fatalf("compile input %d lost context refs: %#v", i, input)
		}
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected exactly one GitOps finalization, got %#v", finalizer.inputs)
	}
	gitopsInput := finalizer.inputs[0]
	if gitopsInput.InputRef != "jira:GENERIC-1044" || gitopsInput.CreatedByRunID != "orchestrator-run-1" || gitopsInput.TraceID != "trace-1" {
		t.Fatalf("GitOps input lost Jira/run/trace refs: %#v", gitopsInput)
	}
	if len(gitopsInput.StageRuns) != 3 || len(gitopsInput.AutomationIDs) != 3 || gitopsInput.WorkPlan.ID != "plan-implementation" {
		t.Fatalf("GitOps input lost stage/automation/implementation plan refs: %#v", gitopsInput)
	}
	if !containsString(gitopsInput.ReviewRefs, "review:task-post-validation") || !containsString(gitopsInput.VerifierRefs, "verifier:task-post-validation") || !containsString(gitopsInput.TestResults, "task-post-validation verified by verifier:task-post-validation") {
		t.Fatalf("GitOps input lost validation review/verifier/test refs: %#v", gitopsInput)
	}
	if !containsString(gitopsInput.AllowedPathspecs, "internal/projectworkflowchain/service.go") || containsString(gitopsInput.AllowedPathspecs, "cmd/mivia-server") {
		t.Fatalf("GitOps input must use explicit implementation edit scope only: %#v", gitopsInput.AllowedPathspecs)
	}
}

func TestGenericSafeRefChainPreservesCodexHandoffDataThroughDraftPR(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{genericSafeRefTestConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{
		CommitRef:      "commit/generic-1044",
		PushRef:        "push/generic-1044",
		PullRequestRef: "github-pr-1044",
		EvidenceRefs:   []string{"gitops-evidence:generic-1044"},
	}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{
		ProjectID:      "project-1",
		ChainRef:       "generic-chain",
		InputText:      "ticket/GENERIC-1044",
		CreatedByRunID: "codex-orchestrator-run-1",
		TraceID:        "trace-generic-1044",
	})
	if err != nil {
		t.Fatalf("start generic chain: %v", err)
	}
	assertChainStageHandoff(t, result.StageRuns[0], "decomposition", "workflow-decomposition", "plan-decomposition", "task-decomposition", "automation-decomposition", StageStatusQueued)
	if result.InputRef != "input:ticket/GENERIC-1044" || result.NextAction != "decomposition automation will run when planned tasks transition to ready" {
		t.Fatalf("start result lost generic input or next action: %#v", result)
	}
	if len(workflows.compileInputs) != 1 {
		t.Fatalf("expected first stage compile, got %#v", workflows.compileInputs)
	}
	assertGenericCodexCompileHandoff(t, workflows.compileInputs[0], "decomposition")
	if got, want := strings.Join(workPlans.events[:2], ","), "release:task-decomposition,activate:plan-decomposition"; got != want {
		t.Fatalf("decomposition activation handoff order mismatch: got %s want %s", got, want)
	}

	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "github-pr-1044" || run.NextAction != "workflow chain completed with draft PR GitOps output" {
		t.Fatalf("completed chain lost final status/action/PR handoff: %#v", run)
	}
	if run.CreatedByRunID != "codex-orchestrator-run-1" || run.TraceID != "trace-generic-1044" || run.InputRef != "input:ticket/GENERIC-1044" {
		t.Fatalf("completed chain lost root refs: %#v", run)
	}
	if len(run.WorkPlanIDs) != 3 || len(run.AutomationIDs) != 3 || len(run.StageRuns) != 3 {
		t.Fatalf("completed chain lost plan or automation refs: %#v", run)
	}
	assertChainStageHandoff(t, run.StageRuns[0], "decomposition", "workflow-decomposition", "plan-decomposition", "task-decomposition", "automation-decomposition", StageStatusCompleted)
	assertChainStageHandoff(t, run.StageRuns[1], "implementation", "workflow-implementation", "plan-implementation", "task-implementation", "automation-implementation", StageStatusCompleted)
	assertChainStageHandoff(t, run.StageRuns[2], "post-validation", "workflow-validation", "plan-post-validation", "task-post-validation", "automation-post-validation", StageStatusCompleted)
	if got, want := strings.Join(workPlans.events, ","), "release:task-decomposition,activate:plan-decomposition,release:task-implementation,activate:plan-implementation,release:task-post-validation,activate:plan-post-validation"; got != want {
		t.Fatalf("stage activation handoff order mismatch:\n got: %s\nwant: %s", got, want)
	}
	if len(workflows.compileInputs) != 3 {
		t.Fatalf("expected all three stages compiled, got %#v", workflows.compileInputs)
	}
	for _, tc := range []struct {
		index int
		stage string
	}{
		{index: 0, stage: "decomposition"},
		{index: 1, stage: "implementation"},
		{index: 2, stage: "post-validation"},
	} {
		assertGenericCodexCompileHandoff(t, workflows.compileInputs[tc.index], tc.stage)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected exactly one GitOps finalization, got %#v", finalizer.inputs)
	}
	gitopsInput := finalizer.inputs[0]
	if gitopsInput.ProjectID != "project-1" || gitopsInput.ChainRef != "generic-chain" || gitopsInput.ChainRunID != result.ChainRunID {
		t.Fatalf("GitOps input lost project/chain refs: %#v", gitopsInput)
	}
	if gitopsInput.InputRef != "input:ticket/GENERIC-1044" || gitopsInput.CreatedByRunID != "codex-orchestrator-run-1" || gitopsInput.TraceID != "trace-generic-1044" {
		t.Fatalf("GitOps input lost generic/run/trace refs: %#v", gitopsInput)
	}
	if len(gitopsInput.StageRuns) != 3 || len(gitopsInput.AutomationIDs) != 3 || gitopsInput.WorkPlan.ID != "plan-implementation" {
		t.Fatalf("GitOps input lost stage/automation/implementation plan refs: %#v", gitopsInput)
	}
	for _, ref := range []string{"review:task-decomposition", "review:task-implementation", "review:task-post-validation"} {
		if !containsString(gitopsInput.ReviewRefs, ref) {
			t.Fatalf("GitOps input lost review ref %q: %#v", ref, gitopsInput.ReviewRefs)
		}
	}
	for _, ref := range []string{"verifier:task-decomposition", "verifier:task-implementation", "verifier:task-post-validation"} {
		if !containsString(gitopsInput.VerifierRefs, ref) {
			t.Fatalf("GitOps input lost verifier ref %q: %#v", ref, gitopsInput.VerifierRefs)
		}
	}
	if !containsString(gitopsInput.TestResults, "task-post-validation verified by verifier:task-post-validation") {
		t.Fatalf("GitOps input lost validation test result: %#v", gitopsInput.TestResults)
	}
	if !containsString(gitopsInput.AllowedPathspecs, "internal/projectworkflowchain/service.go") || containsString(gitopsInput.AllowedPathspecs, "cmd/mivia-server") {
		t.Fatalf("GitOps input must use explicit implementation edit scope only: %#v", gitopsInput.AllowedPathspecs)
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("idempotent post-validation event: %v", err)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected no duplicate GitOps finalization, got %d", len(finalizer.inputs))
	}
}

func TestGenericSafeRefChainWithRealWorkflowCompilerPreservesGeneratedTaskOutputsThroughGitOps(t *testing.T) {
	ctx := context.Background()
	workPlanStore := workplanstore.NewMemoryStore()
	workPlans := projectworkplan.New(workPlanStore)
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  projectautomation.RunnerExecutionExternal,
		MaxParallelTasks: 2,
		PermissionResolver: realChainPermissionResolver{
			allowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
		},
		WorkPlanStatusTrigger: projectautomation.WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	workflows := projectworkflow.New(workflowstore.NewMemoryStore())
	workflows.SetCompilerDependencies(workPlans, automations)
	for _, path := range []string{
		"configs/workflows/governed-decomposition-planning.toml",
		"configs/workflows/governed-workplan-implementation.toml",
		"configs/workflows/governed-post-implementation-validation.toml",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatalf("read workflow %s: %v", path, err)
		}
		if _, err := workflows.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{
			ProjectID:      "project-1",
			Data:           data,
			CreatedByRunID: "import-real-chain",
			TraceID:        "trace-real-chain",
		}); err != nil {
			t.Fatalf("import workflow %s: %v", path, err)
		}
	}
	store := newTestChainStore()
	svc := New(store, workflows, workPlans, []Config{genericSafeRefTestConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{
		CommitRef:      "commit/generic-real-2044",
		PushRef:        "push/generic-real-2044",
		PullRequestRef: "github-pr-2044",
		EvidenceRefs:   []string{"gitops-evidence:generic-real-2044"},
	}}
	svc.SetGitOpsFinalizer(finalizer)
	workPlans.SetStatusChangeHandler(realChainStatusFanout{handlers: []projectworkplan.WorkPlanStatusChangeHandler{automations, svc}})

	result, err := svc.Start(ctx, StartInput{
		ProjectID:      "project-1",
		ChainRef:       "generic-chain",
		InputText:      "ticket/GENERIC-2044",
		CreatedByRunID: "codex-orchestrator-run-2044",
		TraceID:        "trace-generic-real-2044",
	})
	if err != nil {
		t.Fatalf("start generic real chain: %v", err)
	}
	if result.Status != ChainStatusQueued || len(result.WorkPlanIDs) != 1 || len(result.AutomationIDs) == 0 {
		t.Fatalf("start lost generated first-stage refs: %#v", result)
	}
	for _, stage := range []string{"decomposition", "implementation", "post-validation"} {
		run, err := svc.Get(ctx, "project-1", result.ChainRunID)
		if err != nil {
			t.Fatalf("get chain before %s: %v", stage, err)
		}
		stageRun := chainStageRunByRef(t, run, stage)
		if stageRun.WorkPlanID == "" || len(stageRun.WorkTaskIDs) == 0 || len(stageRun.AutomationIDs) == 0 {
			t.Fatalf("stage %s missing generated handoff refs: %#v", stage, stageRun)
		}
		assertQueuedAutomationRunsForGeneratedStage(t, ctx, automations, "project-1", stageRun)
		if stage == "decomposition" {
			if _, err := workPlans.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
				ProjectID:               "project-1",
				PlanID:                  stageRun.WorkPlanID,
				TaskRef:                 "generic-2044-implementation-slice",
				Title:                   "Implement GENERIC-2044 Slice",
				Status:                  projectworkplan.WorkTaskStatusPlanned,
				OwnerAgent:              "developer",
				FilesToEdit:             []string{"internal/projectworkflowchain/service.go"},
				VerificationRequirement: "focused workflow-chain tests",
				ReviewResultRefs:        []string{"review:planning-readiness-approved"},
				VerifierResultRefs:      []string{"verifier:planning-readiness"},
				DecompositionQuality:    projectworkplan.DecompositionReady,
				AcceptanceCriteria:      []string{"Implementation slice is executable from task metadata."},
				StopConditions:          []string{"Stop if workflow-chain scope changes."},
				VerifierLadder:          []string{"focused workflow-chain tests"},
				RegressionApplicability: "required for workflow-chain behavior",
				DownstreamImpactRefs:    []string{"workflow-chain-impact-ref"},
				OutputContract:          "bounded diff refs and verifier refs",
			}); err != nil {
				t.Fatalf("create generated implementation child: %v", err)
			}
			completeGeneratedStagePlanWithOpenChildren(t, ctx, workPlans, workPlanStore, svc, "project-1", stageRun.WorkPlanID, stage)
			continue
		}
		completeGeneratedStagePlan(t, ctx, workPlans, "project-1", stageRun.WorkPlanID, stage)
	}

	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get completed real chain: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "github-pr-2044" {
		t.Fatalf("completed real chain lost final status or PR ref: %#v", run)
	}
	if run.InputRef != "input:ticket/GENERIC-2044" || run.CreatedByRunID != "codex-orchestrator-run-2044" || run.TraceID != "trace-generic-real-2044" {
		t.Fatalf("completed real chain lost root handoff refs: %#v", run)
	}
	if len(run.WorkPlanIDs) != 3 || len(run.AutomationIDs) < 3 || len(run.StageRuns) != 3 {
		t.Fatalf("completed real chain lost generated plan/automation/stage refs: %#v", run)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected one GitOps finalization, got %#v", finalizer.inputs)
	}
	gitopsInput := finalizer.inputs[0]
	if gitopsInput.ProjectID != "project-1" || gitopsInput.ChainRunID != result.ChainRunID || gitopsInput.ChainRef != "generic-chain" {
		t.Fatalf("GitOps input lost chain refs: %#v", gitopsInput)
	}
	if gitopsInput.InputRef != "input:ticket/GENERIC-2044" || gitopsInput.CreatedByRunID != "codex-orchestrator-run-2044" || gitopsInput.TraceID != "trace-generic-real-2044" {
		t.Fatalf("GitOps input lost root refs: %#v", gitopsInput)
	}
	for _, ref := range []string{
		"review:generated-decomposition",
		"review:generated-implementation",
		"review:generated-post-validation",
	} {
		if !containsString(gitopsInput.ReviewRefs, ref) {
			t.Fatalf("GitOps input missing generated review ref %q: %#v", ref, gitopsInput.ReviewRefs)
		}
	}
	for _, ref := range []string{
		"verifier:generated-decomposition",
		"verifier:generated-implementation",
		"verifier:generated-post-validation",
	} {
		if !containsString(gitopsInput.VerifierRefs, ref) {
			t.Fatalf("GitOps input missing generated verifier ref %q: %#v", ref, gitopsInput.VerifierRefs)
		}
	}
	if !containsString(gitopsInput.AllowedPathspecs, "internal/projectworkflowchain/service.go") || !containsString(gitopsInput.AllowedPathspecs, "cmd/mivia-automation-runner/main_test.go") || containsString(gitopsInput.AllowedPathspecs, ".ai") {
		t.Fatalf("GitOps input must derive allowed pathspecs from generated implementation tasks only: %#v", gitopsInput.AllowedPathspecs)
	}
	if !containsString(gitopsInput.TestResults, "select-ready-tasks verified by verifier:generated-implementation") {
		t.Fatalf("GitOps input missing generated implementation test result: %#v", gitopsInput.TestResults)
	}
}

func TestRealChainCarriesConcreteChildTaskFromCompletedDecompositionPlan(t *testing.T) {
	ctx := context.Background()
	workPlanStore := workplanstore.NewMemoryStore()
	workPlans := projectworkplan.New(workPlanStore)
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: projectautomation.RunnerExecutionExternal,
		PermissionResolver: realChainPermissionResolver{
			allowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
		},
		WorkPlanStatusTrigger: projectautomation.WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	workflows := projectworkflow.New(workflowstore.NewMemoryStore())
	workflows.SetCompilerDependencies(workPlans, automations)
	for _, path := range []string{
		"configs/workflows/governed-decomposition-planning.toml",
		"configs/workflows/governed-workplan-implementation.toml",
		"configs/workflows/governed-post-implementation-validation.toml",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatalf("read workflow %s: %v", path, err)
		}
		if _, err := workflows.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{
			ProjectID:      "project-1",
			Data:           data,
			CreatedByRunID: "import-real-chain",
			TraceID:        "trace-real-chain",
		}); err != nil {
			t.Fatalf("import workflow %s: %v", path, err)
		}
	}
	store := newTestChainStore()
	svc := New(store, workflows, workPlans, []Config{genericSafeRefTestConfig()})
	svc.SetAutomationAPI(automations)
	workPlans.SetStatusChangeHandler(realChainStatusFanout{handlers: []projectworkplan.WorkPlanStatusChangeHandler{automations, svc}})

	result, err := svc.Start(ctx, StartInput{
		ProjectID:      "project-1",
		ChainRef:       "generic-chain",
		InputText:      "ticket/GENERIC-2044",
		CreatedByRunID: "codex-orchestrator-run-2044",
		TraceID:        "trace-real-child-handoff",
	})
	if err != nil {
		t.Fatalf("start real chain: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	decomposition := chainStageRunByRef(t, run, "decomposition")
	child, err := workPlans.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  decomposition.WorkPlanID,
		TaskRef:                 "PROJ-1044-expired-booking-query",
		Title:                   "Add Expired Booking Selection Query",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              "developer",
		FilesToEdit:             []string{"apps/domain-booking/src/infrastructure/database/repositories/booking.repository.ts"},
		VerificationRequirement: "focused booking repository tests",
		ReviewResultRefs:        []string{"review:planning-readiness-approved"},
		VerifierResultRefs:      []string{"verifier:planning-readiness"},
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"Expired eligible bookings are selected for cleanup."},
		StopConditions:          []string{"Stop if booking repository scope is unclear."},
		VerifierLadder:          []string{"focused booking repository tests"},
		RegressionApplicability: "required for booking expiry behavior",
		DownstreamImpactRefs:    []string{"booking-expiry-impact-ref"},
		OutputContract:          "bounded diff refs and verifier refs",
	})
	if err != nil {
		t.Fatalf("create concrete child task: %v", err)
	}
	compiledTaskIDs := map[string]struct{}{}
	for _, taskID := range decomposition.WorkTaskIDs {
		compiledTaskIDs[taskID] = struct{}{}
	}
	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: decomposition.WorkPlanID})
	if err != nil {
		t.Fatalf("list decomposition tasks: %v", err)
	}
	for _, task := range tasks {
		if _, compiled := compiledTaskIDs[task.ID]; !compiled {
			continue
		}
		task.Status = projectworkplan.WorkTaskStatusDone
		task.Outcome = "decomposition wrapper accepted"
		task.ReviewResultRefs = appendUnique(task.ReviewResultRefs, "review:decomposition-wrapper")
		task.VerifierResultRefs = appendUnique(task.VerifierResultRefs, "verifier:decomposition-wrapper")
		if _, err := workPlans.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("complete decomposition wrapper task %s: %v", task.ID, err)
		}
	}
	plan, err := workPlanStore.GetWorkPlan(ctx, "project-1", decomposition.WorkPlanID)
	if err != nil {
		t.Fatalf("get decomposition plan: %v", err)
	}
	plan.Status = projectworkplan.WorkPlanStatusDone
	plan.Outcome = "decomposition produced concrete implementation child task"
	plan.ResumeSummary = "advance to implementation with child task handoff"
	if _, err := workPlanStore.UpdateWorkPlan(ctx, plan); err != nil {
		t.Fatalf("persist completed decomposition plan: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID: "project-1",
		PlanID:    decomposition.WorkPlanID,
		OldStatus: projectworkplan.WorkPlanStatusActive,
		NewStatus: projectworkplan.WorkPlanStatusDone,
		ChangedAt: plan.UpdatedAt,
	}); err != nil {
		t.Fatalf("advance chain after completed decomposition plan: %v", err)
	}

	run, err = svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get advanced chain: %v", err)
	}
	implementation := chainStageRunByRef(t, run, "implementation")
	if implementation.Status != StageStatusQueued || implementation.WorkPlanID == "" {
		tasks, _ := workPlans.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: implementation.WorkPlanID})
		runs, _ := automations.ListRuns(ctx, projectautomation.RunFilter{ProjectID: "project-1", PlanID: implementation.WorkPlanID})
		t.Fatalf("implementation stage not queued after decomposition child handoff: %#v tasks=%#v runs=%#v", implementation, tasks, runs)
	}
	implementationPlan, err := workPlanStore.GetWorkPlan(ctx, "project-1", implementation.WorkPlanID)
	if err != nil {
		t.Fatalf("get implementation plan: %v", err)
	}
	if implementationPlan.Status != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("implementation plan must be active after chain stage activation, got %#v", implementationPlan)
	}
	implementationTasks, err := workPlans.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: implementation.WorkPlanID})
	if err != nil {
		t.Fatalf("list implementation tasks: %v", err)
	}
	var carried projectworkplan.WorkTask
	for _, task := range implementationTasks {
		if task.TaskRef == child.TaskRef {
			carried = task
			break
		}
	}
	if carried.ID == "" || carried.Status != projectworkplan.WorkTaskStatusPlanned || carried.OwnerAgent != "implementation-worker" {
		t.Fatalf("concrete child task was not carried as planned into implementation plan, child=%#v tasks=%#v", child, implementationTasks)
	}
	if len(carried.ReviewResultRefs) != 0 || len(carried.VerifierResultRefs) != 0 {
		t.Fatalf("carried child task must not inherit planning review/verifier refs, got reviews=%#v verifiers=%#v", carried.ReviewResultRefs, carried.VerifierResultRefs)
	}
	if !containsString(carried.EvidenceRefs, "review:planning-readiness-approved") {
		t.Fatalf("carried child task must preserve planning review as handoff evidence, got %#v", carried.EvidenceRefs)
	}
	implementation = chainStageRunByRef(t, run, "implementation")
	if !containsString(implementation.WorkTaskIDs, carried.ID) {
		t.Fatalf("implementation stage lost carried task id %q: %#v", carried.ID, implementation)
	}
	runs, err := automations.ListRuns(ctx, projectautomation.RunFilter{ProjectID: "project-1", PlanID: implementation.WorkPlanID, Status: projectautomation.RunStatusQueued})
	if err != nil {
		t.Fatalf("list queued implementation runs: %v", err)
	}
	foundPrematureWorkerRun := false
	for _, automationRun := range runs {
		if automationRun.TaskID == carried.ID && automationRun.WorkTaskStatus == projectworkplan.WorkTaskStatusReady {
			foundPrematureWorkerRun = true
			break
		}
	}
	if foundPrematureWorkerRun {
		t.Fatalf("implementation worker run must not queue before selector releases carried task %#v; runs=%#v", carried, runs)
	}
}

func TestHandleWorkPlanStatusChangedDoesNotActivateNextStageBeforeChainUpdatePersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	store.updateErr = errors.New("chain update unavailable")
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatalf("expected next-stage chain update failure")
	}
	if len(workPlans.activations) != 1 || workPlans.activations[0] != "plan-decomposition" {
		t.Fatalf("must not activate implementation before chain update, start=%#v activations=%#v", result, workPlans.activations)
	}
	if len(workPlans.released) != 1 || workPlans.released[0] != "task-decomposition" {
		t.Fatalf("must not release implementation before chain update, released=%#v", workPlans.released)
	}
}

func TestHandleWorkPlanStatusChangedDoesNotFinalizeGitOpsBeforeCheckpointPersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	store.updateErr = errors.New("chain update unavailable")
	err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatalf("expected GitOps checkpoint update failure")
	}
	if len(finalizer.inputs) != 0 {
		t.Fatalf("must not create draft PR GitOps output before checkpoint persists, inputs=%#v", finalizer.inputs)
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenDraftPRFinalizerMissing(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected missing finalizer to block with invalid input, got %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || run.StageRuns[2].BlockedReason == "" {
		t.Fatalf("expected blocked GitOps-ready chain with explicit reason, got %#v", run)
	}
	if !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed_invalid_project_workflow_chain_input_gitops_finalizer_missing") {
		t.Fatalf("expected explicit missing-finalizer blocked reason, got %#v", run.StageRuns[2].BlockedReason)
	}
}

func TestHandleWorkPlanStatusChangedStopsChainWhenStagePlanCancelledOrSuperseded(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		planStatus  string
		chainStatus string
		stageStatus string
		reason      string
	}{
		{name: "cancelled", planStatus: projectworkplan.WorkPlanStatusCancelled, chainStatus: ChainStatusCancelled, stageStatus: StageStatusCancelled, reason: "work_plan_cancelled"},
		{name: "superseded", planStatus: projectworkplan.WorkPlanStatusSuperseded, chainStatus: ChainStatusSuperseded, stageStatus: StageStatusSuperseded, reason: "work_plan_superseded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			workPlans := &fakeWorkPlans{}
			svc := New(store, workflows, workPlans, []Config{testConfig()})
			svc.newID = deterministicIDs("workflow_chain_run_1")

			result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: tc.planStatus}); err != nil {
				t.Fatalf("terminal change: %v", err)
			}
			run, err := svc.Get(ctx, "project-1", result.ChainRunID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if run.Status != tc.chainStatus || run.StageRuns[0].Status != tc.stageStatus || run.StageRuns[0].BlockedReason != tc.reason {
				t.Fatalf("expected chain/stage terminal %s/%s, got %#v", tc.chainStatus, tc.stageStatus, run)
			}
		})
	}
}

func TestHandleWorkPlanStatusChangedBlocksNextStageWhenCompletedPlanContainsFailedOrCancelledTasks(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		taskStatus string
	}{
		{name: "failed", taskStatus: projectworkplan.WorkTaskStatusFailed},
		{name: "cancelled", taskStatus: projectworkplan.WorkTaskStatusCancelled},
		{name: "superseded", taskStatus: projectworkplan.WorkTaskStatusSuperseded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			workPlans := &fakeWorkPlans{
				allTasksByPlan: map[string][]projectworkplan.WorkTask{
					"plan-decomposition": {
						{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "task-decomposition", Status: projectworkplan.WorkTaskStatusDone},
						{ID: "task-unsuccessful", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "task-unsuccessful", Status: tc.taskStatus},
					},
				},
			}
			svc := New(store, workflows, workPlans, []Config{testConfig()})
			svc.newID = deterministicIDs("workflow_chain_run_1")

			result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected unsuccessful task to block stage advancement, got %v", err)
			}
			run, err := svc.Get(ctx, "project-1", result.ChainRunID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if run.Status != ChainStatusBlocked || run.StageRuns[0].Status != StageStatusBlocked || !strings.Contains(run.StageRuns[0].BlockedReason, tc.taskStatus) {
				t.Fatalf("expected blocked decomposition stage for %s task, got %#v", tc.taskStatus, run)
			}
			if run.StageRuns[1].Status != StageStatusPlanned || run.StageRuns[1].WorkPlanID != "" {
				t.Fatalf("next stage must not be compiled after unsuccessful terminal task, got %#v", run.StageRuns[1])
			}
			if len(workflows.compileInputs) != 1 {
				t.Fatalf("expected only initial decomposition compile, got %d", len(workflows.compileInputs))
			}
		})
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenDraftPRFinalizationFails(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetGitOpsFinalizer(&fakeGitOpsFinalizer{err: errors.New("gitops failed")})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err == nil {
		t.Fatalf("expected GitOps finalization failure")
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed") {
		t.Fatalf("expected blocked chain after GitOps failure, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedRecordsRepairableGitOpsVerificationFailure(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{err: fmt.Errorf("%w: abcdef123456", projectgitops.ErrVerificationFailed)}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone})
		if planID == "plan-post-validation" {
			if err == nil {
				t.Fatalf("expected GitOps verification failure")
			}
			continue
		}
		if err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || run.GitOpsAttemptCount != 1 {
		t.Fatalf("expected first blocked GitOps recovery attempt, got %#v", run)
	}
	if run.GitOpsFailureCategory != "gitops_verification_failed_abcdef123456" || run.GitOpsRecoveryStatus != GitOpsRecoveryStatusRepairable {
		t.Fatalf("expected repairable verifier category, got category=%q status=%q", run.GitOpsFailureCategory, run.GitOpsRecoveryStatus)
	}
	if !containsString(run.GitOpsFailureEvidenceRefs, "gitops-failure:gitops_verification_failed_abcdef123456") || !containsString(run.GitOpsFailureEvidenceRefs, "gitops-attempt:1") {
		t.Fatalf("expected safe verifier failure evidence refs, got %#v", run.GitOpsFailureEvidenceRefs)
	}
	if !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_verification_failed_abcdef123456_repairable_attempt_1") {
		t.Fatalf("expected structured blocked reason, got %q", run.StageRuns[2].BlockedReason)
	}
}

func TestHandleWorkPlanStatusChangedRecordsDirtyScopeGitOpsRecoveryEvidence(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{err: projectgitops.DirtyWorktreeScopeError{Paths: []string{"outside/generated.pb.go"}}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone})
		if planID == "plan-post-validation" {
			if err == nil {
				t.Fatalf("expected GitOps dirty-scope failure")
			}
			continue
		}
		if err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.GitOpsFailureCategory != "gitops_dirty_worktree_scope" || run.GitOpsRecoveryStatus != GitOpsRecoveryStatusRepairable {
		t.Fatalf("expected repairable dirty-scope category, got category=%q status=%q", run.GitOpsFailureCategory, run.GitOpsRecoveryStatus)
	}
	var dirtyScopeRef string
	for _, ref := range run.GitOpsFailureEvidenceRefs {
		if strings.HasPrefix(ref, "gitops-dirty-scope:") {
			dirtyScopeRef = ref
		}
		if strings.Contains(ref, "outside/generated.pb.go") {
			t.Fatalf("dirty-scope evidence ref must not leak raw path, got %#v", run.GitOpsFailureEvidenceRefs)
		}
	}
	if dirtyScopeRef == "" || !containsString(run.GitOpsFailureEvidenceRefs, "gitops-failure:gitops_dirty_worktree_scope") {
		t.Fatalf("expected hashed dirty-scope evidence refs, got %#v", run.GitOpsFailureEvidenceRefs)
	}
}

func TestHandleWorkPlanStatusChangedBlocksGitOpsWhenPostValidationLacksReviewOrVerifierRefs(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		task projectworkplan.WorkTask
	}{
		{
			name: "missing-review",
			task: projectworkplan.WorkTask{
				ID:                 "task-post-validation",
				ProjectID:          "project-1",
				PlanID:             "plan-post-validation",
				TaskRef:            "task-post-validation",
				Status:             projectworkplan.WorkTaskStatusDone,
				VerifierResultRefs: []string{"verifier:task-post-validation"},
			},
		},
		{
			name: "missing-verifier",
			task: projectworkplan.WorkTask{
				ID:               "task-post-validation",
				ProjectID:        "project-1",
				PlanID:           "plan-post-validation",
				TaskRef:          "task-post-validation",
				Status:           projectworkplan.WorkTaskStatusDone,
				ReviewResultRefs: []string{"review:task-post-validation"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			workPlans := &fakeWorkPlans{
				tasksByID: map[string]projectworkplan.WorkTask{
					"task-post-validation": tc.task,
				},
			}
			finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}}
			svc := New(store, workflows, workPlans, []Config{testConfig()})
			svc.SetGitOpsFinalizer(finalizer)
			svc.newID = deterministicIDs("workflow_chain_run_1")

			result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
				if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
					t.Fatalf("advance after %s done: %v", planID, err)
				}
			}
			if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected missing post-validation refs to block GitOps, got %v", err)
			}
			run, err := svc.Get(ctx, "project-1", result.ChainRunID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if run.Status != ChainStatusBlocked || !run.GitOpsReady || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed_invalid_project_workflow_chain_input") {
				t.Fatalf("expected GitOps-ready blocked chain for missing post-validation refs, got %#v", run)
			}
			if len(finalizer.inputs) != 0 {
				t.Fatalf("GitOps finalizer must not run without post-validation refs, got %#v", finalizer.inputs)
			}
		})
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenDraftPRFinalizerCreatesNoOutput(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		result GitOpsFinalizeResult
	}{
		{name: "missing-pr", result: GitOpsFinalizeResult{CommitRef: "commit/GENERIC-1044"}},
		{name: "no-changes", result: GitOpsFinalizeResult{NoChanges: true}},
		{name: "skipped", result: GitOpsFinalizeResult{Skipped: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			workPlans := &fakeWorkPlans{}
			svc := New(store, workflows, workPlans, []Config{testConfig()})
			svc.SetGitOpsFinalizer(&fakeGitOpsFinalizer{result: tc.result})
			svc.newID = deterministicIDs("workflow_chain_run_1")

			result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
				if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
					t.Fatalf("advance after %s done: %v", planID, err)
				}
			}
			if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected no-output GitOps finalization to block, got %v", err)
			}
			run, err := svc.Get(ctx, "project-1", result.ChainRunID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if run.Status != ChainStatusBlocked || !run.GitOpsReady || run.PullRequestRef != "" || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed") {
				t.Fatalf("expected blocked GitOps-ready chain without PR ref, got %#v", run)
			}
		})
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenDraftPRFinalizerReturnsNonActionablePRRef(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetGitOpsFinalizer(&fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "pr/GENERIC-1044"}})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected non-actionable PR ref to block, got %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get blocked run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || run.PullRequestRef != "" || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed_invalid_project_workflow_chain_input") {
		t.Fatalf("non-actionable PR ref must block without publishing PullRequestRef, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedDoesNotImplicitlyRetryBlockedDraftPRFinalization(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{err: errors.New("git worktree failed: unsafe path")}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err == nil {
		t.Fatalf("expected first GitOps finalization failure")
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get blocked run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed_git_worktree_failed") {
		t.Fatalf("expected blocked run with safe reason, got %#v", run)
	}

	finalizer.err = nil
	finalizer.result = GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("duplicate post-validation event: %v", err)
	}
	run, err = svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get duplicate-event run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || run.PullRequestRef != "" || run.GitOpsAttemptCount != 1 {
		t.Fatalf("duplicate status events must not implicitly retry GitOps, got %#v", run)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("duplicate status event must not call finalizer again, got %#v", finalizer.inputs)
	}

	retried, err := svc.RetryGitOps(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("explicit RetryGitOps returned error: %v", err)
	}
	if retried.Status != ChainStatusCompleted || retried.GitOpsReady || retried.PullRequestRef != "github-pr-1044" {
		t.Fatalf("expected explicit retry to complete chain with PR ref, got %#v", retried)
	}
	if len(finalizer.inputs) != 2 || finalizer.inputs[1].WorkPlan.ID != "plan-implementation" {
		t.Fatalf("expected explicit retry to finalize implementation plan, got %#v", finalizer.inputs)
	}
}

func TestRetryGitOpsFinalizesBlockedReadyChainThroughPublicAPI(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{err: errors.New("git push failed")}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "orchestrator-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); planID == "plan-post-validation" {
			if err == nil {
				t.Fatalf("expected initial GitOps failure")
			}
		} else if err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	blocked, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get blocked chain: %v", err)
	}
	if blocked.Status != ChainStatusBlocked || !blocked.GitOpsReady || !allStagesCompleted(blocked) {
		t.Fatalf("expected blocked GitOps-ready chain before retry, got %#v", blocked)
	}
	if blocked.GitOpsAttemptCount != 1 || blocked.GitOpsRecoveryStatus != GitOpsRecoveryStatusRepairable || blocked.GitOpsFailureCategory == "" || len(blocked.GitOpsFailureEvidenceRefs) == 0 {
		t.Fatalf("expected structured repairable GitOps failure before retry, got %#v", blocked)
	}

	finalizer.err = nil
	finalizer.result = GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}
	retried, err := svc.RetryGitOps(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("RetryGitOps returned error: %v", err)
	}
	if retried.Status != ChainStatusCompleted || retried.GitOpsReady || retried.PullRequestRef != "github-pr-1044" {
		t.Fatalf("expected direct retry to complete chain with PR ref, got %#v", retried)
	}
	if retried.GitOpsRecoveryStatus != GitOpsRecoveryStatusCompleted || retried.GitOpsFailureCategory != "" || len(retried.GitOpsFailureEvidenceRefs) != 0 {
		t.Fatalf("expected successful retry to clear failure metadata and mark recovery completed, got %#v", retried)
	}
	if len(finalizer.inputs) != 2 {
		t.Fatalf("expected initial failure plus direct retry finalization, got %#v", finalizer.inputs)
	}
	retryInput := finalizer.inputs[1]
	if retryInput.WorkPlan.ID != "plan-implementation" || retryInput.InputRef != "jira:GENERIC-1044" || retryInput.CreatedByRunID != "orchestrator-1" || retryInput.TraceID != "trace-1" {
		t.Fatalf("direct GitOps retry lost implementation plan or root refs: %#v", retryInput)
	}
	if !containsString(retryInput.AllowedPathspecs, "internal/projectworkflowchain/service.go") || !containsString(retryInput.ReviewRefs, "review:task-post-validation") || !containsString(retryInput.VerifierRefs, "verifier:task-post-validation") {
		t.Fatalf("direct GitOps retry lost implementation scope or validation evidence: %#v", retryInput)
	}
}

func TestRetryGitOpsStopsAfterTerminalRecoveryExhaustion(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{err: fmt.Errorf("%w: abcdef123456", projectgitops.ErrVerificationFailed)}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone})
		if planID == "plan-post-validation" {
			if err == nil {
				t.Fatalf("expected first GitOps finalization failure")
			}
			continue
		}
		if err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	for attempt := 2; attempt <= maxChainGitOpsRecoveryAttempts; attempt++ {
		if _, err := svc.RetryGitOps(ctx, "project-1", result.ChainRunID); err == nil {
			t.Fatalf("expected retry attempt %d to fail", attempt)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get exhausted run: %v", err)
	}
	if run.GitOpsAttemptCount != maxChainGitOpsRecoveryAttempts || run.GitOpsRecoveryStatus != GitOpsRecoveryStatusTerminal {
		t.Fatalf("expected exhausted terminal GitOps recovery, got %#v", run)
	}
	if !strings.Contains(run.StageRuns[2].BlockedReason, "terminal_attempt_3") {
		t.Fatalf("expected terminal blocked reason to name exhausted attempt, got %q", run.StageRuns[2].BlockedReason)
	}
	finalizer.err = nil
	finalizer.result = GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}
	if _, err := svc.RetryGitOps(ctx, "project-1", result.ChainRunID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected retry after terminal exhaustion to be rejected, got %v", err)
	}
	if len(finalizer.inputs) != maxChainGitOpsRecoveryAttempts {
		t.Fatalf("terminal retry must not call finalizer again, got %d calls", len(finalizer.inputs))
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenNextStageCannotCompile(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows(), failWorkflowID: "workflow-implementation"}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatalf("expected next-stage compile failure")
	}
	run, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get run: %v", getErr)
	}
	if run.Status != ChainStatusBlocked || run.StageRuns[1].Status != StageStatusBlocked || run.StageRuns[1].BlockedReason == "" {
		t.Fatalf("expected blocked chain and implementation stage, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedBlocksChainWhenStagePlanBlocks(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusBlocked}); err != nil {
		t.Fatalf("block chain: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || run.StageRuns[0].Status != StageStatusBlocked || run.StageRuns[1].Status == StageStatusQueued {
		t.Fatalf("expected blocked chain without next-stage advancement, got %#v", run)
	}
	if run.StageRuns[0].BlockedReason != "work_plan_blocked" || run.NextAction == "" {
		t.Fatalf("expected safe blocked reason and next action, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedFailsChainWhenStagePlanFails(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusFailed}); err != nil {
		t.Fatalf("fail chain: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusFailed || run.StageRuns[0].Status != StageStatusFailed || run.StageRuns[0].BlockedReason != "work_plan_failed" {
		t.Fatalf("expected failed chain and failed stage, got %#v", run)
	}
}

func TestStartRejectsUnknownWorkflowRef(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{workflows: enabledWorkflows()[:1]}, &fakeWorkPlans{}, []Config{testConfig()})
	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", DryRun: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unknown workflow rejection, got %v", err)
	}
}

func testConfig() Config {
	return Config{
		ProjectID:            "project-1",
		ChainRef:             "chain-1",
		Enabled:              true,
		InputKind:            InputKindJiraIssueKey,
		InputPattern:         "^GENERIC-[0-9]+$",
		ContextProvider:      ContextProviderJira,
		DefaultTitleTemplate: "{{input_ref}} governed delivery",
		GitOpsMode:           GitOpsModeDraftPRAfterValidation,
		GitOpsEnabled:        true,
		Stages: []StageConfig{
			{StageRef: "decomposition", WorkflowRef: "governed-decomposition-planning", Trigger: TriggerOnChainStart, RequiredStatusBeforeNext: StageStatusCompleted},
			{StageRef: "implementation", WorkflowRef: "governed-workplan-implementation", Trigger: TriggerAfterStageReviewPassed, DependsOn: []string{"decomposition"}, RequiredStatusBeforeNext: StageStatusCompleted},
			{StageRef: "post-validation", WorkflowRef: "governed-post-implementation-validation", Trigger: TriggerAfterStageReviewPassed, DependsOn: []string{"implementation"}, RequiredStatusBeforeNext: StageStatusCompleted},
		},
	}
}

func localIngestedTestConfig() Config {
	cfg := testConfig()
	cfg.ContextMode = ContextModeLocalIngested
	return cfg
}

func genericSafeRefTestConfig() Config {
	cfg := testConfig()
	cfg.ChainRef = "generic-chain"
	cfg.InputKind = InputKindSafeRef
	cfg.InputPattern = "^ticket/GENERIC-[0-9]+$"
	cfg.ContextProvider = ContextProviderIndexedRepo
	return cfg
}

func enabledWorkflows() []projectworkflow.WorkflowDefinition {
	return []projectworkflow.WorkflowDefinition{
		{ID: "workflow-decomposition", ProjectID: "project-1", WorkflowRef: "governed-decomposition-planning", Status: projectworkflow.WorkflowStatusEnabled},
		{ID: "workflow-implementation", ProjectID: "project-1", WorkflowRef: "governed-workplan-implementation", Status: projectworkflow.WorkflowStatusEnabled},
		{ID: "workflow-validation", ProjectID: "project-1", WorkflowRef: "governed-post-implementation-validation", Status: projectworkflow.WorkflowStatusEnabled},
	}
}

type fakeWorkflowAPI struct {
	workflows      []projectworkflow.WorkflowDefinition
	failWorkflowID string
	compileInputs  []projectworkflow.WorkflowCompileInput
}

func (fake *fakeWorkflowAPI) ListWorkflows(_ context.Context, filter projectworkflow.WorkflowFilter) ([]projectworkflow.WorkflowDefinition, error) {
	var out []projectworkflow.WorkflowDefinition
	for _, workflow := range fake.workflows {
		if workflow.ProjectID == filter.ProjectID && workflow.WorkflowRef == filter.WorkflowRef && workflow.Status == filter.Status {
			out = append(out, workflow)
		}
	}
	return out, nil
}

func (fake *fakeWorkflowAPI) CompileWorkflow(_ context.Context, input projectworkflow.WorkflowCompileInput) (projectworkflow.WorkflowCompileResult, error) {
	fake.compileInputs = append(fake.compileInputs, input)
	if input.WorkflowID == fake.failWorkflowID {
		return projectworkflow.WorkflowCompileResult{}, errors.New("compile failed")
	}
	stage := "unknown"
	switch input.WorkflowID {
	case "workflow-decomposition":
		stage = "decomposition"
	case "workflow-implementation":
		stage = "implementation"
	case "workflow-validation":
		stage = "post-validation"
	}
	return projectworkflow.WorkflowCompileResult{
		WorkflowID:    input.WorkflowID,
		WorkPlanID:    "plan-" + stage,
		WorkTaskIDs:   []string{"task-" + stage},
		AutomationIDs: []string{"automation-" + stage},
		DryRun:        input.DryRun,
	}, nil
}

type fakeLocalContextReader struct {
	result projectintegrations.RichContentReadResult
	err    error
}

func (fake fakeLocalContextReader) ReadLocalContent(_ context.Context, _ projectintegrations.LocalReadInput) (projectintegrations.RichContentReadResult, error) {
	if fake.err != nil {
		return projectintegrations.RichContentReadResult{}, fake.err
	}
	return fake.result, nil
}

type realChainStatusFanout struct {
	handlers []projectworkplan.WorkPlanStatusChangeHandler
}

func (fanout realChainStatusFanout) HandleWorkPlanStatusChanged(ctx context.Context, change projectworkplan.WorkPlanStatusChange) error {
	for _, handler := range fanout.handlers {
		if handler == nil {
			continue
		}
		if err := handler.HandleWorkPlanStatusChanged(ctx, change); err != nil {
			return err
		}
	}
	return nil
}

type realChainPermissionResolver struct {
	allowedRunnerKinds []string
}

func (resolver realChainPermissionResolver) CheckAutomationPermission(_ context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      input.PermissionRef,
		AgentID:            input.AgentID,
		AllowedRunnerKinds: append([]string(nil), resolver.allowedRunnerKinds...),
	}, nil
}

func chainStageRunByRef(t *testing.T, run ChainRun, stageRef string) StageRun {
	t.Helper()
	for _, stageRun := range run.StageRuns {
		if stageRun.StageRef == stageRef {
			return stageRun
		}
	}
	t.Fatalf("stage %q not found in %#v", stageRef, run.StageRuns)
	return StageRun{}
}

func assertQueuedAutomationRunsForGeneratedStage(t *testing.T, ctx context.Context, svc *projectautomation.Service, projectID string, stageRun StageRun) {
	t.Helper()
	runs, err := svc.ListRuns(ctx, projectautomation.RunFilter{ProjectID: projectID, PlanID: stageRun.WorkPlanID, Status: projectautomation.RunStatusQueued})
	if err != nil {
		t.Fatalf("list queued runs for %s: %v", stageRun.StageRef, err)
	}
	if len(runs) == 0 {
		t.Fatalf("stage %s did not queue any automation runs", stageRun.StageRef)
	}
	taskIDs := map[string]struct{}{}
	for _, taskID := range stageRun.WorkTaskIDs {
		taskIDs[taskID] = struct{}{}
	}
	for _, run := range runs {
		if run.OrchestratorRunID == "" || run.TaskID == "" || run.WorkTaskStatus != projectworkplan.WorkTaskStatusReady {
			t.Fatalf("queued automation run lost live refs/status for %s: %#v", stageRun.StageRef, run)
		}
		if _, ok := taskIDs[run.TaskID]; !ok {
			t.Fatalf("queued automation run references task outside generated stage %s: %#v", stageRun.StageRef, run)
		}
	}
}

func completeGeneratedStagePlan(t *testing.T, ctx context.Context, svc *projectworkplan.Service, projectID string, planID string, stageRef string) {
	t.Helper()
	tasks, err := svc.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		t.Fatalf("list stage tasks for %s: %v", stageRef, err)
	}
	if len(tasks) == 0 {
		t.Fatalf("stage %s has no generated tasks", stageRef)
	}
	for _, task := range tasks {
		if stageRef == "implementation" && len(task.FilesToEdit) == 0 {
			task.FilesToEdit = []string{"internal/projectworkflowchain/service.go", "cmd/mivia-automation-runner/main_test.go"}
		}
		task.Status = projectworkplan.WorkTaskStatusDone
		task.Outcome = "generated " + stageRef + " task output accepted"
		task.EvidenceRefs = appendUnique(task.EvidenceRefs, "evidence:generated-"+stageRef)
		task.ReviewResultRefs = appendUnique(task.ReviewResultRefs, "review:generated-"+stageRef)
		task.VerifierResultRefs = appendUnique(task.VerifierResultRefs, "verifier:generated-"+stageRef)
		task.ClaimRefs = appendUnique(task.ClaimRefs, "claim:generated-"+stageRef)
		if _, err := svc.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("complete generated task %s/%s: %v", stageRef, task.TaskRef, err)
		}
	}
	if _, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      projectID,
		PlanID:         planID,
		Status:         projectworkplan.WorkPlanStatusDone,
		Outcome:        "generated " + stageRef + " stage output accepted",
		SafeNextAction: "advance generic chain after generated stage completion",
		RunID:          "complete-" + stageRef,
		TraceID:        "trace-generic-real-2044",
	}); err != nil {
		t.Fatalf("mark stage plan %s done: %v", stageRef, err)
	}
}

func completeGeneratedStagePlanWithOpenChildren(t *testing.T, ctx context.Context, svc *projectworkplan.Service, store *workplanstore.MemoryStore, chain *Service, projectID string, planID string, stageRef string) {
	t.Helper()
	tasks, err := svc.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		t.Fatalf("list stage tasks for %s: %v", stageRef, err)
	}
	for _, task := range tasks {
		if !workflowChainWrapperTaskRef(task.TaskRef) && !strings.HasPrefix(task.TaskRef, "review-") {
			continue
		}
		task.Status = projectworkplan.WorkTaskStatusDone
		task.Outcome = "generated " + stageRef + " wrapper output accepted"
		task.EvidenceRefs = appendUnique(task.EvidenceRefs, "evidence:generated-"+stageRef)
		task.ReviewResultRefs = appendUnique(task.ReviewResultRefs, "review:generated-"+stageRef)
		task.VerifierResultRefs = appendUnique(task.VerifierResultRefs, "verifier:generated-"+stageRef)
		task.ClaimRefs = appendUnique(task.ClaimRefs, "claim:generated-"+stageRef)
		if _, err := svc.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("complete generated wrapper task %s/%s: %v", stageRef, task.TaskRef, err)
		}
	}
	plan, err := store.GetWorkPlan(ctx, projectID, planID)
	if err != nil {
		t.Fatalf("get stage plan %s: %v", stageRef, err)
	}
	oldStatus := plan.Status
	plan.Status = projectworkplan.WorkPlanStatusDone
	plan.Outcome = "generated " + stageRef + " stage output accepted"
	plan.ResumeSummary = "advance generic chain after generated stage completion"
	updated, err := store.UpdateWorkPlan(ctx, plan)
	if err != nil {
		t.Fatalf("mark stage plan %s done in store: %v", stageRef, err)
	}
	if err := chain.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID:  projectID,
		PlanID:     planID,
		PlanRef:    updated.PlanRef,
		OldStatus:  oldStatus,
		NewStatus:  updated.Status,
		OwnerAgent: updated.OwnerAgent,
		ChangedAt:  updated.UpdatedAt,
	}); err != nil {
		t.Fatalf("advance chain after generated stage %s: %v", stageRef, err)
	}
}

func workflowChainWrapperTaskRef(ref string) bool {
	switch strings.TrimSpace(ref) {
	case "discover-planning-context", "decompose-work-plan", "mark-ready-after-review", "select-ready-tasks", "run-implementation-batch", "review-implementation-batch", "orchestrator-verification", "pr-gitops-readiness", "post-implementation-validation":
		return true
	default:
		return false
	}
}

func localJiraContext(issueKey string, includeScope bool) projectintegrations.RichContentReadResult {
	chunks := []projectintegrations.RichContentChunkView{{
		ItemKey:   issueKey,
		FieldName: "summary",
		Text:      "Implement bounded automation ticket delivery",
	}}
	if includeScope {
		chunks = append(chunks, projectintegrations.RichContentChunkView{
			ItemKey:   issueKey,
			FieldName: "description",
			Text:      "Acceptance criteria: decompose, implement, verify, and open a draft PR. Source anchors and verifier scope identify the implementation evidence.",
		})
	}
	return projectintegrations.RichContentReadResult{
		Artifact: projectintegrations.RichContentArtifact{
			ID:      "integration-artifact-1",
			ItemID:  "10001",
			ItemKey: issueKey,
		},
		Chunks: chunks,
	}
}

func localJiraContextWithoutImplementationEvidence(issueKey string) projectintegrations.RichContentReadResult {
	result := localJiraContext(issueKey, false)
	result.Chunks = append(result.Chunks, projectintegrations.RichContentChunkView{
		ItemKey:   issueKey,
		FieldName: "description",
		Text:      "Acceptance criteria: deliver the requested behavior.",
	})
	return result
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func assertChainStageHandoff(t *testing.T, stage StageRun, stageRef string, workflowID string, planID string, taskID string, automationID string, status string) {
	t.Helper()
	if stage.StageRef != stageRef || stage.WorkflowID != workflowID || stage.WorkPlanID != planID || stage.Status != status {
		t.Fatalf("stage %s lost refs/status: %#v", stageRef, stage)
	}
	if len(stage.WorkTaskIDs) != 1 || stage.WorkTaskIDs[0] != taskID {
		t.Fatalf("stage %s lost work task refs: %#v", stageRef, stage)
	}
	if len(stage.AutomationIDs) != 1 || stage.AutomationIDs[0] != automationID {
		t.Fatalf("stage %s lost automation refs: %#v", stageRef, stage)
	}
	if status == StageStatusCompleted && stage.CompletedAt.IsZero() {
		t.Fatalf("stage %s completed without completed_at: %#v", stageRef, stage)
	}
}

func assertGenericCodexCompileHandoff(t *testing.T, input projectworkflow.WorkflowCompileInput, stage string) {
	t.Helper()
	if input.UserRequestRef != "input:ticket/GENERIC-1044" || input.CreatedByRunID != "codex-orchestrator-run-1" || input.TraceID != "trace-generic-1044" {
		t.Fatalf("compile input for %s lost generic/run/trace refs: %#v", stage, input)
	}
	if !containsString(input.ContextPackRefs, "repo:ticket/GENERIC-1044") {
		t.Fatalf("compile input for %s lost context refs: %#v", stage, input.ContextPackRefs)
	}
	if !strings.Contains(input.TitleOverride, "input:ticket/GENERIC-1044") || !strings.Contains(input.TitleOverride, stage) {
		t.Fatalf("compile input for %s lost usable title data: %q", stage, input.TitleOverride)
	}
}

func TestGitOpsFinalizeMetadataUsesExplicitEditScopeOnly(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{}, &fakeWorkPlans{}, []Config{testConfig()})
	run := ChainRun{
		ID:        "chain-run-1",
		ProjectID: "project-1",
		StageRuns: []StageRun{{
			StageRef:    "implementation",
			WorkTaskIDs: []string{"task-implementation"},
		}, {
			StageRef:    "post-validation",
			WorkTaskIDs: []string{"task-post-validation"},
		}},
	}

	metadata, err := svc.gitOpsFinalizeMetadata(ctx, run)
	if err != nil {
		t.Fatalf("gitOpsFinalizeMetadata returned error: %v", err)
	}
	if len(metadata.AllowedPathspecs) != 1 || metadata.AllowedPathspecs[0] != "internal/projectworkflowchain/service.go" {
		t.Fatalf("allowed pathspecs should use explicit edit scope only, got %#v", metadata.AllowedPathspecs)
	}
}

type fakeWorkPlans struct {
	activations       []string
	released          []string
	events            []string
	allTasksByPlan    map[string][]projectworkplan.WorkTask
	openTasksByPlan   map[string][]projectworkplan.WorkTask
	tasksByID         map[string]projectworkplan.WorkTask
	createdTasks      []projectworkplan.CreateWorkTaskInput
	statusUpdates     []projectworkplan.UpdateWorkPlanStatusInput
	taskStatusUpdates []projectworkplan.UpdateWorkTaskStatusInput
}

type fakeAutomationAPI struct {
	existing []projectautomation.Automation
	created  []projectautomation.Automation
}

func (fake *fakeAutomationAPI) CreateAutomation(_ context.Context, input projectautomation.CreateAutomationInput) (projectautomation.Automation, error) {
	automation := projectautomation.Automation{
		ID:              "automation-" + input.AutomationRef,
		ProjectID:       input.ProjectID,
		AutomationRef:   input.AutomationRef,
		Title:           input.Title,
		Purpose:         input.Purpose,
		Status:          input.Status,
		AgentID:         input.AgentID,
		PlanID:          input.PlanID,
		AllowedTaskRefs: append([]string(nil), input.AllowedTaskRefs...),
		TriggerKind:     input.TriggerKind,
		SourceKind:      input.SourceKind,
		SchedulePolicy:  input.SchedulePolicy,
		PermissionRef:   input.PermissionRef,
		CreatedByRunID:  input.CreatedByRunID,
		TraceID:         input.TraceID,
	}
	fake.created = append(fake.created, automation)
	return automation, nil
}

func (fake *fakeAutomationAPI) ListAutomations(_ context.Context, filter projectautomation.AutomationFilter) ([]projectautomation.Automation, error) {
	out := make([]projectautomation.Automation, 0, len(fake.existing))
	for _, automation := range fake.existing {
		if filter.ProjectID != "" && automation.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Status != "" && automation.Status != filter.Status {
			continue
		}
		if filter.AgentID != "" && automation.AgentID != filter.AgentID {
			continue
		}
		out = append(out, automation)
	}
	return out, nil
}

func (fake *fakeWorkPlans) GetWorkPlan(_ context.Context, projectID string, planID string) (projectworkplan.WorkPlan, error) {
	return projectworkplan.WorkPlan{ID: planID, ProjectID: projectID, Status: projectworkplan.WorkPlanStatusDone}, nil
}

func (fake *fakeWorkPlans) UpdateWorkPlanStatus(_ context.Context, input projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error) {
	fake.activations = append(fake.activations, input.PlanID)
	fake.events = append(fake.events, "activate:"+input.PlanID)
	fake.statusUpdates = append(fake.statusUpdates, input)
	return projectworkplan.WorkPlan{ID: input.PlanID, ProjectID: input.ProjectID, Status: input.Status}, nil
}

func (fake *fakeWorkPlans) GetWorkTask(_ context.Context, projectID string, taskID string) (projectworkplan.WorkTask, error) {
	if fake.tasksByID != nil {
		if task, ok := fake.tasksByID[taskID]; ok {
			return task, nil
		}
	}
	if fake.allTasksByPlan != nil {
		for _, tasks := range fake.allTasksByPlan {
			for _, task := range tasks {
				if task.ID == taskID {
					return task, nil
				}
			}
		}
	}
	if fake.openTasksByPlan != nil {
		for _, tasks := range fake.openTasksByPlan {
			for _, task := range tasks {
				if task.ID == taskID {
					return task, nil
				}
			}
		}
	}
	stage := strings.TrimPrefix(taskID, "task-")
	task := projectworkplan.WorkTask{
		ID:                   taskID,
		ProjectID:            projectID,
		PlanID:               "plan-" + stage,
		TaskRef:              taskID,
		Status:               projectworkplan.WorkTaskStatusDone,
		DecompositionQuality: projectworkplan.DecompositionReady,
		ReviewResultRefs:     []string{"review:" + taskID},
		VerifierResultRefs:   []string{"verifier:" + taskID},
	}
	if stage == "implementation" {
		task.FilesToEdit = []string{"internal/projectworkflowchain/service.go"}
		task.LikelyFilesAffected = []string{"cmd/mivia-server"}
	}
	return task, nil
}

func (fake *fakeWorkPlans) ListWorkTasks(_ context.Context, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	if fake.allTasksByPlan != nil {
		return append([]projectworkplan.WorkTask(nil), fake.allTasksByPlan[filter.PlanID]...), nil
	}
	return fake.ListOpenWorkTasks(context.Background(), filter)
}

func (fake *fakeWorkPlans) ListOpenWorkTasks(_ context.Context, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	if fake.openTasksByPlan != nil {
		return append([]projectworkplan.WorkTask(nil), fake.openTasksByPlan[filter.PlanID]...), nil
	}
	stage := "unknown"
	switch filter.PlanID {
	case "plan-decomposition":
		stage = "decomposition"
	case "plan-implementation":
		stage = "implementation"
	case "plan-post-validation":
		stage = "post-validation"
	}
	return []projectworkplan.WorkTask{{
		ID:                   "task-" + stage,
		ProjectID:            filter.ProjectID,
		PlanID:               filter.PlanID,
		TaskRef:              "task-" + stage,
		Status:               projectworkplan.WorkTaskStatusPlanned,
		DecompositionQuality: projectworkplan.DecompositionReady,
	}}, nil
}

func (fake *fakeWorkPlans) CreateWorkTask(_ context.Context, input projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error) {
	fake.createdTasks = append(fake.createdTasks, input)
	task := projectworkplan.WorkTask{
		ID:                      "created-" + input.TaskRef,
		ProjectID:               input.ProjectID,
		PlanID:                  input.PlanID,
		TaskRef:                 input.TaskRef,
		Title:                   input.Title,
		Description:             input.Description,
		Status:                  input.Status,
		OwnerAgent:              input.OwnerAgent,
		TraceID:                 input.TraceID,
		EvidenceNeeded:          append([]string(nil), input.EvidenceNeeded...),
		ContextPackRefs:         append([]string(nil), input.ContextPackRefs...),
		FilesToRead:             append([]string(nil), input.FilesToRead...),
		FilesToEdit:             append([]string(nil), input.FilesToEdit...),
		LikelyFilesAffected:     append([]string(nil), input.LikelyFilesAffected...),
		DependencyTaskIDs:       append([]string(nil), input.DependencyTaskIDs...),
		VerificationRequirement: input.VerificationRequirement,
		GitOpsVerificationMode:  input.GitOpsVerificationMode,
		ExpectedOutput:          input.ExpectedOutput,
		FailureCriteria:         input.FailureCriteria,
		ReviewGate:              input.ReviewGate,
		ResumeInstructions:      input.ResumeInstructions,
		EvidenceRefs:            append([]string(nil), input.EvidenceRefs...),
		ClaimRefs:               append([]string(nil), input.ClaimRefs...),
		VerifierResultRefs:      append([]string(nil), input.VerifierResultRefs...),
		ReviewResultRefs:        append([]string(nil), input.ReviewResultRefs...),
		ReviewExemptReason:      input.ReviewExemptReason,
		ArtifactRefs:            append([]string(nil), input.ArtifactRefs...),
		AgentRunIDs:             append([]string(nil), input.AgentRunIDs...),
		DecompositionQuality:    input.DecompositionQuality,
		AcceptanceCriteria:      append([]string(nil), input.AcceptanceCriteria...),
		StopConditions:          append([]string(nil), input.StopConditions...),
		VerifierLadder:          append([]string(nil), input.VerifierLadder...),
		RegressionApplicability: input.RegressionApplicability,
		DownstreamImpactRefs:    append([]string(nil), input.DownstreamImpactRefs...),
		OutputContract:          input.OutputContract,
	}
	if fake.openTasksByPlan != nil {
		fake.openTasksByPlan[input.PlanID] = append(fake.openTasksByPlan[input.PlanID], task)
	}
	if fake.allTasksByPlan != nil {
		fake.allTasksByPlan[input.PlanID] = append(fake.allTasksByPlan[input.PlanID], task)
	}
	return task, nil
}

func (fake *fakeWorkPlans) UpdateWorkTaskStatus(_ context.Context, input projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error) {
	fake.released = append(fake.released, input.TaskID)
	fake.events = append(fake.events, "release:"+input.TaskID)
	fake.taskStatusUpdates = append(fake.taskStatusUpdates, input)
	return projectworkplan.WorkTask{ID: input.TaskID, ProjectID: input.ProjectID, Status: input.Status}, nil
}

func deterministicIDs(values ...string) func(string) string {
	i := 0
	return func(prefix string) string {
		if i >= len(values) {
			return prefix + "_extra"
		}
		value := values[i]
		i++
		return value
	}
}

type fakeGitOpsFinalizer struct {
	result GitOpsFinalizeResult
	err    error
	inputs []GitOpsFinalizeInput
}

func (fake *fakeGitOpsFinalizer) FinalizeWorkflowChain(_ context.Context, input GitOpsFinalizeInput) (GitOpsFinalizeResult, error) {
	fake.inputs = append(fake.inputs, input)
	if fake.err != nil {
		return GitOpsFinalizeResult{}, fake.err
	}
	return fake.result, nil
}

type testChainStore struct {
	runs      map[string]ChainRun
	createErr error
	updateErr error
}

func newTestChainStore() *testChainStore {
	return &testChainStore{runs: map[string]ChainRun{}}
}

func (store *testChainStore) CreateChainRun(_ context.Context, run ChainRun) (ChainRun, error) {
	if store.createErr != nil {
		return ChainRun{}, store.createErr
	}
	store.runs[run.ID] = run
	return run, nil
}

func (store *testChainStore) GetChainRun(_ context.Context, _ string, chainRunID string) (ChainRun, error) {
	return store.runs[chainRunID], nil
}

func (store *testChainStore) ListChainRuns(_ context.Context, _ ChainFilter) ([]ChainRun, error) {
	out := make([]ChainRun, 0, len(store.runs))
	for _, run := range store.runs {
		out = append(out, run)
	}
	return out, nil
}

func (store *testChainStore) UpdateChainRun(_ context.Context, run ChainRun) (ChainRun, error) {
	if store.updateErr != nil {
		return ChainRun{}, store.updateErr
	}
	store.runs[run.ID] = run
	return run, nil
}

func (store *testChainStore) FindChainRunByWorkPlan(_ context.Context, _ string, workPlanID string) (ChainRun, error) {
	for _, run := range store.runs {
		for _, planID := range run.WorkPlanIDs {
			if planID == workPlanID {
				return run, nil
			}
		}
		for _, stage := range run.StageRuns {
			if stage.WorkPlanID == workPlanID {
				return run, nil
			}
		}
	}
	return ChainRun{}, errors.New("not found")
}
