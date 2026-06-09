package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestEffectiveInitialScanOnStartSkipsWhenRestartRecoveryQueued(t *testing.T) {
	if effectiveInitialScanOnStart(true, 3) {
		t.Fatalf("expected restart recovery scans to suppress live initial scans")
	}
}

func TestEffectiveInitialScanOnStartKeepsConfiguredValueWithoutRecovery(t *testing.T) {
	if !effectiveInitialScanOnStart(true, 0) {
		t.Fatalf("expected configured initial scan to remain enabled")
	}
	if effectiveInitialScanOnStart(false, 0) {
		t.Fatalf("expected configured disabled initial scan to remain disabled")
	}
}

func TestDirtyScopeRecoveryResolverUsesProjectGitOpsPolicy(t *testing.T) {
	resolver := dirtyScopeRecoveryResolver(config.Config{
		GitOperations: config.GitOperations{
			DirtyScopeRecovery: config.DirtyScopeRecovery{AllowedSupportPathspecs: []string{".global"}},
		},
		Projects: []config.Project{
			{
				ID:      "project-a",
				Aliases: []string{"alias-a"},
				GitOperations: &config.GitOperations{
					DirtyScopeRecovery: config.DirtyScopeRecovery{AllowedSupportPathspecs: []string{".codex", ".github"}},
				},
			},
		},
	})

	if got := strings.Join(resolver("project-a"), ","); got != ".codex,.github" {
		t.Fatalf("expected project dirty scope policy, got %q", got)
	}
	if got := strings.Join(resolver("alias-a"), ","); got != ".codex,.github" {
		t.Fatalf("expected alias dirty scope policy, got %q", got)
	}
	if got := strings.Join(resolver("missing"), ","); got != "" {
		t.Fatalf("expected no project policy for missing project, got %q", got)
	}
}

func TestWorkflowChainConfigsInheritGlobalGitOpsWhenProjectOverridesBranchPolicy(t *testing.T) {
	chains := workflowChainConfigs(config.Config{
		GitOperations: config.GitOperations{Enabled: true},
		Projects: []config.Project{
			{
				ID: "mass-monorepo",
				GitOperations: &config.GitOperations{
					BranchPrefix:      "",
					BranchNamePattern: "^(feat|fix|docs|chore)-MASS-[0-9]+$",
				},
				WorkflowChains: []config.WorkflowChain{
					{
						ChainRef:     "mass-governed-ticket-delivery",
						Enabled:      true,
						InputKind:    "jira_issue_key",
						InputPattern: "^MASS-[0-9]+$",
						ContextMode:  "local_ingested",
						GitOpsMode:   projectworkflowchain.GitOpsModeDraftPRAfterValidation,
						Stages:       []config.WorkflowChainStage{{StageRef: "decomposition", WorkflowRef: "governed-decomposition-planning"}},
					},
				},
			},
		},
	})
	if len(chains) != 1 {
		t.Fatalf("expected one workflow chain config, got %#v", chains)
	}
	if !chains[0].GitOpsEnabled {
		t.Fatalf("workflow chain must inherit globally enabled GitOps when project override only sets branch policy: %#v", chains[0])
	}
}

func TestServerWorkflowChainGitOpsFinalizerCommitsGeneratedTaskHandoffRefs(t *testing.T) {
	repo := initServerGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "apps", "domain", "src", "module.ts"), []byte("export const value = 2;\n"), 0o600); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	t.Setenv("MIVIA_TEST_GIT_EMAIL", "mivia@example.test")
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "mass-monorepo",
		DisplayName:    "MASS",
		RootPath:       repo,
		Enabled:        true,
		GraphNamespace: "mass-monorepo",
		DigestMode:     projectregistry.DigestModeMetadataOnly,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
	}}, projectregistry.Options{
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	finalizer := serverWorkflowChainGitOpsFinalizer{
		cfg: config.Config{
			GitOperations: config.GitOperations{
				Enabled:              true,
				CommitAfterTask:      true,
				BranchNamePattern:    "^(feat|fix|docs|chore)-MASS-[0-9]+(-[a-z0-9-]+)*$",
				CommitAuthorEmailEnv: "MIVIA_TEST_GIT_EMAIL",
			},
		},
		registry: registry,
	}

	result, err := finalizer.FinalizeWorkflowChain(t.Context(), projectworkflowchain.GitOpsFinalizeInput{
		ProjectID:        "mass-monorepo",
		ChainRunID:       "workflow_chain_run_mass_1044",
		InputRef:         "jira:MASS-1044",
		WorkPlan:         projectworkplan.WorkPlan{ID: "plan-post-validation", ProjectID: "mass-monorepo", GitBranchRef: "chore-MASS-1044-governed-post-implementation-validation-compile-1", GitBaseRef: "main"},
		StageRuns:        []projectworkflowchain.StageRun{{StageRef: "post-validation", WorkTaskIDs: []string{"task-final-pr-readiness"}}},
		AllowedPathspecs: []string{"apps/domain/src/module.ts"},
		ReviewRefs:       []string{"review_result:post_validation_approved"},
		VerifierRefs:     []string{"verifier:focused_tests_passed"},
		TestResults:      []string{"go test ./apps/domain -run TestMass1044 -count=1"},
	})
	if err != nil {
		t.Fatalf("FinalizeWorkflowChain returned error: %v", err)
	}
	if result.CommitRef == "" || !containsServerString(result.EvidenceRefs, "git-commit-created") {
		t.Fatalf("expected local commit result, got %#v", result)
	}
	message := runServerGitOutput(t, repo, "log", "-1", "--format=%B")
	for _, want := range []string{
		"Work Task ID: task-final-pr-readiness",
		"Automation Run ID: workflow_chain_run_mass_1044",
		"Review refs: review_result:post_validation_approved",
		"Verifier refs: verifier:focused_tests_passed",
		"go test ./apps/domain -run TestMass1044 -count=1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("final GitOps commit lost handoff field %q:\n%s", want, message)
		}
	}
}

