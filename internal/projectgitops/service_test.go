package projectgitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingRunner struct {
	commands []Command
	results  []CommandResult
	errs     []error
}

func (runner *recordingRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	runner.commands = append(runner.commands, command)
	idx := len(runner.commands) - 1
	if idx < len(runner.errs) && runner.errs[idx] != nil {
		return CommandResult{}, runner.errs[idx]
	}
	if idx < len(runner.results) {
		return runner.results[idx], nil
	}
	return CommandResult{}, nil
}

func testGitOpsCredentialFiles(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	knownHostsPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("write test ssh key: %v", err)
	}
	if err := os.WriteFile(knownHostsPath, []byte("github.com ssh-ed25519 test\n"), 0o600); err != nil {
		t.Fatalf("write test known hosts: %v", err)
	}
	return keyPath, knownHostsPath
}

func TestPostTaskCommitsWhenChangesExist(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M internal/projectgitops/service.go\n"},
		{Stdout: "mivia/generic-gitops-conventions\n"},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
	}}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		RemoteName:           "origin",
		GitHubCLIPath:        "gh",
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"internal/projectgitops"},
	})
	if err != nil {
		t.Fatalf("expected post task commit to succeed: %v", err)
	}
	if result.CommitRef != "git-commit-abc123def456" {
		t.Fatalf("unexpected commit ref: %+v", result)
	}
	if len(runner.commands) != 7 {
		t.Fatalf("expected seven git commands, got %d", len(runner.commands))
	}
	if got := strings.Join(runner.commands[0].Args, " "); got != "-c safe.directory=/tmp/worktree rev-parse --show-toplevel" {
		t.Fatalf("expected trust probe command, got %q", got)
	}
	if got := strings.Join(runner.commands[5].Args, " "); !strings.Contains(got, "commit --no-verify -m") {
		t.Fatalf("expected commit command, got %q", got)
	}
	messageArg := ""
	for i, arg := range runner.commands[5].Args {
		if arg == "-m" && i+1 < len(runner.commands[5].Args) {
			messageArg = runner.commands[5].Args[i+1]
			break
		}
	}
	if message := messageArg; !strings.Contains(message, "Project ID: project-1") || !strings.Contains(message, "Automation ID: automation_1") {
		t.Fatalf("expected rendered metadata in commit message, got %q", message)
	}
	if !containsEnv(runner.commands[5].Env, "GIT_AUTHOR_EMAIL=automation@example.test") {
		t.Fatalf("expected author email env, got %+v", runner.commands[5].Env)
	}
}

func TestPostTaskCreatesDraftPRForCleanBranchAheadOfMain(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	runner := &recordingRunner{
		results: []CommandResult{
			{},
			{},
			{Stdout: "1\n"},
			{},
			{},
			{},
			{},
			{},
			{Stdout: "https://github.com/example/repo/pull/123\n"},
		},
		errs: []error{
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			errors.New("no existing pr"),
		},
	}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		PushAfterTask:        true,
		DraftPRAfterPush:     true,
		RemoteName:           "origin",
		BranchNamePattern:    "^(feat|fix|docs|chore)-MASS-[0-9]+(-[a-z0-9-]+)*$",
		SSHPrivateKeyPath:    sshKey,
		SSHKnownHostsPath:    knownHosts,
		GitHubTokenEnv:       "GITHUB_TOKEN",
		GitHubCLIPath:        "gh",
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		Verification: VerificationProfile{
			AlwaysBeforePR: []string{"pnpm -s nx affected -t test --base=origin/main --head=HEAD"},
		},
	}, runner)
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:         "/tmp/worktree",
		ProjectID:       "mass-monorepo",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		TaskRef:         "workflow-chain-finalize",
		TaskTitle:       "jira:MASS-1044 final GitOps",
		BranchName:      "chore-MASS-1044-governed-workplan-implementation",
		AutomationID:    "workflow-chain-gitops",
		AutomationRunID: "workflow_chain_run_1",
		OperatorID:      "mivia-workflow-chain",
		ReviewRefs:      []string{"review/ref"},
		VerifierRefs:    []string{"verifier/ref"},
		TestResults:     []string{"post-validation completed"},
	})
	if err != nil {
		t.Fatalf("expected clean ahead branch PR finalization to succeed: %v", err)
	}
	if result.NoChanges || result.PushRef == "" || result.PullRequestRef == "" {
		t.Fatalf("expected push and draft PR refs, got %#v", result)
	}
	joined := commandArgs(runner.commands)
	if !strings.Contains(joined, "rev-list --count origin/main..HEAD") ||
		!strings.Contains(joined, "sh -lc pnpm -s nx affected -t test --base=origin/main --head=HEAD") ||
		!strings.Contains(joined, "push --no-verify origin HEAD:chore-MASS-1044-governed-workplan-implementation") ||
		!strings.Contains(joined, "pr create --draft") {
		t.Fatalf("expected rev-list, verifier, push, and draft PR commands, got %q", joined)
	}
	if !containsString(result.EvidenceRefs, "project-verification-passed") {
		t.Fatalf("expected clean-ahead verification evidence, got %#v", result.EvidenceRefs)
	}
}

