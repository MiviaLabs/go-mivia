package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileConfig_ExampleConfigParses(t *testing.T) {
	path := filepath.Join(repoRoot(t), "configs", "mivia-server.example.toml")

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected example config to parse: %v", err)
	}

	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected example config to apply: %v", err)
	}
	if len(merged.Projects) != 2 {
		t.Fatalf("expected two example projects, got %d", len(merged.Projects))
	}
}

func TestLoadFileConfig_RejectsUnknownTopLevelField(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[unexpected]
enabled = true
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unknown top-level field to fail")
	}
}

func TestLoadFileConfig_RejectsUnknownProjectField(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
aliases = ["github.com/MiviaLabs/go-mivia"]
display_name = "Example"
root_path = "/absolute/path/to/project"
unexpected = true
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unknown project field to fail")
	}
}

func TestLoadFileConfig_RejectsUnknownIngestionField(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[ingestion]
unexpected = true
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unknown ingestion field to fail")
	}
}

func TestLoadFileConfig_AcceptsDisabledAutomationConfig(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[workflows]
enabled = false
definition_paths = ["configs/workflows/governed-feature.toml"]

[automation]
enabled = false
runner_enabled = false
runner_execution = "managed"
require_codex_when_available = true
allow_manual_runner = false
queue_depth = 32
poll_interval = "2s"
global_worker_count = 2
per_project_worker_limit = 1
per_agent_worker_limit = 1
max_parallel_tasks = 2
default_max_runtime = "5m"
codex_binary_path = "codex"

[automation.work_plan_status_trigger]
enabled = true
statuses = ["active"]
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected automation config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected automation config to apply: %v", err)
	}
	if merged.Automation.Enabled || merged.Automation.RunnerEnabled {
		t.Fatalf("expected disabled automation defaults, got %+v", merged.Automation)
	}
	if merged.Automation.QueueDepth != 32 || merged.Automation.PollInterval.String() != "2s" || merged.Automation.MaxParallelTasks != 2 {
		t.Fatalf("unexpected automation limits: %+v", merged.Automation)
	}
	if merged.Automation.RunnerExecution != "managed" {
		t.Fatalf("unexpected runner execution: %+v", merged.Automation)
	}
	if !merged.Automation.WorkPlanStatusTrigger.Enabled || len(merged.Automation.WorkPlanStatusTrigger.Statuses) != 1 || merged.Automation.WorkPlanStatusTrigger.Statuses[0] != "active" {
		t.Fatalf("unexpected work plan status trigger config: %+v", merged.Automation.WorkPlanStatusTrigger)
	}
	if merged.Workflows.Enabled || len(merged.Workflows.DefinitionPaths) != 1 || merged.Workflows.DefinitionPaths[0] != "configs/workflows/governed-feature.toml" {
		t.Fatalf("unexpected workflow config: %+v", merged.Workflows)
	}
}

func TestAutomationEnvOverrides(t *testing.T) {
	t.Setenv("MIVIA_AUTOMATION_ENABLED", "true")
	t.Setenv("MIVIA_AUTOMATION_RUNNER_ENABLED", "true")
	t.Setenv("MIVIA_AUTOMATION_RUNNER_EXECUTION", "external")
	t.Setenv("MIVIA_AUTOMATION_CODEX_BINARY_PATH", "codex")
	t.Setenv("MIVIA_AUTOMATION_WORK_PLAN_STATUS_TRIGGER_ENABLED", "true")
	t.Setenv("MIVIA_AUTOMATION_WORK_PLAN_STATUS_TRIGGER_STATUSES", "active,needs_review")

	cfg := defaultConfig("")
	if err := applyEnvOverrides(&cfg); err != nil {
		t.Fatalf("apply env overrides: %v", err)
	}
	if !cfg.Automation.Enabled || !cfg.Automation.RunnerEnabled {
		t.Fatalf("expected automation enabled from env: %+v", cfg.Automation)
	}
	if cfg.Automation.RunnerExecution != "external" || cfg.Automation.CodexBinaryPath != "codex" {
		t.Fatalf("unexpected automation runner env config: %+v", cfg.Automation)
	}
	if !cfg.Automation.WorkPlanStatusTrigger.Enabled || strings.Join(cfg.Automation.WorkPlanStatusTrigger.Statuses, ",") != "active,needs_review" {
		t.Fatalf("unexpected status trigger env config: %+v", cfg.Automation.WorkPlanStatusTrigger)
	}
}

