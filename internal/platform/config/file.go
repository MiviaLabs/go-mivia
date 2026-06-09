package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	configVersion          = 1
	digestModeMetadataOnly = "metadata_only"
	digestModeContentGraph = "content_graph"
	updatePolicyManual     = "manual"
	updatePolicyLive       = "live"
	graphStoragePersistent = "persistent"
	graphStorageInMemory   = "in_memory"
	authModeAPITokenBasic  = "api_token_basic"
	initialFullSyncManual  = "manual"
	initialFullSyncOnStart = "on_start"
)

var jiraProjectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*$`)

func parseAutoIntValue(value any) (int, error) {
	switch typed := value.(type) {
	case string:
		return parseAutoIntString("auto integer setting", typed)
	case int64:
		if typed <= 0 {
			return 0, fmt.Errorf("auto integer setting must be %q or a positive integer", "auto")
		}
		return int(typed), nil
	case int:
		if typed <= 0 {
			return 0, fmt.Errorf("auto integer setting must be %q or a positive integer", "auto")
		}
		return typed, nil
	default:
		return 0, fmt.Errorf("auto integer setting must be %q or a positive integer", "auto")
	}
}

type fileConfig struct {
	Version      int                     `toml:"version"`
	Server       *fileServerConfig       `toml:"server"`
	Storage      *fileStorageConfig      `toml:"storage"`
	SQLite       *fileSQLiteConfig       `toml:"sqlite"`
	Debug        *fileDebugConfig        `toml:"debug"`
	Logging      *fileLoggingConfig      `toml:"logging"`
	Ingestion    *fileIngestionConfig    `toml:"ingestion"`
	Workspace    *fileWorkspaceConfig    `toml:"workspace"`
	Workflows    *fileWorkflowConfig     `toml:"workflows"`
	Automation   *fileAutomationConfig   `toml:"automation"`
	GitOps       *fileGitOpsConfig       `toml:"git_operations"`
	Verification *fileVerificationConfig `toml:"verification"`
	Projects     []fileProjectConfig     `toml:"projects"`
}

type fileServerConfig struct {
	HTTPAddr          *string `toml:"http_addr"`
	CPUCount          any     `toml:"cpu_count"`
	MaxRequestBytes   *int64  `toml:"max_request_bytes"`
	RequestTimeout    *string `toml:"request_timeout"`
	ReadHeaderTimeout *string `toml:"read_header_timeout"`
	ShutdownTimeout   *string `toml:"shutdown_timeout"`
}

type fileStorageConfig struct {
	LadybugPath *string `toml:"ladybug_path"`
	SQLitePath  *string `toml:"sqlite_path"`
}

type fileLoggingConfig struct {
	FileEnabled *bool   `toml:"file_enabled"`
	FilePath    *string `toml:"file_path"`
}

type fileDebugConfig struct {
	Enabled               *bool `toml:"enabled"`
	PprofEnabled          *bool `toml:"pprof_enabled"`
	ExpvarEnabled         *bool `toml:"expvar_enabled"`
	RuntimeMetricsEnabled *bool `toml:"runtime_metrics_enabled"`
}

type fileSQLiteConfig struct {
	WALEnabled               *bool   `toml:"wal_enabled"`
	BusyTimeout              *string `toml:"busy_timeout"`
	Synchronous              *string `toml:"synchronous"`
	CheckpointAfterIngestion *bool   `toml:"checkpoint_after_ingestion"`
}

type fileWorkspaceConfig struct {
	Enabled *bool `toml:"enabled"`
}

type fileWorkflowConfig struct {
	Enabled         *bool    `toml:"enabled"`
	DefinitionPaths []string `toml:"definition_paths"`
}

type fileAutomationConfig struct {
	Enabled                   *bool                       `toml:"enabled"`
	RunnerEnabled             *bool                       `toml:"runner_enabled"`
	RequireCodexWhenAvailable *bool                       `toml:"require_codex_when_available"`
	AllowManualRunner         *bool                       `toml:"allow_manual_runner"`
	RunnerExecution           *string                     `toml:"runner_execution"`
	QueueDepth                *int                        `toml:"queue_depth"`
	PollInterval              *string                     `toml:"poll_interval"`
	GlobalWorkerCount         *int                        `toml:"global_worker_count"`
	PerProjectWorkerLimit     *int                        `toml:"per_project_worker_limit"`
	PerAgentWorkerLimit       *int                        `toml:"per_agent_worker_limit"`
	MaxParallelTasks          *int                        `toml:"max_parallel_tasks"`
	DefaultMaxRuntime         *string                     `toml:"default_max_runtime"`
	CodexBinaryPath           *string                     `toml:"codex_binary_path"`
	Agents                    []fileAutomationAgentConfig `toml:"agents"`
	WorkPlanStatusTrigger     *fileWorkPlanStatusTrigger  `toml:"work_plan_status_trigger"`
}

type fileWorkPlanStatusTrigger struct {
	Enabled  *bool    `toml:"enabled"`
	Statuses []string `toml:"statuses"`
}

type fileAutomationAgentConfig struct {
	ID              string                        `toml:"id"`
	DisplayName     string                        `toml:"display_name"`
	Purpose         string                        `toml:"purpose"`
	Enabled         bool                          `toml:"enabled"`
	AllowedSkills   []string                      `toml:"allowed_skills"`
	AllowedTools    []string                      `toml:"allowed_tools"`
	AllowedCommands []fileAutomationCommandConfig `toml:"allowed_commands"`
	DeniedCommands  []string                      `toml:"denied_commands"`
	WorkspaceMode   string                        `toml:"workspace_mode"`
	NetworkPolicy   string                        `toml:"network_policy"`
	SecretPolicy    string                        `toml:"secret_policy"`
	LogPolicy       string                        `toml:"log_policy"`
	MaxRuntime      *string                       `toml:"max_runtime"`
	MaxRetries      int                           `toml:"max_retries"`
}

type fileAutomationCommandConfig struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

type fileGitOpsConfig struct {
	Enabled                      *bool                        `toml:"enabled"`
	CommitAfterTask              *bool                        `toml:"commit_after_task"`
	PushAfterTask                *bool                        `toml:"push_after_task"`
	DraftPRAfterPush             *bool                        `toml:"draft_pr_after_push"`
	RequireCleanBeforeTask       *bool                        `toml:"require_clean_before_task"`
	CleanupWorktreeAfterPlanDone *bool                        `toml:"cleanup_worktree_after_plan_done"`
	RemoteName                   *string                      `toml:"remote_name"`
	BranchPrefix                 *string                      `toml:"branch_prefix"`
	BranchNamePattern            *string                      `toml:"branch_name_pattern"`
	CommitAuthorName             *string                      `toml:"commit_author_name"`
	CommitAuthorEmailEnv         *string                      `toml:"commit_author_email_env"`
	CommitAuthorEmailFile        *string                      `toml:"commit_author_email_file"`
	SignCommits                  *bool                        `toml:"sign_commits"`
	SSHPrivateKeyPath            *string                      `toml:"ssh_private_key_path"`
	SSHPublicKeyPath             *string                      `toml:"ssh_public_key_path"`
	SSHKnownHostsPath            *string                      `toml:"ssh_known_hosts_path"`
	GitHubTokenEnv               *string                      `toml:"github_token_env"`
	GitHubTokenFile              *string                      `toml:"github_token_file"`
	GitHubCLIPath                *string                      `toml:"github_cli_path"`
	Conventions                  *fileGitOpsConventionsConfig `toml:"conventions"`
	DirtyScopeRecovery           *fileDirtyScopeRecovery      `toml:"dirty_scope_recovery"`
}

type fileDirtyScopeRecovery struct {
	AllowedSupportPathspecs []string `toml:"allowed_support_pathspecs"`
}

type fileGitOpsConventionsConfig struct {
	CommitType               *string `toml:"commit_type"`
	CommitScope              *string `toml:"commit_scope"`
	CommitSummaryTemplate    *string `toml:"commit_summary_template"`
	PullRequestTitleTemplate *string `toml:"pull_request_title_template"`
	WhatChangedTemplate      *string `toml:"what_changed_template"`
	HowVerifiedTemplate      *string `toml:"how_verified_template"`
	TestsTemplate            *string `toml:"tests_template"`
}

type fileVerificationConfig struct {
	BootstrapCommands  []string                            `toml:"bootstrap_commands"`
	AlwaysBeforePR     []string                            `toml:"always_before_pr"`
	AutofixCommands    []string                            `toml:"autofix_commands"`
	GeneratedArtifacts []fileGeneratedArtifactVerification `toml:"generated_artifacts"`
	Env                map[string]string                   `toml:"env"`
}

type fileGeneratedArtifactVerification struct {
	Paths            []string `toml:"paths"`
	Command          string   `toml:"command"`
	RequiredBeforePR bool     `toml:"required_before_pr"`
}

type fileIngestionConfig struct {
	ContentGraphEnabled      *bool   `toml:"content_graph_enabled"`
	LiveUpdatesEnabled       *bool   `toml:"live_updates_enabled"`
	ASTExtractionEnabled     *bool   `toml:"ast_extraction_enabled"`
	ExtractorCacheEnabled    *bool   `toml:"extractor_cache_enabled"`
	DebounceInterval         *string `toml:"debounce_interval"`
	MaxFileBytes             *int64  `toml:"max_file_bytes"`
	MaxChunkBytes            *int    `toml:"max_chunk_bytes"`
	QueueDepth               *int    `toml:"queue_depth"`
	WorkerCount              any     `toml:"worker_count"`
	GlobalWorkerCount        any     `toml:"global_worker_count"`
	PerProjectWorkerLimit    any     `toml:"per_project_worker_limit"`
	LivePathPriority         *bool   `toml:"live_path_priority"`
	MaxWatchedDirectoryCount *int    `toml:"max_watched_directory_count"`
	TaskWarnAfter            *string `toml:"task_warn_after"`
	FullScanBatchSize        *int    `toml:"full_scan_batch_size"`
	InitialScanOnStart       *bool   `toml:"initial_scan_on_start"`
	SensitiveMarkerPolicy    string  `toml:"sensitive_marker_policy"`
}

type fileProjectConfig struct {
	ID                    string                         `toml:"id"`
	Aliases               []string                       `toml:"aliases"`
	DisplayName           string                         `toml:"display_name"`
	Description           string                         `toml:"description"`
	RootPath              string                         `toml:"root_path"`
	Enabled               bool                           `toml:"enabled"`
	Classification        string                         `toml:"classification"`
	GraphNamespace        string                         `toml:"graph_namespace"`
	GraphStorage          string                         `toml:"graph_storage"`
	DigestMode            string                         `toml:"digest_mode"`
	UpdatePolicy          string                         `toml:"update_policy"`
	WorkspaceMode         string                         `toml:"workspace_mode"`
	Include               []string                       `toml:"include"`
	Exclude               []string                       `toml:"exclude"`
	FollowSymlinks        bool                           `toml:"follow_symlinks"`
	MaxFileBytes          *int64                         `toml:"max_file_bytes"`
	MaxChunkBytes         *int                           `toml:"max_chunk_bytes"`
	SensitiveMarkerPolicy string                         `toml:"sensitive_marker_policy"`
	Integrations          *fileProjectIntegrationsConfig `toml:"integrations"`
	GitOps                *fileGitOpsConfig              `toml:"git_operations"`
	Verification          *fileVerificationConfig        `toml:"verification"`
	WorkflowChains        []fileWorkflowChainConfig      `toml:"workflow_chains"`
}

type fileWorkflowChainConfig struct {
	ChainRef             string                   `toml:"chain_ref"`
	Enabled              bool                     `toml:"enabled"`
	InputKind            string                   `toml:"input_kind"`
	InputPattern         string                   `toml:"input_pattern"`
	ContextProvider      string                   `toml:"context_provider"`
	ContextMode          string                   `toml:"context_mode"`
	DefaultTitleTemplate string                   `toml:"default_title_template"`
	GitOpsMode           string                   `toml:"gitops_mode"`
	Stages               []fileWorkflowChainStage `toml:"stages"`
}

type fileWorkflowChainStage struct {
	StageRef                 string   `toml:"stage_ref"`
	WorkflowRef              string   `toml:"workflow_ref"`
	Trigger                  string   `toml:"trigger"`
	DependsOn                []string `toml:"depends_on"`
	AutomationRefTemplate    string   `toml:"automation_ref_template"`
	RequiredStatusBeforeNext string   `toml:"required_status_before_next"`
}

type fileProjectIntegrationsConfig struct {
	Jira       *fileJiraIntegrationConfig       `toml:"jira"`
	Confluence *fileConfluenceIntegrationConfig `toml:"confluence"`
}

type fileAtlassianIntegrationConfig struct {
	Enabled             bool    `toml:"enabled"`
	SiteURL             string  `toml:"site_url"`
	CloudID             string  `toml:"cloud_id"`
	AuthMode            string  `toml:"auth_mode"`
	EmailEnv            string  `toml:"email_env"`
	EmailFile           string  `toml:"email_file"`
	APITokenEnv         string  `toml:"api_token_env"`
	APITokenFile        string  `toml:"api_token_file"`
	CredentialsFile     string  `toml:"credentials_file"`
	ReadTimeout         *string `toml:"read_timeout"`
	MaxResults          *int    `toml:"max_results"`
	IngestionEnabled    bool    `toml:"ingestion_enabled"`
	InitialFullSync     string  `toml:"initial_full_sync"`
	IncrementalInterval *string `toml:"incremental_interval"`
	EmptyPollSleep      *string `toml:"empty_poll_sleep"`
	MaxIdleSleep        *string `toml:"max_idle_sleep"`
	OverlapWindow       *string `toml:"overlap_window"`
	InitialPageSize     *int    `toml:"initial_page_size"`
	IncrementalPageSize *int    `toml:"incremental_page_size"`
}

type fileJiraIntegrationConfig struct {
	fileAtlassianIntegrationConfig
	ProjectKeys       []string `toml:"project_keys"`
	DefaultFields     []string `toml:"default_fields"`
	AllowedFields     []string `toml:"allowed_fields"`
	IncludeRichFields bool     `toml:"include_rich_fields"`
	IncludeComments   bool     `toml:"include_comments"`
	JQLExtraFilter    string   `toml:"jql_extra_filter"`
}

type fileConfluenceIntegrationConfig struct {
	fileAtlassianIntegrationConfig
	SpaceKeys          []string `toml:"space_keys"`
	BodyRepresentation string   `toml:"body_representation"`
	IncludeBody        bool     `toml:"include_body"`
	IncludeComments    bool     `toml:"include_comments"`
	IncludeLabels      bool     `toml:"include_labels"`
	IncludeProperties  bool     `toml:"include_properties"`
	RootPageIDs        []string `toml:"root_page_ids"`
	CQLExtraFilter     string   `toml:"cql_extra_filter"`
}

func loadFileConfig(path string) (fileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileConfig{}, err
	}
	defer file.Close()

	var cfg fileConfig
	decoder := toml.NewDecoder(file).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, err
	}
	if err := cfg.validate(); err != nil {
		return fileConfig{}, err
	}
	return cfg, nil
}

func (cfg fileConfig) validate() error {
	if cfg.Version != configVersion {
		return fmt.Errorf("version must be %d", configVersion)
	}
	if cfg.Ingestion != nil {
		if err := cfg.Ingestion.validate(); err != nil {
			return err
		}
	}
	for i, project := range cfg.Projects {
		switch project.DigestMode {
		case "", digestModeMetadataOnly, digestModeContentGraph:
		default:
			return fmt.Errorf("projects[%d].digest_mode must be %q or %q", i, digestModeMetadataOnly, digestModeContentGraph)
		}
		switch project.UpdatePolicy {
		case "", updatePolicyManual, updatePolicyLive:
		default:
			return fmt.Errorf("projects[%d].update_policy must be %q or %q", i, updatePolicyManual, updatePolicyLive)
		}
		switch project.GraphStorage {
		case "", graphStoragePersistent, graphStorageInMemory:
		default:
			return fmt.Errorf("projects[%d].graph_storage must be %q or %q", i, graphStoragePersistent, graphStorageInMemory)
		}
		switch project.WorkspaceMode {
		case "", "disabled", "read_only", "edit":
		default:
			return fmt.Errorf("projects[%d].workspace_mode must be %q, %q, or %q", i, "disabled", "read_only", "edit")
		}
		if (project.WorkspaceMode == "read_only" || project.WorkspaceMode == "edit") && project.DigestMode != digestModeContentGraph {
			return fmt.Errorf("projects[%d].workspace_mode %q requires digest_mode %q", i, project.WorkspaceMode, digestModeContentGraph)
		}
		if project.MaxFileBytes != nil && *project.MaxFileBytes < 0 {
			return fmt.Errorf("projects[%d].max_file_bytes must be non-negative", i)
		}
		if project.MaxChunkBytes != nil && *project.MaxChunkBytes <= 0 {
			return fmt.Errorf("projects[%d].max_chunk_bytes must be positive", i)
		}
		if project.SensitiveMarkerPolicy != "" && project.SensitiveMarkerPolicy != sensitiveMarkerPolicySkipFile {
			return fmt.Errorf("projects[%d].sensitive_marker_policy must be %q", i, sensitiveMarkerPolicySkipFile)
		}
		if project.Integrations != nil {
			if err := project.Integrations.validate(i); err != nil {
				return err
			}
		}
		if err := project.validateWorkflowChains(i); err != nil {
			return err
		}
	}
	return nil
}

func (project fileProjectConfig) validateWorkflowChains(projectIndex int) error {
	for i, chain := range project.WorkflowChains {
		prefix := fmt.Sprintf("projects[%d].workflow_chains[%d]", projectIndex, i)
		if strings.TrimSpace(chain.ChainRef) == "" {
			return fmt.Errorf("%s.chain_ref must not be empty", prefix)
		}
		if strings.TrimSpace(chain.InputPattern) != "" {
			if len(strings.TrimSpace(chain.InputPattern)) > 128 {
				return fmt.Errorf("%s.input_pattern is too long", prefix)
			}
			if _, err := regexp.Compile(strings.TrimSpace(chain.InputPattern)); err != nil {
				return fmt.Errorf("%s.input_pattern must compile", prefix)
			}
		}
		if len(chain.Stages) == 0 {
			return fmt.Errorf("%s.stages must contain at least one stage", prefix)
		}
		seen := map[string]bool{}
		for j, stage := range chain.Stages {
			stagePrefix := fmt.Sprintf("%s.stages[%d]", prefix, j)
			if strings.TrimSpace(stage.StageRef) == "" {
				return fmt.Errorf("%s.stage_ref must not be empty", stagePrefix)
			}
			if strings.TrimSpace(stage.WorkflowRef) == "" {
				return fmt.Errorf("%s.workflow_ref must not be empty", stagePrefix)
			}
			if seen[stage.StageRef] {
				return fmt.Errorf("%s.stage_ref duplicates an earlier stage", stagePrefix)
			}
			seen[stage.StageRef] = true
		}
	}
	return nil
}

func (cfg fileProjectIntegrationsConfig) validate(projectIndex int) error {
	if cfg.Jira != nil {
		if err := cfg.Jira.validate(projectIndex); err != nil {
			return err
		}
	}
	if cfg.Confluence != nil {
		if err := cfg.Confluence.validate(projectIndex); err != nil {
			return err
		}
	}
	return nil
}

func (cfg fileJiraIntegrationConfig) validate(projectIndex int) error {
	prefix := fmt.Sprintf("projects[%d].integrations.jira", projectIndex)
	if err := cfg.fileAtlassianIntegrationConfig.validate(prefix); err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.ProjectKeys) == 0 {
		return fmt.Errorf("%s.project_keys must contain at least one key when enabled", prefix)
	}
	for _, key := range cfg.ProjectKeys {
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if !jiraProjectKeyPattern.MatchString(normalized) {
			return fmt.Errorf("%s.project_keys must contain uppercase alphanumeric keys starting with a letter", prefix)
		}
	}
	return nil
}

func (cfg fileConfluenceIntegrationConfig) validate(projectIndex int) error {
	prefix := fmt.Sprintf("projects[%d].integrations.confluence", projectIndex)
	if err := cfg.fileAtlassianIntegrationConfig.validate(prefix); err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.SpaceKeys) == 0 {
		return fmt.Errorf("%s.space_keys must contain at least one key when enabled", prefix)
	}
	for _, key := range cfg.SpaceKeys {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s.space_keys must not contain empty keys", prefix)
		}
	}
	return nil
}

func (cfg fileAtlassianIntegrationConfig) validate(prefix string) error {
	if strings.TrimSpace(cfg.AuthMode) != "" && strings.TrimSpace(cfg.AuthMode) != authModeAPITokenBasic {
		return fmt.Errorf("%s.auth_mode must be %q", prefix, authModeAPITokenBasic)
	}
	if strings.TrimSpace(cfg.InitialFullSync) != "" && strings.TrimSpace(cfg.InitialFullSync) != initialFullSyncManual && strings.TrimSpace(cfg.InitialFullSync) != initialFullSyncOnStart {
		return fmt.Errorf("%s.initial_full_sync must be %q or %q", prefix, initialFullSyncManual, initialFullSyncOnStart)
	}
	if err := validateOptionalDuration(prefix+".read_timeout", cfg.ReadTimeout, true); err != nil {
		return err
	}
	if err := validateOptionalDuration(prefix+".incremental_interval", cfg.IncrementalInterval, true); err != nil {
		return err
	}
	if err := validateOptionalDuration(prefix+".empty_poll_sleep", cfg.EmptyPollSleep, true); err != nil {
		return err
	}
	if err := validateOptionalDuration(prefix+".max_idle_sleep", cfg.MaxIdleSleep, true); err != nil {
		return err
	}
	if err := validateOptionalDuration(prefix+".overlap_window", cfg.OverlapWindow, false); err != nil {
		return err
	}
	if durationDefault(cfg.EmptyPollSleep, defaultIntegrationEmptyPollSleep) > durationDefault(cfg.MaxIdleSleep, defaultIntegrationMaxIdleSleep) {
		return fmt.Errorf("%s.empty_poll_sleep must be <= max_idle_sleep", prefix)
	}
	if cfg.InitialPageSize != nil && *cfg.InitialPageSize <= 0 {
		return fmt.Errorf("%s.initial_page_size must be positive", prefix)
	}
	if cfg.IncrementalPageSize != nil && *cfg.IncrementalPageSize <= 0 {
		return fmt.Errorf("%s.incremental_page_size must be positive", prefix)
	}
	if cfg.MaxResults != nil && *cfg.MaxResults < 0 {
		return fmt.Errorf("%s.max_results must be non-negative", prefix)
	}
	if cfg.Enabled {
		if err := validateAtlassianSiteURL(prefix+".site_url", cfg.SiteURL); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.AuthMode) == "" {
			return fmt.Errorf("%s.auth_mode must be %q when enabled", prefix, authModeAPITokenBasic)
		}
		if err := validateAtlassianCredentialRefs(prefix, cfg.EmailEnv, cfg.EmailFile, cfg.APITokenEnv, cfg.APITokenFile, cfg.CredentialsFile); err != nil {
			return err
		}
	}
	return nil
}

func validateOptionalDuration(name string, value *string, mustBePositive bool) error {
	if value == nil {
		return nil
	}
	parsed, err := time.ParseDuration(*value)
	if err != nil {
		return fmt.Errorf("%s must be a Go duration", name)
	}
	if mustBePositive && parsed <= 0 {
		return fmt.Errorf("%s must be positive", name)
	}
	if !mustBePositive && parsed < 0 {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}

func validateAtlassianSiteURL(name string, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an HTTPS Atlassian Cloud host", name)
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "api.atlassian.com" && !strings.HasSuffix(host, ".atlassian.net") {
		return fmt.Errorf("%s must be an HTTPS Atlassian Cloud host", name)
	}
	return nil
}

func validateAtlassianCredentialRefs(prefix, emailEnv, emailFile, apiTokenEnv, apiTokenFile, credentialsFile string) error {
	hasCredentialsFile := strings.TrimSpace(credentialsFile) != ""
	hasEmailRef := strings.TrimSpace(emailEnv) != "" || strings.TrimSpace(emailFile) != ""
	hasTokenRef := strings.TrimSpace(apiTokenEnv) != "" || strings.TrimSpace(apiTokenFile) != ""
	if hasCredentialsFile {
		if hasEmailRef || hasTokenRef {
			return fmt.Errorf("%s.credentials_file must not be combined with email or api token references", prefix)
		}
		return nil
	}
	if err := validateCredentialRefPair(prefix+".email", emailEnv, emailFile); err != nil {
		return err
	}
	if err := validateCredentialRefPair(prefix+".api_token", apiTokenEnv, apiTokenFile); err != nil {
		return err
	}
	return nil
}

func validateCredentialRefPair(name, envRef, fileRef string) error {
	hasEnv := strings.TrimSpace(envRef) != ""
	hasFile := strings.TrimSpace(fileRef) != ""
	if hasEnv == hasFile {
		return fmt.Errorf("%s must use exactly one env or file reference", name)
	}
	return nil
}

func (cfg fileIngestionConfig) validate() error {
	if cfg.MaxFileBytes != nil && *cfg.MaxFileBytes < 0 {
		return fmt.Errorf("ingestion.max_file_bytes must be non-negative")
	}
	if cfg.MaxChunkBytes != nil && *cfg.MaxChunkBytes <= 0 {
		return fmt.Errorf("ingestion.max_chunk_bytes must be positive")
	}
	if cfg.QueueDepth != nil && *cfg.QueueDepth <= 0 {
		return fmt.Errorf("ingestion.queue_depth must be positive")
	}
	if cfg.MaxWatchedDirectoryCount != nil && *cfg.MaxWatchedDirectoryCount < 0 {
		return fmt.Errorf("ingestion.max_watched_directory_count must be non-negative")
	}
	if cfg.TaskWarnAfter != nil {
		if _, err := time.ParseDuration(*cfg.TaskWarnAfter); err != nil {
			return fmt.Errorf("ingestion.task_warn_after must be a valid duration")
		}
	}
	if cfg.FullScanBatchSize != nil && *cfg.FullScanBatchSize <= 0 {
		return fmt.Errorf("ingestion.full_scan_batch_size must be positive")
	}
	if cfg.SensitiveMarkerPolicy != "" && cfg.SensitiveMarkerPolicy != sensitiveMarkerPolicySkipFile {
		return fmt.Errorf("ingestion.sensitive_marker_policy must be %q", sensitiveMarkerPolicySkipFile)
	}
	return nil
}

func (cfg fileConfig) applyTo(base Config) (Config, error) {
	if cfg.Server != nil {
		if cfg.Server.HTTPAddr != nil {
			base.HTTPAddr = *cfg.Server.HTTPAddr
		}
		if cfg.Server.CPUCount != nil {
			value, err := parseAutoIntValue(cfg.Server.CPUCount)
			if err != nil {
				return Config{}, fmt.Errorf("server.cpu_count: %w", err)
			}
			base.CPUCount = value
		}
		if cfg.Server.MaxRequestBytes != nil {
			base.MaxRequestBytes = *cfg.Server.MaxRequestBytes
		}
		var err error
		if base.RequestTimeout, err = applyDuration("server.request_timeout", cfg.Server.RequestTimeout, base.RequestTimeout); err != nil {
			return Config{}, err
		}
		if base.ReadHeaderTimeout, err = applyDuration("server.read_header_timeout", cfg.Server.ReadHeaderTimeout, base.ReadHeaderTimeout); err != nil {
			return Config{}, err
		}
		if base.ShutdownTimeout, err = applyDuration("server.shutdown_timeout", cfg.Server.ShutdownTimeout, base.ShutdownTimeout); err != nil {
			return Config{}, err
		}
	}

	if cfg.Storage != nil {
		if cfg.Storage.LadybugPath != nil {
			base.LadybugPath = *cfg.Storage.LadybugPath
		}
		if cfg.Storage.SQLitePath != nil {
			base.SQLitePath = *cfg.Storage.SQLitePath
		}
	}

	if cfg.SQLite != nil {
		if cfg.SQLite.WALEnabled != nil {
			base.SQLite.WALEnabled = *cfg.SQLite.WALEnabled
		}
		if cfg.SQLite.BusyTimeout != nil {
			var err error
			base.SQLite.BusyTimeout, err = applyDuration("sqlite.busy_timeout", cfg.SQLite.BusyTimeout, base.SQLite.BusyTimeout)
			if err != nil {
				return Config{}, err
			}
		}
		if cfg.SQLite.Synchronous != nil {
			base.SQLite.Synchronous = strings.ToUpper(strings.TrimSpace(*cfg.SQLite.Synchronous))
		}
		if cfg.SQLite.CheckpointAfterIngestion != nil {
			base.SQLite.CheckpointAfterIngestion = *cfg.SQLite.CheckpointAfterIngestion
		}
	}

	if cfg.Debug != nil {
		if cfg.Debug.Enabled != nil {
			base.Debug.Enabled = *cfg.Debug.Enabled
		}
		if cfg.Debug.PprofEnabled != nil {
			base.Debug.PprofEnabled = *cfg.Debug.PprofEnabled
		}
		if cfg.Debug.ExpvarEnabled != nil {
			base.Debug.ExpvarEnabled = *cfg.Debug.ExpvarEnabled
		}
		if cfg.Debug.RuntimeMetricsEnabled != nil {
			base.Debug.RuntimeMetricsEnabled = *cfg.Debug.RuntimeMetricsEnabled
		}
	}

	if cfg.Logging != nil {
		if cfg.Logging.FileEnabled != nil {
			base.Logging.FileEnabled = *cfg.Logging.FileEnabled
		}
		if cfg.Logging.FilePath != nil {
			base.Logging.FilePath = *cfg.Logging.FilePath
		}
	}

	if cfg.Ingestion != nil {
		if cfg.Ingestion.ContentGraphEnabled != nil {
			base.Ingestion.ContentGraphEnabled = *cfg.Ingestion.ContentGraphEnabled
		}
		if cfg.Ingestion.LiveUpdatesEnabled != nil {
			base.Ingestion.LiveUpdatesEnabled = *cfg.Ingestion.LiveUpdatesEnabled
		}
		if cfg.Ingestion.ASTExtractionEnabled != nil {
			base.Ingestion.ASTExtractionEnabled = *cfg.Ingestion.ASTExtractionEnabled
		}
		if cfg.Ingestion.ExtractorCacheEnabled != nil {
			base.Ingestion.ExtractorCacheEnabled = *cfg.Ingestion.ExtractorCacheEnabled
		}
		var err error
		if base.Ingestion.DebounceInterval, err = applyDuration("ingestion.debounce_interval", cfg.Ingestion.DebounceInterval, base.Ingestion.DebounceInterval); err != nil {
			return Config{}, err
		}
		if cfg.Ingestion.MaxFileBytes != nil {
			base.Ingestion.MaxFileBytes = *cfg.Ingestion.MaxFileBytes
		}
		if cfg.Ingestion.MaxChunkBytes != nil {
			base.Ingestion.MaxChunkBytes = *cfg.Ingestion.MaxChunkBytes
		}
		if cfg.Ingestion.QueueDepth != nil {
			base.Ingestion.QueueDepth = *cfg.Ingestion.QueueDepth
		}
		if cfg.Ingestion.WorkerCount != nil {
			value, err := parseAutoIntValue(cfg.Ingestion.WorkerCount)
			if err != nil {
				return Config{}, fmt.Errorf("ingestion.worker_count: %w", err)
			}
			base.Ingestion.WorkerCount = value
		}
		if cfg.Ingestion.GlobalWorkerCount != nil {
			value, err := parseAutoIntValue(cfg.Ingestion.GlobalWorkerCount)
			if err != nil {
				return Config{}, fmt.Errorf("ingestion.global_worker_count: %w", err)
			}
			base.Ingestion.GlobalWorkerCount = value
		} else if cfg.Ingestion.WorkerCount != nil {
			value, err := parseAutoIntValue(cfg.Ingestion.WorkerCount)
			if err != nil {
				return Config{}, fmt.Errorf("ingestion.worker_count: %w", err)
			}
			base.Ingestion.GlobalWorkerCount = value
		}
		if cfg.Ingestion.PerProjectWorkerLimit != nil {
			value, err := parseAutoIntValue(cfg.Ingestion.PerProjectWorkerLimit)
			if err != nil {
				return Config{}, fmt.Errorf("ingestion.per_project_worker_limit: %w", err)
			}
			base.Ingestion.PerProjectWorkerLimit = value
		}
		if cfg.Ingestion.LivePathPriority != nil {
			base.Ingestion.LivePathPriority = *cfg.Ingestion.LivePathPriority
		}
		if cfg.Ingestion.MaxWatchedDirectoryCount != nil {
			base.Ingestion.MaxWatchedDirectoryCount = *cfg.Ingestion.MaxWatchedDirectoryCount
		}
		if base.Ingestion.TaskWarnAfter, err = applyDuration("ingestion.task_warn_after", cfg.Ingestion.TaskWarnAfter, base.Ingestion.TaskWarnAfter); err != nil {
			return Config{}, err
		}
		if cfg.Ingestion.FullScanBatchSize != nil {
			base.Ingestion.FullScanBatchSize = *cfg.Ingestion.FullScanBatchSize
		}
		if cfg.Ingestion.InitialScanOnStart != nil {
			base.Ingestion.InitialScanOnStart = *cfg.Ingestion.InitialScanOnStart
		}
		if cfg.Ingestion.SensitiveMarkerPolicy != "" {
			base.Ingestion.SensitiveMarkerPolicy = cfg.Ingestion.SensitiveMarkerPolicy
		}
	}

	if cfg.Workspace != nil && cfg.Workspace.Enabled != nil {
		base.Workspace.Enabled = *cfg.Workspace.Enabled
	}

	if cfg.Workflows != nil {
		if cfg.Workflows.Enabled != nil {
			base.Workflows.Enabled = *cfg.Workflows.Enabled
		}
		if cfg.Workflows.DefinitionPaths != nil {
			base.Workflows.DefinitionPaths = append([]string(nil), cfg.Workflows.DefinitionPaths...)
		}
	}

	if cfg.Automation != nil {
		if cfg.Automation.Enabled != nil {
			base.Automation.Enabled = *cfg.Automation.Enabled
		}
		if cfg.Automation.RunnerEnabled != nil {
			base.Automation.RunnerEnabled = *cfg.Automation.RunnerEnabled
		}
		if cfg.Automation.RequireCodexWhenAvailable != nil {
			base.Automation.RequireCodexWhenAvailable = *cfg.Automation.RequireCodexWhenAvailable
		}
		if cfg.Automation.AllowManualRunner != nil {
			base.Automation.AllowManualRunner = *cfg.Automation.AllowManualRunner
		}
		if cfg.Automation.RunnerExecution != nil {
			base.Automation.RunnerExecution = *cfg.Automation.RunnerExecution
		}
		if cfg.Automation.QueueDepth != nil {
			base.Automation.QueueDepth = *cfg.Automation.QueueDepth
		}
		var err error
		if base.Automation.PollInterval, err = applyDuration("automation.poll_interval", cfg.Automation.PollInterval, base.Automation.PollInterval); err != nil {
			return Config{}, err
		}
		if cfg.Automation.GlobalWorkerCount != nil {
			base.Automation.GlobalWorkerCount = *cfg.Automation.GlobalWorkerCount
		}
		if cfg.Automation.PerProjectWorkerLimit != nil {
			base.Automation.PerProjectWorkerLimit = *cfg.Automation.PerProjectWorkerLimit
		}
		if cfg.Automation.PerAgentWorkerLimit != nil {
			base.Automation.PerAgentWorkerLimit = *cfg.Automation.PerAgentWorkerLimit
		}
		if cfg.Automation.MaxParallelTasks != nil {
			base.Automation.MaxParallelTasks = *cfg.Automation.MaxParallelTasks
		}
		if base.Automation.DefaultMaxRuntime, err = applyDuration("automation.default_max_runtime", cfg.Automation.DefaultMaxRuntime, base.Automation.DefaultMaxRuntime); err != nil {
			return Config{}, err
		}
		if cfg.Automation.CodexBinaryPath != nil {
			base.Automation.CodexBinaryPath = *cfg.Automation.CodexBinaryPath
		}
		if cfg.Automation.WorkPlanStatusTrigger != nil {
			if cfg.Automation.WorkPlanStatusTrigger.Enabled != nil {
				base.Automation.WorkPlanStatusTrigger.Enabled = *cfg.Automation.WorkPlanStatusTrigger.Enabled
			}
			if cfg.Automation.WorkPlanStatusTrigger.Statuses != nil {
				base.Automation.WorkPlanStatusTrigger.Statuses = append([]string(nil), cfg.Automation.WorkPlanStatusTrigger.Statuses...)
			}
		}
		base.Automation.Agents = make([]AutomationAgent, 0, len(cfg.Automation.Agents))
		for _, agent := range cfg.Automation.Agents {
			converted, err := agent.toAutomationAgent(base.Automation.DefaultMaxRuntime)
			if err != nil {
				return Config{}, err
			}
			base.Automation.Agents = append(base.Automation.Agents, converted)
		}
	}

	if cfg.GitOps != nil {
		applyFileGitOps(&base.GitOperations, cfg.GitOps)
	}
	if cfg.Verification != nil {
		base.Verification = cfg.Verification.toVerification()
	}

	base.Projects = make([]Project, 0, len(cfg.Projects))
	for _, project := range cfg.Projects {
		base.Projects = append(base.Projects, project.toProject(base.GitOperations, base.Verification))
	}
	return base, nil
}

func applyFileGitOps(base *GitOperations, cfg *fileGitOpsConfig) {
	if cfg.Enabled != nil {
		base.Enabled = *cfg.Enabled
	}
	if cfg.CommitAfterTask != nil {
		base.CommitAfterTask = *cfg.CommitAfterTask
	}
	if cfg.PushAfterTask != nil {
		base.PushAfterTask = *cfg.PushAfterTask
	}
	if cfg.DraftPRAfterPush != nil {
		base.DraftPRAfterPush = *cfg.DraftPRAfterPush
	}
	if cfg.RequireCleanBeforeTask != nil {
		base.RequireCleanBeforeTask = *cfg.RequireCleanBeforeTask
	}
	if cfg.CleanupWorktreeAfterPlanDone != nil {
		base.CleanupWorktreeAfterPlanDone = *cfg.CleanupWorktreeAfterPlanDone
	}
	if cfg.RemoteName != nil {
		base.RemoteName = strings.TrimSpace(*cfg.RemoteName)
	}
	if cfg.BranchPrefix != nil {
		base.BranchPrefix = strings.TrimSpace(*cfg.BranchPrefix)
	}
	if cfg.BranchNamePattern != nil {
		base.BranchNamePattern = strings.TrimSpace(*cfg.BranchNamePattern)
	}
	if cfg.CommitAuthorName != nil {
		base.CommitAuthorName = strings.TrimSpace(*cfg.CommitAuthorName)
	}
	if cfg.CommitAuthorEmailEnv != nil {
		base.CommitAuthorEmailEnv = strings.TrimSpace(*cfg.CommitAuthorEmailEnv)
	}
	if cfg.CommitAuthorEmailFile != nil {
		base.CommitAuthorEmailFile = strings.TrimSpace(*cfg.CommitAuthorEmailFile)
	}
	if cfg.SignCommits != nil {
		base.SignCommits = *cfg.SignCommits
	}
	if cfg.SSHPrivateKeyPath != nil {
		base.SSHPrivateKeyPath = strings.TrimSpace(*cfg.SSHPrivateKeyPath)
	}
	if cfg.SSHPublicKeyPath != nil {
		base.SSHPublicKeyPath = strings.TrimSpace(*cfg.SSHPublicKeyPath)
	}
	if cfg.SSHKnownHostsPath != nil {
		base.SSHKnownHostsPath = strings.TrimSpace(*cfg.SSHKnownHostsPath)
	}
	if cfg.GitHubTokenEnv != nil {
		base.GitHubTokenEnv = strings.TrimSpace(*cfg.GitHubTokenEnv)
	}
	if cfg.GitHubTokenFile != nil {
		base.GitHubTokenFile = strings.TrimSpace(*cfg.GitHubTokenFile)
	}
	if cfg.GitHubCLIPath != nil {
		base.GitHubCLIPath = strings.TrimSpace(*cfg.GitHubCLIPath)
	}
	if cfg.DirtyScopeRecovery != nil {
		base.DirtyScopeRecovery.AllowedSupportPathspecs = trimStringSlice(cfg.DirtyScopeRecovery.AllowedSupportPathspecs)
	}
	if cfg.Conventions != nil {
		if cfg.Conventions.CommitType != nil {
			base.Conventions.CommitType = strings.TrimSpace(*cfg.Conventions.CommitType)
		}
		if cfg.Conventions.CommitScope != nil {
			base.Conventions.CommitScope = strings.TrimSpace(*cfg.Conventions.CommitScope)
		}
		if cfg.Conventions.CommitSummaryTemplate != nil {
			base.Conventions.CommitSummaryTemplate = strings.TrimSpace(*cfg.Conventions.CommitSummaryTemplate)
		}
		if cfg.Conventions.PullRequestTitleTemplate != nil {
			base.Conventions.PullRequestTitleTemplate = strings.TrimSpace(*cfg.Conventions.PullRequestTitleTemplate)
		}
		if cfg.Conventions.WhatChangedTemplate != nil {
			base.Conventions.WhatChangedTemplate = strings.TrimSpace(*cfg.Conventions.WhatChangedTemplate)
		}
		if cfg.Conventions.HowVerifiedTemplate != nil {
			base.Conventions.HowVerifiedTemplate = strings.TrimSpace(*cfg.Conventions.HowVerifiedTemplate)
		}
		if cfg.Conventions.TestsTemplate != nil {
			base.Conventions.TestsTemplate = strings.TrimSpace(*cfg.Conventions.TestsTemplate)
		}
	}
}

func (cfg fileVerificationConfig) toVerification() Verification {
	generated := make([]GeneratedArtifactVerification, 0, len(cfg.GeneratedArtifacts))
	for _, item := range cfg.GeneratedArtifacts {
		generated = append(generated, GeneratedArtifactVerification{
			Paths:            trimStringSlice(item.Paths),
			Command:          strings.TrimSpace(item.Command),
			RequiredBeforePR: item.RequiredBeforePR,
		})
	}
	return Verification{
		BootstrapCommands:  trimStringSlice(cfg.BootstrapCommands),
		AlwaysBeforePR:     trimStringSlice(cfg.AlwaysBeforePR),
		AutofixCommands:    trimStringSlice(cfg.AutofixCommands),
		GeneratedArtifacts: generated,
		Env:                trimStringMap(cfg.Env),
	}
}

func mergeVerification(base Verification, override *fileVerificationConfig) Verification {
	if override == nil {
		return base
	}
	return override.toVerification()
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func trimStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		out[trimmedKey] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (cfg fileAutomationAgentConfig) toAutomationAgent(defaultMaxRuntime time.Duration) (AutomationAgent, error) {
	maxRuntime, err := applyDuration("automation.agents.max_runtime", cfg.MaxRuntime, defaultMaxRuntime)
	if err != nil {
		return AutomationAgent{}, err
	}
	commands := make([]AutomationCommand, 0, len(cfg.AllowedCommands))
	for _, command := range cfg.AllowedCommands {
		commands = append(commands, AutomationCommand{Command: command.Command, Args: append([]string(nil), command.Args...)})
	}
	return AutomationAgent{
		ID:              cfg.ID,
		DisplayName:     cfg.DisplayName,
		Purpose:         cfg.Purpose,
		Enabled:         cfg.Enabled,
		AllowedSkills:   append([]string(nil), cfg.AllowedSkills...),
		AllowedTools:    append([]string(nil), cfg.AllowedTools...),
		AllowedCommands: commands,
		DeniedCommands:  append([]string(nil), cfg.DeniedCommands...),
		WorkspaceMode:   cfg.WorkspaceMode,
		NetworkPolicy:   cfg.NetworkPolicy,
		SecretPolicy:    cfg.SecretPolicy,
		LogPolicy:       cfg.LogPolicy,
		MaxRuntime:      maxRuntime,
		MaxRetries:      cfg.MaxRetries,
	}, nil
}

func applyDuration(name string, value *string, fallback time.Duration) (time.Duration, error) {
	if value == nil {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(*value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	return parsed, nil
}

func (project fileProjectConfig) toProject(globalGitOps GitOperations, globalVerification Verification) Project {
	digestMode := project.DigestMode
	if digestMode == "" {
		digestMode = digestModeMetadataOnly
	}
	updatePolicy := project.UpdatePolicy
	if updatePolicy == "" {
		updatePolicy = updatePolicyManual
	}
	graphStorage := project.GraphStorage
	if graphStorage == "" {
		graphStorage = graphStoragePersistent
	}
	workspaceMode := project.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = "disabled"
	}

	cfgProject := Project{
		ID:             project.ID,
		Aliases:        append([]string(nil), project.Aliases...),
		DisplayName:    project.DisplayName,
		Description:    project.Description,
		RootPath:       project.RootPath,
		Enabled:        project.Enabled,
		Classification: project.Classification,
		GraphNamespace: project.GraphNamespace,
		GraphStorage:   graphStorage,
		DigestMode:     digestMode,
		UpdatePolicy:   updatePolicy,
		WorkspaceMode:  workspaceMode,
		Include:        append([]string(nil), project.Include...),
		Exclude:        append([]string(nil), project.Exclude...),
		FollowSymlinks: project.FollowSymlinks,
	}
	if project.MaxFileBytes != nil {
		cfgProject.MaxFileBytes = *project.MaxFileBytes
	}
	if project.MaxChunkBytes != nil {
		cfgProject.MaxChunkBytes = *project.MaxChunkBytes
	}
	cfgProject.SensitiveMarkerPolicy = project.SensitiveMarkerPolicy
	if project.Integrations != nil {
		cfgProject.Integrations = project.Integrations.toIntegrationConfig()
	}
	if project.GitOps != nil {
		gitops := globalGitOps
		applyFileGitOps(&gitops, project.GitOps)
		cfgProject.GitOperations = &gitops
	}
	if project.Verification != nil {
		verification := mergeVerification(globalVerification, project.Verification)
		cfgProject.Verification = &verification
	}
	cfgProject.WorkflowChains = workflowChainsFromFile(project.WorkflowChains)
	return cfgProject
}

func workflowChainsFromFile(chains []fileWorkflowChainConfig) []WorkflowChain {
	out := make([]WorkflowChain, 0, len(chains))
	for _, chain := range chains {
		converted := WorkflowChain{
			ChainRef:             strings.TrimSpace(chain.ChainRef),
			Enabled:              chain.Enabled,
			InputKind:            strings.TrimSpace(chain.InputKind),
			InputPattern:         strings.TrimSpace(chain.InputPattern),
			ContextProvider:      strings.TrimSpace(chain.ContextProvider),
			ContextMode:          strings.TrimSpace(chain.ContextMode),
			DefaultTitleTemplate: strings.TrimSpace(chain.DefaultTitleTemplate),
			GitOpsMode:           strings.TrimSpace(chain.GitOpsMode),
			Stages:               make([]WorkflowChainStage, 0, len(chain.Stages)),
		}
		for _, stage := range chain.Stages {
			converted.Stages = append(converted.Stages, WorkflowChainStage{
				StageRef:                 strings.TrimSpace(stage.StageRef),
				WorkflowRef:              strings.TrimSpace(stage.WorkflowRef),
				Trigger:                  strings.TrimSpace(stage.Trigger),
				DependsOn:                trimStringSlice(stage.DependsOn),
				AutomationRefTemplate:    strings.TrimSpace(stage.AutomationRefTemplate),
				RequiredStatusBeforeNext: strings.TrimSpace(stage.RequiredStatusBeforeNext),
			})
		}
		out = append(out, converted)
	}
	return out
}

func (cfg fileProjectIntegrationsConfig) toIntegrationConfig() IntegrationConfig {
	integrations := IntegrationConfig{}
	if cfg.Jira != nil {
		jira := cfg.Jira.toJiraIntegration()
		integrations.Jira = &jira
	}
	if cfg.Confluence != nil {
		confluence := cfg.Confluence.toConfluenceIntegration()
		integrations.Confluence = &confluence
	}
	return integrations
}

func (cfg fileJiraIntegrationConfig) toJiraIntegration() JiraIntegration {
	return JiraIntegration{
		Enabled:           cfg.Enabled,
		SiteURL:           strings.TrimSpace(cfg.SiteURL),
		CloudID:           strings.TrimSpace(cfg.CloudID),
		AuthMode:          defaultString(strings.TrimSpace(cfg.AuthMode), authModeAPITokenBasic),
		CredentialRefs:    cfg.toCredentialRefs(),
		ReadTimeout:       cfg.readTimeout(),
		MaxResults:        intDefault(cfg.MaxResults, defaultIntegrationMaxResults),
		Polling:           cfg.toPolling(defaultIntegrationPageSize),
		ProjectKeys:       normalizeJiraProjectKeys(cfg.ProjectKeys),
		DefaultFields:     trimStrings(cfg.DefaultFields),
		AllowedFields:     trimStrings(cfg.AllowedFields),
		IncludeRichFields: cfg.IncludeRichFields,
		IncludeComments:   cfg.IncludeComments,
		JQLExtraFilter:    strings.TrimSpace(cfg.JQLExtraFilter),
	}
}

func (cfg fileConfluenceIntegrationConfig) toConfluenceIntegration() ConfluenceIntegration {
	return ConfluenceIntegration{
		Enabled:            cfg.Enabled,
		SiteURL:            strings.TrimSpace(cfg.SiteURL),
		CloudID:            strings.TrimSpace(cfg.CloudID),
		AuthMode:           defaultString(strings.TrimSpace(cfg.AuthMode), authModeAPITokenBasic),
		CredentialRefs:     cfg.toCredentialRefs(),
		ReadTimeout:        cfg.readTimeout(),
		MaxResults:         intDefault(cfg.MaxResults, defaultIntegrationMaxResults),
		Polling:            cfg.toPolling(defaultConfluencePageSize),
		SpaceKeys:          trimStrings(cfg.SpaceKeys),
		BodyRepresentation: defaultString(strings.TrimSpace(cfg.BodyRepresentation), "storage"),
		IncludeBody:        cfg.IncludeBody,
		IncludeComments:    cfg.IncludeComments,
		IncludeLabels:      cfg.IncludeLabels,
		IncludeProperties:  cfg.IncludeProperties,
		RootPageIDs:        trimStrings(cfg.RootPageIDs),
		CQLExtraFilter:     strings.TrimSpace(cfg.CQLExtraFilter),
	}
}

func (cfg fileAtlassianIntegrationConfig) toCredentialRefs() AtlassianCredentialRefs {
	return AtlassianCredentialRefs{
		EmailEnv:        strings.TrimSpace(cfg.EmailEnv),
		EmailFile:       strings.TrimSpace(cfg.EmailFile),
		APITokenEnv:     strings.TrimSpace(cfg.APITokenEnv),
		APITokenFile:    strings.TrimSpace(cfg.APITokenFile),
		CredentialsFile: strings.TrimSpace(cfg.CredentialsFile),
	}
}

func (cfg fileAtlassianIntegrationConfig) toPolling(defaultPageSize int) IntegrationPolling {
	return IntegrationPolling{
		IngestionEnabled:    cfg.IngestionEnabled,
		InitialFullSync:     defaultString(strings.TrimSpace(cfg.InitialFullSync), initialFullSyncManual),
		IncrementalInterval: durationDefault(cfg.IncrementalInterval, defaultIntegrationIncremental),
		EmptyPollSleep:      durationDefault(cfg.EmptyPollSleep, defaultIntegrationEmptyPollSleep),
		MaxIdleSleep:        durationDefault(cfg.MaxIdleSleep, defaultIntegrationMaxIdleSleep),
		OverlapWindow:       durationDefault(cfg.OverlapWindow, defaultIntegrationOverlapWindow),
		InitialPageSize:     intDefault(cfg.InitialPageSize, defaultPageSize),
		IncrementalPageSize: intDefault(cfg.IncrementalPageSize, defaultPageSize),
	}
}

func (cfg fileAtlassianIntegrationConfig) readTimeout() time.Duration {
	return durationDefault(cfg.ReadTimeout, defaultIntegrationReadTimeout)
}

func durationDefault(value *string, fallback time.Duration) time.Duration {
	if value == nil {
		return fallback
	}
	parsed, err := time.ParseDuration(*value)
	if err != nil {
		return fallback
	}
	return parsed
}

func intDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func normalizeJiraProjectKeys(keys []string) []string {
	normalized := make([]string, 0, len(keys))
	for _, key := range keys {
		normalized = append(normalized, strings.ToUpper(strings.TrimSpace(key)))
	}
	return normalized
}

func trimStrings(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		trimmed = append(trimmed, strings.TrimSpace(value))
	}
	return trimmed
}