func TestPostTaskCreatesDraftPRForCleanAheadBranchUsesConfiguredBaseRef(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	runner := &recordingRunner{
		results: []CommandResult{
			{},
			{},
			{Stdout: "1\n"},
			{},
			{},
			{},
			{},
			{},
			{Stdout: "https://github.com/example/repo/pull/456\n"},
		},
		errs: []error{
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			errors.New("no existing pr"),
		},
	}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		PushAfterTask:        true,
		DraftPRAfterPush:     true,
		RemoteName:           "origin",
		BranchNamePattern:    "^(feat|fix|docs|chore)-MASS-[0-9]+(-[a-z0-9-]+)*$",
		SSHPrivateKeyPath:    sshKey,
		SSHKnownHostsPath:    knownHosts,
		GitHubTokenEnv:       "GITHUB_TOKEN",
		GitHubCLIPath:        "gh",
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
	}, runner)
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:         "/tmp/worktree",
		ProjectID:       "mass-monorepo",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		TaskRef:         "workflow-chain-finalize",
		TaskTitle:       "jira:MASS-1044 final GitOps",
		BranchName:      "chore-MASS-1044-governed-workplan-implementation",
		BaseRef:         "release/mass-2026-06",
		AutomationID:    "workflow-chain-gitops",
		AutomationRunID: "workflow_chain_run_1",
		OperatorID:      "mivia-workflow-chain",
		ReviewRefs:      []string{"review/ref"},
		VerifierRefs:    []string{"verifier/ref"},
		TestResults:     []string{"post-validation completed"},
	})
	if err != nil {
		t.Fatalf("expected clean ahead branch PR finalization to succeed: %v", err)
	}
	if result.NoChanges || result.PullRequestRef == "" {
		t.Fatalf("expected draft PR refs, got %#v", result)
	}
	joined := commandArgs(runner.commands)
	if !strings.Contains(joined, "rev-list --count origin/release/mass-2026-06..HEAD") {
		t.Fatalf("expected configured base ref in rev-list, got %q", joined)
	}
	if strings.Contains(joined, "rev-list --count origin/main..HEAD") {
		t.Fatalf("must not hard-code origin/main for configured base ref, got %q", joined)
	}
}

func commandArgs(commands []Command) string {
	var out []string
	for _, command := range commands {
		out = append(out, command.Path+" "+strings.Join(command.Args, " "))
	}
	return strings.Join(out, "\n")
}

func TestPostTaskStagesOnlyChangedFilesInsideAllowedScopes(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M apps/domain-inventory/src/trpc/trpc.router.ts\n?? apps/domain-inventory/src/trpc/__tests__/family-pricing-user-context.spec.ts\n"},
		{Stdout: "fix-MASS-0000-family-pricing\n"},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
	}}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		RemoteName:           "origin",
		GitHubCLIPath:        "gh",
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"apps", "libs", "packages", "services", "src"},
	})
	if err != nil {
		t.Fatalf("expected post task commit to ignore absent allowed scopes and stage changed files: %v", err)
	}
	if len(runner.commands) != 7 {
		t.Fatalf("expected seven git commands, got %d", len(runner.commands))
	}
	addArgs := strings.Join(runner.commands[3].Args, "\n")
	if strings.Contains(addArgs, "\nservices") || strings.Contains(addArgs, "\npackages") {
		t.Fatalf("unexpected add args: %q", addArgs)
	}
	if !strings.Contains(addArgs, "apps/domain-inventory/src/trpc/trpc.router.ts") ||
		!strings.Contains(addArgs, "apps/domain-inventory/src/trpc/__tests__/family-pricing-user-context.spec.ts") {
		t.Fatalf("expected changed app files to be staged, got %q", addArgs)
	}
}

func TestPostTaskRejectsDirtyFilesOutsideAllowedScopes(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M apps/domain-inventory/src/trpc/trpc.router.ts\n M README.md\n"},
	}}
	svc := NewWithRunner(Options{
		Enabled:         true,
		CommitAfterTask: true,
		RemoteName:      "origin",
		GitHubCLIPath:   "gh",
	}, runner)

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"apps/domain-inventory/src/trpc"},
	})
	if !errors.Is(err, ErrDirtyWorktreeScope) {
		t.Fatalf("expected dirty worktree scope error, got %v", err)
	}
	if got := FailureCategory(err); got != "gitops_dirty_worktree_scope" {
		t.Fatalf("expected scoped dirty category, got %q", got)
	}
	if paths := DirtyWorktreeScopePaths(err); len(paths) != 1 || paths[0] != "README.md" {
		t.Fatalf("expected rejected dirty path detail, got %+v", paths)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected trust probe and status only, got %d", len(runner.commands))
	}
}

