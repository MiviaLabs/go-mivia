package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/service"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
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
