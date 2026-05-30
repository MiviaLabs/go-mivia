package projectregistry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

func TestDigestProject_StoresMetadataOnlyGraphNodes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeDigestFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n")
	writeDigestFile(t, filepath.Join(root, "README.md"), "# Example\n")
	writeDigestFile(t, filepath.Join(root, "data", "cache.go"), "package cache\n")
	linkTarget := t.TempDir()
	writeDigestFile(t, filepath.Join(linkTarget, "linked.go"), "package linked\n")
	if err := os.Symlink(linkTarget, filepath.Join(root, "linked")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	projectConfig := validConfigProject(root)
	projectConfig.Include = []string{"**/*.go", "README.md"}
	projectConfig.Exclude = []string{"data/**"}
	registry := newDigestRegistry(t, projectConfig)
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	svc := NewDigestService(registry, graph)
	svc.now = fixedDigestClock(time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC))
	svc.newID = func(Project, time.Time) string { return "digest_run_1" }

	run, err := svc.DigestProject(ctx, "example-service")
	if err != nil {
		t.Fatalf("digest project: %v", err)
	}

	if run.Status != DigestStatusCompleted {
		t.Fatalf("expected completed status, got %q", run.Status)
	}
	if run.FilesStored != 2 {
		t.Fatalf("expected two stored files, got %d", run.FilesStored)
	}
	if run.FilesSkipped == 0 {
		t.Fatal("expected excluded directory and symlink to be skipped")
	}

	project, ok := registry.Get("example-service")
	if !ok {
		t.Fatal("expected project")
	}
	projectNode, err := graph.GetNode(ctx, "Project", project.ID)
	if err != nil {
		t.Fatalf("get project node: %v", err)
	}
	if projectNode.Properties["graph_namespace"] != project.GraphNamespace {
		t.Fatalf("expected graph namespace %q, got %q", project.GraphNamespace, projectNode.Properties["graph_namespace"])
	}
	if _, ok := projectNode.Properties["root_path"]; ok {
		t.Fatal("project graph node must not store root_path")
	}

	repoFileID := repoFileNodeID(project.GraphNamespace, "cmd/main.go")
	repoFileNode, err := graph.GetNode(ctx, "RepoFile", repoFileID)
	if err != nil {
		t.Fatalf("get repo file node: %v", err)
	}
	if repoFileNode.Properties["relative_path"] != "cmd/main.go" {
		t.Fatalf("expected relative path, got %#v", repoFileNode.Properties)
	}
	if repoFileNode.Properties["extension"] != ".go" {
		t.Fatalf("expected .go extension, got %q", repoFileNode.Properties["extension"])
	}
	if repoFileNode.Properties["language_hint"] != "go" {
		t.Fatalf("expected go language hint, got %q", repoFileNode.Properties["language_hint"])
	}
	if repoFileNode.Properties["metadata_sha256"] == "" {
		t.Fatalf("expected metadata sha256 fingerprint, got %#v", repoFileNode.Properties)
	}
	if _, ok := repoFileNode.Properties["content_sha256"]; ok {
		t.Fatalf("repo file node must not store content hash: %#v", repoFileNode.Properties)
	}
	assertPropertiesDoNotContain(t, repoFileNode.Properties, "package main")

	if _, err := graph.GetNode(ctx, "RepoFile", repoFileNodeID(project.GraphNamespace, "data/cache.go")); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected excluded file to be absent, got %v", err)
	}
	if _, err := graph.GetNode(ctx, "RepoFile", repoFileNodeID(project.GraphNamespace, "linked/linked.go")); !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected symlink target file to be absent, got %v", err)
	}

	assertRelationship(t, graph, "PROJECT_HAS_REPO_FILE",
		ladybug.NodeRef{Label: "Project", ID: project.ID},
		ladybug.NodeRef{Label: "RepoFile", ID: repoFileID},
	)
	assertRelationship(t, graph, "PROJECT_HAS_DIGEST_RUN",
		ladybug.NodeRef{Label: "Project", ID: project.ID},
		ladybug.NodeRef{Label: "DigestRun", ID: run.ID},
	)

	digestRunNode, err := graph.GetNode(ctx, "DigestRun", run.ID)
	if err != nil {
		t.Fatalf("get digest run node: %v", err)
	}
	if digestRunNode.Properties["status"] != DigestStatusCompleted {
		t.Fatalf("expected digest run status, got %#v", digestRunNode.Properties)
	}
	if digestRunNode.Properties["files_stored"] != strconv.Itoa(run.FilesStored) {
		t.Fatalf("expected stored count, got %#v", digestRunNode.Properties)
	}
}

