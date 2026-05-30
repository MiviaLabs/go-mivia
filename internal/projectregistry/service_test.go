package projectregistry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
)

func TestNewRegistry_ValidProjects_ReturnsRegistry(t *testing.T) {
	root := t.TempDir()
	cfgProjects := []config.Project{
		validConfigProject(root),
		{
			ID:             "disabled_project",
			DisplayName:    "Disabled Project",
			RootPath:       "/absolute/path/to/not-created",
			Enabled:        false,
			GraphNamespace: "disabled_project",
			DigestMode:     DigestModeMetadataOnly,
			UpdatePolicy:   UpdatePolicyManual,
			Include:        []string{"README.md"},
			Exclude:        []string{".git/**"},
			FollowSymlinks: false,
		},
	}

	registry, err := NewRegistry(cfgProjects, Options{
		LadybugPath: filepath.Join(root, "data", "graph.lbug"),
		SQLitePath:  filepath.Join(root, ".state", "config.sqlite"),
	})
	if err != nil {
		t.Fatalf("expected valid registry: %v", err)
	}

	projects := registry.List()
	if len(projects) != 2 {
		t.Fatalf("expected two projects, got %d", len(projects))
	}
	project, ok := registry.Get("example-service")
	if !ok {
		t.Fatal("expected project by ID")
	}
	if project.RootPath != filepath.Clean(root) {
		t.Fatalf("expected clean root path %q, got %q", filepath.Clean(root), project.RootPath)
	}
	if project.CanonicalRootPath != filepath.Clean(root) {
		t.Fatalf("expected canonical root path %q, got %q", filepath.Clean(root), project.CanonicalRootPath)
	}
	if project.ValidationStatus != ValidationStatusValid {
		t.Fatalf("expected valid status, got %q", project.ValidationStatus)
	}
	assertContains(t, project.Exclude, ".state/config.sqlite")
}

func TestNewRegistry_DuplicateIDs_ReturnsError(t *testing.T) {
	root := t.TempDir()
	projects := []config.Project{
		validConfigProject(root),
		validConfigProject(root),
	}

	_, err := NewRegistry(projects, Options{})
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("expected duplicate ID error, got %v", err)
	}
}

func TestNewRegistry_DuplicateGraphNamespaces_ReturnsError(t *testing.T) {
	root := t.TempDir()
	first := validConfigProject(root)
	second := validConfigProject(root)
	second.ID = "another_project"
	second.GraphNamespace = first.GraphNamespace

	_, err := NewRegistry([]config.Project{first, second}, Options{})
	if err == nil || !strings.Contains(err.Error(), "graph_namespace") {
		t.Fatalf("expected duplicate graph namespace error, got %v", err)
	}
}

func TestNewRegistry_InvalidProjectFields_ReturnError(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		mutate  func(*config.Project)
		message string
	}{
		{
			name: "invalid id",
			mutate: func(project *config.Project) {
				project.ID = "Example Service"
			},
			message: "id",
		},
		{
			name: "empty display name",
			mutate: func(project *config.Project) {
				project.DisplayName = ""
			},
			message: "display_name",
		},
		{
			name: "unsupported digest mode",
			mutate: func(project *config.Project) {
				project.DigestMode = "embedding"
			},
			message: "digest_mode",
		},
		{
			name: "unsupported update policy",
			mutate: func(project *config.Project) {
				project.UpdatePolicy = "watch"
			},
			message: "update_policy",
		},
		{
			name: "follow symlinks",
			mutate: func(project *config.Project) {
				project.FollowSymlinks = true
			},
			message: "follow_symlinks",
		},
		{
			name: "unsafe include",
			mutate: func(project *config.Project) {
				project.Include = []string{"../outside"}
			},
			message: "include",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := validConfigProject(root)
			tt.mutate(&project)

			_, err := NewRegistry([]config.Project{project}, Options{})
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("expected %q error, got %v", tt.message, err)
			}
		})
	}
}

func TestNewRegistry_InvalidRootPaths_ReturnError(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(filePath, []byte("not source"), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	symlinkTarget := t.TempDir()
	symlinkPath := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(symlinkTarget, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	tests := []struct {
		name     string
		rootPath string
		message  string
	}{
		{name: "relative", rootPath: "relative/path", message: "absolute"},
		{name: "missing", rootPath: filepath.Join(t.TempDir(), "missing"), message: "exist"},
		{name: "file", rootPath: filePath, message: "directory"},
		{name: "traversal", rootPath: t.TempDir() + "/../outside", message: "traversal"},
		{name: "symlink", rootPath: symlinkPath, message: "symlink"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := validConfigProject(t.TempDir())
			project.RootPath = tt.rootPath

			_, err := NewRegistry([]config.Project{project}, Options{})
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("expected %q error, got %v", tt.message, err)
			}
		})
	}
}

func TestRegistry_ReturnsDefensiveCopies(t *testing.T) {
	root := t.TempDir()
	registry, err := NewRegistry([]config.Project{validConfigProject(root)}, Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	listed := registry.List()
	listed[0].Include[0] = "mutated"
	fetched, ok := registry.Get("example-service")
	if !ok {
		t.Fatal("expected project")
	}
	if fetched.Include[0] == "mutated" {
		t.Fatal("expected defensive copy")
	}
}

func validConfigProject(root string) config.Project {
	return config.Project{
		ID:             "example-service",
		DisplayName:    "Example Service",
		Description:    "Synthetic local service",
		RootPath:       root,
		Enabled:        true,
		Classification: ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     DigestModeMetadataOnly,
		UpdatePolicy:   UpdatePolicyManual,
		Include:        []string{"**/*.go", "go.mod"},
		Exclude:        []string{".git/**"},
		FollowSymlinks: false,
	}
}