func TestPostTaskRejectsRenameFromOutsideAllowedScope(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: "R  README.md -> apps/domain-inventory/src/trpc/readme.ts\n"},
	}}
	svc := NewWithRunner(Options{
		Enabled:         true,
		CommitAfterTask: true,
		RemoteName:      "origin",
		GitHubCLIPath:   "gh",
	}, runner)

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"apps/domain-inventory/src/trpc"},
	})
	if !errors.Is(err, ErrDirtyWorktreeScope) {
		t.Fatalf("expected dirty worktree scope error for rename source, got %v", err)
	}
}

func TestPostTaskAllowsRenameWithinAllowedScope(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: "R  apps/domain-inventory/src/trpc/old.ts -> apps/domain-inventory/src/trpc/new.ts\n"},
		{Stdout: "fix-MASS-0000-family-pricing\n"},
		{},
		{},
		{},
		{},
	}}
	svc := NewWithRunner(Options{
		Enabled:         true,
		CommitAfterTask: true,
		RemoteName:      "origin",
		GitHubCLIPath:   "gh",
	}, runner)

	if _, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"apps/domain-inventory/src/trpc"},
	}); err != nil {
		t.Fatalf("expected in-scope rename to commit: %v", err)
	}
	addArgs := strings.Join(runner.commands[3].Args, "\n")
	if !strings.Contains(addArgs, "apps/domain-inventory/src/trpc/old.ts") || !strings.Contains(addArgs, "apps/domain-inventory/src/trpc/new.ts") {
		t.Fatalf("expected both rename paths to be staged, got %q", addArgs)
	}
}

func TestPostTaskFailsClosedWithoutSafePathspecs(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: " M README.md\n"}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"../unsafe"},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected trust probe and status commands, got %d", len(runner.commands))
	}
}

func TestPostTaskReturnsNoChanges(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: ""}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:         "/tmp/worktree",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		AutomationRunID: "automation_run_1",
	})
	if err != nil {
		t.Fatalf("expected no changes result: %v", err)
	}
	if !result.NoChanges || len(runner.commands) != 2 {
		t.Fatalf("unexpected result or command count: %+v commands=%d", result, len(runner.commands))
	}
}

func TestPostTaskPushRequiresSSHConfig(t *testing.T) {
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, PushAfterTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, &recordingRunner{})

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:         "/tmp/worktree",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		AutomationRunID: "automation_run_1",
	})
	if err == nil || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got %v", err)
	}
}

func TestPostTaskDerivesProjectBranchWhenCurrentBranchViolatesPattern(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M README.md\n"},
		{Stdout: "feature/MASS-123-docs\n"},
		{},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
		{},
	}}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		PushAfterTask:        true,
		RemoteName:           "origin",
		BranchPrefix:         "",
		BranchNamePattern:    `^(feat|fix|docs)-MASS-[0-9]+(-[a-z0-9-]+)*$`,
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		SSHPrivateKeyPath:    sshKey,
		SSHKnownHostsPath:    knownHosts,
		GitHubCLIPath:        "gh",
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"README.md"},
	})
	if err != nil {
		t.Fatalf("expected derived branch to pass policy: %v", err)
	}
	if got := strings.Join(runner.commands[3].Args, " "); got != "-c safe.directory=/tmp/worktree checkout -B fix-MASS-0000-automation-run-1" {
		t.Fatalf("expected derived branch checkout, got %q", got)
	}
	if got := strings.Join(runner.commands[8].Args, " "); got != "-c safe.directory=/tmp/worktree push --no-verify origin HEAD:fix-MASS-0000-automation-run-1" {
		t.Fatalf("expected push to derived branch, got %q", got)
	}
}