func TestGitOperationsFileAndEnvOverrides(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[git_operations]
enabled = true
commit_after_task = true
push_after_task = true
draft_pr_after_push = true
require_clean_before_task = true
cleanup_worktree_after_plan_done = true
remote_name = "upstream"
branch_prefix = "mivia/"
commit_author_name = "Mivia Automation"
commit_author_email_env = "MIVIA_GIT_AUTHOR_EMAIL"
ssh_private_key_path = "/run/secrets/mivia_git_key"
ssh_public_key_path = "/run/secrets/mivia_git_key.pub"
ssh_known_hosts_path = "/run/secrets/mivia_known_hosts"
github_token_env = "GITHUB_TOKEN"
github_cli_path = "gh"

[git_operations.conventions]
commit_type = "feat"
commit_scope = "gitops"
commit_summary_template = "finish {{work_task_ref}}"
pull_request_title_template = "feat({{ticket_ref}}): finish {{work_task_ref}}"
what_changed_template = "Completed {{work_task_title}} for {{project_id}} on {{branch_name}}."
how_verified_template = "Review refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}"
tests_template = "{{test_results}}"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected git operations config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected git operations config to apply: %v", err)
	}
	merged.resolveAutoSettings(1)
	if err := merged.Validate(); err != nil {
		t.Fatalf("expected git operations config to validate: %v", err)
	}
	if !merged.GitOperations.Enabled || !merged.GitOperations.PushAfterTask || !merged.GitOperations.DraftPRAfterPush {
		t.Fatalf("expected git operations enabled flags, got %+v", merged.GitOperations)
	}
	if merged.GitOperations.RemoteName != "upstream" || merged.GitOperations.SSHKnownHostsPath == "" {
		t.Fatalf("unexpected git operations config: %+v", merged.GitOperations)
	}
	if merged.GitOperations.Conventions.CommitType != "feat" || merged.GitOperations.Conventions.CommitScope != "gitops" || !strings.Contains(merged.GitOperations.Conventions.WhatChangedTemplate, "{{work_task_title}}") {
		t.Fatalf("unexpected git operations conventions: %+v", merged.GitOperations.Conventions)
	}

	t.Setenv("MIVIA_GIT_OPS_REMOTE_NAME", "origin")
	t.Setenv("MIVIA_GIT_OPS_DRAFT_PR_AFTER_PUSH", "false")
	t.Setenv("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_TYPE", "fix")
	t.Setenv("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SCOPE", "runner")
	t.Setenv("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SUMMARY_TEMPLATE", "complete {{work_task_id}}")
	if err := applyEnvOverrides(&merged); err != nil {
		t.Fatalf("expected git operations env overrides: %v", err)
	}
	if merged.GitOperations.RemoteName != "origin" || merged.GitOperations.DraftPRAfterPush {
		t.Fatalf("unexpected git operations env overrides: %+v", merged.GitOperations)
	}
	if merged.GitOperations.Conventions.CommitType != "fix" || merged.GitOperations.Conventions.CommitScope != "runner" || merged.GitOperations.Conventions.CommitSummaryTemplate != "complete {{work_task_id}}" {
		t.Fatalf("unexpected git operations convention env overrides: %+v", merged.GitOperations.Conventions)
	}
}

