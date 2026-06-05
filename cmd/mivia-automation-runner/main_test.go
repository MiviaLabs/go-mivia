package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

func TestResolveRunWorkDirUsesDedicatedWorktreePlan(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans/plan-1":
			writeJSON(t, w, runnerWorkPlan{
				ID:             "plan-1",
				ProjectID:      "project-1",
				IsolationMode:  "dedicated_worktree",
				GitBaseRef:     "master",
				GitBranchRef:   "mivia/worktree-docs-smoke",
				GitWorktreeRef: "worktree-docs-smoke",
			})
		case "/api/v1/projects/project-1/workspace/git/worktrees":
			var input struct {
				WorktreeRef string `json:"worktree_ref"`
				BranchRef   string `json:"branch_ref"`
				BaseRef     string `json:"base_ref"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode worktree create input: %v", err)
			}
			if input.WorktreeRef != "worktree-docs-smoke" || input.BranchRef != "mivia/worktree-docs-smoke" || input.BaseRef != "master" {
				t.Fatalf("unexpected worktree create input: %+v", input)
			}
			target := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-worktree-docs-smoke")
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatalf("create worktree target: %v", err)
			}
			if err := os.WriteFile(filepath.Join(target, ".git"), []byte("gitdir: ../metadata\n"), 0o644); err != nil {
				t.Fatalf("write worktree git file: %v", err)
			}
			writeJSON(t, w, map[string]any{"applied": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	resolved, err := client.resolveRunWorkDir(t.Context(), "project-1", "plan-1", baseWorkDir)
	if err != nil {
		t.Fatalf("resolveRunWorkDir returned error: %v", err)
	}
	want := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-worktree-docs-smoke")
	if resolved != want {
		t.Fatalf("expected %q, got %q", want, resolved)
	}
}

func TestResolveRunWorkDirFallsBackForSharedPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans/plan-1":
			writeJSON(t, w, runnerWorkPlan{
				ID:            "plan-1",
				ProjectID:     "project-1",
				IsolationMode: "shared",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	resolved, err := client.resolveRunWorkDir(t.Context(), "project-1", "plan-1", baseWorkDir)
	if err != nil {
		t.Fatalf("resolveRunWorkDir returned error: %v", err)
	}
	if resolved != baseWorkDir {
		t.Fatalf("expected fallback workdir %q, got %q", baseWorkDir, resolved)
	}
}

func TestGetWorkTaskMetadataReadsReviewAndVerifierRefs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-tasks/task-1":
			writeJSON(t, w, runnerWorkTaskMetadata{
				ID:                 "task-1",
				TaskRef:            "task/ref",
				Title:              "GitOps conventions",
				ReviewResultRefs:   []string{"review:approved"},
				VerifierResultRefs: []string{"verifier:focused"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	metadata, err := client.getWorkTaskMetadata(t.Context(), "project-1", "task-1")
	if err != nil {
		t.Fatalf("getWorkTaskMetadata returned error: %v", err)
	}
	if metadata.TaskRef != "task/ref" || strings.Join(metadata.ReviewResultRefs, ",") != "review:approved" || strings.Join(metadata.VerifierResultRefs, ",") != "verifier:focused" {
		t.Fatalf("unexpected task metadata: %+v", metadata)
	}
}

func TestGitOpsPostTaskInputCarriesRunAndTaskMetadata(t *testing.T) {
	input := gitOpsPostTaskInput("fallback-project", "/tmp/worktree", "fallback-operator", projectautomation.ClaimedRun{
		Run: projectautomation.AutomationRun{
			ID:           "automation_run_1",
			ProjectID:    "project-1",
			AutomationID: "automation_1",
			AgentID:      "operator_1",
			PlanID:       "work_plan_1",
			TaskID:       "work_task_1",
		},
		CodexInput: projectautomation.CodexTaskInput{
			TaskRef:             "task/ref-from-codex",
			Title:               "Task title from Codex input",
			LikelyFilesAffected: []string{"internal/projectgitops"},
		},
	}, runnerWorkTaskMetadata{
		TaskRef:            "task/ref-current",
		Title:              "Current task title",
		ReviewResultRefs:   []string{"review:approved"},
		VerifierResultRefs: []string{"verifier:focused"},
	})

	if input.ProjectID != "project-1" || input.AutomationID != "automation_1" || input.OperatorID != "operator_1" {
		t.Fatalf("expected run metadata in gitops input, got %+v", input)
	}
	if input.TaskRef != "task/ref-current" || input.TaskTitle != "Current task title" {
		t.Fatalf("expected current task metadata to win, got %+v", input)
	}
	if strings.Join(input.ReviewRefs, ",") != "review:approved" || strings.Join(input.VerifierRefs, ",") != "verifier:focused" {
		t.Fatalf("expected review/verifier refs, got %+v", input)
	}
}

func TestGitOpsOptionsFromConfigMapsConventions(t *testing.T) {
	options := gitOpsOptionsFromConfig(config.GitOperations{
		Enabled:         true,
		CommitAfterTask: true,
		Conventions: config.GitOpsConventions{
			CommitType:               "feat",
			CommitScope:              "gitops",
			CommitSummaryTemplate:    "finish {{work_task_id}}",
			PullRequestTitleTemplate: "{{commit_subject}}",
			WhatChangedTemplate:      "Changed {{project_id}}.",
			HowVerifiedTemplate:      "Verifier refs: {{verifier_refs}}",
			TestsTemplate:            "{{test_results}}",
		},
	})
	if options.Conventions.CommitType != "feat" || options.Conventions.CommitScope != "gitops" || options.Conventions.WhatChangedTemplate != "Changed {{project_id}}." {
		t.Fatalf("expected convention mapping, got %+v", options.Conventions)
	}
}

func TestDedicatedWorktreePathRequiresAbsoluteBase(t *testing.T) {
	if _, err := dedicatedWorktreePath("relative/repo", "project-1", "worktree-1"); err == nil {
		t.Fatal("expected relative base workdir to be rejected")
	}
}

func TestDedicatedWorktreePathRejectsTraversalRefs(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	if _, err := dedicatedWorktreePath(baseWorkDir, "Project_1", "../Worktree/Docs"); err == nil {
		t.Fatal("expected traversal-looking worktree ref to be rejected")
	}
}

func TestDedicatedWorktreePathMatchesWorkspaceWorktreeNaming(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	resolved, err := dedicatedWorktreePath(baseWorkDir, "Project_1", "worktree/Docs_v1.2")
	if err != nil {
		t.Fatalf("dedicatedWorktreePath returned error: %v", err)
	}
	want := filepath.Join(baseWorkDir, ".mivia-worktrees", "Project_1", "Project_1-worktree-Docs_v1.2")
	if resolved != want {
		t.Fatalf("expected %q, got %q", want, resolved)
	}
}

func TestRunOnceReportsCompletedAttempt(t *testing.T) {
	setReadableCodexHome(t)
	var completed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{
				Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1"},
				CodexInput: testCodexInput("run-1"),
				TimeoutMS:  1000,
			})
		case "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			var input projectautomation.CompleteAttemptInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode attempt: %v", err)
			}
			if input.Status != projectautomation.RunStatusCompleted {
				t.Fatalf("expected completed status, got %q", input.Status)
			}
			completed.Add(1)
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: projectautomation.RunStatusVerifying})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", "/bin/true"})
	if status != 0 {
		t.Fatalf("expected exit 0, got %d", status)
	}
	if completed.Load() != 1 {
		t.Fatalf("expected one completed attempt report, got %d", completed.Load())
	}
}

func TestRunOnceDiscoversProjectsWhenProjectOmitted(t *testing.T) {
	setReadableCodexHome(t)
	var completed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			writeJSON(t, w, map[string]any{
				"projects": []map[string]any{
					{"id": "project-1", "enabled": true},
					{"id": "disabled-project", "enabled": false},
				},
			})
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{
				Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1"},
				CodexInput: testCodexInput("run-1"),
				TimeoutMS:  1000,
			})
		case "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			completed.Add(1)
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: projectautomation.RunStatusVerifying})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--codex", "/bin/true"})
	if status != 0 {
		t.Fatalf("expected exit 0, got %d", status)
	}
	if completed.Load() != 1 {
		t.Fatalf("expected one completed attempt report, got %d", completed.Load())
	}
}

func TestWriteCodexInputWritesRenderedPrompt(t *testing.T) {
	path, cleanup, err := writeCodexInput(testCodexInput("run-1"))
	if err != nil {
		t.Fatalf("writeCodexInput returned error: %v", err)
	}
	defer cleanup()
	if filepath.Base(path) != "codex-input.txt" {
		t.Fatalf("expected codex-input.txt, got %q", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read codex input: %v", err)
	}
	prompt := string(data)
	for _, want := range []string{
		"Perform the task now",
		"Automation run ID: run-1",
		"Task",
		"Do not run full test suites",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRunOnceWithNoConfiguredProjectsExitsIdle(t *testing.T) {
	setReadableCodexHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{"projects": []map[string]any{}})
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--codex", "/bin/true"})
	if status != 0 {
		t.Fatalf("expected idle exit 0, got %d", status)
	}
}

func TestWatchContinuesAfterReportedTaskFailure(t *testing.T) {
	setReadableCodexHome(t)
	codexPath := fakeCodex(t, 1)
	var claimCount atomic.Int32
	var failedReports atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			claim := claimCount.Add(1)
			if claim == 1 {
				writeJSON(t, w, projectautomation.ClaimedRun{
					Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1"},
					CodexInput: testCodexInput("run-1"),
					TimeoutMS:  1000,
				})
				return
			}
			http.Error(w, `{"error":{"code":"invalid_input","message":"no queued automation run"}}`, http.StatusBadRequest)
		case "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			var input projectautomation.CompleteAttemptInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode attempt: %v", err)
			}
			if input.Status != projectautomation.RunStatusFailed {
				t.Fatalf("expected failed status, got %q", input.Status)
			}
			failedReports.Add(1)
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: projectautomation.RunStatusFailed})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", codexPath, "--watch", "--poll-interval", "1ms", "--idle-exit-after", "5ms"})
	if status != 0 {
		t.Fatalf("watch should continue after reported task failure and exit idle with 0, got %d", status)
	}
	if failedReports.Load() != 1 {
		t.Fatalf("expected one failed attempt report, got %d", failedReports.Load())
	}
	if claimCount.Load() < 2 {
		t.Fatalf("expected watch mode to keep polling after failed task, got %d claims", claimCount.Load())
	}
}

func TestClaimNextTreatsPolicyBadRequestAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"invalid_input","message":"task_dependencies_not_done"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	if _, ok, err := client.claimNext(t.Context(), "project-1", ""); err == nil || ok {
		t.Fatalf("expected non-idle bad request to return error, ok=%v err=%v", ok, err)
	}
}

func TestRunPreflightFailsBeforeClaim(t *testing.T) {
	setReadableCodexHome(t)
	var claims atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next" {
			claims.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", filepath.Join(t.TempDir(), "missing-codex")})
	if status == 0 {
		t.Fatal("expected preflight failure")
	}
	if claims.Load() != 0 {
		t.Fatalf("expected no claims after failed preflight, got %d", claims.Load())
	}
}

func TestRunGitOpsRequiresAbsoluteCodexCDBeforeClaim(t *testing.T) {
	setReadableCodexHome(t)
	var claims atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next" {
			claims.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "mivia.toml")
	if err := os.WriteFile(configPath, []byte(`version = 1

[git_operations]
enabled = true
commit_after_task = true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("MIVIA_CONFIG_PATH", configPath)

	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", fakeCodex(t, 0)})
	if status == 0 {
		t.Fatal("expected git operations preflight failure")
	}
	if claims.Load() != 0 {
		t.Fatalf("expected no claims after git operations preflight failure, got %d", claims.Load())
	}
}

func TestCheckCodexLauncherDirect(t *testing.T) {
	binary := fakeCodex(t, 0)
	if err := checkCodexLauncher(t.Context(), codexLaunchOptions{Path: binary}); err != nil {
		t.Fatalf("expected fake codex launcher to pass: %v", err)
	}
}

func TestCheckCodexConfigReadableUsesCodexHome(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"test\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	if err := checkCodexConfigReadable(); err != nil {
		t.Fatalf("expected readable CODEX_HOME config to pass: %v", err)
	}
}

