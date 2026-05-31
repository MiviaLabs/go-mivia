package projectworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
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

func TestWorkspaceService_GitStatusPreservesContextTimeout(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "cmd/main.go", "package main\n")
	registry := newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly)
	svc := NewService(registry, nil, Options{Enabled: true})
	svc.SetGitRunner(contextErrorGitRunner{err: context.DeadlineExceeded})

	_, err := svc.GitStatus(context.Background(), "example-service", GitStatusOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
}

func TestWorkspaceService_GitAvailableUsesFastRevParse(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "cmd/main.go", "package main\n")
	registry := newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly)
	svc := NewService(registry, nil, Options{Enabled: true})
	runner := &recordingGitRunner{out: []byte("true\n")}
	svc.SetGitRunner(runner)

	available, err := svc.GitAvailable(context.Background(), "example-service")
	if err != nil {
		t.Fatalf("git available: %v", err)
	}
	if !available {
		t.Fatalf("expected git to be available")
	}
	if len(runner.args) != 2 || runner.args[0] != "rev-parse" || runner.args[1] != "--is-inside-work-tree" {
		t.Fatalf("expected fast rev-parse probe, got %#v", runner.args)
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

func TestWorkspaceService_GitStatusRunsAgainstConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})
	runner := &recordingGitRunner{}
	svc.SetGitRunner(runner)

	_, err := svc.GitStatus(context.Background(), "example-service", GitStatusOptions{})
	if err != nil {
		t.Fatalf("git status: %v", err)
	}

	if runner.root != filepath.Clean(root) {
		t.Fatalf("expected canonical root %q, got %q", filepath.Clean(root), runner.root)
	}
	if len(runner.args) < 4 || runner.args[0] != "status" || runner.args[1] != "--porcelain=v2" {
		t.Fatalf("unexpected git args: %#v", runner.args)
	}
}

func TestWorkspaceService_GitDiffReturnsCredentialReferenceConfigDiff(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "configs/mivia-server.example.toml", "[projects.integrations.jira]\n")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", "configs/mivia-server.example.toml")
	runGit(t, root, "commit", "-m", "initial")
	writeFixture(t, root, "configs/mivia-server.example.toml", `[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE_SERVICE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE_SERVICE"
project_keys = ["ABC"]
`)

	svc := NewService(newWorkspaceRegistryWithInclude(t, root, projectregistry.WorkspaceModeReadOnly, []string{"configs/*.toml"}), nil, Options{Enabled: true})
	diff, err := svc.GitDiff(context.Background(), "example-service", GitDiffOptions{RelativePath: "configs/mivia-server.example.toml", MaxDiffBytes: 4096})
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if len(diff.Skipped) != 0 {
		t.Fatalf("expected config diff, got skipped files: %#v", diff.Skipped)
	}
	if len(diff.Files) != 1 {
		t.Fatalf("expected one diff file, got %#v", diff)
	}
	body := diff.Files[0].Diff
	if !strings.Contains(body, `api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE_SERVICE"`) {
		t.Fatalf("expected credential reference in diff, got: %s", body)
	}
	if strings.Contains(body, "[REDACTED_SECRET]") || strings.Contains(body, "[REDACTED_EMAIL]") {
		t.Fatalf("ordinary credential references should not be redacted: %s", body)
	}
}

func TestWorkspaceService_GitDiffReturnsRawEligibleTextDiff(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "Test User")
	runGit(t, root, "add", "main.go")
	runGit(t, root, "commit", "-m", "initial")
	writeFixture(t, root, "main.go", `package main

const token = "plain-secret-token"
const contact = "alice@example.com"
const auth = "Bearer abcdefghijk"
const aws = "AKIA1234567890ABCDEF"
`)

	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})
	diff, err := svc.GitDiff(context.Background(), "example-service", GitDiffOptions{RelativePath: "main.go", MaxDiffBytes: 4096})
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if len(diff.Skipped) != 0 || len(diff.Files) != 1 {
		t.Fatalf("expected raw diff file, got %#v", diff)
	}
	body := diff.Files[0].Diff
	for _, raw := range []string{"plain-secret-token", "alice@example.com", "Bearer abcdefghijk", "AKIA1234567890ABCDEF"} {
		if !strings.Contains(body, raw) {
			t.Fatalf("expected raw value %q in diff: %s", raw, body)
		}
	}
}

func newWorkspaceRegistry(t *testing.T, root string, mode string) *projectregistry.Registry {
	return newWorkspaceRegistryWithInclude(t, root, mode, []string{"**/*.go"})
}

func newWorkspaceRegistryWithInclude(t *testing.T, root string, mode string, include []string) *projectregistry.Registry {
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
		Include:               include,
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

type contextErrorGitRunner struct {
	err error
}

func (runner contextErrorGitRunner) Run(context.Context, string, int, ...string) ([]byte, bool, error) {
	return nil, false, runner.err
}

type recordingGitRunner struct {
	root string
	args []string
	out  []byte
}

func (runner *recordingGitRunner) Run(_ context.Context, root string, _ int, args ...string) ([]byte, bool, error) {
	runner.root = root
	runner.args = append([]string(nil), args...)
	if runner.out != nil {
		return runner.out, false, nil
	}
	return []byte("# branch.head main\x00# branch.oid 1234567890abcdef\x00"), false, nil
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
