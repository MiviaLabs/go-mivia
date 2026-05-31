package projectingestion

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestSearchStoreRouter_RoutesPersistentProjectsToSeparateStores(t *testing.T) {
	ctx := context.Background()
	registry, err := projectregistry.NewRegistry([]config.Project{
		{
			ID:             "first-project",
			DisplayName:    "First Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "first",
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
		{
			ID:             "second-project",
			DisplayName:    "Second Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "second",
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
	}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  filepath.Join(t.TempDir(), "graph.lbug"),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	firstStore := openTestSearchSQLiteStore(t, filepath.Join(t.TempDir(), "first.sqlite"))
	secondStore := openTestSearchSQLiteStore(t, filepath.Join(t.TempDir(), "second.sqlite"))
	router := NewProjectScopedSearchStoreRouter(registry, nil, []SearchStoreBackend{
		{ProjectID: "first-project", Store: firstStore, StorageKey: "first-project"},
		{ProjectID: "second-project", Store: secondStore, StorageKey: "second-project"},
	})
	first, _ := registry.Get("first-project")
	second, _ := registry.Get("second-project")

	if err := router.UpsertSearchFile(ctx, first, testRouterSearchFileState("first-project", "main.go"), []Chunk{{Index: 0, Text: "first unique needle"}}, nil, nil, nil); err != nil {
		t.Fatalf("write first search file: %v", err)
	}
	if err := router.UpsertSearchFile(ctx, second, testRouterSearchFileState("second-project", "main.go"), []Chunk{{Index: 0, Text: "second unique needle"}}, nil, nil, nil); err != nil {
		t.Fatalf("write second search file: %v", err)
	}

	firstResults, err := firstStore.SearchText(ctx, first, TextSearchOptions{Query: "first", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("query first store: %v", err)
	}
	if len(firstResults.Results) != 1 {
		t.Fatalf("expected first result in first store, got %#v", firstResults.Results)
	}
	crossFirst, err := firstStore.SearchText(ctx, second, TextSearchOptions{Query: "second", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("cross query first store: %v", err)
	}
	if len(crossFirst.Results) != 0 {
		t.Fatalf("expected second project absent from first store, got %#v", crossFirst.Results)
	}

	secondResults, err := secondStore.SearchText(ctx, second, TextSearchOptions{Query: "second", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("query second store: %v", err)
	}
	if len(secondResults.Results) != 1 {
		t.Fatalf("expected second result in second store, got %#v", secondResults.Results)
	}
	crossSecond, err := secondStore.SearchText(ctx, first, TextSearchOptions{Query: "first", PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("cross query second store: %v", err)
	}
	if len(crossSecond.Results) != 0 {
		t.Fatalf("expected first project absent from second store, got %#v", crossSecond.Results)
	}

	diagnostics := router.SearchStorageDiagnostics()
	if len(diagnostics) != 2 || diagnostics[0].Backend != "persistent_project" || diagnostics[1].Backend != "persistent_project" {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Write == nil || diagnostic.Write.TransactionCount != 1 || diagnostic.Write.RowsInserted["project_search_files_fts"] != 1 {
			t.Fatalf("expected per-project search write diagnostics, got %#v", diagnostics)
		}
	}
}

func TestSearchStoreRouter_UsesSharedFileStateForProjectSearchRepair(t *testing.T) {
	ctx := context.Background()
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "first-project",
		DisplayName:    "First Project",
		RootPath:       t.TempDir(),
		Enabled:        true,
		GraphNamespace: "first",
		GraphStorage:   projectregistry.GraphStoragePersistent,
		DigestMode:     projectregistry.DigestModeContentGraph,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  filepath.Join(t.TempDir(), "graph.lbug"),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	project, _ := registry.Get("first-project")
	stateStore := openTestSearchSQLiteStore(t, filepath.Join(t.TempDir(), "state.sqlite"))
	searchStore := openTestSearchSQLiteStore(t, filepath.Join(t.TempDir(), "search.sqlite"))
	searchStore.SetSearchStateStore(stateStore)
	router := NewProjectScopedSearchStoreRouter(registry, stateStore, []SearchStoreBackend{
		{ProjectID: "first-project", Store: searchStore, StorageKey: "first-project"},
	})
	if err := stateStore.SaveFileState(ctx, testRouterSearchFileState("first-project", "main.go")); err != nil {
		t.Fatalf("save shared file state: %v", err)
	}

	health, err := router.SearchIndexHealth(ctx, project)
	if err != nil {
		t.Fatalf("search health: %v", err)
	}
	if !health.Degraded || health.Reason != "search_index_drift" {
		t.Fatalf("expected drift from shared file state, got %#v", health)
	}
	repair, err := router.ReconcileSearchIndex(ctx, project)
	if err != nil {
		t.Fatalf("reconcile search index: %v", err)
	}
	if len(repair) != 1 || repair[0].RelativePath != "main.go" {
		t.Fatalf("expected repair from shared file state, got %#v", repair)
	}
}

func openTestSearchSQLiteStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	db, err := sqliteplatform.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	return NewSQLiteStore(db.SQLDB())
}

func testRouterSearchFileState(projectID string, relativePath string) FileState {
	return FileState{
		ProjectID:        projectID,
		RelativePathHash: relativePath,
		RelativePath:     relativePath,
		RelativePathSafe: true,
		Status:           FileStatusEligible,
		Present:          true,
		ContentSHA256:    projectID + "-sha",
		SizeBytes:        24,
		ModifiedAt:       time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
	}
}
