package projectgitops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBaselineGitOpsMetadataContract(t *testing.T) {
	t.Parallel()

	input := PostTaskInput{
		WorkDir:          "worktree-ref",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		TaskRef:          "task-1",
		TaskTitle:        "Implement contract",
		TicketRef:        "PROJ-1044",
		ChangeType:       "feat",
		BranchName:       "feat-PROJ-1044-contract",
		BaseRef:          "main",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "runner-1",
		CommitBody:       "metadata-only commit body",
		AllowedPathspecs: []string{"internal/projectgitops"},
		ReviewRefs:       []string{"review:baseline"},
		VerifierRefs:     []string{"verifier:baseline"},
		TestResults:      []string{"go test ./internal/projectgitops: passed"},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal gitops input: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal gitops input: %v", err)
	}
	for _, key := range []string{
		"WorkDir", "ProjectID", "PlanID", "TaskID", "TaskRef", "TaskTitle",
		"TicketRef", "ChangeType", "BranchName", "BaseRef", "AutomationID",
		"AutomationRunID", "OperatorID", "CommitBody", "AllowedPathspecs",
		"ReviewRefs", "VerifierRefs", "TestResults",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("gitops input JSON omitted %q: %s", key, data)
		}
	}
}

func TestBaselineGitOpsOptionsCoverRecoveryAndPRControls(t *testing.T) {
	t.Parallel()

	options := Options{
		Enabled:                      true,
		CommitAfterTask:              true,
		PushAfterTask:                true,
		DraftPRAfterPush:             true,
		RequireCleanBeforeTask:       true,
		CleanupWorktreeAfterPlanDone: true,
		RemoteName:                   "origin",
		BranchPrefix:                 "mivia/",
		BranchNamePattern:            "^mivia/[a-z0-9-]+$",
		CommitAuthorName:             "Mivia Automation",
		CommitAuthorEmailEnv:         "MIVIA_GIT_AUTHOR_EMAIL",
		SignCommits:                  true,
		SSHPrivateKeyPath:            "ssh-private-key-ref",
		SSHKnownHostsPath:            "known-hosts-ref",
		GitHubTokenEnv:               "MIVIA_GITHUB_TOKEN",
		GitHubCLIPath:                "gh",
		DirtyScopeSupportPathspecs:   []string{"docs/generated"},
		Verification: VerificationProfile{
			BootstrapCommands: []string{"go test ./internal/projectgitops"},
			AlwaysBeforePR:    []string{"go test ./internal/projectgitops"},
			AutofixCommands:   []string{"gofmt -w internal/projectgitops"},
			GeneratedArtifacts: []GeneratedArtifactVerifier{{
				Paths:            []string{"docs/generated/report.md"},
				Command:          "go test ./internal/projectgitops",
				RequiredBeforePR: true,
			}},
		},
		PostPRChecks: PostPRChecks{Enabled: true, RequiredOnly: true, Watch: true, FailFast: true, IntervalSeconds: 30},
	}
	if !options.Enabled || !options.CommitAfterTask || !options.PushAfterTask || !options.DraftPRAfterPush {
		t.Fatalf("expected enabled commit/push/draft PR controls: %+v", options)
	}
	if !options.RequireCleanBeforeTask || !options.CleanupWorktreeAfterPlanDone || !options.SignCommits {
		t.Fatalf("expected clean/signing/cleanup controls: %+v", options)
	}
	if len(options.Verification.GeneratedArtifacts) != 1 || !options.Verification.GeneratedArtifacts[0].RequiredBeforePR {
		t.Fatalf("expected generated artifact gate: %+v", options.Verification)
	}
	if !options.PostPRChecks.Enabled || !options.PostPRChecks.Watch {
		t.Fatalf("expected post-PR check controls: %+v", options.PostPRChecks)
	}
}