func TestProjectGitOperationsOverrideGlobalDefaults(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[git_operations]
enabled = true
commit_after_task = true
push_after_task = true
draft_pr_after_push = true
require_clean_before_task = true
remote_name = "origin"
branch_prefix = "mivia/"
commit_author_name = "Mivia Automation"
commit_author_email_env = "MIVIA_GIT_AUTHOR_EMAIL"
ssh_private_key_path = "/run/secrets/mivia_git_key"
ssh_known_hosts_path = "/run/secrets/mivia_known_hosts"
github_token_env = "GITHUB_TOKEN"
github_cli_path = "gh"

[[projects]]
id = "project-a"
root_path = "/repo/project-a"
enabled = true
digest_mode = "content_graph"
workspace_mode = "edit"

[projects.git_operations]
branch_prefix = ""
branch_name_pattern = "^(feat|fix|docs)-ABC-[0-9]+(-[a-z0-9-]+)*$"

[projects.git_operations.conventions]
commit_type = "docs"
commit_summary_template = "complete {{work_task_ref}}"
pull_request_title_template = "{{commit_subject}}"
what_changed_template = "Summary: {{work_task_title}}"
how_verified_template = "Automation Run ID: {{automation_run_id}}"
tests_template = "{{test_results}}"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected project git operations config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected project git operations config to apply: %v", err)
	}
	merged.resolveAutoSettings(1)
	if err := merged.Validate(); err != nil {
		t.Fatalf("expected project git operations config to validate: %v", err)
	}
	if len(merged.Projects) != 1 || merged.Projects[0].GitOperations == nil {
		t.Fatalf("expected project git operations override, got %+v", merged.Projects)
	}
	projectGitOps := merged.Projects[0].GitOperations
	if !projectGitOps.PushAfterTask || projectGitOps.SSHPrivateKeyPath == "" || projectGitOps.GitHubTokenEnv != "GITHUB_TOKEN" {
		t.Fatalf("expected project override to inherit global push settings, got %+v", projectGitOps)
	}
	if projectGitOps.BranchPrefix != "" || projectGitOps.BranchNamePattern == "" {
		t.Fatalf("expected project branch policy override, got %+v", projectGitOps)
	}
	if projectGitOps.Conventions.CommitType != "docs" || !strings.Contains(projectGitOps.Conventions.WhatChangedTemplate, "Summary") {
		t.Fatalf("expected project convention override, got %+v", projectGitOps.Conventions)
	}
}

func TestLoadFileConfig_RejectsInvalidAutomationSettings(t *testing.T) {
	for name, body := range map[string]string{
		"runner_without_enabled": `
version = 1

[automation]
enabled = false
runner_enabled = true
`,
		"zero_parallel": `
version = 1

[automation]
max_parallel_tasks = 0
`,
		"negative_poll_interval": `
version = 1

[automation]
poll_interval = "-1s"
`,
		"absolute_workflow_path": `
version = 1

[workflows]
definition_paths = ["/home/mac/workflow.toml"]
`,
		"parent_workflow_path": `
version = 1

[workflows]
definition_paths = ["../workflow.toml"]
`,
		"windows_drive_workflow_path": `
version = 1

[workflows]
definition_paths = ["C:/workflow.toml"]
`,
		"unknown_field": `
version = 1

[automation]
unexpected = true
`,
		"unknown_runner_execution": `
version = 1

[automation]
runner_execution = "container_magic"
`,
		"unsupported_status_trigger": `
version = 1

[automation.work_plan_status_trigger]
enabled = true
statuses = ["ready"]
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, body)
			cfg, err := loadFileConfig(path)
			if name == "unknown_field" {
				if err == nil {
					t.Fatal("expected unknown automation field to fail")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected TOML decode to succeed before validation: %v", err)
			}
			merged, err := cfg.applyTo(defaultConfig(path))
			if err != nil {
				t.Fatalf("expected TOML apply to succeed before validation: %v", err)
			}
			if err := merged.Validate(); err == nil {
				t.Fatal("expected invalid automation settings to fail validation")
			}
		})
	}
}

func TestGitOperationsValidateRejectsUnsafeCombinations(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"push_without_commit": func(cfg *Config) {
			cfg.GitOperations.PushAfterTask = true
			cfg.GitOperations.CommitAfterTask = false
		},
		"draft_without_push": func(cfg *Config) {
			cfg.GitOperations.DraftPRAfterPush = true
			cfg.GitOperations.PushAfterTask = false
		},
		"push_without_known_hosts": func(cfg *Config) {
			cfg.GitOperations.PushAfterTask = true
			cfg.GitOperations.SSHPrivateKeyPath = "/run/secrets/mivia_git_key"
			cfg.GitOperations.SSHKnownHostsPath = ""
		},
		"relative_key_path": func(cfg *Config) {
			cfg.GitOperations.SSHPrivateKeyPath = "secrets/mivia_git_key"
		},
		"two_token_refs": func(cfg *Config) {
			cfg.GitOperations.GitHubTokenEnv = "GITHUB_TOKEN"
			cfg.GitOperations.GitHubTokenFile = "/run/secrets/github_token"
		},
		"bad_convention_type": func(cfg *Config) {
			cfg.GitOperations.Conventions.CommitType = "Feature"
		},
		"bad_convention_placeholder": func(cfg *Config) {
			cfg.GitOperations.Conventions.CommitSummaryTemplate = "complete {{repository_name}}"
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := defaultConfig("test.toml")
			cfg.resolveAutoSettings(1)
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid git operations config to fail")
			}
		})
	}
}

func TestLoadFileConfig_RejectsInvalidAutoIntegerSettings(t *testing.T) {
	for name, body := range map[string]string{
		"server_zero_cpu": `
version = 1

[server]
cpu_count = 0
`,
		"server_bad_cpu": `
version = 1

[server]
cpu_count = "bad"
`,
		"ingestion_bad_worker": `
version = 1

[ingestion]
worker_count = "bad"
`,
		"ingestion_zero_global_worker": `
version = 1

[ingestion]
global_worker_count = 0
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, body)
			cfg, err := loadFileConfig(path)
			if err != nil {
				t.Fatalf("expected TOML decode to succeed before apply: %v", err)
			}
			_, err = cfg.applyTo(defaultConfig(path))
			if err == nil {
				t.Fatal("expected invalid auto integer setting to fail")
			}
		})
	}
}