func TestPostTaskDerivesFakeMassBranchForTicketlessSmokeInputBranch(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: "?? .agentic/automation-smoke.md\n"},
		{},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
		{},
	}}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		PushAfterTask:        true,
		RemoteName:           "origin",
		BranchPrefix:         "",
		BranchNamePattern:    `^(feat|fix|docs|chore|refactor|hotfix|revert)-MASS-[0-9]+(-[a-z0-9-]+)*$|^chore-smoke-[A-Za-z0-9._+-]{1,80}(-[a-z0-9-]+)*$`,
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		SSHPrivateKeyPath:    sshKey,
		SSHKnownHostsPath:    knownHosts,
		GitHubCLIPath:        "gh",
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "mass-monorepo",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		TaskRef:          "smoke-draft-pr",
		TaskTitle:        "Smoke Draft PR",
		BranchName:       "chore-input-smoke-20260608g-governed-smoke-gitops-compile-a13ce1fb8c207136",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "smoke-gitops-worker",
		AllowedPathspecs: []string{".agentic/automation-smoke.md"},
	})
	if err != nil {
		t.Fatalf("expected ticketless smoke branch to be normalized and committed: %v", err)
	}
	if result.CommitRef != "git-commit-abc123def456" {
		t.Fatalf("unexpected commit ref: %#v", result)
	}
	if got := strings.Join(runner.commands[2].Args, " "); got != "-c safe.directory=/tmp/worktree checkout -B chore-MASS-0000-smoke-draft-pr" {
		t.Fatalf("expected fake MASS-0000 smoke branch checkout, got %q", got)
	}
	if got := strings.Join(runner.commands[3].Args, " "); got != "-c safe.directory=/tmp/worktree add -- .agentic/automation-smoke.md" {
		t.Fatalf("expected only smoke marker staged, got %q", got)
	}
}

func TestFailureCategoryUsesBranchPolicyError(t *testing.T) {
	err := fmt.Errorf("%w: %w: branch mismatch", ErrInvalidInput, ErrBranchPolicy)
	if got := FailureCategory(err); got != "gitops_branch_policy_failed" {
		t.Fatalf("expected branch policy failure category, got %q", got)
	}
}

func TestFailureCategoryUsesVerificationError(t *testing.T) {
	err := fmt.Errorf("%w: verifier failed", ErrVerificationFailed)
	if got := FailureCategory(err); got != "gitops_verification_failed" {
		t.Fatalf("expected verification failure category, got %q", got)
	}
}

func TestFailureCategoryWithDetailPreservesVerificationCommandHash(t *testing.T) {
	err := fmt.Errorf("%w: %s", ErrVerificationFailed, "abcdef123456")
	if got := FailureCategoryWithDetail(err); got != "gitops_verification_failed_abcdef123456" {
		t.Fatalf("expected detailed verification category, got %q", got)
	}
}

func TestFailureCategoryWithDetailPreservesCommandStep(t *testing.T) {
	svc := NewWithRunner(Options{}, &recordingRunner{
		errs: []error{errors.New("gh failed")},
	})
	_, err := svc.run(context.Background(), Command{Path: "gh", Args: []string{"pr", "create", "--draft"}, Dir: "/tmp/worktree"})
	if err == nil {
		t.Fatal("expected command failure")
	}
	if got := FailureCategoryWithDetail(err); got != "gitops_command_failed_gh_pr_create" {
		t.Fatalf("expected detailed command category, got %q", got)
	}
}

func TestFailureCategoryWithDetailPreservesInvalidGitOpsConfig(t *testing.T) {
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, PushAfterTask: true}, &recordingRunner{})
	_, err := svc.PostTask(context.Background(), PostTaskInput{WorkDir: "/tmp/worktree"})
	if err == nil {
		t.Fatal("expected invalid push config")
	}
	if got := FailureCategoryWithDetail(err); got != "gitops_invalid_input_ssh_config_required" {
		t.Fatalf("expected detailed invalid input category, got %q", got)
	}
}

func TestFailureCategoryWithDetailReportsMissingSSHKeyBeforePush(t *testing.T) {
	_, knownHosts := testGitOpsCredentialFiles(t)
	svc := NewWithRunner(Options{
		Enabled:           true,
		CommitAfterTask:   true,
		PushAfterTask:     true,
		SSHPrivateKeyPath: filepath.Join(t.TempDir(), "missing_id_ed25519"),
		SSHKnownHostsPath: knownHosts,
	}, &recordingRunner{})

	_, err := svc.PostTask(context.Background(), PostTaskInput{WorkDir: "/tmp/worktree"})
	if err == nil {
		t.Fatal("expected missing ssh key error")
	}
	if got := FailureCategoryWithDetail(err); got != "gitops_invalid_input_ssh_key_unavailable" {
		t.Fatalf("expected missing ssh key category, got %q", got)
	}
}

func TestFailureCategoryWithDetailReportsMissingGitHubTokenBeforeDraftPR(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	svc := NewWithRunner(Options{
		Enabled:           true,
		CommitAfterTask:   true,
		PushAfterTask:     true,
		DraftPRAfterPush:  true,
		SSHPrivateKeyPath: sshKey,
		SSHKnownHostsPath: knownHosts,
		GitHubTokenEnv:    "MISSING_GITHUB_TOKEN",
	}, &recordingRunner{})

	_, err := svc.PostTask(context.Background(), PostTaskInput{WorkDir: "/tmp/worktree"})
	if err == nil {
		t.Fatal("expected missing github token error")
	}
	if got := FailureCategoryWithDetail(err); got != "gitops_invalid_input_github_token_unavailable" {
		t.Fatalf("expected missing github token category, got %q", got)
	}
}