func TestCheckCodexConfigReadableAllowsMissingConfig(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-codex-home"))
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	if err := checkCodexConfigReadable(); err != nil {
		t.Fatalf("expected missing Codex config to pass: %v", err)
	}
}

func TestCheckCodexConfigReadableClassifiesPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permission semantics differ on Windows")
	}
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"test\"\n"), 0o000); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(configPath, 0o600) })
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	if err := checkCodexConfigReadable(); !errors.Is(err, projectautomation.ErrInvalidInput) || !strings.Contains(err.Error(), "codex_config_unreadable") {
		t.Fatalf("expected codex_config_unreadable invalid input, got %v", err)
	}
}

func setReadableCodexHome(t *testing.T) {
	t.Helper()
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"test\"\n"), 0o600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
}

func fakeCodex(t *testing.T, execStatus int) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo codex-test; exit 0; fi\nexit " + string(rune('0'+execStatus)) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return binary
}

func TestBuildRunnerCodexCommandSupportsWindowsLauncher(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "codex-input.json")
	originalConverter := windowsPathForRunner
	windowsPathForRunner = func(path string) (string, error) {
		switch path {
		case inputPath:
			return `\\wsl.localhost\Ubuntu\tmp\codex-input.json`, nil
		case "/workspace/repo":
			return `\\wsl.localhost\Ubuntu\workspace\repo`, nil
		default:
			t.Fatalf("unexpected path to convert: %q", path)
			return "", nil
		}
	}
	t.Cleanup(func() { windowsPathForRunner = originalConverter })

	command, err := buildRunnerCodexCommand(inputPath, time.Minute, codexLaunchOptions{Path: "codex", Launcher: "windows-cmd", WorkDir: "/workspace/repo", Sandbox: "workspace-write"})
	if err != nil {
		t.Fatalf("buildRunnerCodexCommand returned error: %v", err)
	}
	if command.Path != "cmd.exe" {
		t.Fatalf("expected cmd.exe launcher, got %q", command.Path)
	}
	want := []string{"/c", "type", `\\wsl.localhost\Ubuntu\tmp\codex-input.json`, "|", "codex", "exec", "--sandbox", "workspace-write", "--cd", `\\wsl.localhost\Ubuntu\workspace\repo`, "-"}
	if len(command.Args) != len(want) {
		t.Fatalf("unexpected launcher args: %#v", command.Args)
	}
	for index := range want {
		if command.Args[index] != want[index] {
			t.Fatalf("arg %d = %q, want %q; all args %#v", index, command.Args[index], want[index], command.Args)
		}
	}
}

