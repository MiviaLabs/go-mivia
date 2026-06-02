package projectworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
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

func TestWorkspaceService_CreateFileCreatesParentsAndQueuesIngestion(t *testing.T) {
	root := t.TempDir()
	registry := newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit)
	ingest := &fakeWorkspaceIngestion{runID: "create-run-1"}
	svc := NewService(registry, ingest, Options{Enabled: true})

	result, err := svc.CreateFile(context.Background(), "example-service", CreateFileOptions{
		RelativePath:     "cmd/new/main.go",
		Text:             "package main\n",
		CreateParentDirs: true,
	})
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if result.File.RelativePath != "cmd/new/main.go" || result.File.EditToken == "" || result.NewEditToken == "" {
		t.Fatalf("unexpected create result: %#v", result)
	}
	if result.IngestionRunID != "create-run-1" || ingest.path != "cmd/new/main.go" {
		t.Fatalf("expected path ingestion for created file, got result=%#v ingest=%#v", result, ingest)
	}
	if got := readFixture(t, root, "cmd/new/main.go"); got != "package main\n" {
		t.Fatalf("unexpected created content: %q", got)
	}
}

func TestWorkspaceService_CreateFileDryRunDoesNotWriteOrCreateParents(t *testing.T) {
	root := t.TempDir()
	ingest := &fakeWorkspaceIngestion{runID: "create-run-1"}
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), ingest, Options{Enabled: true})

	result, err := svc.CreateFile(context.Background(), "example-service", CreateFileOptions{
		RelativePath:     "cmd/new/main.go",
		Text:             "package main\n",
		CreateParentDirs: true,
		DryRun:           true,
	})
	if err != nil {
		t.Fatalf("dry-run create file: %v", err)
	}
	if result.Applied || result.IngestionRunID != "" || ingest.path != "" || result.File.EditToken == "" {
		t.Fatalf("unexpected dry-run create result: %#v ingest=%#v", result, ingest)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run create mutated parents, stat err=%v", err)
	}
}

func TestWorkspaceService_CreateFileRejectsReadOnlyOverwriteAndUnsafeContent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	readOnly := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})
	_, err := readOnly.CreateFile(context.Background(), "example-service", CreateFileOptions{
		RelativePath: "other.go",
		Text:         "package main\n",
	})
	if !errors.Is(err, ErrWorkspaceReadOnly) {
		t.Fatalf("expected read-only error, got %v", err)
	}

	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})
	_, err = svc.CreateFile(context.Background(), "example-service", CreateFileOptions{
		RelativePath: "main.go",
		Text:         "package changed\n",
	})
	if !errors.Is(err, ErrEditConflict) {
		t.Fatalf("expected overwrite conflict, got %v", err)
	}
	if got := readFixture(t, root, "main.go"); got != "package main\n" {
		t.Fatalf("create overwrote existing file: %q", got)
	}

	_, err = svc.CreateFile(context.Background(), "example-service", CreateFileOptions{
		RelativePath: "secret.go",
		Text:         "api_key = \"placeholder\"\n",
	})
	if !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("expected unsafe content rejection, got %v", err)
	}
}

func TestWorkspaceService_CreateFileRejectsSymlinkParentTraversal(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "cmd")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})

	_, err := svc.CreateFile(context.Background(), "example-service", CreateFileOptions{
		RelativePath:     "cmd/main.go",
		Text:             "package main\n",
		CreateParentDirs: true,
	})
	if !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
}

func TestWorkspaceService_DeleteFileRequiresCurrentTokenAndQueuesIngestion(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	registry := newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit)
	ingest := &fakeWorkspaceIngestion{runID: "delete-run-1"}
	svc := NewService(registry, ingest, Options{Enabled: true})

	file, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	writeFixture(t, root, "main.go", "package changed\n")
	_, err = svc.DeleteFile(context.Background(), "example-service", DeleteFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
	})
	if !errors.Is(err, ErrEditTokenInvalid) {
		t.Fatalf("expected stale token rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "main.go")); err != nil {
		t.Fatalf("stale-token delete removed file: %v", err)
	}

	file, err = svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read changed file: %v", err)
	}
	result, err := svc.DeleteFile(context.Background(), "example-service", DeleteFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
	})
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if !result.Deleted || result.IngestionRunID != "delete-run-1" || ingest.path != "main.go" {
		t.Fatalf("unexpected delete result: %#v ingest=%#v", result, ingest)
	}
	if _, err := os.Stat(filepath.Join(root, "main.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

func TestWorkspaceService_DeleteFileDryRunDoesNotRemoveOrQueueIngestion(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	ingest := &fakeWorkspaceIngestion{runID: "delete-run-1"}
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), ingest, Options{Enabled: true})

	file, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	result, err := svc.DeleteFile(context.Background(), "example-service", DeleteFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("dry-run delete file: %v", err)
	}
	if result.Deleted || result.IngestionRunID != "" || ingest.path != "" {
		t.Fatalf("unexpected dry-run delete result: %#v ingest=%#v", result, ingest)
	}
	if got := readFixture(t, root, "main.go"); got != "package main\n" {
		t.Fatalf("dry-run delete removed or changed file: %q", got)
	}
}

