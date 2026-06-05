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
	assertContains(t, excludes, ".mivia-worktrees/**")
	assertContains(t, excludes, "lib-ladybug/**")
	assertContains(t, excludes, ".dart_tool/**")
	assertContains(t, excludes, ".gradle/**")
	assertContains(t, excludes, "android/.gradle/**")
	assertContains(t, excludes, "ios/Pods/**")
	assertContains(t, excludes, ".local-data/graph.lbug")
	assertContains(t, excludes, "build/**")
	assertOccursOnce(t, excludes, ".git/**")
	if contains(excludes, filepath.ToSlash(sqlitePath)) {
		t.Fatalf("expected storage path outside root to be omitted: %#v", excludes)
	}
}

func TestProjectMayIncludeRelativePath(t *testing.T) {
	scopedProject := Project{
		Include: []string{"apps/**", "libs/**/*.ts", "docs/*.md", "**/*.json"},
		Exclude: []string{"apps/generated/**"},
	}

	for _, relative := range []string{"apps", "apps/api", "libs", "libs/shared", "docs", "package.json", "infra"} {
		if !ProjectMayIncludeRelativePath(scopedProject, relative) {
			t.Fatalf("expected %q to be traversable", relative)
		}
	}
	for _, relative := range []string{"apps/generated"} {
		if ProjectMayIncludeRelativePath(scopedProject, relative) {
			t.Fatalf("expected %q not to be traversable", relative)
		}
	}

	prefixProject := Project{
		Include: []string{"apps/**", "libs/**/*.ts", "docs/*.md"},
	}
	for _, relative := range []string{"infra", "tools"} {
		if ProjectMayIncludeRelativePath(prefixProject, relative) {
			t.Fatalf("expected %q not to be traversable", relative)
		}
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