func TestBuildRunnerCodexCommandSupportsDirectLauncherWorkDir(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "codex-input.json")
	command, err := buildRunnerCodexCommand(inputPath, time.Minute, codexLaunchOptions{Path: "/usr/local/bin/codex", Launcher: "direct", WorkDir: "/workspace/repo", Sandbox: "workspace-write"})
	if err != nil {
		t.Fatalf("buildRunnerCodexCommand returned error: %v", err)
	}
	want := []string{"exec", "--sandbox", "workspace-write", "--cd", "/workspace/repo", "-"}
	if len(command.Args) != len(want) {
		t.Fatalf("unexpected args: %#v", command.Args)
	}
	for index := range want {
		if command.Args[index] != want[index] {
			t.Fatalf("arg %d = %q, want %q; all args %#v", index, command.Args[index], want[index], command.Args)
		}
	}
	if command.StdinFile != inputPath {
		t.Fatalf("stdin file = %q, want %q", command.StdinFile, inputPath)
	}
}

func TestBuildRunnerCodexCommandSupportsBypassMode(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "codex-input.txt")
	command, err := buildRunnerCodexCommand(inputPath, time.Minute, codexLaunchOptions{Path: "/usr/local/bin/codex", Launcher: "direct", WorkDir: "/workspace/repo", Sandbox: "workspace-write", BypassApprovalsAndSandbox: true})
	if err != nil {
		t.Fatalf("buildRunnerCodexCommand returned error: %v", err)
	}
	want := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--cd", "/workspace/repo", "-"}
	if len(command.Args) != len(want) {
		t.Fatalf("unexpected args: %#v", command.Args)
	}
	for index := range want {
		if command.Args[index] != want[index] {
			t.Fatalf("arg %d = %q, want %q; all args %#v", index, command.Args[index], want[index], command.Args)
		}
	}
}

func testCodexInput(runID string) projectautomation.CodexTaskInput {
	return projectautomation.CodexTaskInput{
		SchemaVersion:           1,
		ProjectID:               "project-1",
		AutomationRunID:         runID,
		PlanID:                  "plan-1",
		TaskID:                  "task-1",
		TaskRef:                 "task-1",
		Title:                   "Task",
		VerificationRequirement: "orchestrator verifies",
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
