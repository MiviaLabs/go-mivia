package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectgitops"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan/httpapi"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
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
			writeFakeWorktreeMetadata(t, target, "../metadata")
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

func TestComposeRunnerDefaultsToInImageCodexBinary(t *testing.T) {
	for _, path := range []string{"../../docker-compose.yml", "../../.docker-compose.local.yml"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read compose file: %v", err)
			}
			text := string(data)
			if !strings.Contains(text, "${MIVIA_AUTOMATION_CODEX_BINARY:-/usr/local/bin/codex}") {
				t.Fatalf("runner compose default must use in-image codex binary: %s", path)
			}
			if strings.Contains(text, "${MIVIA_AUTOMATION_CODEX_BINARY:-codex}") {
				t.Fatalf("runner compose default must not fall back to PATH codex: %s", path)
			}
		})
	}
}

func TestWorktreePathReadyRejectsStaleGitdirPointer(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".mivia-worktrees", "project-1", "project-1-worktree-docs-smoke")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("create worktree target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, ".git"), []byte("gitdir: ../../.git/worktrees/missing-worktree\n"), 0o644); err != nil {
		t.Fatalf("write stale git pointer: %v", err)
	}
	if worktreePathReady(target) {
		t.Fatalf("stale worktree gitdir pointer must not be considered ready")
	}
}

func writeFakeWorktreeMetadata(t *testing.T, target string, gitdir string) {
	t.Helper()
	metadata := filepath.Clean(filepath.Join(target, filepath.FromSlash(gitdir)))
	if err := os.MkdirAll(metadata, 0o700); err != nil {
		t.Fatalf("create worktree metadata: %v", err)
	}
	for _, name := range []string{"HEAD", "commondir", "gitdir"} {
		if err := os.WriteFile(filepath.Join(metadata, name), []byte("ref: refs/heads/mivia/worktree-docs-smoke\n"), 0o644); err != nil {
			t.Fatalf("write worktree metadata %s: %v", name, err)
		}
	}
}

func TestPrepareRunWorktreeTrustsDedicatedWorktreeBeforeCodex(t *testing.T) {
	baseWorkDir := initRunnerGitRepo(t)
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-worktree-docs-smoke")
	runGit(t, baseWorkDir, "worktree", "add", "-B", "mivia/worktree-docs-smoke", runWorkDir, "main")

	gitLog := filepath.Join(t.TempDir(), "git-log.txt")
	fakeGit := fakeGitLoggingAndDelegating(t, gitLog)
	withPrependedPath(t, filepath.Dir(fakeGit))

	if err := prepareRunWorktree(t.Context(), runWorkDir); err != nil {
		t.Fatalf("prepareRunWorktree returned error: %v", err)
	}
	logData, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatalf("read git log: %v", err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "-c safe.directory="+runWorkDir+" rev-parse --show-toplevel") {
		t.Fatalf("expected safe-directory trust probe before Codex, log=%s", logText)
	}
	if !strings.Contains(logText, "config --global --add safe.directory "+runWorkDir) {
		t.Fatalf("expected persistent safe.directory config before Codex, log=%s", logText)
	}
}

func TestDedicatedWorktreeRunnerInstructionsBlockBaseWorkspaceEdits(t *testing.T) {
	instructions := dedicatedWorktreeRunnerInstructions("/repo/.mivia-worktrees/project/project-wt", "/repo")
	if len(instructions) == 0 {
		t.Fatal("expected dedicated worktree instructions")
	}
	prompt := projectautomation.RenderCodexTaskPrompt(projectautomation.CodexTaskInput{
		SchemaVersion:      1,
		ProjectID:          "mass-monorepo",
		AutomationRunID:    "run-1",
		PlanID:             "plan-1",
		TaskID:             "task-1",
		RunnerInstructions: instructions,
	})
	for _, want := range []string{
		"dedicated worktree",
		"Do not use projects.workspace.file_edit",
		"base checkout",
		"current process workspace",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "/repo/.mivia-worktrees/project/project-wt") || strings.Contains(prompt, "/repo") {
		t.Fatalf("dedicated worktree instructions must not expose absolute paths, got:\n%s", prompt)
	}
}

