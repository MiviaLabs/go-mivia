package projectregistry

import (
	"path/filepath"
	"testing"
)

func TestProjectGraphPath_DerivesScopedPathUnderBaseParent(t *testing.T) {
	path, err := ProjectGraphPath(filepath.Join("data", "mivialabs.lbug"), "generic-monorepo")
	if err != nil {
		t.Fatalf("derive project graph path: %v", err)
	}
	expected := filepath.Join("data", "projects", "generic-monorepo", "mivialabs.lbug")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestProjectSearchPath_DerivesScopedPathUnderBaseParent(t *testing.T) {
	path, err := ProjectSearchPath(filepath.Join("data", "mivialabs.lbug"), "generic-monorepo")
	if err != nil {
		t.Fatalf("derive project search path: %v", err)
	}
	expected := filepath.Join("data", "projects", "generic-monorepo", "mivialabs-pebble-search.sqlite")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
	if filepath.Base(path) == "mivialabs-search.sqlite" {
		t.Fatalf("project search path must not reuse the legacy graph-search epoch filename: %q", path)
	}
}

func TestProjectGraphPath_RejectsUnsafeProjectID(t *testing.T) {
	for _, projectID := range []string{"", "../GENERIC", "GENERIC", "GENERIC.monorepo", "GENERIC/monorepo"} {
		if _, err := ProjectGraphPath(filepath.Join("data", "mivialabs.lbug"), projectID); err == nil {
			t.Fatalf("expected unsafe project id %q to fail", projectID)
		}
		if _, err := ProjectSearchPath(filepath.Join("data", "mivialabs.lbug"), projectID); err == nil {
			t.Fatalf("expected unsafe project search id %q to fail", projectID)
		}
	}
}