func TestBaselineGitOpsPostTaskDraftPRBehavior(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{
			{},
			{Stdout: " M internal/projectgitops/service.go\n"},
			{Stdout: "feat-PROJ-1044-gitops\n"},
			{},
			{},
			{},
			{Stdout: "abc123def456\n"},
			{Stdout: "https://github.com/MiviaLabs/PROJ-monorepo.git\n"},
			{},
			{},
			{Stdout: "https://github.com/MiviaLabs/PROJ-monorepo/pull/1044\n"},
		},
		errs: []error{
			nil, nil, nil, nil, nil, nil, nil, nil,
			errNoPullRequestForBaseline(),
			nil,
		},
	}
	svc := NewWithRunner(Options{
		Enabled:              true,
		CommitAfterTask:      true,
		PushAfterTask:        true,
		DraftPRAfterPush:     true,
		BranchNamePattern:    "^(feat|fix|chore)-PROJ-[0-9]+(-[a-z0-9-]+)*$",
		CommitAuthorName:     "Mivia Automation",
		CommitAuthorEmailEnv: "MIVIA_GIT_AUTHOR_EMAIL",
		GitHubTokenEnv:       "MIVIA_TEST_GH_TOKEN",
		Conventions: Conventions{
			AllowedChangeTypes:       []string{"feat", "fix", "chore"},
			DefaultChangeType:        "feat",
			RequireTicket:            true,
			TicketRefPattern:         "^PROJ-[0-9]+$",
			PullRequestTitleTemplate: "{{change_type}}({{ticket_ref}}): {{work_task_title}}",
			PullRequestBodyTemplate:  "Review refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}\nTests: {{test_results}}",
		},
	}, runner)
	t.Setenv("MIVIA_GIT_AUTHOR_EMAIL", "automation@example.test")
	t.Setenv("MIVIA_TEST_GH_TOKEN", "token-ref")

	result, err := svc.PostTask(context.Background(), PostTaskInput{
		WorkDir:          "/tmp/PROJ-worktree",
		ProjectID:        "project-1",
		PlanID:           "work_plan_1",
		TaskID:           "work_task_1",
		TaskRef:          "final-pr-readiness",
		TaskTitle:        "GitOps baseline",
		TicketRef:        "PROJ-1044",
		ChangeType:       "feat",
		BranchName:       "feat-PROJ-1044-gitops",
		BaseRef:          "main",
		AutomationID:     "automation_1",
		AutomationRunID:  "automation_run_1",
		OperatorID:       "runner-1",
		AllowedPathspecs: []string{"internal/projectgitops/service.go"},
		ReviewRefs:       []string{"review:phase0"},
		VerifierRefs:     []string{"verifier:phase0"},
		TestResults:      []string{"go test ./internal/projectgitops: passed"},
	})
	if err != nil {
		t.Fatalf("post task: %v", err)
	}
	if result.PullRequestRef != "github-pr-1044" || !containsString(result.EvidenceRefs, "git-commit-created") || !containsString(result.EvidenceRefs, "git-push-completed") || !containsString(result.EvidenceRefs, "draft-pr-ready") {
		t.Fatalf("GitOps result lost commit/PR evidence: %#v", result)
	}
	var commitSeen, pushSeen, draftPRSeen bool
	for _, command := range runner.commands {
		joined := strings.Join(command.Args, " ")
		if strings.Contains(joined, "commit -m") {
			commitSeen = true
		}
		if strings.Contains(joined, "push") {
			pushSeen = true
		}
		if command.Path == "gh" && strings.Contains(joined, "pr create") && containsString(command.Args, "--draft") {
			draftPRSeen = true
		}
	}
	if !commitSeen || !pushSeen || !draftPRSeen {
		t.Fatalf("expected commit, push, and draft PR commands, got %#v", runner.commands)
	}
}

func TestBaselineGitOpsFailureCategoryContract(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"dirty_worktree": {
			err:  ErrDirtyWorktree,
			want: "gitops_dirty_worktree",
		},
		"dirty_scope_without_path_leak": {
			err:  DirtyWorktreeScopeError{Paths: []string{"/workspace/private/root/internal/projectgitops/service.go"}},
			want: "gitops_dirty_worktree_scope",
		},
		"verification_hash": {
			err:  fmt.Errorf("%w: abcdef123456", ErrVerificationFailed),
			want: "gitops_verification_failed_abcdef123456",
		},
		"signing_key_required": {
			err:  fmt.Errorf("%w: signed commits require ssh private key", ErrInvalidInput),
			want: "gitops_invalid_input_signing_key_required",
		},
		"github_token_unavailable": {
			err:  fmt.Errorf("%w: github token env is unavailable", ErrInvalidInput),
			want: "gitops_invalid_input_github_token_unavailable",
		},
		"draft_pr_create": {
			err:  gitOpsStageFailure("draft_pr", fmt.Errorf("%w: gh_pr_create", ErrCommandFailed)),
			want: "gitops_command_failed_gh_pr_create",
		},
		"post_pr_checks": {
			err:  fmt.Errorf("%w: failing,missing", ErrDownstreamChecks),
			want: "gitops_downstream_checks_failed_failing_missing",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := FailureCategoryWithDetail(tc.err)
			if got != tc.want {
				t.Fatalf("failure category mismatch: got %q want %q", got, tc.want)
			}
			for _, forbidden := range []string{"private/root", "ghp_SECRET", "permission denied"} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("failure category leaked unsafe detail %q in %q", forbidden, got)
				}
			}
		})
	}
}

func errNoPullRequestForBaseline() error {
	return baselineError("no pull request found")
}

type baselineError string

func (err baselineError) Error() string {
	return string(err)
}
