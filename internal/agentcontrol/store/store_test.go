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
