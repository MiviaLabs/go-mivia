package projectworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func TestWorkspaceService_ReadEditAndQueueIngestion(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "cmd/main.go", "package main\n\nfunc main() {}\n")
	registry := newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit)
	ingest := &fakeWorkspaceIngestion{runID: "ingest-path-1"}
	svc := NewService(registry, ingest, Options{Enabled: true})

	file, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "cmd/main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if file.EditToken == "" || strings.Contains(file.EditToken, "sha256") || !strings.Contains(file.Text, "func main") {
		t.Fatalf("unexpected read result: %#v", file)
	}

	start := strings.Index(file.Text, "main()")
	result, err := svc.EditFile(context.Background(), "example-service", EditFileOptions{
		RelativePath: "cmd/main.go",
		EditToken:    file.EditToken,
		Edits: []ExactEdit{{
			StartByte: start,
			EndByte:   start + len("main()"),
			OldText:   "main()",
			NewText:   "Run()",
		}},
	})
	if err != nil {
		t.Fatalf("edit file: %v", err)
	}
	if !result.Applied || result.IngestionRunID != "ingest-path-1" || result.NewEditToken == "" {
		t.Fatalf("unexpected edit result: %#v", result)
	}
	if ingest.path != "cmd/main.go" {
		t.Fatalf("expected path ingestion for edited file, got %q", ingest.path)
	}
	content := readFixture(t, root, "cmd/main.go")
	if !strings.Contains(content, "func Run()") {
		t.Fatalf("edit was not written: %s", content)
	}
}

func TestWorkspaceService_RejectsReadOnlyAndStaleToken(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	readOnly := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})
	file, err := readOnly.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	_, err = readOnly.EditFile(context.Background(), "example-service", EditFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
		Edits:        []ExactEdit{{StartByte: 0, EndByte: 7, OldText: "package", NewText: "module"}},
	})
	if !errors.Is(err, ErrWorkspaceReadOnly) {
		t.Fatalf("expected read-only error, got %v", err)
	}

	editSvc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})
	file, err = editSvc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	writeFixture(t, root, "main.go", "package changed\n")
	_, err = editSvc.EditFile(context.Background(), "example-service", EditFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
		Edits:        []ExactEdit{{StartByte: 0, EndByte: 7, OldText: "package", NewText: "module"}},
	})
	if !errors.Is(err, ErrEditTokenInvalid) {
		t.Fatalf("expected stale token error, got %v", err)
	}
}

func TestWorkspaceService_RejectsDeniedAndSensitiveContent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	writeFixture(t, root, ".env", "TOKEN=secret\n")
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})
	if _, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: ".env"}); !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("expected denied path error, got %v", err)
	}
	file, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	_, err = svc.EditFile(context.Background(), "example-service", EditFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
		Edits:        []ExactEdit{{StartByte: 0, EndByte: 0, OldText: "", NewText: "api_key = \"placeholder\"\n"}},
	})
	if !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("expected sensitive-content rejection, got %v", err)
	}
	if strings.Contains(readFixture(t, root, "main.go"), "api_key") {
		t.Fatalf("sensitive edit was written")
	}
}

func TestWorkspaceService_GitStatusAndDiffAreGoverned(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", "main.go")
	runGit(t, root, "commit", "-m", "initial")
	writeFixture(t, root, "main.go", "package main\n\nfunc Run() {}\n")

	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})
	status, err := svc.GitStatus(context.Background(), "example-service", GitStatusOptions{IncludeUntracked: true})
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if len(status.Entries) != 1 || status.Entries[0].RelativePath != "main.go" || strings.Contains(status.Branch, root) {
		t.Fatalf("unexpected status: %#v", status)
	}

	diff, err := svc.GitDiff(context.Background(), "example-service", GitDiffOptions{RelativePath: "main.go", MaxDiffBytes: 4096})
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if len(diff.Files) != 1 || !strings.Contains(diff.Files[0].Diff, "func Run") {
		t.Fatalf("unexpected diff: %#v", diff)
	}
	body := diff.Files[0].Diff
	if strings.Contains(body, root) || strings.Contains(body, "git diff") {
		t.Fatalf("diff leaked root or command line: %s", body)
	}
}

func newWorkspaceRegistry(t *testing.T, root string, mode string) *projectregistry.Registry {
	t.Helper()
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example-service",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          projectregistry.UpdatePolicyManual,
		WorkspaceMode:         mode,
		Include:               []string{"**/*.go"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: projectregistry.SensitiveMarkerPolicySkipFile,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry
}

type fakeWorkspaceIngestion struct {
	runID string
	path  string
}

func (fake *fakeWorkspaceIngestion) GetFile(context.Context, string, string) (projectingestion.FileMetadata, error) {
	return projectingestion.FileMetadata{}, projectingestion.ErrIngestionNotFound
}

func (fake *fakeWorkspaceIngestion) IngestPath(_ context.Context, projectID string, relativePath string, trigger projectingestion.Trigger) (projectingestion.Run, error) {
	fake.path = relativePath
	return projectingestion.Run{ID: fake.runID, ProjectID: projectID, Trigger: trigger}, nil
}

func writeFixture(t *testing.T, root string, relativePath string, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func readFixture(t *testing.T, root string, relativePath string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		t.Fatalf("git fixture command failed: %v", err)
	}
}