func TestLoadFileConfig_AcceptsContentGraphAndLiveContractValues(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[workspace]
enabled = true

[[projects]]
id = "example"
display_name = "Example"
aliases = ["github.com/MiviaLabs/go-mivia"]
root_path = "/absolute/path/to/project"
digest_mode = "content_graph"
update_policy = "live"
graph_storage = "in_memory"
workspace_mode = "edit"
max_file_bytes = 0
max_chunk_bytes = 512
sensitive_marker_policy = "skip_file"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected config contract values to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected config contract values to apply: %v", err)
	}
	project := merged.Projects[0]
	if project.DigestMode != digestModeContentGraph || project.UpdatePolicy != updatePolicyLive {
		t.Fatalf("unexpected project modes: %+v", project)
	}
	if project.GraphStorage != graphStorageInMemory {
		t.Fatalf("unexpected graph storage: %+v", project)
	}
	if len(project.Aliases) != 1 || project.Aliases[0] != "github.com/MiviaLabs/go-mivia" {
		t.Fatalf("unexpected aliases: %+v", project.Aliases)
	}
	if !merged.Workspace.Enabled || project.WorkspaceMode != "edit" {
		t.Fatalf("unexpected workspace config: %+v", merged)
	}
	if project.MaxFileBytes != 0 || project.MaxChunkBytes != 512 {
		t.Fatalf("unexpected project caps: %+v", project)
	}
}

func TestLoadFileConfig_AcceptsLargeFullScanBatchSize(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[ingestion]
full_scan_batch_size = 20000
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected config to apply: %v", err)
	}
	if merged.Ingestion.FullScanBatchSize != 20000 {
		t.Fatalf("expected full scan batch size 20000, got %d", merged.Ingestion.FullScanBatchSize)
	}
}

func TestLoadFileConfig_AcceptsZeroCoverageCapsAsUnlimited(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[ingestion]
max_file_bytes = 0

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
max_file_bytes = 0

[projects.integrations.jira]
max_results = 0

[projects.integrations.confluence]
max_results = 0
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected zero coverage caps to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected zero coverage caps to apply: %v", err)
	}
	project := merged.Projects[0]
	if merged.Ingestion.MaxFileBytes != 0 || project.MaxFileBytes != 0 {
		t.Fatalf("expected source file caps to be unlimited, got global=%d project=%d", merged.Ingestion.MaxFileBytes, project.MaxFileBytes)
	}
	if project.Integrations.Jira == nil || project.Integrations.Jira.MaxResults != 0 {
		t.Fatalf("expected Jira max results to be unlimited, got %+v", project.Integrations.Jira)
	}
	if project.Integrations.Confluence == nil || project.Integrations.Confluence.MaxResults != 0 {
		t.Fatalf("expected Confluence max results to be unlimited, got %+v", project.Integrations.Confluence)
	}
}

