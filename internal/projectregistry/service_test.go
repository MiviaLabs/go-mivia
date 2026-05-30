package projectregistry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
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
			name: "unsupported graph storage",
			mutate: func(project *config.Project) {
				project.GraphStorage = "remote"
			},
			message: "graph_storage",
		},
		{
			name: "unsupported digest mode",
			mutate: func(project *config.Project) {
				project.DigestMode = "embedding"
			},
			message: "digest_mode",
		},
		{
			name: "content graph disabled",
			mutate: func(project *config.Project) {
				project.DigestMode = DigestModeContentGraph
			},
			message: "content graph",
		},
		{
			name: "unsupported update policy",
			mutate: func(project *config.Project) {
				project.UpdatePolicy = "watch"
			},
			message: "update_policy",
		},
		{
			name: "live without content graph",
			mutate: func(project *config.Project) {
				project.UpdatePolicy = UpdatePolicyLive
			},
			message: "requires digest_mode",
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
		{
			name: "invalid sensitive marker policy",
			mutate: func(project *config.Project) {
				project.SensitiveMarkerPolicy = "store"
			},
			message: "sensitive_marker_policy",
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

func TestNewRegistry_ContentGraphRequiresApprovalGate(t *testing.T) {
	root := t.TempDir()
	project := validConfigProject(root)
	project.DigestMode = DigestModeContentGraph

	_, err := NewRegistry([]config.Project{project}, Options{
		ContentGraphEnabled: true,
	})
	if err == nil || !strings.Contains(err.Error(), "ADR-0007") {
		t.Fatalf("expected ADR approval gate error, got %v", err)
	}
}

func TestNewRegistry_ContentGraphAndLiveAllowedOnlyWhenExplicitlyGated(t *testing.T) {
	root := t.TempDir()
	project := validConfigProject(root)
	project.DigestMode = DigestModeContentGraph
	project.UpdatePolicy = UpdatePolicyLive
	project.MaxFileBytes = 1024
	project.MaxChunkBytes = 512
	project.SensitiveMarkerPolicy = SensitiveMarkerPolicySkipFile

	_, err := NewRegistry([]config.Project{project}, Options{
		ContentGraphEnabled:          true,
		LiveUpdatesEnabled:           false,
		ContentGraphApprovalAccepted: true,
	})
	if err == nil || !strings.Contains(err.Error(), "live updates") {
		t.Fatalf("expected live updates gate error, got %v", err)
	}

	registry, err := NewRegistry([]config.Project{project}, Options{
		ContentGraphEnabled:          true,
		LiveUpdatesEnabled:           true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("expected gated registry to load: %v", err)
	}
	loaded, ok := registry.Get("example-service")
	if !ok {
		t.Fatal("expected content graph project")
	}
	if loaded.DigestMode != DigestModeContentGraph || loaded.UpdatePolicy != UpdatePolicyLive {
		t.Fatalf("unexpected loaded project modes: %+v", loaded)
	}
	if loaded.GraphStorage != GraphStoragePersistent {
		t.Fatalf("expected persistent graph storage default, got %+v", loaded)
	}
	if loaded.MaxFileBytes != 1024 || loaded.MaxChunkBytes != 512 {
		t.Fatalf("unexpected loaded project caps: %+v", loaded)
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

func TestRegistry_MetadataExposesRedactedIntegrationState(t *testing.T) {
	root := t.TempDir()
	project := validConfigProject(root)
	project.Integrations = config.IntegrationConfig{
		Jira: &config.JiraIntegration{
			Enabled:  true,
			AuthMode: "api_token_basic",
			CredentialRefs: config.AtlassianCredentialRefs{
				EmailEnv:    "MIVIA_ATLASSIAN_EMAIL_EXAMPLE",
				APITokenEnv: "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE",
			},
			ProjectKeys: []string{"ABC", "XYZ"},
			Polling: config.IntegrationPolling{
				IngestionEnabled: true,
			},
		},
		Confluence: &config.ConfluenceIntegration{
			Enabled:  true,
			AuthMode: "api_token_basic",
			CredentialRefs: config.AtlassianCredentialRefs{
				EmailFile:    "secrets/email",
				APITokenFile: "secrets/token",
			},
			SpaceKeys: []string{"ENG"},
		},
	}

	registry, err := NewRegistry([]config.Project{project}, Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	loaded, ok := registry.Get("example-service")
	if !ok {
		t.Fatal("expected project")
	}
	metadata := MetadataForProject(loaded)
	if metadata.Integrations == nil || metadata.Integrations.Jira == nil || metadata.Integrations.Confluence == nil {
		t.Fatalf("expected integration metadata: %+v", metadata.Integrations)
	}
	if metadata.Integrations.Jira.ProjectKeyCount != 2 || metadata.Integrations.Jira.CredentialSource != "env" {
		t.Fatalf("unexpected Jira metadata: %+v", metadata.Integrations.Jira)
	}
	if metadata.Integrations.Confluence.SpaceKeyCount != 1 || metadata.Integrations.Confluence.CredentialSource != "file" {
		t.Fatalf("unexpected Confluence metadata: %+v", metadata.Integrations.Confluence)
	}
}

func TestRegistry_DoesNotExposeIntegrationCredentialReferences(t *testing.T) {
	root := t.TempDir()
	project := validConfigProject(root)
	project.Integrations = config.IntegrationConfig{
		Jira: &config.JiraIntegration{
			Enabled:  true,
			AuthMode: "api_token_basic",
			CredentialRefs: config.AtlassianCredentialRefs{
				EmailEnv:     "MIVIA_ATLASSIAN_EMAIL_EXAMPLE",
				APITokenFile: "secrets/token",
			},
			ProjectKeys: []string{"ABC"},
		},
	}

	registry, err := NewRegistry([]config.Project{project}, Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	loaded, ok := registry.Get("example-service")
	if !ok {
		t.Fatal("expected project")
	}
	metadata := MetadataForProject(loaded)
	if metadata.Integrations == nil || metadata.Integrations.Jira == nil {
		t.Fatalf("expected Jira integration metadata: %+v", metadata.Integrations)
	}
	rendered := strings.Join([]string{
		metadata.Integrations.Jira.AuthMode,
		metadata.Integrations.Jira.CredentialSource,
	}, " ")
	if strings.Contains(rendered, "MIVIA_ATLASSIAN") || strings.Contains(rendered, "secrets/token") {
		t.Fatalf("metadata exposed credential references: %q", rendered)
	}
	if metadata.Integrations.Jira.CredentialSource != "mixed" {
		t.Fatalf("expected mixed credential source, got %+v", metadata.Integrations.Jira)
	}
}

func validConfigProject(root string) config.Project {
	return config.Project{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		Description:           "Synthetic local service",
		RootPath:              root,
		Enabled:               true,
		Classification:        ClassificationInternal,
		GraphNamespace:        "example-service",
		DigestMode:            DigestModeMetadataOnly,
		UpdatePolicy:          UpdatePolicyManual,
		Include:               []string{"**/*.go", "go.mod"},
		Exclude:               []string{".git/**"},
		FollowSymlinks:        false,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	}
}
