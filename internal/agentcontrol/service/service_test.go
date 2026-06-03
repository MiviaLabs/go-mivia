package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/service"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
)

func TestCreateTask_ValidInput_PersistsTask(t *testing.T) {
	svc := service.New(store.NewMemoryStore(), store.NewMemoryStore())

	task, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: "  Valid task  "})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Title != "Valid task" || task.Status != model.TaskStatusPending {
		t.Fatalf("unexpected task: %#v", task)
	}
}

func TestCreateTask_EmptyTitle_ReturnsInvalidInput(t *testing.T) {
	svc := service.New(store.NewMemoryStore(), store.NewMemoryStore())

	_, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: " "})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestCreateTask_TooLongTitle_ReturnsInvalidInput(t *testing.T) {
	svc := service.New(store.NewMemoryStore(), store.NewMemoryStore())

	_, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: strings.Repeat("a", 201)})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestCreateTask_ObviousPII_ReturnsInvalidInput(t *testing.T) {
	svc := service.New(store.NewMemoryStore(), store.NewMemoryStore())

	_, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: "Contact person@example.com"})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestCreateResearchRun_MissingTask_ReturnsNotFound(t *testing.T) {
	svc := service.New(store.NewMemoryStore(), store.NewMemoryStore())

	_, err := svc.CreateResearchRun(context.Background(), model.CreateResearchRunInput{
		TaskID:      "task_missing",
		GoalSummary: "Summarize approved fixture metadata",
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestCreateResearchRun_RawQuerySummary_ReturnsInvalidInput(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	task, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: "Parent"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = svc.CreateResearchRun(context.Background(), model.CreateResearchRunInput{
		TaskID:      task.ID,
		GoalSummary: "MATCH (n) RETURN n",
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestCreateResearchRun_ObviousPIISummary_ReturnsInvalidInput(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	task, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: "Parent"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = svc.CreateResearchRun(context.Background(), model.CreateResearchRunInput{
		TaskID:      task.ID,
		GoalSummary: "Call +1 555 123 4567",
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestTaskTransitions_ValidLifecycle_UpdatesStatus(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	task, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: "Lifecycle"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	running, err := svc.StartTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if running.Status != model.TaskStatusRunning {
		t.Fatalf("expected running, got %s", running.Status)
	}

	done, err := svc.CompleteTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if done.Status != model.TaskStatusDone {
		t.Fatalf("expected done, got %s", done.Status)
	}
}

func TestTaskTransitions_InvalidTransition_ReturnsInvalidInput(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	task, err := svc.CreateTask(context.Background(), model.CreateTaskInput{Title: "Lifecycle"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = svc.CompleteTask(context.Background(), task.ID)
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
}

func TestAgentRunLifecycle_RedactedMetadataOnly(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)

	run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID:    "example-service",
		TraceID:      "trace_manual_1",
		Summary:      "implemented bounded context health",
		ChangedFiles: []string{"internal/projectreliability/service.go"},
		Verifiers: []model.AgentVerifier{{
			Command:    "go",
			Args:       []string{"test", "./internal/projectreliability"},
			Scope:      "focused",
			Status:     "passed",
			ExitStatus: 0,
		}},
		Artifacts: []model.AgentArtifact{{
			Ref:  "finding-authz-boundary",
			Kind: "finding",
		}},
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if run.Status != model.AgentRunStatusRunning || run.ProjectID != "example-service" || run.TraceID != "trace_manual_1" {
		t.Fatalf("unexpected run: %#v", run)
	}

	run, err = svc.AppendAgentStep(context.Background(), run.ID, model.AppendAgentStepInput{
		ToolName:     "go",
		ToolCategory: "test",
		Status:       model.AgentRunStatusCompleted,
		Notes:        "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("append step: %v", err)
	}
	if len(run.Steps) != 1 || run.Steps[0].Notes != "focused verifier passed" {
		t.Fatalf("unexpected steps: %#v", run.Steps)
	}
	if run.Steps[0].TraceID != run.TraceID {
		t.Fatalf("expected step trace to inherit run trace, got %#v", run.Steps[0])
	}

	run, err = svc.PromoteAgentArtifact(context.Background(), run.ID, model.PromoteAgentArtifactInput{
		ArtifactRef: "finding-authz-boundary",
		State:       model.PromotionStateValidated,
		SourceRef:   run.Steps[0].ID,
		VerifierRef: "go/test/internal/projectreliability",
		Decision:    "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("promote artifact: %v", err)
	}
	if len(run.Promotions) != 1 || run.Promotions[0].State != model.PromotionStateValidated || run.Promotions[0].ArtifactKind != "finding" {
		t.Fatalf("unexpected promotions: %#v", run.Promotions)
	}

	run, err = svc.CompleteAgentRun(context.Background(), run.ID, model.CompleteAgentRunInput{Status: model.AgentRunStatusCompleted})
	if err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if run.Status != model.AgentRunStatusCompleted || run.CompletedAt.IsZero() {
		t.Fatalf("unexpected completed run: %#v", run)
	}
}

func TestAgentRunLifecycleRecordsTraceActivity(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	recorder := agentactivity.NewRecorder(10)
	svc.SetPolicyRecorder(recorder)

	run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID: "example-service",
		Summary:   "bounded trace metadata",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.TraceID != run.ID {
		t.Fatalf("expected run id as default trace id, got %#v", run)
	}
	run, err = svc.AppendAgentStep(context.Background(), run.ID, model.AppendAgentStepInput{
		ToolName: "go",
		Status:   model.AgentRunStatusCompleted,
	})
	if err != nil {
		t.Fatalf("append step: %v", err)
	}
	_, err = svc.CompleteAgentRun(context.Background(), run.ID, model.CompleteAgentRunInput{Status: model.AgentRunStatusCompleted})
	if err != nil {
		t.Fatalf("complete run: %v", err)
	}

	events := recorder.Recent("example-service", 10)
	if len(events) != 3 {
		t.Fatalf("expected lifecycle events, got %#v", events)
	}
	for _, event := range events {
		if event.TraceID != run.TraceID || event.RunID != run.ID || event.CorrelationKind != "agent_run" {
			t.Fatalf("expected correlated lifecycle event, got %#v", event)
		}
	}
	if events[0].EventKind != "agent_run_started" || events[1].EventKind != "agent_step" || events[2].EventKind != "agent_run_completed" {
		t.Fatalf("unexpected lifecycle order: %#v", events)
	}
}

func TestAgentRunRejectsRawPromptSourceSecretsAndRoots(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)

	for _, input := range []model.CreateAgentRunInput{
		{ProjectID: "example-service", Summary: "raw prompt: do the thing"},
		{ProjectID: "example-service", Summary: "package main\nfunc main() {}\n"},
		{ProjectID: "example-service", Summary: "token=secret"},
		{ProjectID: "example-service", ChangedFiles: []string{"/home/mac/project/main.go"}},
	} {
		_, err := svc.CreateAgentRun(context.Background(), input)
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected invalid input for %#v, got %v", input, err)
		}
	}
}

func TestPromoteAgentArtifact_RejectsUnknownUnsafeOrUnverifiedDecisions(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID: "example-service",
		Artifacts: []model.AgentArtifact{{Ref: "artifact-1", Kind: "evidence"}},
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	cases := []model.PromoteAgentArtifactInput{
		{ArtifactRef: "missing", State: model.PromotionStateCandidate, SourceRef: "agent_step_1"},
		{ArtifactRef: "artifact-1", State: model.PromotionStatePromoted, SourceRef: "agent_step_1"},
		{ArtifactRef: "artifact-1", State: model.PromotionStateValidated, SourceRef: "agent_step_1", VerifierRef: "go/test"},
		{ArtifactRef: "artifact-1", State: model.PromotionStateRejected, SourceRef: "agent_step_1", VerifierRef: "go/test", Decision: "raw source package main"},
		{ArtifactRef: "artifact-1", State: model.PromotionStateCandidate, SourceRef: "/home/mac/project"},
		{ArtifactRef: "artifact-1", State: model.PromotionStateCandidate, SourceRef: "../project"},
		{ArtifactRef: "artifact-1", State: "approved", SourceRef: "agent_step_1", VerifierRef: "go/test"},
	}
	for _, input := range cases {
		_, err := svc.PromoteAgentArtifact(context.Background(), run.ID, input)
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected invalid input for %#v, got %v", input, err)
		}
	}
}

func TestPromoteAgentArtifact_AllowsNumericLookingInternalSourceRef(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID: "example-service",
		Artifacts: []model.AgentArtifact{{Ref: "artifact-1", Kind: "evidence"}},
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	run, err = svc.PromoteAgentArtifact(context.Background(), run.ID, model.PromoteAgentArtifactInput{
		ArtifactRef: "artifact-1",
		State:       model.PromotionStateCandidate,
		SourceRef:   "agent_step_12345678901234567890",
	})
	if err != nil {
		t.Fatalf("promote artifact: %v", err)
	}
	if len(run.Promotions) != 1 || run.Promotions[0].SourceRef != "agent_step_12345678901234567890" {
		t.Fatalf("unexpected promotions: %#v", run.Promotions)
	}
}

func TestAgentRunArtifactsRejectRootOrTraversalRefs(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	for _, ref := range []string{"/home/mac/project", "../project"} {
		_, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
			ProjectID: "example-service",
			Artifacts: []model.AgentArtifact{{
				Ref:  ref,
				Kind: "evidence",
			}},
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected invalid artifact ref %q, got %v", ref, err)
		}
	}
}

func TestAgentRunVerifierArgsAllowLoopbackURLsOnly(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)

	run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID: "example-service",
		Verifiers: []model.AgentVerifier{{
			Command: "curl",
			Args:    []string{"-sS", "http://127.0.0.1:8080/readyz", "http://localhost:8080/healthz"},
			Status:  "passed",
		}},
	})
	if err != nil {
		t.Fatalf("expected loopback verifier args to be accepted: %v", err)
	}
	if got := run.Verifiers[0].Args[1]; got != "http://127.0.0.1:8080/readyz" {
		t.Fatalf("unexpected verifier arg: %q", got)
	}

	for _, arg := range []string{
		"https://example.com/readyz",
		"http://127.0.0.1:8080/readyz?token=secret",
		"http://user:pass@127.0.0.1:8080/readyz",
	} {
		_, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
			ProjectID: "example-service",
			Verifiers: []model.AgentVerifier{{
				Command: "curl",
				Args:    []string{arg},
			}},
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected unsafe verifier arg %q to be rejected, got %v", arg, err)
		}
	}
}

func TestAgentRunPolicyEventsRecordUnsafeEditAndInvalidVerifierURL(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	recorder := agentactivity.NewRecorder(10)
	svc.SetPolicyRecorder(recorder)

	_, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID:    "example-service",
		ChangedFiles: []string{"../secret.txt"},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected unsafe changed file to be rejected, got %v", err)
	}
	_, err = svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID: "example-service",
		Verifiers: []model.AgentVerifier{{
			Command: "curl",
			Args:    []string{"https://example.com/readyz"},
		}},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected unsafe verifier url to be rejected, got %v", err)
	}

	events := recorder.Recent("example-service", 10)
	if len(events) != 2 {
		t.Fatalf("expected two policy events, got %#v", events)
	}
	if events[0].PolicyCategory != "unsafe_edit" || events[1].PolicyCategory != "invalid_verifier_url" {
		t.Fatalf("expected normalized policy categories, got %#v", events)
	}
}

func TestAgentRunVerifierCommandSplitsSimpleWordsIntoArgs(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
		ProjectID: "example-service",
		Summary:   "bounded metadata",
		Verifiers: []model.AgentVerifier{{
			Command: "go test",
			Args:    []string{"./internal/agentcontrol/service"},
			Status:  "passed",
		}},
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if len(run.Verifiers) != 1 {
		t.Fatalf("expected verifier, got %#v", run.Verifiers)
	}
	verifier := run.Verifiers[0]
	if verifier.Command != "go" {
		t.Fatalf("expected command to be normalized to go, got %#v", verifier)
	}
	if len(verifier.Args) != 2 || verifier.Args[0] != "test" || verifier.Args[1] != "./internal/agentcontrol/service" {
		t.Fatalf("expected split command args, got %#v", verifier.Args)
	}
}

func TestAgentRunVerifierArgsAllowSupportedLanguageSelectors(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	cases := []model.AgentVerifier{
		{Command: "go", Args: []string{"test", "./internal/projectworkplan/..."}, Scope: "go focused packages", Status: "passed"},
		{Command: "pytest", Args: []string{"tests/test_api.py::test_create_plan"}, Scope: "python pytest node id", Status: "passed"},
		{Command: "npm", Args: []string{"test", "--workspace=@mivia/dashboard", "--", "work-plan.spec.ts"}, Scope: "javascript workspace test", Status: "passed"},
		{Command: "pnpm", Args: []string{"vitest", "run", "src/work-plan.spec.tsx"}, Scope: "typescript react spec", Status: "passed"},
		{Command: "dotnet", Args: []string{"test", "tests/Mivia.AgentControl.Tests/Mivia.AgentControl.Tests.csproj"}, Scope: "csharp project test", Status: "passed"},
		{Command: "dart", Args: []string{"test", "test/work_plan_test.dart"}, Scope: "dart package test", Status: "passed"},
	}
	for _, verifier := range cases {
		run, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
			ProjectID: "example-service",
			Verifiers: []model.AgentVerifier{verifier},
		})
		if err != nil {
			t.Fatalf("expected verifier %#v to be accepted: %v", verifier, err)
		}
		if run.Verifiers[0].Scope != verifier.Scope {
			t.Fatalf("expected scope %q to be preserved, got %#v", verifier.Scope, run.Verifiers[0])
		}
	}
}

func TestAgentRunVerifierArgsRejectTraversalAndRoots(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	for _, arg := range []string{"../secret", "tests/../secret", "/home/mac/project", "C:/Users/PC/project"} {
		_, err := svc.CreateAgentRun(context.Background(), model.CreateAgentRunInput{
			ProjectID: "example-service",
			Verifiers: []model.AgentVerifier{{
				Command: "go",
				Args:    []string{"test", arg},
				Status:  "failed",
			}},
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected unsafe verifier arg %q to be rejected, got %v", arg, err)
		}
	}
}
