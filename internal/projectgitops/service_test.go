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
	if got := strings.Join(runner.commands[0].Args, " "); got != "rev-parse --show-toplevel" {
		t.Fatalf("expected trust probe command, got %q", got)
	}
	if got := strings.Join(runner.commands[5].Args, " "); !strings.Contains(got, "commit --no-verify -m") {
		t.Fatalf("expected commit command, got %q", got)
	}
	if message := runner.commands[5].Args[3]; !strings.Contains(message, "Project ID: project-1") || !strings.Contains(message, "Automation ID: automation_1") {
		t.Fatalf("expected rendered metadata in commit message, got %q", message)
	}
	if !containsEnv(runner.commands[5].Env, "GIT_AUTHOR_EMAIL=automation@example.test") {
		t.Fatalf("expected author email env, got %+v", runner.commands[5].Env)
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

func TestPostTaskRejectsPushFromBranchOutsidePolicy(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{},
		{Stdout: " M README.md\n"},
		{Stdout: "feature/MASS-123-docs\n"},
		{},
		{},
		{},
		{Stdout: "abc123def456\n"},
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
		SSHPrivateKeyPath:    "/tmp/id_ed25519",
		SSHKnownHostsPath:    "/tmp/known_hosts",
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
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "required pattern") {
		t.Fatalf("expected branch policy error, got %v", err)
	}
	if len(runner.commands) != 7 {
		t.Fatalf("expected no push after branch policy failure, got %d commands", len(runner.commands))
	}
}

func TestPostTaskAllowsPushFromBranchMatchingProjectPattern(t *testing.T) {
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
		SSHPrivateKeyPath:    "/tmp/id_ed25519",
		SSHKnownHostsPath:    "/tmp/known_hosts",
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
	if got := strings.Join(runner.commands[7].Args, " "); got != "push --no-verify origin HEAD:docs-MASS-123-docs" {
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
	if got := strings.Join(runner.commands[0].Args, " "); got != "rev-parse --show-toplevel" {
		t.Fatalf("expected trust probe command, got %q", got)
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
		SSHPrivateKeyPath: "/run/secrets/mivia_git_key",
		SSHKnownHostsPath: "/run/secrets/mivia_known_hosts",
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