func TestPostTaskRunsVerificationAndStagesGeneratedArtifacts(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M packages/contracts/src/schemas/auth.ts\n"},
		{},
		{},
		{Stdout: " M packages/contracts/src/schemas/auth.ts\n M packages/contracts/dist/openapi.json\n M packages/contracts/dist/openapi.yaml\n"},
		{Stdout: "fix-MASS-0000-contracts\n"},
		{},
		{},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
	}}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		RemoteName:           "origin",
		GitHubCLIPath:        "gh",
		Verification: VerificationProfile{
			AlwaysBeforePR: []string{"pnpm -s nx affected -t lint --base=origin/main --head=HEAD"},
			Env:            map[string]string{"SESSION_PASSWORD": "test-secret", "BFF_ADMIN_URL": "http://localhost:3000"},
			GeneratedArtifacts: []GeneratedArtifactVerifier{{
				Paths:            []string{"packages/contracts/dist/openapi.json", "packages/contracts/dist/openapi.yaml"},
				Command:          "pnpm -s nx run contracts:verify-openapi",
				RequiredBeforePR: true,
			}},
		},
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"packages/contracts/src"},
	})
	if err != nil {
		t.Fatalf("expected post task with verification to succeed: %v", err)
	}
	if !containsString(result.EvidenceRefs, "project-verification-passed") {
		t.Fatalf("expected verification evidence ref, got %+v", result.EvidenceRefs)
	}
	if got := strings.Join(runner.commands[2].Args, " "); !strings.Contains(got, "config --global --add safe.directory /tmp/worktree") {
		t.Fatalf("expected verifier safe.directory command, got %q", got)
	}
	if got := strings.Join(runner.commands[3].Args, " "); got != "-lc pnpm -s nx run contracts:verify-openapi" {
		t.Fatalf("expected openapi verifier command, got %q", got)
	}
	addArgs := strings.Join(runner.commands[6].Args, "\n")
	for _, want := range []string{"packages/contracts/src/schemas/auth.ts", "packages/contracts/dist/openapi.json", "packages/contracts/dist/openapi.yaml"} {
		if !strings.Contains(addArgs, want) {
			t.Fatalf("expected staged generated artifact %q in add args %q", want, addArgs)
		}
	}
	if got := strings.Join(runner.commands[10].Args, " "); got != "-lc pnpm -s nx affected -t lint --base=origin/main --head=HEAD" {
		t.Fatalf("expected post-commit lint verifier command, got %q", got)
	}
	if got := strings.Join(runner.commands[10].Env, "\n"); !strings.Contains(got, "XDG_CONFIG_HOME=") || !strings.Contains(got, "BFF_ADMIN_URL=http://localhost:3000") || !strings.Contains(got, "SESSION_PASSWORD=test-secret") {
		t.Fatalf("expected sorted verifier env, got %q", got)
	}
	if got := strings.Join(runner.commands[11].Args, " "); !strings.Contains(got, "commit --amend --no-verify") {
		t.Fatalf("expected commit amended with post-commit verification results, got %q", got)
	}
}

func TestPostTaskFailsBeforePushWhenPostCommitVerificationFails(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{
			{},
			{Stdout: " M packages/contracts/src/schemas/auth.ts\n"},
			{Stdout: "fix-MASS-0000-contracts\n"},
		},
		errs: []error{
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			errors.New("lint failed"),
		},
	}
	svc := NewWithRunner(Options{
		Enabled:         true,
		CommitAfterTask: true,
		RemoteName:      "origin",
		GitHubCLIPath:   "gh",
		Verification: VerificationProfile{
			AlwaysBeforePR: []string{"pnpm -s nx affected -t lint --base=origin/main --head=HEAD"},
		},
	}, runner)

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"packages/contracts/src"},
	})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected verification failure, got %v", err)
	}
	if len(runner.commands) != 8 {
		t.Fatalf("expected no push or PR commands after failed verifier, got %d", len(runner.commands))
	}
	if got := strings.Join(runner.commands[5].Args, " "); !strings.Contains(got, "commit --no-verify") {
		t.Fatalf("expected local commit before CI-equivalent verification, got %q", got)
	}
	if got := strings.Join(runner.commands[7].Args, " "); got != "-lc pnpm -s nx affected -t lint --base=origin/main --head=HEAD" {
		t.Fatalf("expected post-commit lint verifier command, got %q", got)
	}
}