func TestLoadFileConfig_IntegrationDefaultsAreUnlimitedWithProviderPageSizes(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
project_keys = ["ABC"]

[projects.integrations.confluence]
space_keys = ["ENG"]
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected integration defaults to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected integration defaults to apply: %v", err)
	}
	jira := merged.Projects[0].Integrations.Jira
	confluence := merged.Projects[0].Integrations.Confluence
	if jira.MaxResults != 0 || confluence.MaxResults != 0 {
		t.Fatalf("expected unlimited provider max results, got jira=%d confluence=%d", jira.MaxResults, confluence.MaxResults)
	}
	if jira.Polling.InitialPageSize != 100 || jira.Polling.IncrementalPageSize != 100 {
		t.Fatalf("expected Jira page size defaults 100, got %+v", jira.Polling)
	}
	if confluence.Polling.InitialPageSize != 50 || confluence.Polling.IncrementalPageSize != 50 {
		t.Fatalf("expected Confluence page size defaults 50, got %+v", confluence.Polling)
	}
}

func TestLoadFileConfig_AcceptsLoggingFileSection(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[logging]
file_enabled = true
file_path = "data/mivia-server.log"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected logging config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected logging config to apply: %v", err)
	}
	if !merged.Logging.FileEnabled || merged.Logging.FilePath != "data/mivia-server.log" {
		t.Fatalf("unexpected logging config: %+v", merged.Logging)
	}
}

func TestLoadFileConfig_AcceptsProjectIntegrationSections(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
cloud_id = "00000000-0000-0000-0000-000000000000"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
api_token_file = "secrets/atlassian-token"
project_keys = ["abc", "XYZ"]
ingestion_enabled = true
initial_full_sync = "manual"
incremental_interval = "1m"
empty_poll_sleep = "10m"
max_idle_sleep = "30m"
overlap_window = "2m"
initial_page_size = 50
incremental_page_size = 25
read_timeout = "5s"
max_results = 200
default_fields = ["summary", "status"]
allowed_fields = ["customfield_10010"]
include_rich_fields = true
include_comments = true
jql_extra_filter = "issuetype is not EMPTY"

[projects.integrations.confluence]
enabled = true
site_url = "https://example.atlassian.net"
cloud_id = "00000000-0000-0000-0000-000000000000"
auth_mode = "api_token_basic"
email_file = "secrets/atlassian-email"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
space_keys = ["ENG", "Ops"]
body_representation = "storage"
include_body = true
include_comments = true
include_labels = true
include_properties = true
ingestion_enabled = true
incremental_interval = "1m"
empty_poll_sleep = "5m"
max_idle_sleep = "30m"
overlap_window = "30s"
initial_page_size = 100
incremental_page_size = 100
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected integration config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected integration config to apply: %v", err)
	}
	project := merged.Projects[0]
	if project.Integrations.Jira == nil || project.Integrations.Confluence == nil {
		t.Fatalf("expected both integration providers: %+v", project.Integrations)
	}
	if got := project.Integrations.Jira.ProjectKeys; len(got) != 2 || got[0] != "ABC" || got[1] != "XYZ" {
		t.Fatalf("expected normalized Jira project keys, got %#v", got)
	}
	if project.Integrations.Jira.CredentialRefs.EmailEnv == "" || project.Integrations.Jira.CredentialRefs.APITokenFile == "" {
		t.Fatalf("expected Jira credential references only, got %+v", project.Integrations.Jira.CredentialRefs)
	}
	if project.Integrations.Jira.Polling.IncrementalInterval.String() != "1m0s" {
		t.Fatalf("unexpected Jira polling defaults/overrides: %+v", project.Integrations.Jira.Polling)
	}
	if got := project.Integrations.Confluence.SpaceKeys; len(got) != 2 || got[0] != "ENG" || got[1] != "Ops" {
		t.Fatalf("expected Confluence space keys, got %#v", got)
	}
}

func TestLoadFileConfig_AcceptsAtlassianCredentialsFileRef(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
credentials_file = "data/local-atlassian-credentials.json"
project_keys = ["ABC"]
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected credentials_file config to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected credentials_file config to apply: %v", err)
	}
	refs := merged.Projects[0].Integrations.Jira.CredentialRefs
	if refs.CredentialsFile != "data/local-atlassian-credentials.json" || refs.EmailEnv != "" || refs.APITokenFile != "" {
		t.Fatalf("expected single credentials file ref, got %+v", refs)
	}
}

func TestLoadFileConfig_RejectsEnabledIntegrationWithoutAllowlists(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{
			name: "jira project keys",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
`,
			message: "project_keys",
		},
		{
			name: "confluence space keys",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.confluence]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
`,
			message: "space_keys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.content)
			_, err := loadFileConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("expected %q error, got %v", tt.message, err)
			}
		})
	}
}