func TestDigestProject_RepoFileNodeIDIsStableAcrossRuns(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "main.go")
	writeDigestFile(t, filePath, "package first\n")
	firstModTime := time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filePath, firstModTime, firstModTime); err != nil {
		t.Fatalf("set first mtime: %v", err)
	}

	registry := newDigestRegistry(t, validConfigProject(root))
	graph := ladybug.NewMemoryGraph()
	svc := NewDigestService(registry, graph)
	svc.now = fixedDigestClock(time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC))
	svc.newID = func(_ Project, startedAt time.Time) string {
		return "digest_" + startedAt.Format("150405")
	}

	if _, err := svc.DigestProject(ctx, "example-service"); err != nil {
		t.Fatalf("first digest: %v", err)
	}
	project, _ := registry.Get("example-service")
	nodeID := repoFileNodeID(project.GraphNamespace, "main.go")
	firstNode, err := graph.GetNode(ctx, "RepoFile", nodeID)
	if err != nil {
		t.Fatalf("get first repo file node: %v", err)
	}

	writeDigestFile(t, filePath, "package second\n")
	secondModTime := firstModTime.Add(time.Minute)
	if err := os.Chtimes(filePath, secondModTime, secondModTime); err != nil {
		t.Fatalf("set second mtime: %v", err)
	}
	if _, err := svc.DigestProject(ctx, "example-service"); err != nil {
		t.Fatalf("second digest: %v", err)
	}
	secondNode, err := graph.GetNode(ctx, "RepoFile", nodeID)
	if err != nil {
		t.Fatalf("get second repo file node: %v", err)
	}

	if secondNode.ID != firstNode.ID {
		t.Fatalf("expected stable node ID %q, got %q", firstNode.ID, secondNode.ID)
	}
	if secondNode.Properties["metadata_sha256"] == firstNode.Properties["metadata_sha256"] {
		t.Fatal("expected metadata update for changed file metadata")
	}
}

func TestDigestProject_DoesNotOpenFilesForContentHash(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "main.go")
	writeDigestFile(t, filePath, "package unreadable\n")
	if err := os.Chmod(filePath, 0o000); err != nil {
		t.Fatalf("chmod unreadable file: %v", err)
	}
	defer os.Chmod(filePath, 0o600)

	registry := newDigestRegistry(t, validConfigProject(root))
	graph := ladybug.NewMemoryGraph()
	svc := NewDigestService(registry, graph)

	run, err := svc.DigestProject(ctx, "example-service")
	if err != nil {
		t.Fatalf("digest unreadable file metadata: %v", err)
	}
	if run.FilesStored != 1 {
		t.Fatalf("expected unreadable file metadata to be stored, got %d", run.FilesStored)
	}
	project, _ := registry.Get("example-service")
	node, err := graph.GetNode(ctx, "RepoFile", repoFileNodeID(project.GraphNamespace, "main.go"))
	if err != nil {
		t.Fatalf("get unreadable repo file node: %v", err)
	}
	if node.Properties["metadata_sha256"] == "" {
		t.Fatalf("expected metadata fingerprint, got %#v", node.Properties)
	}
	if _, ok := node.Properties["content_sha256"]; ok {
		t.Fatalf("must not store content hash: %#v", node.Properties)
	}
}

func TestDigestProject_DisabledProjectIsRejected(t *testing.T) {
	root := t.TempDir()
	projectConfig := validConfigProject(root)
	projectConfig.Enabled = false
	registry := newDigestRegistry(t, projectConfig)
	graph := ladybug.NewMemoryGraph()
	svc := NewDigestService(registry, graph)

	_, err := svc.DigestProject(context.Background(), "example-service")
	if !errors.Is(err, ErrDigestProjectDisabled) {
		t.Fatalf("expected disabled project error, got %v", err)
	}
}

func TestDigest_RejectsSymlinkRoot(t *testing.T) {
	targetRoot := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(targetRoot, symlinkRoot); err != nil {
		t.Fatalf("create symlink root: %v", err)
	}
	project := Project{
		ID:             "example-service",
		DisplayName:    "Example Service",
		RootPath:       symlinkRoot,
		Enabled:        true,
		Classification: ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     DigestModeMetadataOnly,
		UpdatePolicy:   UpdatePolicyManual,
		FollowSymlinks: false,
	}
	svc := NewDigestService(nil, ladybug.NewMemoryGraph())

	_, err := svc.Digest(context.Background(), project)
	if !errors.Is(err, ErrDigestUnsupported) {
		t.Fatalf("expected unsupported digest error, got %v", err)
	}
}

func TestMatchSlashPattern_SupportsRootRelativeGlobstar(t *testing.T) {
	tests := []struct {
		pattern  string
		relative string
		want     bool
	}{
		{pattern: "**/*.go", relative: "main.go", want: true},
		{pattern: "**/*.go", relative: "cmd/main.go", want: true},
		{pattern: "docs/**", relative: "docs", want: true},
		{pattern: "docs/**", relative: "docs/guide/local.md", want: true},
		{pattern: ".env*", relative: ".env.local", want: true},
		{pattern: "docs/**", relative: "cmd/main.go", want: false},
	}

	for _, tt := range tests {
		if got := matchSlashPattern(tt.pattern, tt.relative); got != tt.want {
			t.Fatalf("matchSlashPattern(%q, %q) = %v, want %v", tt.pattern, tt.relative, got, tt.want)
		}
	}
}

func newDigestRegistry(t *testing.T, projects ...config.Project) *Registry {
	t.Helper()
	registry, err := NewRegistry(projects, Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry
}

func writeDigestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func fixedDigestClock(start time.Time) func() time.Time {
	current := start
	return func() time.Time {
		value := current
		current = current.Add(time.Second)
		return value
	}
}

func assertPropertiesDoNotContain(t *testing.T, properties map[string]string, content string) {
	t.Helper()
	for key, value := range properties {
		if strings.Contains(value, content) {
			t.Fatalf("property %q stored file content", key)
		}
	}
}

func assertRelationship(t *testing.T, graph *ladybug.MemoryGraph, relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef) {
	t.Helper()
	if _, err := graph.GetRelationship(context.Background(), relationshipType, from, to); err != nil {
		t.Fatalf("expected relationship %s from %#v to %#v: %v", relationshipType, from, to, err)
	}
}
