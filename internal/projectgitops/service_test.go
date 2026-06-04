package projectgitops

import (
	"context"
	"errors"
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

func TestPostTaskCommitsWhenChangesExist(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M internal/projectgitops/service.go\n"},
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
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationRunID:  "automation_run_1",
		CommitSubject:    "chore: complete work_task_1",
		AllowedPathspecs: []string{"internal/projectgitops"},
	})
	if err != nil {
		t.Fatalf("expected post task commit to succeed: %v", err)
	}
	if result.CommitRef != "git-commit-abc123def456" {
		t.Fatalf("unexpected commit ref: %+v", result)
	}
	if len(runner.commands) != 6 {
		t.Fatalf("expected six git commands, got %d", len(runner.commands))
	}
	if got := strings.Join(runner.commands[0].Args, " "); !strings.Contains(got, "config --global --add safe.directory /tmp/worktree") {
		t.Fatalf("expected safe.directory command, got %q", got)
	}
	if got := strings.Join(runner.commands[4].Args, " "); !strings.Contains(got, "commit -m") {
		t.Fatalf("expected commit command, got %q", got)
	}
	if !containsEnv(runner.commands[4].Env, "GIT_AUTHOR_EMAIL=automation@example.test") {
		t.Fatalf("expected author email env, got %+v", runner.commands[4].Env)
	}
}

func TestPostTaskFailsClosedWithoutSafePathspecs(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: " M README.md\n"}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	_, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/worktree",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		AutomationRunID:  "automation_run_1",
		AllowedPathspecs: []string{"../unsafe"},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected safe.directory and status commands, got %d", len(runner.commands))
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

func TestPreTaskRejectsDirtyWorktreeWhenRequired(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{}, {Stdout: " M README.md\n"}}}
	svc := NewWithRunner(Options{Enabled: true, CommitAfterTask: true, RequireCleanBeforeTask: true, RemoteName: "origin", GitHubCLIPath: "gh"}, runner)

	err := svc.PreTask(context.Background(), "/tmp/worktree")
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("expected dirty worktree error, got %v", err)
	}
	if got := strings.Join(runner.commands[0].Args, " "); !strings.Contains(got, "config --global --add safe.directory /tmp/worktree") {
		t.Fatalf("expected safe.directory command, got %q", got)
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

func containsEnv(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