func TestLoadFileConfig_RejectsUnsafeIntegrationCredentialFields(t *testing.T) {
	for _, field := range []string{"email", "api_token", "password", "token", "basic_auth"} {
		t.Run(field, func(t *testing.T) {
			path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
project_keys = ["ABC"]
`+field+` = "unsafe"
`)
			_, err := loadFileConfig(path)
			if err == nil {
				t.Fatal("expected raw credential-like field to fail")
			}
		})
	}
}

func TestLoadFileConfig_RejectsInvalidIntegrationSettings(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{
			name: "raw site URL",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "http://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
project_keys = ["ABC"]
`,
			message: "site_url",
		},
		{
			name: "both email refs",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
email_file = "secrets/email"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
project_keys = ["ABC"]
`,
			message: "email",
		},
		{
			name: "credentials file combined with split refs",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
credentials_file = "data/local-atlassian-credentials.json"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
project_keys = ["ABC"]
`,
			message: "credentials_file",
		},
		{
			name: "empty poll above max idle",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "MIVIA_ATLASSIAN_EMAIL_EXAMPLE"
api_token_env = "MIVIA_ATLASSIAN_API_TOKEN_EXAMPLE"
project_keys = ["ABC"]
empty_poll_sleep = "31m"
max_idle_sleep = "30m"
`,
			message: "empty_poll_sleep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.content)
			_, err := loadFileConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("expected %q error, got %v", tt.message, err)
			}
		})
	}
}

func TestLoadFileConfig_RejectsUnsupportedWorkspaceMode(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
workspace_mode = "shell"
`)

	_, err := loadFileConfig(path)
	if err == nil || !strings.Contains(err.Error(), "workspace_mode") {
		t.Fatalf("expected workspace_mode error, got %v", err)
	}
}

func TestLoadFileConfig_WorkspaceModeRequiresContentGraph(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
digest_mode = "metadata_only"
workspace_mode = "read_only"
`)

	_, err := loadFileConfig(path)
	if err == nil || !strings.Contains(err.Error(), "content_graph") {
		t.Fatalf("expected content_graph requirement, got %v", err)
	}
}

func TestLoadFileConfig_RejectsUnsupportedDigestMode(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
digest_mode = "embedding"
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unsupported digest mode to fail")
	}
	if !strings.Contains(err.Error(), "digest_mode") {
		t.Fatalf("expected digest_mode error, got %v", err)
	}
}

func TestLoadFileConfig_RejectsUnsupportedUpdatePolicy(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
update_policy = "watch"
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unsupported update policy to fail")
	}
	if !strings.Contains(err.Error(), "update_policy") {
		t.Fatalf("expected update_policy error, got %v", err)
	}
}

func TestLoadFileConfig_RejectsUnsupportedGraphStorage(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
graph_storage = "remote"
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unsupported graph storage to fail")
	}
	if !strings.Contains(err.Error(), "graph_storage") {
		t.Fatalf("expected graph_storage error, got %v", err)
	}
}

func TestFileConfigApplyTo_RejectsInvalidDuration(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[server]
request_timeout = "soon"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected TOML to parse before apply validation: %v", err)
	}
	if _, err := cfg.applyTo(defaultConfig(path)); err == nil {
		t.Fatal("expected invalid duration to fail")
	}
}

func TestFileConfigApplyTo_RejectsInvalidIngestionDuration(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[ingestion]
debounce_interval = "soon"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected TOML to parse before apply validation: %v", err)
	}
	if _, err := cfg.applyTo(defaultConfig(path)); err == nil {
		t.Fatal("expected invalid ingestion duration to fail")
	}
}

func TestLoadFileConfig_RejectsInvalidIngestionValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{
			name: "global max file bytes",
			content: `
version = 1

[ingestion]
max_file_bytes = -1
`,
			message: "max_file_bytes",
		},
		{
			name: "project max chunk bytes",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
max_chunk_bytes = 0
`,
			message: "max_chunk_bytes",
		},
		{
			name: "sensitive marker policy",
			content: `
version = 1

[ingestion]
sensitive_marker_policy = "store"
`,
			message: "sensitive_marker_policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.content)
			_, err := loadFileConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("expected %q error, got %v", tt.message, err)
			}
		})
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mivia-server.local.toml")
	writeFile(t, path, content)
	return path
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found while locating repo root")
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
