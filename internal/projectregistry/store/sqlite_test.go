package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry/store"
)

func TestSQLiteStore_SaveAndGetProject(t *testing.T) {
	db := openStoreDB(t)
	defer db.Close()
	sqliteStore := store.NewSQLiteStore(db.SQLDB())
	project := validProject(t, "example-service")

	if err := sqliteStore.SaveProjects(context.Background(), []projectregistry.Project{project}); err != nil {
		t.Fatalf("save projects: %v", err)
	}

	fetched, err := sqliteStore.GetProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if fetched.ID != project.ID ||
		fetched.GraphNamespace != project.GraphNamespace ||
		fetched.GraphStorage != project.GraphStorage ||
		fetched.RootPath != project.RootPath ||
		fetched.ValidationStatus != projectregistry.ValidationStatusValid {
		t.Fatalf("unexpected fetched project: %#v", fetched)
	}
	if len(fetched.Include) != len(project.Include) || fetched.Include[0] != project.Include[0] {
		t.Fatalf("unexpected include patterns: %#v", fetched.Include)
	}
	assertContains(t, fetched.Exclude, ".git/**")
	assertContains(t, fetched.Exclude, ".local/config.sqlite")
}

func TestSQLiteStore_SaveProjects_UpsertsProject(t *testing.T) {
	db := openStoreDB(t)
	defer db.Close()
	sqliteStore := store.NewSQLiteStore(db.SQLDB())
	project := validProject(t, "example-service")

	if err := sqliteStore.SaveProjects(context.Background(), []projectregistry.Project{project}); err != nil {
		t.Fatalf("save projects: %v", err)
	}
	project.DisplayName = "Renamed Service"
	project.Description = "Updated non-sensitive summary"
	if err := sqliteStore.SaveProjects(context.Background(), []projectregistry.Project{project}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	listed, err := sqliteStore.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one project, got %d", len(listed))
	}
	if listed[0].DisplayName != "Renamed Service" {
		t.Fatalf("expected updated display name, got %q", listed[0].DisplayName)
	}
}

func TestSQLiteStore_GetProject_NotFound(t *testing.T) {
	db := openStoreDB(t)
	defer db.Close()
	sqliteStore := store.NewSQLiteStore(db.SQLDB())

	_, err := sqliteStore.GetProject(context.Background(), "missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func openStoreDB(t *testing.T) *sqliteplatform.DB {
	t.Helper()
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		db.Close()
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	return db
}

func validProject(t *testing.T, id string) projectregistry.Project {
	t.Helper()
	root := t.TempDir()
	sqlitePath := filepath.Join(root, ".local", "config.sqlite")
	registry, err := projectregistry.NewRegistry([]config.Project{
		{
			ID:             id,
			DisplayName:    "Example Service",
			Description:    "Synthetic local service",
			RootPath:       root,
			Enabled:        true,
			Classification: projectregistry.ClassificationInternal,
			GraphNamespace: id,
			DigestMode:     projectregistry.DigestModeMetadataOnly,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
			Include:        []string{"**/*.go"},
			Exclude:        []string{".git/**"},
			FollowSymlinks: false,
		},
	}, projectregistry.Options{SQLitePath: sqlitePath})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	project, ok := registry.Get(id)
	if !ok {
		t.Fatalf("project %s not found", id)
	}
	return project
}

func assertContains(t *testing.T, values []string, expected string) {
	t.Helper()
	for _, value := range values {
		if value == expected {
			return
		}
	}
	t.Fatalf("expected %#v to contain %q", values, expected)
}