func TestSafeTestResultPreservesLongVerifierCommand(t *testing.T) {
	command := "pnpm exec nx affected -t lint --base=origin/main --head=HEAD --exclude=mass-core,tag:platform:mobile,tag:platform:web --parallel=4 --max-warnings=0"
	got := safeTestResult(command, "passed")
	if !strings.Contains(got, "tag:platform:web") || !strings.Contains(got, "--max-warnings=0") {
		t.Fatalf("expected full verifier command preserved, got %q", got)
	}
	if strings.Contains(got, "tag:platform:w: passed") {
		t.Fatalf("expected verifier command not to be truncated, got %q", got)
	}
}

func TestDerivePolicyBranchPrefersConfiguredCommitType(t *testing.T) {
	svc := NewWithRunner(Options{
		BranchNamePattern: `^(feat|fix|docs|chore|refactor)-ABC-[0-9]+(-[a-z0-9-]+)*$`,
		Conventions:       Conventions{CommitType: "chore"},
	}, &recordingRunner{})
	got := svc.derivePolicyBranch(PostTaskInput{
		TaskTitle:       "Update generated docs",
		AutomationRunID: "automation_run_1",
	})
	if got != "chore-ABC-0000-update-generated-docs" {
		t.Fatalf("expected configured commit type branch, got %q", got)
	}
}

func TestPostTaskAllowsPushFromBranchMatchingProjectPattern(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M README.md\n"},
		{Stdout: "docs-MASS-123-docs\n"},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
		{},
	}}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		PushAfterTask:        true,
		RemoteName:           "origin",
		BranchPrefix:         "",
		BranchNamePattern:    `^(feat|fix|docs)-MASS-[0-9]+(-[a-z0-9-]+)*$`,
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		SSHPrivateKeyPath:    sshKey,
		SSHKnownHostsPath:    knownHosts,
		GitHubCLIPath:        "gh",
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"README.md"},
	})
	if err != nil {
		t.Fatalf("expected matching branch to push: %v", err)
	}
	if result.PushRef == "" {
		t.Fatalf("expected push ref, got %+v", result)
	}
	if got := strings.Join(runner.commands[7].Args, " "); got != "-c safe.directory=/tmp/worktree push --no-verify origin HEAD:docs-MASS-123-docs" {
		t.Fatalf("expected push to matching branch, got %q", got)
	}
}

func TestPreTaskRejectsDirtyWorktreeWhenRequired(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: " M README.md\n"}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	err := svc.PreTask(context.Background(), "/tmp/worktree")
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("expected dirty worktree error, got %v", err)
	}
	if got := strings.Join(runner.commands[0].Args, " "); got != "-c safe.directory=/tmp/worktree rev-parse --show-toplevel" {
		t.Fatalf("expected trust probe command, got %q", got)
	}
}

func TestPreTaskWithinScopeAllowsOnlyTaskScopedDirtyWorktree(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: " M apps/domain-inventory/src/trpc/trpc.router.ts\n"}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	if err := svc.PreTaskWithinScope(context.Background(), "/tmp/worktree", []string{"apps/domain-inventory/src/trpc"}); err != nil {
		t.Fatalf("expected in-scope dirty worktree to pass scoped pre-task: %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected trust probe and status commands, got %d", len(runner.commands))
	}
}

func TestPreTaskWithinScopeRejectsUnrelatedDirtyWorktree(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: " M apps/domain-inventory/src/trpc/trpc.router.ts\n M apps/sos/src/workflows/sos.ts\n"}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	err := svc.PreTaskWithinScope(context.Background(), "/tmp/worktree", []string{"apps/domain-inventory/src/trpc"})
	if !errors.Is(err, ErrDirtyWorktreeScope) {
		t.Fatalf("expected scoped dirty worktree error, got %v", err)
	}
	if paths := DirtyWorktreeScopePaths(err); len(paths) != 1 || paths[0] != "apps/sos/src/workflows/sos.ts" {
		t.Fatalf("expected rejected pre-task dirty path detail, got %+v", paths)
	}
}