func TestWorkspaceService_DeleteFileRejectsReadOnlyDirectoriesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	readOnly := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})
	file, err := readOnly.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	_, err = readOnly.DeleteFile(context.Background(), "example-service", DeleteFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
	})
	if !errors.Is(err, ErrWorkspaceReadOnly) {
		t.Fatalf("expected read-only error, got %v", err)
	}

	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})
	_, err = svc.DeleteFile(context.Background(), "example-service", DeleteFileOptions{
		RelativePath: "cmd",
		EditToken:    "token",
	})
	if !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrUnsafeContent) {
		t.Fatalf("expected directory rejection, got %v", err)
	}

	target := filepath.Join(root, "main.go")
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = svc.DeleteFile(context.Background(), "example-service", DeleteFileOptions{
		RelativePath: "link.go",
		EditToken:    "token",
	})
	if !errors.Is(err, ErrUnsafeContent) && !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected symlink rejection, got %v", err)
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

func TestWorkspaceService_ReadGoSourceAllowsOrdinarySensitiveLookingIdentifiers(t *testing.T) {
	root := t.TempDir()
	source := `package main

type EditToken struct {
	Value string
}

func secret() EditToken {
	return EditToken{Value: "not-a-credential"}
}

func NewService() []byte {
	secret := make([]byte, 32)
	return secret
}
`
	writeFixture(t, root, "cmd/main.go", source)
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})

	file, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "cmd/main.go"})
	if err != nil {
		t.Fatalf("read ordinary Go source identifiers: %v", err)
	}
	for _, expected := range []string{"type EditToken", "func secret()", "secret := make"} {
		if !strings.Contains(file.Text, expected) {
			t.Fatalf("expected %q in read source, got %q", expected, file.Text)
		}
	}
}

func TestWorkspaceService_ReadFileMaxBytesClampsInsteadOfRejecting(t *testing.T) {
	root := t.TempDir()
	source := strings.Repeat("a", 20)
	writeFixture(t, root, "main.go", source)
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeReadOnly), nil, Options{Enabled: true})

	aboveFileSize, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{
		RelativePath: "main.go",
		MaxBytes:     len(source) + 100,
	})
	if err != nil {
		t.Fatalf("read with max_bytes above file size: %v", err)
	}
	if aboveFileSize.Text != source || aboveFileSize.TextTruncated {
		t.Fatalf("expected full untruncated file, got %#v", aboveFileSize)
	}

	aboveLimit, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{
		RelativePath: "main.go",
		MaxBytes:     MaxReadBytes + 1,
	})
	if err != nil {
		t.Fatalf("read with max_bytes above MaxReadBytes: %v", err)
	}
	if aboveLimit.Text != source || aboveLimit.TextTruncated {
		t.Fatalf("expected clamp to read limit without truncating small file, got %#v", aboveLimit)
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

func TestWorkspaceService_EditConflictAndStaleTokenErrorsStayExplicit(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})

	file, err := svc.ReadFile(context.Background(), "example-service", ReadFileOptions{RelativePath: "main.go"})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	_, err = svc.EditFile(context.Background(), "example-service", EditFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
		Edits:        []ExactEdit{{StartByte: 0, EndByte: 7, OldText: "module", NewText: "package"}},
	})
	if !errors.Is(err, ErrEditConflict) {
		t.Fatalf("expected explicit edit conflict error, got %v", err)
	}

	writeFixture(t, root, "main.go", "package changed\n")
	_, err = svc.EditFile(context.Background(), "example-service", EditFileOptions{
		RelativePath: "main.go",
		EditToken:    file.EditToken,
		Edits:        []ExactEdit{{StartByte: 0, EndByte: 7, OldText: "package", NewText: "module"}},
	})
	if !errors.Is(err, ErrEditTokenInvalid) {
		t.Fatalf("expected explicit stale token error, got %v", err)
	}
}

func TestWorkspaceService_RejectsDeniedAndSensitiveContent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "main.go", "package main\n")
	writeFixture(t, root, ".env", "TOKEN=secret\n")
	svc := NewService(newWorkspaceRegistry(t, root, projectregistry.WorkspaceModeEdit), nil, Options{Enabled: true})
	recorder := agentactivity.NewRecorder(10)
	svc.SetPolicyRecorder(recorder)
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
	events := recorder.Recent("example-service", 10)
	if len(events) != 1 || events[0].PolicyCategory != "unsafe_edit" || events[0].RelativePath != "main.go" {
		t.Fatalf("expected unsafe edit policy event, got %#v", events)
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

func TestAtomicWriteIgnoresUnsupportedChmod(t *testing.T) {
	err := &os.PathError{Op: "chmod", Path: "/mnt/c/example/.mivia-edit-test", Err: syscall.EPERM}

	if !chmodUnsupported(err) {
		t.Fatal("expected unsupported chmod error to be tolerated")
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
