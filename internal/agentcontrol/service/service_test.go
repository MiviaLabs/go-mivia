package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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
		Summary:      "implemented bounded context health",
		ChangedFiles: []string{"internal/projectreliability/service.go"},
		Verifiers: []model.AgentVerifier{{
			Command:    "go",
			Args:       []string{"test", "./internal/projectreliability"},
			Scope:      "focused",
			Status:     "passed",
			ExitStatus: 0,
		}},
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if run.Status != model.AgentRunStatusRunning || run.ProjectID != "example-service" {
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

	run, err = svc.CompleteAgentRun(context.Background(), run.ID, model.CompleteAgentRunInput{Status: model.AgentRunStatusCompleted})
	if err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if run.Status != model.AgentRunStatusCompleted || run.CompletedAt.IsZero() {
		t.Fatalf("unexpected completed run: %#v", run)
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