func TestPreTaskUsesWritableHomeForSafeDirectoryFallback(t *testing.T) {
	runner := &recordingRunner{
		errs: []error{
			ErrCommandFailed,
			nil,
			nil,
		},
		results: []CommandResult{
			{},
			{},
			{Stdout: ""},
		},
	}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	if err := svc.PreTask(context.Background(), "/tmp/worktree"); err != nil {
		t.Fatalf("expected pre task to succeed after safe.directory fallback: %v", err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("expected trust probe, safe.directory, and status commands, got %d", len(runner.commands))
	}
	if got := strings.Join(runner.commands[1].Args, " "); !strings.Contains(got, "config --global --add safe.directory /tmp/worktree") {
		t.Fatalf("expected safe.directory fallback command, got %q", got)
	}
	if !hasEnvPrefix(runner.commands[1].Env, "HOME=") || !hasEnvPrefix(runner.commands[1].Env, "XDG_CONFIG_HOME=") {
		t.Fatalf("expected writable git config env, got %+v", runner.commands[1].Env)
	}
}

func TestPreTaskSkipsWhenCleanCheckDisabled(t *testing.T) {
	runner := &recordingRunner{}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: false, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	if err := svc.PreTask(context.Background(), ""); err != nil {
		t.Fatalf("expected disabled clean check to skip: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("expected no commands, got %d", len(runner.commands))
	}
}

func TestGitHubEnvFallsBackToGhAuthWhenConfiguredEnvIsEmpty(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	svc := NewWithRunner(Options{GitHubTokenEnv: "GH_TOKEN"}, &recordingRunner{})
	if env := svc.githubEnv(); env != nil {
		t.Fatalf("expected empty token env to fall back to gh auth, got %+v", env)
	}
}

func TestRenderUsesConfiguredConventionsAndMetadata(t *testing.T) {
	rendered, err := Render(PostTaskInput{
		ProjectID:       "project-1",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		TaskRef:         "task/ref",
		TaskTitle:       "Generic GitOps conventions",
		BranchName:      "docs-ABC-123-generic-gitops-conventions",
		AutomationID:    "automation_1",
		AutomationRunID: "automation_run_1",
		OperatorID:      "operator_1",
		ReviewRefs:      []string{"review:approved", "review:secondary"},
		VerifierRefs:    []string{"verifier:focused-tests"},
		TestResults:     []string{"go test ./internal/projectgitops/...: passed"},
	}, Conventions{
		CommitType:               "feat",
		CommitScope:              "gitops",
		CommitSummaryTemplate:    "finish {{work_task_ref}}",
		PullRequestTitleTemplate: "{{commit_subject}}",
		WhatChangedTemplate:      "Implemented {{work_task_title}} for {{project_id}}.",
		HowVerifiedTemplate:      "Review refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}",
		TestsTemplate:            "{{test_results}}",
	})
	if err != nil {
		t.Fatalf("expected render to succeed: %v", err)
	}
	if rendered.CommitSubject != "feat(gitops): finish task/ref" {
		t.Fatalf("unexpected commit subject %q", rendered.CommitSubject)
	}
	if !strings.Contains(rendered.CommitBody, "Ticket: ABC-123") {
		t.Fatalf("commit body missing derived ticket metadata:\n%s", rendered.CommitBody)
	}
	for _, want := range []string{
		"## What changed",
		"Implemented Generic GitOps conventions for project-1.",
		"## How verified",
		"Review refs: review:approved, review:secondary",
		"Verifier refs: verifier:focused-tests",
		"## Tests",
		"go test ./internal/projectgitops/...: passed",
	} {
		if !strings.Contains(rendered.PullRequestBody, want) {
			t.Fatalf("PR body missing %q:\n%s", want, rendered.PullRequestBody)
		}
	}
}

func TestRenderDerivesTicketRefFromBranchForProjectPRTemplates(t *testing.T) {
	rendered, err := Render(PostTaskInput{
		ProjectID:       "project-1",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		TaskRef:         "fix-readme-structure",
		TaskTitle:       "Fix README structure entry",
		BranchName:      "fix-MASS-0000-readme-structure-entry",
		AutomationID:    "automation_1",
		AutomationRunID: "automation_run_1",
		OperatorID:      "operator_1",
		VerifierRefs:    []string{"worker-verifier"},
	}, Conventions{
		CommitType:               "chore",
		CommitSummaryTemplate:    "complete {{work_task_ref}}",
		PullRequestTitleTemplate: "chore({{ticket_ref}}): complete {{work_task_ref}}",
		WhatChangedTemplate:      "Jira: https://rimthan-lab.atlassian.net/browse/{{ticket_ref}}\n\nSummary:\n- Completed {{work_task_title}}.",
		HowVerifiedTemplate:      "- plan-task: yes (https://rimthan-lab.atlassian.net/browse/{{ticket_ref}})\n- pr-audit: N/A\n\nAgent workflow:\nProject ID: {{project_id}}\nWork Plan ID: {{work_plan_id}}\nWork Task ID: {{work_task_id}}\nAutomation ID: {{automation_id}}\nAutomation Run ID: {{automation_run_id}}\nOperator ID: {{operator_id}}\nReview refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}",
		TestsTemplate:            "{{test_results}}",
	})
	if err != nil {
		t.Fatalf("expected render to succeed: %v", err)
	}
	if rendered.PullRequestTitle != "chore(MASS-0000): complete fix-readme-structure" {
		t.Fatalf("unexpected PR title %q", rendered.PullRequestTitle)
	}
	for _, want := range []string{
		"https://rimthan-lab.atlassian.net/browse/MASS-0000",
		"- plan-task: yes (https://rimthan-lab.atlassian.net/browse/MASS-0000)",
		"- pr-audit: N/A",
		"Automation Run ID: automation_run_1",
	} {
		if !strings.Contains(rendered.PullRequestBody, want) {
			t.Fatalf("PR body missing %q:\n%s", want, rendered.PullRequestBody)
		}
	}
}

func TestRenderUsesSafeUnavailableTicketPlaceholderForTicketlessSmokePR(t *testing.T) {
	rendered, err := Render(PostTaskInput{
		ProjectID:       "mass-monorepo",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		TaskRef:         "smoke-draft-pr",
		TaskTitle:       "Smoke Draft PR",
		BranchName:      "chore-input-smoke-20260608f-governed-smoke-gitops-compile-043cae090c73a404",
		AutomationID:    "automation_1",
		AutomationRunID: "automation_run_1",
		OperatorID:      "smoke-gitops-worker",
		ReviewRefs:      []string{"review_result_smoke_draft_pr_approved"},
		VerifierRefs:    []string{"bounded-diff-only"},
	}, Conventions{
		CommitType:               "chore",
		CommitSummaryTemplate:    "complete {{work_task_ref}}",
		PullRequestTitleTemplate: "chore({{ticket_ref}}): complete {{work_task_ref}}",
		WhatChangedTemplate:      "Jira: https://rimthan-lab.atlassian.net/browse/{{ticket_ref}}\n\nSummary:\n- Completed {{work_task_title}}.",
		HowVerifiedTemplate:      "Verifier refs: {{verifier_refs}}",
		TestsTemplate:            "Testing:\n{{test_results}}",
	})
	if err != nil {
		t.Fatalf("expected ticketless smoke PR metadata to render safely: %v", err)
	}
	if rendered.PullRequestTitle != "chore(unavailable): complete smoke-draft-pr" {
		t.Fatalf("unexpected PR title: %q", rendered.PullRequestTitle)
	}
}

func TestRenderRejectsUnknownConventionPlaceholder(t *testing.T) {
	_, err := Render(PostTaskInput{
		ProjectID:       "project-1",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		AutomationID:    "automation_1",
		AutomationRunID: "automation_run_1",
		OperatorID:      "operator_1",
	}, Conventions{CommitSummaryTemplate: "complete {{repository_name}}"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "unknown placeholder") {
		t.Fatalf("expected clear convention placeholder error, got %v", err)
	}
}

func TestPostTaskCreatesDraftPRWithRenderedMetadata(t *testing.T) {
	sshKey, knownHosts := testGitOpsCredentialFiles(t)
	runner := &recordingRunner{
		results: []CommandResult{
			{},
			{Stdout: " M internal/projectgitops/service.go\n"},
			{Stdout: "mivia/generic-gitops-conventions\n"},
			{},
			{},
			{},
			{Stdout: "abc123def456\n"},
			{},
			{Stdout: "https://github.example/pull/1\n"},
		},
		errs: []error{
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			errors.New("no pull request"),
		},
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	svc := NewWithRunner(Options{
		Enabled:           true,
		CommitAfterTask:   true,
		PushAfterTask:     true,
		DraftPRAfterPush:  true,
		RemoteName:        "origin",
		SSHPrivateKeyPath: sshKey,
		SSHKnownHostsPath: knownHosts,
		GitHubTokenEnv:    "GITHUB_TOKEN",
		GitHubCLIPath:     "gh",
		Conventions:       Conventions{CommitType: "feat", CommitScope: "gitops", CommitSummaryTemplate: "finish {{work_task_id}}"},
	}, runner)

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "operator_1",
		AllowedPathspecs: []string{"internal/projectgitops"},
		ReviewRefs:       []string{"review:ready"},
		VerifierRefs:     []string{"verifier:focused"},
		TestResults:      []string{"go test ./internal/projectgitops/...: passed"},
	})
	if err != nil {
		t.Fatalf("expected post task to create draft PR: %v", err)
	}
	if result.PullRequestRef == "" {
		t.Fatalf("expected PR ref, got %+v", result)
	}
	create := runner.commands[len(runner.commands)-1]
	if got := strings.Join(create.Args[:3], " "); got != "pr create --draft" {
		t.Fatalf("expected draft PR create command, got %#v", create.Args)
	}
	body := create.Args[len(create.Args)-1]
	for _, want := range []string{"--title", "feat(gitops): finish work_task_1", "Project ID: project-1", "Automation Run ID: automation_run_1", "review:ready", "go test ./internal/projectgitops/...: passed"} {
		joined := strings.Join(create.Args, "\n")
		if !strings.Contains(joined, want) && !strings.Contains(body, want) {
			t.Fatalf("expected PR command/body to contain %q, args=%#v", want, create.Args)
		}
	}
}

func containsEnv(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func hasEnvPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
