package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
)

func TestLadybugStore_CreateAndGetTask(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	taskStore := store.NewLadybugStore(graph)
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	created, err := taskStore.CreateTask(context.Background(), model.Task{
		ID:        "task_test",
		Title:     "Test task",
		Status:    model.TaskStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	fetched, err := taskStore.GetTask(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if fetched.ID != created.ID || fetched.Title != created.Title {
		t.Fatalf("unexpected task: %#v", fetched)
	}
}

func TestLadybugStore_AgentRunLifecycle(t *testing.T) {
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	runStore := store.NewLadybugStore(graph)
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	created, err := runStore.CreateAgentRun(context.Background(), model.AgentRun{
		ID:           "agent_run_test",
		ProjectID:    "example-service",
		Status:       model.AgentRunStatusRunning,
		StartedAt:    now,
		ChangedFiles: []string{"internal/agentcontrol/model/model.go"},
		Artifacts:    []model.AgentArtifact{{Ref: "artifact-1", Kind: "evidence"}},
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	updated, err := runStore.AppendAgentStep(context.Background(), created.ID, model.AgentStep{
		ID:        "agent_step_test",
		ToolName:  "go",
		Status:    model.AgentRunStatusCompleted,
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("append step: %v", err)
	}
	if len(updated.Steps) != 1 {
		t.Fatalf("expected one step, got %#v", updated)
	}
	updated, err = runStore.PromoteAgentArtifact(context.Background(), created.ID, model.AgentPromotion{
		ArtifactRef:  "artifact-1",
		ArtifactKind: "evidence",
		State:        model.PromotionStatePromoted,
		SourceRef:    "agent_step_test",
		VerifierRef:  "go/test/internal/agentcontrol",
		Decision:     "focused verifier passed",
		DecidedAt:    now,
	})
	if err != nil {
		t.Fatalf("promote artifact: %v", err)
	}
	if len(updated.Promotions) != 1 || updated.Promotions[0].State != model.PromotionStatePromoted {
		t.Fatalf("unexpected promotions: %#v", updated.Promotions)
	}
	updated.Status = model.AgentRunStatusCompleted
	updated.CompletedAt = now
	completed, err := runStore.CompleteAgentRun(context.Background(), updated)
	if err != nil {
		t.Fatalf("complete run: %v", err)
	}
	fetched, err := runStore.GetAgentRun(context.Background(), completed.ID)
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}
	if fetched.Status != model.AgentRunStatusCompleted || len(fetched.Steps) != 1 || len(fetched.Promotions) != 1 || fetched.ChangedFiles[0] != "internal/agentcontrol/model/model.go" {
		t.Fatalf("unexpected fetched run: %#v", fetched)
	}
}

func TestSQLiteConfigStore_PersistsSettingsAndFlags(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	configStore := store.NewSQLiteConfigStore(db.SQLDB())

	if err := configStore.SetAppSetting(context.Background(), "mode", "local", "string"); err != nil {
		t.Fatalf("set app setting: %v", err)
	}
	value, valueType, err := configStore.GetAppSetting(context.Background(), "mode")
	if err != nil {
		t.Fatalf("get app setting: %v", err)
	}
	if value != "local" || valueType != "string" {
		t.Fatalf("unexpected setting: %s %s", value, valueType)
	}

	if err := configStore.SetRuntimeFlag(context.Background(), "research.enabled", false, "disabled until provider ADR"); err != nil {
		t.Fatalf("set runtime flag: %v", err)
	}
	enabled, description, err := configStore.GetRuntimeFlag(context.Background(), "research.enabled")
	if err != nil {
		t.Fatalf("get runtime flag: %v", err)
	}
	if enabled || description == "" {
		t.Fatalf("unexpected runtime flag: %v %q", enabled, description)
	}
}