func TestGitOpsDirtyScopeEvidenceRefsUseRejectedPaths(t *testing.T) {
	err := projectgitops.DirtyWorktreeScopeError{Paths: []string{"apps/domain/src/module.ts"}}
	refs := gitOpsDirtyScopeEvidenceRefs(err)
	if len(refs) != 1 || refs[0] != "gitops-dirty-path:apps/domain/src/module.ts" {
		t.Fatalf("expected dirty path evidence ref, got %+v", refs)
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

func TestResolveTaskScopedRunWorkDirCreatesSuffixedDedicatedWorktree(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	taskToken := taskScopedRefToken("work_task_0529")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans/plan-1":
			writeJSON(t, w, runnerWorkPlan{
				ID:             "plan-1",
				ProjectID:      "project-1",
				IsolationMode:  "dedicated_worktree",
				GitBaseRef:     "main",
				GitBranchRef:   "mivia.audit-0606031743-domain-realtime-ops",
				GitWorktreeRef: "audit.0606031743-domain-realtime-ops",
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
			if input.WorktreeRef != "audit.0606031743-domain-realtime-ops-"+taskToken {
				t.Fatalf("expected task-scoped worktree ref, got %+v", input)
			}
			if input.BranchRef != "mivia.audit-0606031743-domain-realtime-ops-"+taskToken || input.BaseRef != "main" {
				t.Fatalf("unexpected task-scoped branch/base refs: %+v", input)
			}
			target := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-audit.0606031743-domain-realtime-ops-"+taskToken)
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatalf("create task-scoped worktree target: %v", err)
			}
			if err := os.WriteFile(filepath.Join(target, ".git"), []byte("gitdir: ../metadata\n"), 0o644); err != nil {
				t.Fatalf("write task-scoped worktree git file: %v", err)
			}
			writeFakeWorktreeMetadata(t, target, "../metadata")
			writeJSON(t, w, map[string]any{"applied": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	resolved, err := client.resolveTaskScopedRunWorkDir(t.Context(), "project-1", "plan-1", "work_task_0529", baseWorkDir)
	if err != nil {
		t.Fatalf("resolveTaskScopedRunWorkDir returned error: %v", err)
	}
	want := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-audit.0606031743-domain-realtime-ops-"+taskToken)
	if resolved != want {
		t.Fatalf("expected %q, got %q", want, resolved)
	}
}

func TestResolveTaskScopedRunWorkDirSupportsLegacySharedRefsSafely(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	taskID := "MASS/../../old task\nunsafe"
	taskToken := taskScopedRefToken(taskID)
	if taskToken == "" || strings.Contains(taskToken, "..") || strings.ContainsAny(taskToken, "/\\\r\n") || taskToken == taskID {
		t.Fatalf("expected deterministic safe task token, got %q", taskToken)
	}
	var observedWorktreeRef string
	var observedBranchRef string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/mass/work-plans/legacy-plan":
			writeJSON(t, w, runnerWorkPlan{
				ID:             "legacy-plan",
				ProjectID:      "mass",
				IsolationMode:  "shared",
				GitBaseRef:     "main",
				GitBranchRef:   "fix-MASS-0000-shared-audit",
				GitWorktreeRef: "audit/0606031743-domain-realtime-ops",
			})
		case "/api/v1/projects/mass/workspace/git/worktrees":
			var input struct {
				WorktreeRef string `json:"worktree_ref"`
				BranchRef   string `json:"branch_ref"`
				BaseRef     string `json:"base_ref"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode worktree create input: %v", err)
			}
			observedWorktreeRef = input.WorktreeRef
			observedBranchRef = input.BranchRef
			wantWorktreeRef := "audit/0606031743-domain-realtime-ops-" + taskToken
			if input.WorktreeRef != wantWorktreeRef {
				t.Fatalf("expected scoped legacy worktree ref %q, got %+v", wantWorktreeRef, input)
			}
			wantBranchRef := "fix-MASS-0000-shared-audit-" + taskToken
			if input.BranchRef != wantBranchRef || input.BaseRef != "main" {
				t.Fatalf("unexpected scoped legacy branch/base refs: %+v", input)
			}
			target, err := dedicatedWorktreePath(baseWorkDir, "mass", input.WorktreeRef)
			if err != nil {
				t.Fatalf("dedicated worktree path: %v", err)
			}
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatalf("create task-scoped worktree target: %v", err)
			}
			if err := os.WriteFile(filepath.Join(target, ".git"), []byte("gitdir: ../metadata\n"), 0o644); err != nil {
				t.Fatalf("write task-scoped worktree git file: %v", err)
			}
			writeFakeWorktreeMetadata(t, target, "../metadata")
			writeJSON(t, w, map[string]any{"applied": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	resolved, err := client.resolveTaskScopedRunWorkDir(t.Context(), "mass", "legacy-plan", taskID, baseWorkDir)
	if err != nil {
		t.Fatalf("resolveTaskScopedRunWorkDir returned error: %v", err)
	}
	if observedWorktreeRef == "" || observedBranchRef == "" {
		t.Fatal("expected task-scoped worktree creation")
	}
	if strings.Contains(observedWorktreeRef, taskID) || strings.Contains(observedBranchRef, taskID) {
		t.Fatalf("scoped refs must not contain raw unsafe task ID: worktree=%q branch=%q", observedWorktreeRef, observedBranchRef)
	}
	want, err := dedicatedWorktreePath(baseWorkDir, "mass", observedWorktreeRef)
	if err != nil {
		t.Fatalf("dedicated worktree path: %v", err)
	}
	if resolved != want {
		t.Fatalf("expected %q, got %q", want, resolved)
	}
}

func TestRunnerFallsBackToTaskScopedWorktreeForLegacySharedRefs(t *testing.T) {
	baseWorkDir := initRunnerGitRepo(t)
	if err := os.WriteFile(filepath.Join(baseWorkDir, "README.md"), []byte("shared dirty state\n"), 0o644); err != nil {
		t.Fatalf("dirty shared worktree: %v", err)
	}
	taskID := "work_task/unsafe legacy"
	taskToken := taskScopedRefToken(taskID)
	var createdWorktreeRef string
	var createdTarget string
	argsPath := filepath.Join(t.TempDir(), "codex-args.txt")
	codexPath := fakeCodexRecordingArgs(t, argsPath, 1)
	var completed projectautomation.CompleteAttemptInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/projects/mass/work-tasks/") {
			writeJSON(t, w, runnerWorkTaskMetadata{
				ID:           taskID,
				Status:       "needs_review",
				FilesToEdit:  []string{"internal/task.go"},
				EvidenceRefs: []string{"evidence.previous-implementation"},
			})
			return
		}
		switch r.URL.Path {
		case "/api/v1/projects/mass/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{
				Run: projectautomation.AutomationRun{
					ID:          "run-1",
					ProjectID:   "mass",
					PlanID:      "legacy-plan",
					TaskID:      taskID,
					SafeSummary: "pre_execution_recovery",
				},
				CodexInput: projectautomation.CodexTaskInput{
					SchemaVersion:           1,
					ProjectID:               "mass",
					AutomationRunID:         "run-1",
					PlanID:                  "legacy-plan",
					TaskID:                  taskID,
					TaskRef:                 "task/ref",
					Title:                   "Recover shared audit",
					LikelyFilesAffected:     []string{"internal/task.go"},
					VerificationRequirement: "orchestrator verifies",
				},
				TimeoutMS: 1000,
			})
		case "/api/v1/projects/mass/work-plans/legacy-plan":
			writeJSON(t, w, runnerWorkPlan{
				ID:             "legacy-plan",
				ProjectID:      "mass",
				IsolationMode:  "shared",
				GitBaseRef:     "main",
				GitBranchRef:   "mivia/shared-audit",
				GitWorktreeRef: "shared/audit",
			})
		case "/api/v1/projects/mass/workspace/git/worktrees":
			var input struct {
				WorktreeRef string `json:"worktree_ref"`
				BranchRef   string `json:"branch_ref"`
				BaseRef     string `json:"base_ref"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode worktree create input: %v", err)
			}
			createdWorktreeRef = input.WorktreeRef
			if input.WorktreeRef != "shared/audit-"+taskToken {
				t.Fatalf("expected task-scoped legacy worktree ref, got %+v", input)
			}
			target, err := dedicatedWorktreePath(baseWorkDir, "mass", input.WorktreeRef)
			if err != nil {
				t.Fatalf("dedicated worktree path: %v", err)
			}
			createdTarget = target
			runGit(t, baseWorkDir, "worktree", "add", "-B", input.BranchRef, target, input.BaseRef)
			writeJSON(t, w, map[string]any{"applied": true})
		case "/api/v1/projects/mass/automation-runs/run-1/attempt-result":
			if err := json.NewDecoder(r.Body).Decode(&completed); err != nil {
				t.Fatalf("decode attempt result: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: completed.Status})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{GitOperations: config.GitOperations{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: true}}
	status, _, claimed := claimRunExecuteAndReport(t.Context(), &runnerClient{baseURL: server.URL, http: server.Client()}, cfg, "mass", "agent-1", codexLaunchOptions{Path: codexPath, WorkDir: baseWorkDir, Sandbox: "workspace-write"})
	if !claimed || status == 0 {
		t.Fatalf("expected claimed failed codex run after fallback, claimed=%v status=%d", claimed, status)
	}
	if createdWorktreeRef == "" || createdTarget == "" {
		t.Fatal("expected legacy shared run to create task-scoped fallback worktree")
	}
	if completed.FailureCategory != "codex_cli_failed" {
		t.Fatalf("expected codex failure after pre-task fallback, got %+v", completed)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake codex args: %v", err)
	}
	if !strings.Contains(string(args), "--cd "+createdTarget) {
		t.Fatalf("expected codex to run in fallback worktree %q, args=%s", createdTarget, string(args))
	}
	if strings.Contains(createdWorktreeRef, taskID) {
		t.Fatalf("fallback worktree ref must not include raw unsafe task ID: %q", createdWorktreeRef)
	}
}

func TestCleanupTerminalPlanWorktreeRemovesDedicatedWorktree(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-workflow-audit")
	if err := os.MkdirAll(runWorkDir, 0o700); err != nil {
		t.Fatalf("create worktree dir: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans/plan-1":
			writeJSON(t, w, runnerWorkPlan{
				ID:             "plan-1",
				ProjectID:      "project-1",
				Status:         "done",
				IsolationMode:  "dedicated_worktree",
				GitWorktreeRef: "workflow/audit",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	cleanupTerminalPlanWorktree(t.Context(), client, projectgitops.Options{CleanupWorktreeAfterPlanDone: true}, "project-1", "plan-1", baseWorkDir, runWorkDir)
	if _, err := os.Stat(runWorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected dedicated worktree cleanup, stat err=%v", err)
	}
}

func TestCleanupTerminalProjectWorktreesRemovesDonePlanWithoutActiveRun(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-workflow-audit")
	if err := os.MkdirAll(runWorkDir, 0o700); err != nil {
		t.Fatalf("create worktree dir: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans":
			writeJSON(t, w, runnerWorkPlanListResponse{WorkPlans: []runnerWorkPlan{{
				ID:             "plan-1",
				ProjectID:      "project-1",
				Status:         "done",
				IsolationMode:  "dedicated_worktree",
				GitWorktreeRef: "workflow/audit",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	cleanupTerminalProjectWorktrees(t.Context(), client, projectgitops.Options{CleanupWorktreeAfterPlanDone: true}, "project-1", baseWorkDir)
	if _, err := os.Stat(runWorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected reconciled dedicated worktree cleanup, stat err=%v", err)
	}
}

func TestCleanupTerminalProjectWorktreesLeavesBlockedPlans(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-workflow-blocked")
	if err := os.MkdirAll(runWorkDir, 0o700); err != nil {
		t.Fatalf("create worktree dir: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans":
			writeJSON(t, w, runnerWorkPlanListResponse{WorkPlans: []runnerWorkPlan{{
				ID:             "plan-1",
				ProjectID:      "project-1",
				Status:         "blocked",
				IsolationMode:  "dedicated_worktree",
				GitWorktreeRef: "workflow/blocked",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	cleanupTerminalProjectWorktrees(t.Context(), client, projectgitops.Options{CleanupWorktreeAfterPlanDone: true}, "project-1", baseWorkDir)
	if _, err := os.Stat(runWorkDir); err != nil {
		t.Fatalf("expected blocked dedicated worktree to remain: %v", err)
	}
}

func TestCleanupTerminalProjectWorktreesLeavesActivePlans(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-workflow-audit")
	if err := os.MkdirAll(runWorkDir, 0o700); err != nil {
		t.Fatalf("create worktree dir: %v", err)
	}
	orphanWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-old-run")
	if err := os.MkdirAll(orphanWorkDir, 0o700); err != nil {
		t.Fatalf("create orphan worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphanWorkDir, ".git"), []byte("gitdir: stale\n"), 0o600); err != nil {
		t.Fatalf("create stale git marker: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans":
			writeJSON(t, w, runnerWorkPlanListResponse{WorkPlans: []runnerWorkPlan{{
				ID:             "plan-1",
				ProjectID:      "project-1",
				Status:         "active",
				IsolationMode:  "dedicated_worktree",
				GitWorktreeRef: "workflow/audit",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	cleanupTerminalProjectWorktrees(t.Context(), client, projectgitops.Options{CleanupWorktreeAfterPlanDone: true}, "project-1", baseWorkDir)
	if _, err := os.Stat(runWorkDir); err != nil {
		t.Fatalf("expected active plan worktree to remain: %v", err)
	}
	if _, err := os.Stat(orphanWorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected orphan project worktree cleanup, stat err=%v", err)
	}
}

func TestCleanupTerminalProjectWorktreesProtectsPaginatedActiveAndBlockedPlans(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	activeWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-workflow-active")
	blockedWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-workflow-blocked")
	orphanWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-old-run")
	for _, dir := range []string{activeWorkDir, blockedWorkDir, orphanWorkDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create worktree dir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(orphanWorkDir, ".git"), []byte("gitdir: stale\n"), 0o600); err != nil {
		t.Fatalf("create stale git marker: %v", err)
	}

	var pageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans":
			if got := r.URL.Query().Get("page_size"); got != "100" {
				t.Fatalf("expected page_size=100, got %q", got)
			}
			pageRequests.Add(1)
			switch r.URL.Query().Get("page_token") {
			case "":
				writeJSON(t, w, runnerWorkPlanListResponse{
					WorkPlans: []runnerWorkPlan{{
						ID:             "plan-done",
						ProjectID:      "project-1",
						Status:         "done",
						IsolationMode:  "dedicated_worktree",
						GitWorktreeRef: "workflow/done",
					}},
					NextPageToken: "1",
				})
			case "1":
				writeJSON(t, w, runnerWorkPlanListResponse{
					WorkPlans: []runnerWorkPlan{{
						ID:             "plan-active",
						ProjectID:      "project-1",
						Status:         "active",
						IsolationMode:  "dedicated_worktree",
						GitWorktreeRef: "workflow/active",
					}, {
						ID:             "plan-blocked",
						ProjectID:      "project-1",
						Status:         "blocked",
						IsolationMode:  "dedicated_worktree",
						GitWorktreeRef: "workflow/blocked",
					}},
					NextPageToken: "2",
				})
			case "2":
				writeJSON(t, w, runnerWorkPlanListResponse{WorkPlans: []runnerWorkPlan{}})
			default:
				t.Fatalf("unexpected page_token %q", r.URL.Query().Get("page_token"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	cleanupTerminalProjectWorktrees(t.Context(), client, projectgitops.Options{CleanupWorktreeAfterPlanDone: true}, "project-1", baseWorkDir)
	if got := pageRequests.Load(); got != 3 {
		t.Fatalf("expected all work plan pages to be fetched, got %d", got)
	}
	if _, err := os.Stat(activeWorkDir); err != nil {
		t.Fatalf("expected paginated active plan worktree to remain: %v", err)
	}
	if _, err := os.Stat(blockedWorkDir); err != nil {
		t.Fatalf("expected paginated blocked plan worktree to remain: %v", err)
	}
	if _, err := os.Stat(orphanWorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected orphan project worktree cleanup, stat err=%v", err)
	}
}

func TestRemoveDedicatedWorktreeRejectsPathOutsideMiviaWorktrees(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	outside := filepath.Join(baseWorkDir, "not-a-worktree")
	if err := removeDedicatedWorktree(t.Context(), baseWorkDir, outside); err == nil {
		t.Fatal("expected cleanup outside .mivia-worktrees to fail")
	}
}

func TestRemoveDedicatedWorktreePreservesDirtyRegisteredWorktree(t *testing.T) {
	baseWorkDir := initRunnerGitRepo(t)
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-dirty-run")
	runGit(t, baseWorkDir, "worktree", "add", "-B", "mivia/dirty-run", runWorkDir, "main")
	if err := os.WriteFile(filepath.Join(runWorkDir, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("dirty worktree: %v", err)
	}
	err := removeDedicatedWorktree(t.Context(), baseWorkDir, runWorkDir)
	if !errors.Is(err, errDirtyDedicatedWorktree) {
		t.Fatalf("expected dirty worktree preservation, got %v", err)
	}
	if _, statErr := os.Stat(runWorkDir); statErr != nil {
		t.Fatalf("expected dirty worktree to remain, stat err=%v", statErr)
	}
}

func TestGitArgsWithSafeDirectoriesPrecedesWorktreeRemove(t *testing.T) {
	baseWorkDir := filepath.Join(t.TempDir(), "repo")
	runWorkDir := filepath.Join(baseWorkDir, ".mivia-worktrees", "project-1", "project-1-run")
	args := gitArgsWithSafeDirectories([]string{baseWorkDir, runWorkDir, baseWorkDir}, "-C", baseWorkDir, "worktree", "remove", "--force", runWorkDir)
	wantPrefix := []string{
		"-c", "safe.directory=" + filepath.Clean(baseWorkDir),
		"-c", "safe.directory=" + filepath.Clean(runWorkDir),
		"-C", baseWorkDir,
	}
	if len(args) < len(wantPrefix) {
		t.Fatalf("expected safe.directory prefix, got %#v", args)
	}
	for index, want := range wantPrefix {
		if args[index] != want {
			t.Fatalf("arg %d: expected %q, got %q in %#v", index, want, args[index], args)
		}
	}
	gotBaseSafeDir := 0
	for _, arg := range args {
		if arg == "safe.directory="+filepath.Clean(baseWorkDir) {
			gotBaseSafeDir++
		}
	}
	if gotBaseSafeDir != 1 {
		t.Fatalf("expected base safe.directory once, got %d in %#v", gotBaseSafeDir, args)
	}
}

func TestRunnerClientThrottlesProjectCleanup(t *testing.T) {
	client := &runnerClient{projectCleanupInterval: time.Hour}
	if !client.shouldRunProjectCleanup("project-1", "/tmp/repo") {
		t.Fatal("expected first cleanup to run")
	}
	if client.shouldRunProjectCleanup("project-1", "/tmp/repo") {
		t.Fatal("expected repeated cleanup to be throttled")
	}
	if !client.shouldRunProjectCleanup("project-2", "/tmp/repo") {
		t.Fatal("expected different project cleanup to run")
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

func TestReadOnlyReviewRunSkipsGitOpsMutationGuards(t *testing.T) {
	if !isReadOnlyReviewRun(projectautomation.ClaimedRun{
		Run: projectautomation.AutomationRun{SafeSummary: projectautomation.RunSafeSummaryPostImplementationReviewQueued},
	}) {
		t.Fatal("expected post-implementation review run to be read-only")
	}
	if !isReadOnlyReviewRun(projectautomation.ClaimedRun{
		CodexInput: projectautomation.CodexTaskInput{TaskRef: "review-fix-finding"},
	}) {
		t.Fatal("expected review task ref to be read-only")
	}
	if isReadOnlyReviewRun(projectautomation.ClaimedRun{
		CodexInput: projectautomation.CodexTaskInput{TaskRef: "fix-finding"},
	}) {
		t.Fatal("implementation task must not bypass GitOps mutation guards")
	}
}

func TestShouldRunGitOpsForTaskRequiresEditScope(t *testing.T) {
	if shouldRunGitOpsForTask(runnerWorkTaskMetadata{TaskRef: "create-confirmed-bug-work-plans"}) {
		t.Fatal("metadata-only planner tasks must not run GitOps mutation guards")
	}
	if !shouldRunGitOpsForTask(runnerWorkTaskMetadata{TaskRef: "fix-bug", FilesToEdit: []string{"internal/foo.go"}}) {
		t.Fatal("write-capable tasks should run GitOps mutation guards")
	}
}

func TestShouldAutoCloseoutMetadataOnlyTaskRejectsFalseGreenGovernanceSteps(t *testing.T) {
	for _, taskRef := range []string{
		"decompose-work-plan",
		"mark-ready-after-review",
		"select-ready-tasks",
		"run-implementation-batch",
		"review-implementation-batch",
		"orchestrator-verification",
		"pr-gitops-readiness",
	} {
		if shouldAutoCloseoutMetadataOnlyTask(false, runnerWorkTaskMetadata{TaskRef: taskRef}) {
			t.Fatalf("governance step %q must require explicit closeout evidence", taskRef)
		}
	}
	if !shouldAutoCloseoutMetadataOnlyTask(false, runnerWorkTaskMetadata{TaskRef: "context-summary"}) {
		t.Fatal("ordinary metadata-only tasks may still auto-close")
	}
	if !shouldAutoCloseoutMetadataOnlyTask(true, runnerWorkTaskMetadata{TaskRef: "review-pr-gitops-readiness-implementation-independent-review"}) {
		t.Fatal("read-only review gate closeout should remain allowed")
	}
}

func TestShouldAllowScopedDirtyWorktreeForExistingImplementation(t *testing.T) {
	claimed := projectautomation.ClaimedRun{
		Run: projectautomation.AutomationRun{SafeSummary: "dependency_ready_automation_queued"},
	}
	task := runnerWorkTaskMetadata{
		BlockedReason: "toolchain unavailable",
		FilesToEdit:   []string{"apps/frontend-mobile/lib/foo.dart"},
		EvidenceRefs:  []string{"evidence.previous-implementation"},
	}
	if !shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("expected blocked retry with implementation evidence to allow scoped dirty pre-task")
	}
	claimed.Run.SafeSummary = "pre_execution_recovery"
	if !shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("expected recovered blocked retry with implementation evidence to allow scoped dirty pre-task")
	}
	claimed.Run.SafeSummary = "external_runner_queued"
	if !shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("expected explicit rerun with implementation evidence to allow scoped dirty pre-task")
	}
	if shouldAllowScopedDirtyWorktreeForExistingImplementation(projectautomation.ClaimedRun{}, task) {
		t.Fatal("fresh or non-replacement runs must require clean pre-task")
	}
	task.BlockedReason = ""
	if !shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("replacement runs with implementation refs should allow scoped dirty pre-task")
	}
	task.EvidenceRefs = nil
	task.ClaimRefs = []string{"claim.existing-implementation"}
	if !shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("replacement runs with claim refs should allow scoped dirty pre-task")
	}
	task.ClaimRefs = nil
	if shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("replacement runs without implementation refs must require clean pre-task")
	}
	task.FilesToEdit = nil
	task.EvidenceRefs = []string{"evidence.previous-implementation"}
	if shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, task) {
		t.Fatal("metadata-only replacement runs must require clean pre-task")
	}
}

func TestGitOpsTaskPathspecsPreferFilesToEditOverBroadLikelyFiles(t *testing.T) {
	claimed := projectautomation.ClaimedRun{
		CodexInput: projectautomation.CodexTaskInput{LikelyFilesAffected: []string{"apps"}},
	}
	got := gitOpsTaskPathspecs(claimed, runnerWorkTaskMetadata{
		FilesToEdit: []string{"apps/domain-inventory/src/trpc/trpc.router.ts"},
	})
	if strings.Join(got, ",") != "apps/domain-inventory/src/trpc/trpc.router.ts" {
		t.Fatalf("expected files_to_edit to define write scope, got %+v", got)
	}
	got = gitOpsTaskPathspecs(claimed, runnerWorkTaskMetadata{})
	if strings.Join(got, ",") != "apps" {
		t.Fatalf("expected likely files fallback, got %+v", got)
	}
}

func TestPreTaskWithinScopeReportsDirtyPathEvidence(t *testing.T) {
	runner := &gitOpsRecordingRunner{results: []projectgitops.CommandResult{
		{},
		{Stdout: " M apps/domain/src/service.ts\n M apps/domain/src/module.ts\n"},
	}}
	gitOps := projectgitops.NewWithRunner(projectgitops.Options{
		Enabled:                true,
		CommitAfterTask:        true,
		RequireCleanBeforeTask: true,
	}, runner)

	err := gitOps.PreTaskWithinScope(t.Context(), "/tmp/worktree", []string{"apps/domain/src/service.ts"})
	if !errors.Is(err, projectgitops.ErrDirtyWorktreeScope) {
		t.Fatalf("expected scoped dirty worktree, got %v", err)
	}
	refs := gitOpsDirtyScopeEvidenceRefs(err)
	if strings.Join(refs, ",") != "gitops-dirty-path:apps/domain/src/module.ts" {
		t.Fatalf("expected exact dirty-path evidence ref, got %+v", refs)
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

func TestGitOpsOptionsForProjectInheritsSparseOverride(t *testing.T) {
	cfg := config.Config{
		GitOperations: config.GitOperations{
			Enabled:                true,
			CommitAfterTask:        true,
			PushAfterTask:          true,
			DraftPRAfterPush:       true,
			RequireCleanBeforeTask: true,
			RemoteName:             "origin",
			BranchPrefix:           "mivia/",
			CommitAuthorName:       "Mivia Automation",
			CommitAuthorEmailEnv:   "MIVIA_GIT_AUTHOR_EMAIL",
			SSHPrivateKeyPath:      "/keys/id_ed25519",
			SSHKnownHostsPath:      "/keys/known_hosts",
			GitHubTokenEnv:         "GH_TOKEN",
			GitHubCLIPath:          "gh",
			Conventions: config.GitOpsConventions{
				CommitType:               "fix",
				CommitSummaryTemplate:    "finish {{work_task_ref}}",
				PullRequestTitleTemplate: "{{commit_subject}}",
			},
		},
		Projects: []config.Project{{
			ID: "mass-monorepo",
			GitOperations: &config.GitOperations{
				BranchPrefix:      "",
				BranchNamePattern: "^(feat|fix)-MASS-[0-9]+(-[a-z0-9-]+)*$",
				Conventions: config.GitOpsConventions{
					CommitType:          "chore",
					WhatChangedTemplate: "Project-specific summary",
				},
			},
		}},
	}

	options := gitOpsOptionsForProject(cfg, "mass-monorepo")
	if !options.Enabled || !options.CommitAfterTask || !options.PushAfterTask || !options.DraftPRAfterPush || !options.RequireCleanBeforeTask {
		t.Fatalf("expected sparse project override to inherit enabled GitOps booleans, got %+v", options)
	}
	if options.BranchPrefix != "" || options.BranchNamePattern == "" {
		t.Fatalf("expected project branch convention override, got prefix=%q pattern=%q", options.BranchPrefix, options.BranchNamePattern)
	}
	if options.CommitAuthorEmailEnv != "MIVIA_GIT_AUTHOR_EMAIL" || options.SSHPrivateKeyPath == "" || options.GitHubTokenEnv != "GH_TOKEN" {
		t.Fatalf("expected global credential refs to be inherited, got %+v", options)
	}
	if options.Conventions.CommitType != "chore" || options.Conventions.CommitSummaryTemplate == "" || options.Conventions.WhatChangedTemplate != "Project-specific summary" {
		t.Fatalf("expected sparse conventions to merge, got %+v", options.Conventions)
	}
}

func TestRunGitOpsPostTaskRecoveryCommitsWithoutCodex(t *testing.T) {
	runner := &gitOpsRecordingRunner{results: []projectgitops.CommandResult{
		{},
		{Stdout: " M README.md\n"},
		{Stdout: "mivia/recovery\n"},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
	}}
	gitOps := projectgitops.NewWithRunner(projectgitops.Options{
		Enabled:              true,
		CommitAfterTask:      true,
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-tasks/task-1":
			writeJSON(t, w, runnerWorkTaskMetadata{
				ID:                 "task-1",
				TaskRef:            "task/ref",
				Title:              "Recover GitOps",
				Status:             "needs_review",
				EvidenceRefs:       []string{"implementation/evidence"},
				VerifierResultRefs: []string{"verifier/focused"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &runnerClient{baseURL: server.URL, http: server.Client()}

	status, failure, _, evidenceRefs := runGitOpsPostTaskRecovery(t.Context(), client, gitOps, "project-1", "/tmp/worktree", "agent-1", projectautomation.ClaimedRun{
		Run: projectautomation.AutomationRun{
			ID:           "run-1",
			ProjectID:    "project-1",
			AutomationID: "automation-1",
			AgentID:      "agent-1",
			PlanID:       "plan-1",
			TaskID:       "task-1",
			SafeSummary:  projectautomation.RunSafeSummaryGitOpsPostTaskRecovery,
		},
		CodexInput: projectautomation.CodexTaskInput{LikelyFilesAffected: []string{"README.md"}},
	})
	if status != projectautomation.RunStatusCompleted || failure != "" {
		t.Fatalf("expected recovery completion, got status=%q failure=%q", status, failure)
	}
	if strings.Join(evidenceRefs, ",") != "git-commit-created,git-commit-abc123def456" {
		t.Fatalf("unexpected evidence refs: %+v", evidenceRefs)
	}
	if got := strings.Join(runner.commands[5].Args, " "); !strings.Contains(got, "commit --no-verify -m") {
		t.Fatalf("expected commit command, got %q", got)
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

func TestRunOnceFailsCompletedAttemptWithoutGovernedCloseout(t *testing.T) {
	setReadableCodexHome(t)
	var completed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			writeJSON(t, w, projectautomation.ClaimedRun{
				Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", TaskID: "task-1"},
				CodexInput: testCodexInput("run-1"),
				TimeoutMS:  1000,
			})
		case "/api/v1/projects/project-1/work-tasks/task-1":
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", Status: "in_progress", FilesToEdit: []string{"internal/service.go"}})
		case "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			var input projectautomation.CompleteAttemptInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode attempt: %v", err)
			}
			if input.Status != projectautomation.RunStatusFailed || input.FailureCategory != "automation_task_closeout_missing" {
				t.Fatalf("expected closeout failure, got %+v", input)
			}
			completed.Add(1)
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: projectautomation.RunStatusFailed})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", "/bin/true"})
	if status == 0 {
		t.Fatal("expected exit failure when task closeout is missing")
	}
	if completed.Load() != 1 {
		t.Fatalf("expected one attempt report, got %d", completed.Load())
	}
}

func TestRunOnceFailsGovernanceStepsWithoutExplicitCloseout(t *testing.T) {
	for _, taskRef := range []string{
		"decompose-work-plan",
		"mark-ready-after-review",
		"select-ready-tasks",
		"run-implementation-batch",
		"review-implementation-batch",
		"orchestrator-verification",
		"pr-gitops-readiness",
	} {
		t.Run(taskRef, func(t *testing.T) {
			setReadableCodexHome(t)
			var completed atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/projects/mass-monorepo/automation-runs/claim-next":
					input := testCodexInput("run-1")
					input.ProjectID = "mass-monorepo"
					input.TaskRef = taskRef
					writeJSON(t, w, projectautomation.ClaimedRun{
						Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "mass-monorepo", PlanID: "plan-1", TaskID: "task-1"},
						CodexInput: input,
						TimeoutMS:  1000,
					})
				case "/api/v1/projects/mass-monorepo/work-plans/plan-1":
					writeJSON(t, w, runnerWorkPlan{ID: "plan-1", ProjectID: "mass-monorepo", IsolationMode: "shared"})
				case "/api/v1/projects/mass-monorepo/work-tasks/task-1":
					writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: taskRef, Status: "in_progress"})
				case "/api/v1/projects/mass-monorepo/automation-runs/run-1/attempt-result":
					var input projectautomation.CompleteAttemptInput
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Fatalf("decode attempt: %v", err)
					}
					if input.Status != projectautomation.RunStatusFailed || input.FailureCategory != governedCloseoutOutputMissing {
						t.Fatalf("expected governed closeout failure, got %+v", input)
					}
					completed.Add(1)
					writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: projectautomation.RunStatusFailed})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			status := run([]string{"--server", server.URL, "--project", "mass-monorepo", "--codex", "/bin/true"})
			if status == 0 {
				t.Fatal("expected exit failure when governed closeout is missing")
			}
			if completed.Load() != 1 {
				t.Fatalf("expected one failed attempt report, got %d", completed.Load())
			}
		})
	}
}

func TestRunOnceClosesOutReadOnlyReviewTaskAfterCodexSuccess(t *testing.T) {
	setReadableCodexHome(t)
	var attemptCompleted atomic.Int32
	var verifierAttached atomic.Int32
	var taskVerifying atomic.Int32
	var taskCompleted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			input := testCodexInput("run-1")
			input.TaskRef = "review-run-planning-automation"
			writeJSON(t, w, projectautomation.ClaimedRun{
				Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", TaskID: "task-1", TraceID: "trace-1"},
				CodexInput: input,
				TimeoutMS:  1000,
			})
		case "/api/v1/projects/project-1/work-tasks/task-1":
			if taskCompleted.Load() > 0 {
				writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", Status: "done", ReviewResultRefs: []string{"review:run-1"}, VerifierResultRefs: []string{"verifier:run-1"}})
				return
			}
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", Status: "in_progress"})
		case "/api/v1/projects/project-1/work-tasks/task-1/verifier-results":
			var input struct {
				Ref             string `json:"ref"`
				AttachedByRunID string `json:"attached_by_run_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode verifier result: %v", err)
			}
			if input.Ref != "verifier:run-1" || input.AttachedByRunID != "run-1" {
				t.Fatalf("unexpected verifier result input: %+v", input)
			}
			verifierAttached.Add(1)
			writeJSON(t, w, map[string]string{"ref": input.Ref})
		case "/api/v1/projects/project-1/work-tasks/task-1/status":
			if verifierAttached.Load() == 0 {
				t.Fatal("status update attempted before verifier result attachment")
			}
			var input struct {
				RunID  string `json:"run_id"`
				Status string `json:"status"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode status task: %v", err)
			}
			if input.RunID != "run-1" || input.Status != "verifying" {
				t.Fatalf("unexpected status input: %+v", input)
			}
			taskVerifying.Add(1)
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", Status: "verifying"})
		case "/api/v1/projects/project-1/work-tasks/task-1/complete":
			if taskVerifying.Load() == 0 {
				t.Fatal("complete attempted before verifying transition")
			}
			var input struct {
				RunID              string   `json:"run_id"`
				VerifierResultRefs []string `json:"verifier_result_refs"`
				ReviewExemptReason string   `json:"review_exempt_reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode complete task: %v", err)
			}
			if input.RunID != "run-1" || strings.Join(input.VerifierResultRefs, ",") != "verifier:run-1" || !strings.Contains(input.ReviewExemptReason, "nested self-review is prohibited") {
				t.Fatalf("unexpected complete input: %+v", input)
			}
			taskCompleted.Add(1)
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", Status: "done"})
		case "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			var input projectautomation.CompleteAttemptInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatalf("decode attempt: %v", err)
			}
			if input.Status != projectautomation.RunStatusCompleted || input.FailureCategory != "" {
				t.Fatalf("expected completed attempt, got %+v", input)
			}
			attemptCompleted.Add(1)
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
	if attemptCompleted.Load() != 1 || verifierAttached.Load() != 1 || taskVerifying.Load() != 1 || taskCompleted.Load() != 1 {
		t.Fatalf("expected closeout calls and attempt completion, got attempt=%d verifier=%d verifying=%d task=%d", attemptCompleted.Load(), verifierAttached.Load(), taskVerifying.Load(), taskCompleted.Load())
	}
}

func TestParseGovernedCloseoutRejectsMissingAndUnsafeOutput(t *testing.T) {
	if _, err := parseGovernedCloseoutOutput(""); governedCloseoutFailureCategory(err) != governedCloseoutOutputMissing {
		t.Fatalf("expected missing output category, got %v", err)
	}
	for name, payload := range map[string]string{
		"trailing_json":   `{"closeout_action":"block","outcome":"ok","safe_next_action":"retry","evidence_refs":[],"verifier_result_refs":[],"child_tasks":[],"block_reason":"missing evidence","failure_reason":""} {"extra":true}`,
		"unknown_field":   `{"closeout_action":"block","outcome":"ok","safe_next_action":"retry","evidence_refs":[],"verifier_result_refs":[],"child_tasks":[],"block_reason":"missing evidence","failure_reason":"","raw_log":"secret"}`,
		"multiple_fences": "First:\n```json\n" + `{"closeout_action":"block","outcome":"ok","safe_next_action":"retry","evidence_refs":[],"verifier_result_refs":[],"child_tasks":[],"block_reason":"missing evidence","failure_reason":""}` + "\n```\nSecond:\n```json\n" + `{"closeout_action":"block","outcome":"ok","safe_next_action":"retry","evidence_refs":[],"verifier_result_refs":[],"child_tasks":[],"block_reason":"missing evidence","failure_reason":""}` + "\n```",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGovernedCloseoutOutput(payload); governedCloseoutFailureCategory(err) != governedCloseoutInvalidJSON {
				t.Fatalf("expected invalid json category, got %v", err)
			}
		})
	}
	_, err := parseGovernedCloseoutOutput(`{"closeout_action":"needs_review","outcome":"ok","safe_next_action":"next","evidence_refs":["bad/ref"],"verifier_result_refs":[],"child_tasks":[]}`)
	if governedCloseoutFailureCategory(validateGovernedCloseoutOutput(mustParseGovernedCloseout(t, `{"closeout_action":"needs_review","outcome":"ok","safe_next_action":"next","evidence_refs":["bad/ref"],"verifier_result_refs":[],"child_tasks":[]}`), runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"})) != governedCloseoutValidationFailed {
		t.Fatalf("expected unsafe ref validation failure")
	}
	if err == nil {
		// parse alone should allow the JSON shape; validation owns semantic safety.
		return
	}
}

func TestParseGovernedCloseoutAcceptsWrappedSingleJSONObject(t *testing.T) {
	base := `{"closeout_action":"block","outcome":"ok with braces {inside} string","safe_next_action":"retry","evidence_refs":[],"verifier_result_refs":[],"child_tasks":[],"block_reason":"missing evidence","failure_reason":""}`
	for name, payload := range map[string]string{
		"single_fence_with_intro":  "Closeout:\n```json\n" + base + "\n```",
		"single_object_with_intro": "Closeout:\n" + base,
	} {
		t.Run(name, func(t *testing.T) {
			output, err := parseGovernedCloseoutOutput(payload)
			if err != nil {
				t.Fatalf("parse wrapped closeout: %v", err)
			}
			if output.CloseoutAction != "block" || output.BlockReason != "missing evidence" {
				t.Fatalf("unexpected parsed closeout: %+v", output)
			}
		})
	}
}

func TestParseGovernedCloseoutAllowsExtraChildTaskGovernanceFields(t *testing.T) {
	output, err := parseGovernedCloseoutOutput(governedCloseoutFixtureJSON())
	if err != nil {
		t.Fatalf("extra child task governance fields must not fail JSON parsing: %v", err)
	}
	if err := validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"}); err != nil {
		t.Fatalf("runner validation should accept otherwise valid child task: %v", err)
	}
}

func TestValidateGovernedCloseoutRejectsChildTaskMetadataBeforeREST(t *testing.T) {
	output := mustParseGovernedCloseout(t, governedCloseoutFixtureJSON())
	output.ChildTasks[0].ExpectedOutput = strings.Repeat("x", closeoutWorkTaskTextMax+1)
	err := validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"})
	if governedCloseoutFailureCategory(err) != governedCloseoutValidationFailed || !strings.Contains(err.Error(), "REST limits") {
		t.Fatalf("expected REST-compatible validation failure, got %v", err)
	}

	output = mustParseGovernedCloseout(t, governedCloseoutFixtureJSON())
	output.ChildTasks[0].Status = "done"
	err = validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"})
	if governedCloseoutFailureCategory(err) != governedCloseoutValidationFailed || !strings.Contains(err.Error(), "status") {
		t.Fatalf("expected terminal child status validation failure, got %v", err)
	}

	output = mustParseGovernedCloseout(t, governedCloseoutFixtureJSON())
	output.ChildTasks[0].DownstreamImpactRefs = nil
	err = validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"})
	if governedCloseoutFailureCategory(err) != governedCloseoutValidationFailed || !strings.Contains(err.Error(), "governance metadata") {
		t.Fatalf("expected missing governance validation failure, got %v", err)
	}
}

func TestApplyGovernedCloseoutWorksAgainstWorkTaskRESTContract(t *testing.T) {
	svc := projectworkplan.New(workplanstore.NewMemoryStore())
	ctx := t.Context()
	plan, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan-closeout-contract",
		Title:       "Closeout Contract",
		GoalSummary: "verify runner child task closeout matches Work Task REST limits",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "decompose-work-plan",
		Title:                   "Decompose Work Plan",
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              "planning-worker",
		VerificationRequirement: "orchestrator verifies child tasks",
		ExpectedOutput:          "child work tasks",
		ResumeInstructions:      "resume decomposition",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, OwnerAgent: "planning-worker", RunID: "run-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, svc)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		for key, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		if rec.Code >= http.StatusBadRequest {
			t.Logf("%s %s returned %d: %s", r.Method, r.URL.Path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	err = client.applyGovernedCloseoutFromOutput(ctx, "project-1", projectautomation.ClaimedRun{
		Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", PlanID: plan.ID, TaskID: task.ID, TraceID: "trace-1"},
		CodexInput: projectautomation.CodexTaskInput{PlanID: plan.ID, TaskID: task.ID},
	}, runnerWorkTaskMetadata{ID: task.ID, TaskRef: "decompose-work-plan", Status: "in_progress"}, governedCloseoutFixtureJSON())
	if err != nil {
		t.Fatalf("apply governed closeout against REST contract: %v", err)
	}
	tasks, err := svc.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: plan.ID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var childFound bool
	for _, candidate := range tasks {
		if candidate.TaskRef == "implement-ticket-slice" {
			childFound = true
			if candidate.Status != projectworkplan.WorkTaskStatusPlanned || candidate.VerificationRequirement == "" {
				t.Fatalf("unexpected child task: %+v", candidate)
			}
			if len(candidate.AcceptanceCriteria) == 0 || len(candidate.StopConditions) == 0 || len(candidate.VerifierLadder) == 0 {
				t.Fatalf("child task lost governance lists: %+v", candidate)
			}
			if candidate.RegressionApplicability == "" || len(candidate.DownstreamImpactRefs) == 0 || candidate.OutputContract == "" {
				t.Fatalf("child task lost downstream contract metadata: %+v", candidate)
			}
		}
	}
	if !childFound {
		t.Fatalf("expected child task to be created, got %+v", tasks)
	}
}

func TestApplyGovernedCloseoutCreatesChildTasksAndMovesWrapperToNeedsReview(t *testing.T) {
	var childCreated atomic.Int32
	var evidenceAttached atomic.Int32
	var verifierAttached atomic.Int32
	var statusMoved atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/work-plans/plan-1/tasks":
			childCreated.Add(1)
			writeJSON(t, w, map[string]string{"id": "child-1", "status": "planned"})
		case "/api/v1/projects/project-1/work-tasks/task-1/evidence":
			evidenceAttached.Add(1)
			writeJSON(t, w, map[string]string{"ref": "evidence.governed"})
		case "/api/v1/projects/project-1/work-tasks/task-1/verifier-results":
			if statusMoved.Load() == 0 {
				t.Fatal("needs_review closeout attached verifier before status transition")
			}
			verifierAttached.Add(1)
			writeJSON(t, w, map[string]string{"ref": "verifier.governed"})
		case "/api/v1/projects/project-1/work-tasks/task-1/status":
			statusMoved.Add(1)
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "needs_review", EvidenceRefs: []string{"evidence.governed"}})
		case "/api/v1/projects/project-1/work-tasks/task-1":
			if statusMoved.Load() > 0 {
				writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "needs_review", EvidenceRefs: []string{"evidence.governed"}, VerifierResultRefs: []string{"verifier.governed"}})
				return
			}
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "in_progress"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	err := client.applyGovernedCloseoutFromOutput(t.Context(), "project-1", projectautomation.ClaimedRun{
		Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", PlanID: "plan-1", TaskID: "task-1", TraceID: "trace-1"},
		CodexInput: projectautomation.CodexTaskInput{PlanID: "plan-1", TaskID: "task-1"},
	}, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "in_progress"}, governedCloseoutFixtureJSON())
	if err != nil {
		t.Fatalf("apply governed closeout returned error: %v", err)
	}
	if childCreated.Load() != 1 || evidenceAttached.Load() != 1 || verifierAttached.Load() != 1 || statusMoved.Load() != 1 {
		t.Fatalf("expected create/evidence/verifier/status calls, got child=%d evidence=%d verifier=%d status=%d", childCreated.Load(), evidenceAttached.Load(), verifierAttached.Load(), statusMoved.Load())
	}
}

func TestRunOnceAppliesGovernedCloseoutFromCodexOutput(t *testing.T) {
	setReadableCodexHome(t)
	codexPath := fakeCodexWritingLastMessage(t, governedCloseoutFixtureJSON())
	var childCreated atomic.Int32
	var attemptCompleted projectautomation.CompleteAttemptInput
	var statusMoved atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			input := testCodexInput("run-1")
			input.TaskRef = "decompose-work-plan"
			writeJSON(t, w, projectautomation.ClaimedRun{
				Run:        projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", PlanID: "plan-1", TaskID: "task-1"},
				CodexInput: input,
				TimeoutMS:  1000,
			})
		case "/api/v1/projects/project-1/work-plans/plan-1":
			writeJSON(t, w, runnerWorkPlan{ID: "plan-1", ProjectID: "project-1", IsolationMode: "shared"})
		case "/api/v1/projects/project-1/work-plans/plan-1/tasks":
			childCreated.Add(1)
			writeJSON(t, w, map[string]string{"id": "child-1"})
		case "/api/v1/projects/project-1/work-tasks/task-1/evidence", "/api/v1/projects/project-1/work-tasks/task-1/verifier-results":
			writeJSON(t, w, map[string]string{"ref": "ok"})
		case "/api/v1/projects/project-1/work-tasks/task-1/status":
			statusMoved.Add(1)
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "needs_review", EvidenceRefs: []string{"evidence.governed"}})
		case "/api/v1/projects/project-1/work-tasks/task-1":
			if statusMoved.Load() > 0 {
				writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "needs_review", EvidenceRefs: []string{"evidence.governed"}})
				return
			}
			writeJSON(t, w, runnerWorkTaskMetadata{ID: "task-1", TaskRef: "decompose-work-plan", Status: "in_progress"})
		case "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			if err := json.NewDecoder(r.Body).Decode(&attemptCompleted); err != nil {
				t.Fatalf("decode attempt: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", Status: attemptCompleted.Status})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", codexPath})
	if status != 0 {
		t.Fatalf("expected runner success, got %d", status)
	}
	if childCreated.Load() != 1 || statusMoved.Load() != 1 {
		t.Fatalf("expected runner-owned closeout calls, child=%d status=%d", childCreated.Load(), statusMoved.Load())
	}
	if attemptCompleted.Status != projectautomation.RunStatusCompleted || attemptCompleted.FailureCategory != "" {
		t.Fatalf("expected completed attempt, got %+v", attemptCompleted)
	}
}

func TestRunnerClientCloseoutReadOnlyReviewTaskUsesWorkTaskLifecycle(t *testing.T) {
	testRunnerClientCloseoutMetadataOnlyTaskUsesWorkTaskLifecycle(t, true)
}

func TestRunnerClientCloseoutMetadataOnlyPlanningTaskUsesWorkTaskLifecycle(t *testing.T) {
	testRunnerClientCloseoutMetadataOnlyTaskUsesWorkTaskLifecycle(t, false)
}

func testRunnerClientCloseoutMetadataOnlyTaskUsesWorkTaskLifecycle(t *testing.T, reviewTask bool) {
	svc := projectworkplan.New(workplanstore.NewMemoryStore())
	ctx := t.Context()
	plan, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan/runner-closeout",
		Title:       "Runner closeout lifecycle",
		GoalSummary: "verify runner closeout uses valid Work Task lifecycle",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "review-run-planning-automation",
		Title:                   "Review planning automation",
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              "planning-reviewer",
		VerificationRequirement: "attach reviewer gate verifier ref",
		ExpectedOutput:          "metadata-only review closeout",
		ResumeInstructions:      "complete reviewer gate closeout",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, OwnerAgent: "planning-reviewer", RunID: "run-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, svc)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		for key, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		if rec.Code >= http.StatusBadRequest {
			t.Logf("%s %s returned %d: %s", r.Method, r.URL.Path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client()}
	err = client.closeoutMetadataOnlyTask(ctx, "project-1", projectautomation.ClaimedRun{
		Run: projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", TaskID: task.ID, TraceID: "trace-1"},
	}, reviewTask)
	if err != nil {
		t.Fatalf("closeout read-only review task: %v", err)
	}
	done, err := svc.GetWorkTask(ctx, "project-1", task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if done.Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected done task, got %+v", done)
	}
	if strings.Join(done.VerifierResultRefs, ",") != "verifier:run-1" {
		t.Fatalf("expected verifier ref to be recorded, got %+v", done.VerifierResultRefs)
	}
	if reviewTask && !strings.Contains(done.ReviewExemptReason, "nested self-review is prohibited") {
		t.Fatalf("expected reviewer closeout exemption, got %q", done.ReviewExemptReason)
	}
	if !reviewTask && !strings.Contains(done.ReviewExemptReason, "changed no repository files") {
		t.Fatalf("expected metadata-only closeout exemption, got %q", done.ReviewExemptReason)
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
	input := testCodexInput("run-1")
	input.MCPServerURL = "http://mivia-server:8080"
	path, cleanup, err := writeCodexInput(input)
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
		"Mivia server URL: http://mivia-server:8080",
		"Concrete REST closeout endpoints",
		"http://mivia-server:8080/api/v1/projects/project-1/work-tasks/task-1/status",
		"Do not call projects.automation_runs.complete_attempt",
		"runner commits, pushes, and opens draft PRs",
		"projects.automations.create_remediation_from_finding",
		"Do not call projects.automations.run",
		"move this Work Task out of in_progress",
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

func TestWatchRetriesProjectDiscoveryFailure(t *testing.T) {
	setReadableCodexHome(t)
	var listCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			http.NotFound(w, r)
			return
		}
		if listCount.Add(1) == 1 {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, map[string]any{"projects": []map[string]any{}})
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--codex", "/bin/true", "--watch", "--poll-interval", "1ms", "--idle-exit-after", "5ms"})
	if status != 0 {
		t.Fatalf("watch should retry project discovery failure and exit idle with 0, got %d", status)
	}
	if listCount.Load() < 2 {
		t.Fatalf("expected project discovery retry, got %d", listCount.Load())
	}
}

func TestWatchRetriesClaimFailure(t *testing.T) {
	setReadableCodexHome(t)
	var claimCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/project-1/automation-runs/claim-next":
			if claimCount.Add(1) == 1 {
				http.Error(w, "warming up", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, `{"error":{"code":"invalid_input","message":"no queued automation run"}}`, http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	status := run([]string{"--server", server.URL, "--project", "project-1", "--codex", "/bin/true", "--watch", "--poll-interval", "1ms", "--idle-exit-after", "5ms"})
	if status != 0 {
		t.Fatalf("watch should retry claim failure and exit idle with 0, got %d", status)
	}
	if claimCount.Load() < 2 {
		t.Fatalf("expected claim retry, got %d", claimCount.Load())
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

func TestClaimNextHTTPTimeoutPreventsStuckWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		writeJSON(t, w, projectautomation.ClaimedRun{})
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: &http.Client{Timeout: 5 * time.Millisecond}}
	started := time.Now()
	if _, ok, err := client.claimNext(t.Context(), "project-1", ""); err == nil || ok {
		t.Fatalf("expected timeout error, ok=%v err=%v", ok, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("claim timeout took too long: %s", elapsed)
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

func TestCheckCodexLauncherRejectsWindowsCmd(t *testing.T) {
	err := checkCodexLauncher(t.Context(), codexLaunchOptions{Path: "codex", Launcher: "windows-cmd"})
	if !errors.Is(err, projectautomation.ErrInvalidInput) || !strings.Contains(err.Error(), "unknown codex launcher") {
		t.Fatalf("expected windows launcher to be rejected, got %v", err)
	}
}

func TestCheckRunnerCodexPreflightRejectsMissingWorkDirBeforeClaim(t *testing.T) {
	err := checkRunnerCodexPreflight(t.Context(), codexLaunchOptions{Path: fakeCodex(t, 0), Launcher: "direct", WorkDir: filepath.Join(t.TempDir(), "missing"), SmokePreflight: false})
	if err == nil || !strings.Contains(err.Error(), "codex_workdir_unavailable") {
		t.Fatalf("expected missing workdir preflight failure, got %v", err)
	}
}

func TestCheckRunnerCodexPreflightRunsCodexInGitWorkDir(t *testing.T) {
	workDir := initRunnerGitRepo(t)
	binary := fakeCodexWritingLastMessage(t, `{"ok": true}`)
	if err := checkRunnerCodexPreflight(t.Context(), codexLaunchOptions{Path: binary, Launcher: "direct", WorkDir: workDir, Sandbox: "workspace-write", SmokePreflight: true}); err != nil {
		t.Fatalf("expected codex smoke preflight to pass: %v", err)
	}
}

func TestGovernedCloseoutSchemaMatchesAcceptedFixtureConstants(t *testing.T) {
	schemaPath, cleanup, err := createGovernedCloseoutSchemaFile()
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer cleanup()
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	schemaText := string(data)
	for _, want := range []string{
		`"needs_review"`,
		`"block"`,
		`"fail"`,
	} {
		if !strings.Contains(schemaText, want) {
			t.Fatalf("schema missing accepted value %s: %s", want, schemaText)
		}
	}
	if strings.Contains(schemaText, "ready_for_worker") {
		t.Fatalf("schema must not require non-existent decomposition quality constant: %s", schemaText)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema must be valid json: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing properties: %s", schemaText)
	}
	childTasks, ok := properties["child_tasks"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing child_tasks: %s", schemaText)
	}
	if childTasks["maxItems"] != float64(50) {
		t.Fatalf("schema-constrained closeout must allow child task packets: %#v", childTasks)
	}
	items, ok := childTasks["items"].(map[string]any)
	if !ok || items["type"] != "object" || items["additionalProperties"] != false {
		t.Fatalf("child_tasks items must be strict-schema-compatible objects, got %#v", childTasks["items"])
	}
	itemProperties, ok := items["properties"].(map[string]any)
	if !ok || itemProperties["acceptance_criteria"] == nil || itemProperties["stop_conditions"] == nil {
		t.Fatalf("child_tasks schema missing governance metadata fields: %#v", itemProperties)
	}
	required, ok := items["required"].([]any)
	if !ok || len(required) != len(itemProperties) {
		t.Fatalf("child_tasks schema must require every listed property for Codex strict schema: %#v", items["required"])
	}
	output := mustParseGovernedCloseout(t, governedCloseoutFixtureJSON())
	if err := validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"}); err != nil {
		t.Fatalf("fixture accepted by parser must stay accepted by schema constants: %v", err)
	}
}

func TestDecomposeCloseoutUsesStrictSchemaAndRunnerValidation(t *testing.T) {
	task := runnerWorkTaskMetadata{TaskRef: "decompose-work-plan"}
	if !taskRequiresExplicitGovernedCloseout(task) {
		t.Fatal("decompose-work-plan must require explicit governed closeout")
	}
	if !shouldUseCodexOutputSchemaForGovernedCloseout(task) {
		t.Fatal("decompose-work-plan must use Codex output-schema to force parseable closeout JSON")
	}
	output := mustParseGovernedCloseout(t, governedCloseoutFixtureJSON())
	if err := validateGovernedCloseoutOutput(output, task); err != nil {
		t.Fatalf("valid child_tasks must be accepted by runner validation: %v", err)
	}
	invalid := output
	invalid.ChildTasks[0].EvidenceNeeded = nil
	if err := validateGovernedCloseoutOutput(invalid, task); governedCloseoutFailureCategory(err) != governedCloseoutValidationFailed {
		t.Fatalf("invalid child_tasks must be rejected by runner validation, got %v", err)
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

type gitOpsRecordingRunner struct {
	commands []projectgitops.Command
	results  []projectgitops.CommandResult
	errs     []error
}

func (runner *gitOpsRecordingRunner) Run(_ context.Context, command projectgitops.Command) (projectgitops.CommandResult, error) {
	runner.commands = append(runner.commands, command)
	idx := len(runner.commands) - 1
	if idx < len(runner.errs) && runner.errs[idx] != nil {
		return projectgitops.CommandResult{}, runner.errs[idx]
	}
	if idx < len(runner.results) {
		return runner.results[idx], nil
	}
	return projectgitops.CommandResult{}, nil
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

func fakeCodexWritingLastMessage(t *testing.T, message string) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo codex-test; exit 0; fi\nout=''\nprev=''\nfor arg in \"$@\"; do\n  if [ \"$prev\" = \"--output-last-message\" ]; then out=\"$arg\"; fi\n  prev=\"$arg\"\ndone\nif [ -n \"$out\" ]; then cat > \"$out\" <<'EOF'\n" + message + "\nEOF\nfi\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return binary
}

func fakeCodexRecordingArgs(t *testing.T, argsPath string, execStatus int) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo codex-test; exit 0; fi\nprintf '%s' \"$*\" > " + shellQuoteForTest(argsPath) + "\nexit " + string(rune('0'+execStatus)) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return binary
}

func governedCloseoutFixtureJSON() string {
	return `{
		"closeout_action":"needs_review",
		"outcome":"decomposition produced implementation-ready child task metadata",
		"safe_next_action":"review child implementation tasks",
		"evidence_refs":["evidence.governed"],
		"verifier_result_refs":["verifier.governed"],
		"child_tasks":[{
			"task_ref":"implement-ticket-slice",
			"title":"Implement Ticket Slice",
			"description":"Implement one bounded source-verified ticket slice.",
			"status":"planned",
			"owner_agent":"implementation-worker",
			"evidence_needed":["source-evidence"],
			"context_pack_refs":["context.pack"],
			"files_to_read":["apps/example/file.ts"],
			"files_to_edit":["apps/example/file.ts"],
			"likely_files_affected":["apps/example"],
			"dependency_task_ids":[],
			"verification_requirement":"focused regression test",
			"expected_output":"code change and focused test",
			"failure_criteria":"block on missing source evidence",
			"review_gate":"implementation-review",
			"resume_instructions":"claim this task and inspect listed files",
			"decomposition_quality":"ready",
			"acceptance_criteria":["source-backed behavior is implemented"],
			"stop_conditions":["missing source evidence"],
			"verifier_ladder":["focused regression test"],
			"regression_test_applicability":"required",
			"downstream_impact_refs":["downstream.impact"],
			"output_contract":"code change with verifier evidence"
		}],
		"block_reason":"",
		"failure_reason":""
	}`
}

func mustParseGovernedCloseout(t *testing.T, payload string) governedCloseoutOutput {
	t.Helper()
	output, err := parseGovernedCloseoutOutput(payload)
	if err != nil {
		t.Fatalf("parse governed closeout: %v", err)
	}
	return output
}

func initRunnerGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o700); err != nil {
		t.Fatalf("create repo dirs: %v", err)
	}
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("clean\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "task.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatalf("write task file: %v", err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=Mivia Test", "-c", "user.email=mivia@example.test", "commit", "-m", "initial")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func fakeGitLoggingAndDelegating(t *testing.T, logPath string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuoteForTest(logPath) + "\nexec " + shellQuoteForTest(realGit) + " \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	return binary
}

func withPrependedPath(t *testing.T, dir string) {
	t.Helper()
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
}

func shellQuoteForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestBuildRunnerCodexCommandRejectsWindowsLauncher(t *testing.T) {
	_, err := buildRunnerCodexCommand(filepath.Join(t.TempDir(), "codex-input.json"), filepath.Join(t.TempDir(), "last-message.txt"), time.Minute, codexLaunchOptions{Path: "codex", Launcher: "windows-cmd", WorkDir: "/workspace/repo", Sandbox: "workspace-write"})
	if !errors.Is(err, projectautomation.ErrInvalidInput) || !strings.Contains(err.Error(), "unknown codex launcher") {
		t.Fatalf("expected windows launcher to be rejected, got %v", err)
	}
}

func TestBuildRunnerCodexCommandSupportsDirectLauncherWorkDir(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "codex-input.json")
	outputPath := filepath.Join(t.TempDir(), "last-message.txt")
	schemaPath := filepath.Join(t.TempDir(), "schema.json")
	command, err := buildRunnerCodexCommand(inputPath, outputPath, time.Minute, codexLaunchOptions{Path: "/usr/local/bin/codex", Launcher: "direct", WorkDir: "/workspace/repo", Sandbox: "workspace-write", OutputSchemaPath: schemaPath})
	if err != nil {
		t.Fatalf("buildRunnerCodexCommand returned error: %v", err)
	}
	want := []string{"exec", "--sandbox", "workspace-write", "--output-schema", schemaPath, "--output-last-message", outputPath, "--cd", "/workspace/repo", "-"}
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
	if command.Dir != "/workspace/repo" {
		t.Fatalf("command dir = %q, want /workspace/repo", command.Dir)
	}
}

func TestBuildRunnerCodexCommandSupportsBypassMode(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "codex-input.txt")
	outputPath := filepath.Join(t.TempDir(), "last-message.txt")
	command, err := buildRunnerCodexCommand(inputPath, outputPath, time.Minute, codexLaunchOptions{Path: "/usr/local/bin/codex", Launcher: "direct", WorkDir: "/workspace/repo", Sandbox: "workspace-write", BypassApprovalsAndSandbox: true})
	if err != nil {
		t.Fatalf("buildRunnerCodexCommand returned error: %v", err)
	}
	want := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--output-last-message", outputPath, "--cd", "/workspace/repo", "-"}
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
