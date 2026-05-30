package projectregistry

import (
	"path/filepath"
	"testing"
)

func TestValidatePattern_AllowsRootRelativeSlashPatterns(t *testing.T) {
	for _, pattern := range []string{"**/*.go", "docs/**", ".env*", ".git/**", "go.mod"} {
		if err := validatePattern(pattern); err != nil {
			t.Fatalf("expected %q to be valid: %v", pattern, err)
		}
	}
}

func TestValidatePattern_RejectsUnsafePatterns(t *testing.T) {
	tests := []string{
		"",
		" ",
		"/absolute/path",
		"../outside",
		"docs/../outside",
		`docs\file.go`,
		`C:\Users\dev\project`,
		"C:/Users/dev/project",
		"//server/share",
		"a\x00b",
	}

	for _, pattern := range tests {
		if err := validatePattern(pattern); err == nil {
			t.Fatalf("expected %q to be rejected", pattern)
		}
	}
}

func TestMergeExcludePatterns_AddsDefaultsAndStoragePathsUnderRoot(t *testing.T) {
	root := t.TempDir()
	ladybugPath := filepath.Join(root, ".local-data", "graph.lbug")
	sqlitePath := filepath.Join(t.TempDir(), "outside.sqlite")

	excludes := mergeExcludePatterns(root, []string{"build/**", ".git/**"}, ladybugPath, sqlitePath, ":memory:")

	assertContains(t, excludes, ".git/**")
	assertContains(t, excludes, "data/**")
	assertContains(t, excludes, "secrets/**")
	assertContains(t, excludes, ".env*")
	assertContains(t, excludes, "lib-ladybug/**")
	assertContains(t, excludes, ".local-data/graph.lbug")
	assertContains(t, excludes, "build/**")
	assertOccursOnce(t, excludes, ".git/**")
	if contains(excludes, filepath.ToSlash(sqlitePath)) {
		t.Fatalf("expected storage path outside root to be omitted: %#v", excludes)
	}
}

func assertContains(t *testing.T, values []string, expected string) {
	t.Helper()
	if !contains(values, expected) {
		t.Fatalf("expected %#v to contain %q", values, expected)
	}
}

func assertOccursOnce(t *testing.T, values []string, expected string) {
	t.Helper()
	count := 0
	for _, value := range values {
		if value == expected {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected %q to occur once in %#v", expected, values)
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
