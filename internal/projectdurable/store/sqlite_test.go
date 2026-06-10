package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSQLiteBackendCreatesLocalBackend(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	backend, err := NewSQLiteBackend("data/durable-workflows.sqlite")
	if err != nil {
		t.Fatalf("open backend: %v", err)
	}
	if backend == nil {
		t.Fatal("expected backend")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "durable-workflows.sqlite")); err != nil {
		t.Fatalf("expected sqlite file to exist: %v", err)
	}
}

func TestValidateSQLitePathRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{
		"",
		"data/../durable-workflows.sqlite",
		"/tmp/durable-workflows.sqlite",
		"tmp/durable-workflows.sqlite",
		"data/durable-workflows.db",
		`data\durable-workflows.sqlite`,
		"C:/tmp/durable-workflows.sqlite",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := ValidateSQLitePath(path); err == nil {
				t.Fatalf("expected unsafe path %q to fail", path)
			}
		})
	}
}