func TestLocalConfigLoadsMassTicketPipelineEntryWorkflows(t *testing.T) {
	cfg, err := config.LoadPath(filepath.Join("..", "..", "configs", "mivia-server.local.toml"))
	if err != nil {
		t.Fatalf("load local config: %v", err)
	}
	chains := workflowChainConfigs(cfg)
	var massChain projectworkflowchain.Config
	for _, chain := range chains {
		if chain.ProjectID == "mass-monorepo" && chain.ChainRef == "mass-governed-ticket-delivery" {
			massChain = chain
			break
		}
	}
	if massChain.ChainRef == "" || !massChain.Enabled || !massChain.GitOpsEnabled || massChain.GitOpsMode != projectworkflowchain.GitOpsModeDraftPRAfterValidation {
		t.Fatalf("local MASS chain must be enabled with final draft PR GitOps: %#v", massChain)
	}
	if massChain.InputKind != projectworkflowchain.InputKindJiraIssueKey || massChain.InputPattern != "^MASS-[0-9]+$" || massChain.ContextMode != projectworkflowchain.ContextModeLocalIngested {
		t.Fatalf("local MASS chain must start from local-ingested Jira keys only: %#v", massChain)
	}
	workflows := projectworkflow.New(workflowstore.NewMemoryStore())
	if err := loadConfiguredWorkflows(t.Context(), cfg, workflows, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("load configured workflows: %v", err)
	}
	loaded, err := workflows.ListWorkflows(t.Context(), projectworkflow.WorkflowFilter{ProjectID: "mass-monorepo"})
	if err != nil {
		t.Fatalf("list MASS workflows: %v", err)
	}
	loadedRefs := map[string]bool{}
	for _, workflow := range loaded {
		loadedRefs[workflow.WorkflowRef] = true
	}
	if len(massChain.Stages) != 3 {
		t.Fatalf("MASS ticket chain must have decomposition, implementation, and post-validation stages: %#v", massChain.Stages)
	}
	for _, stage := range massChain.Stages {
		if !loadedRefs[stage.WorkflowRef] {
			t.Fatalf("MASS chain stage %s references workflow %s that was not loaded from local config; loaded=%#v", stage.StageRef, stage.WorkflowRef, loadedRefs)
		}
	}
}

func TestProjectPersistentGraphMaxOpenDerivesFromConfiguredProjects(t *testing.T) {
	projects := make([]config.Project, 0, ladybug.DefaultPebbleGraphMaxOpen+4)
	for index := 0; index < ladybug.DefaultPebbleGraphMaxOpen+4; index++ {
		id := fmt.Sprintf("project-%02d", index)
		projects = append(projects, config.Project{
			ID:             id,
			DisplayName:    "Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: id,
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		})
	}
	registry, err := projectregistry.NewRegistry(projects, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := projectPersistentGraphMaxOpen(registry); got != ladybug.DefaultPebbleGraphMaxOpen {
		t.Fatalf("expected cap %d, got %d", ladybug.DefaultPebbleGraphMaxOpen, got)
	}
}

func TestProjectPersistentGraphMaxOpenUsesConfiguredCountBelowCap(t *testing.T) {
	registry, err := projectregistry.NewRegistry([]config.Project{
		{
			ID:             "first",
			DisplayName:    "First",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "first",
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
		{
			ID:             "memory",
			DisplayName:    "Memory",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "memory",
			GraphStorage:   projectregistry.GraphStorageInMemory,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
	}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := projectPersistentGraphMaxOpen(registry); got != 1 {
		t.Fatalf("expected one persistent content graph project, got %d", got)
	}
}

func TestResolveWorkflowDefinitionPathPrefersConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	expected := filepath.Join(dir, "workflows", "workflow.toml")
	if err := os.WriteFile(expected, []byte("id = \"workflow\"\n"), 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	resolved, err := resolveWorkflowDefinitionPath(filepath.Join(dir, "mivia-server.local.toml"), "workflows/workflow.toml")
	if err != nil {
		t.Fatalf("resolve workflow path: %v", err)
	}
	if resolved != expected {
		t.Fatalf("expected config-relative path %q, got %q", expected, resolved)
	}
}

func TestResolveWorkflowDefinitionPathRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"", ".", "/tmp/workflow.toml", "../workflow.toml", "workflows/../workflow.toml"} {
		if _, err := resolveWorkflowDefinitionPath("configs/mivia-server.local.toml", path); err == nil {
			t.Fatalf("expected unsafe workflow path %q to fail", path)
		}
	}
}

func TestConfigCheckRedactedJSONValidConfig(t *testing.T) {
	projectRoot := t.TempDir()
	configPath := writeConfigFixture(t, fmt.Sprintf(`
version = 1

[server]
http_addr = "127.0.0.1:9090"

[storage]
ladybug_path = "/tmp/private/mivialabs.lbug"
sqlite_path = "/tmp/private/mivialabs.sqlite"

[logging]
file_enabled = true
file_path = "/tmp/private/mivia.log"

[ingestion]
content_graph_enabled = true
live_updates_enabled = true

[automation]
enabled = false
runner_enabled = false
codex_binary_path = "/private/bin/codex"

[[projects]]
id = "example-service"
display_name = "Example"
root_path = %q
enabled = true
graph_namespace = "example-service"
digest_mode = "content_graph"
update_policy = "live"
workspace_mode = "edit"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "JIRA_EMAIL"
api_token_file = "/home/user/.secrets/jira-token"
project_keys = ["MIVIA"]
`, projectRoot))

	var stdout, stderr bytes.Buffer
	exitCode := runConfigCheck([]string{"--config", configPath, "--redacted-json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected valid config, exit=%d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	raw := stdout.String()
	for _, forbidden := range []string{configPath, projectRoot, "/tmp/private", "/private/bin", "/home/user", "127.0.0.1:9090", "https://example.atlassian.net", "JIRA_EMAIL", "jira-token"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("redacted report leaked %q: %s", forbidden, raw)
		}
	}
	var report configCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != "valid" || report.Effective == nil {
		t.Fatalf("expected valid effective report, got %#v", report)
	}
	if got := report.Effective.Projects[0].Integrations.Jira.CredentialRefs; !got.Present || got.Class != "credential_ref" {
		t.Fatalf("expected credential refs to be class-only, got %#v", got)
	}
	if got := report.Effective.Automation.CodexBinaryPath; !got.Present || got.Class != "path" {
		t.Fatalf("expected automation codex binary path to be redacted, got %#v", got)
	}
}

func TestConfigCheckRedactedJSONInvalidBind(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[server]
http_addr = "0.0.0.0:8080"
`)

	assertConfigCheckInvalid(t, configPath, "invalid_bind", []string{"0.0.0.0:8080", configPath})
}

func TestConfigCheckRedactedJSONInvalidIngestion(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[ingestion]
live_updates_enabled = true
content_graph_enabled = false
`)

	assertConfigCheckInvalid(t, configPath, "invalid_ingestion", []string{configPath})
}

func TestConfigCheckRedactedJSONInvalidAutomation(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[automation]
enabled = false
runner_enabled = true
codex_binary_path = "/private/bin/codex"
`)

	assertConfigCheckInvalid(t, configPath, "invalid_automation", []string{configPath, "/private/bin/codex"})
}

func TestConfigCheckRedactedJSONInvalidWorkspace(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[[projects]]
id = "example-service"
root_path = "/home/user/private/example"
workspace_mode = "edit"
digest_mode = "metadata_only"
`)

	assertConfigCheckInvalid(t, configPath, "invalid_workspace", []string{"/home/user/private/example", configPath})
}

func TestRunCLIConfigCheckRequiresRedactedJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	handled, exitCode := runCLI([]string{"config", "check", "--config", "config.toml"}, &stdout, &stderr)
	if !handled || exitCode != 2 {
		t.Fatalf("expected handled usage error, handled=%v exit=%d", handled, exitCode)
	}
}

func assertConfigCheckInvalid(t *testing.T, configPath string, category string, forbidden []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exitCode := runConfigCheck([]string{"--config", configPath, "--redacted-json"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected invalid config exit 1, got %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	raw := stdout.String()
	for _, value := range forbidden {
		if strings.Contains(raw, value) {
			t.Fatalf("redacted invalid report leaked %q: %s", value, raw)
		}
	}
	var report configCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != "invalid" || report.ErrorCategory != category {
		t.Fatalf("expected invalid category %q, got %#v", category, report)
	}
	if report.Effective != nil {
		t.Fatalf("invalid report must not include effective config: %#v", report.Effective)
	}
}

func writeConfigFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mivia-server.toml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}

func initServerGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runServerGit(t, dir, "init", "-b", "main")
	runServerGit(t, dir, "config", "user.name", "Mivia Test")
	runServerGit(t, dir, "config", "user.email", "mivia@example.test")
	if err := os.MkdirAll(filepath.Join(dir, "apps", "domain", "src"), 0o755); err != nil {
		t.Fatalf("mkdir fixture tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apps", "domain", "src", "module.ts"), []byte("export const value = 1;\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runServerGit(t, dir, "add", ".")
	runServerGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runServerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runServerGitOutput(t, dir, args...)
}

func runServerGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}

func containsServerString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
