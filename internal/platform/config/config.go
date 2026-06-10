package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultConfigPath                 = "configs/mivia-server.local.toml"
	defaultHTTPAddr                   = "127.0.0.1:8080"
	defaultLadybugPath                = "data/mivialabs.lbug"
	defaultSQLitePath                 = "data/mivialabs-config.sqlite"
	defaultDurableWorkflowSQLitePath  = "data/durable-workflows.sqlite"
	defaultMaxRequestBytes            = int64(1 << 20)
	defaultRequestTimeout             = 10 * time.Second
	defaultReadHeaderTimeout          = 5 * time.Second
	defaultShutdownTimeout            = 10 * time.Second
	defaultIngestionDebounceInterval  = 2 * time.Second
	defaultIngestionMaxChunkBytes     = 16 * 1024
	defaultIngestionQueueDepth        = 10000
	defaultIngestionWorkerCount       = 4
	defaultIngestionGlobalWorkerCount = 4
	defaultIngestionPerProjectLimit   = 2
	defaultIngestionFullScanBatchSize = 500
	defaultIngestionTaskWarnAfter     = 30 * time.Second
	defaultIntegrationIncremental     = time.Minute
	defaultIntegrationEmptyPollSleep  = 10 * time.Minute
	defaultIntegrationMaxIdleSleep    = 30 * time.Minute
	defaultIntegrationOverlapWindow   = 2 * time.Minute
	defaultIntegrationReadTimeout     = 30 * time.Second
	defaultIntegrationPageSize        = 100
	defaultConfluencePageSize         = 50
	defaultIntegrationMaxResults      = 0
	defaultAutomationPollInterval     = 5 * time.Second
	defaultSQLiteBusyTimeout          = 5 * time.Second
	defaultSQLiteSynchronous          = "NORMAL"
	defaultSensitiveMarkerPolicy      = "skip_file"
	sensitiveMarkerPolicySkipFile     = "skip_file"
	durableWorkflowBackendMemory      = "memory"
	durableWorkflowBackendSQLite      = "sqlite"
)

var ErrNonLoopbackBind = errors.New("non_loopback_bind_attempt")

type Config struct {
	ConfigPath        string
	CPUCount          int
	HTTPAddr          string
	LadybugPath       string
	SQLitePath        string
	SQLite            SQLite
	Debug             Debug
	Logging           Logging
	MaxRequestBytes   int64
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
	Ingestion         Ingestion
	Workspace         Workspace
	Workflows         Workflows
	DurableWorkflows  DurableWorkflows
	AgentActivity     AgentActivity
	Automation        Automation
	GitOperations     GitOperations
	Verification      Verification
	Projects          []Project
}

type Logging struct {
	FileEnabled bool
	FilePath    string
}

type Debug struct {
	Enabled               bool
	PprofEnabled          bool
	ExpvarEnabled         bool
	RuntimeMetricsEnabled bool
}

type SQLite struct {
	WALEnabled               bool
	BusyTimeout              time.Duration
	Synchronous              string
	CheckpointAfterIngestion bool
}

type Workspace struct {
	Enabled bool
}

type Workflows struct {
	Enabled         bool
	DefinitionPaths []string
}

type DurableWorkflows struct {
	Enabled         bool
	ShadowMode      bool
	Backend         string
	SQLitePath      string
	WorkerEnabled   bool
	MaxParallelRuns int
}

type AgentActivity struct {
	RetainRawPayloads bool
}

type Automation struct {
	Enabled                   bool
	RunnerEnabled             bool
	RequireCodexWhenAvailable bool
	AllowManualRunner         bool
	RunnerExecution           string
	QueueDepth                int
	PollInterval              time.Duration
	GlobalWorkerCount         int
	PerProjectWorkerLimit     int
	PerAgentWorkerLimit       int
	MaxParallelTasks          int
	DefaultMaxRuntime         time.Duration
	CodexBinaryPath           string
	Agents                    []AutomationAgent
	WorkPlanStatusTrigger     WorkPlanStatusTrigger
}

type GitOperations struct {
	Enabled                      bool
	CommitAfterTask              bool
	PushAfterTask                bool
	DraftPRAfterPush             bool
	RequireCleanBeforeTask       bool
	CleanupWorktreeAfterPlanDone bool
	RemoteName                   string
	BranchPrefix                 string
	BranchNamePattern            string
	CommitAuthorName             string
	CommitAuthorEmailEnv         string
	CommitAuthorEmailFile        string
	SignCommits                  bool
	SSHPrivateKeyPath            string
	SSHPublicKeyPath             string
	SSHKnownHostsPath            string
	GitHubTokenEnv               string
	GitHubTokenFile              string
	GitHubCLIPath                string
	Conventions                  GitOpsConventions
	DirtyScopeRecovery           DirtyScopeRecovery
	PostPRChecks                 PostPRChecks
}

type DirtyScopeRecovery struct {
	AllowedSupportPathspecs []string
}

type PostPRChecks struct {
	Enabled         bool
	RequiredOnly    bool
	Watch           bool
	FailFast        bool
	IntervalSeconds int
}

type GitOpsConventions struct {
	CommitType               string
	CommitScope              string
	BranchTemplate           string
	RequireTicketRef         bool
	TicketRefPattern         string
	TicketURLTemplate        string
	AllowedTypes             []string
	DefaultChangeType        string
	CommitSummaryTemplate    string
	PullRequestTitleTemplate string
	PullRequestBodyTemplate  string
	WhatChangedTemplate      string
	HowVerifiedTemplate      string
	TestsTemplate            string
}

type Verification struct {
	BootstrapCommands  []string
	AlwaysBeforePR     []string
	AutofixCommands    []string
	GeneratedArtifacts []GeneratedArtifactVerification
	Env                map[string]string
}

type GeneratedArtifactVerification struct {
	Paths            []string
	Command          string
	RequiredBeforePR bool
}

type WorkPlanStatusTrigger struct {
	Enabled  bool
	Statuses []string
}

type AutomationAgent struct {
	ID              string
	DisplayName     string
	Purpose         string
	Enabled         bool
	AllowedSkills   []string
	AllowedTools    []string
	AllowedCommands []AutomationCommand
	DeniedCommands  []string
	WorkspaceMode   string
	NetworkPolicy   string
	SecretPolicy    string
	LogPolicy       string
	MaxRuntime      time.Duration
	MaxRetries      int
}

type AutomationCommand struct {
	Command string
	Args    []string
}

type Ingestion struct {
	ContentGraphEnabled      bool
	LiveUpdatesEnabled       bool
	ASTExtractionEnabled     bool
	ExtractorCacheEnabled    bool
	DebounceInterval         time.Duration
	MaxFileBytes             int64
	MaxChunkBytes            int
	QueueDepth               int
	WorkerCount              int
	GlobalWorkerCount        int
	PerProjectWorkerLimit    int
	LivePathPriority         bool
	MaxWatchedDirectoryCount int
	TaskWarnAfter            time.Duration
	FullScanBatchSize        int
	InitialScanOnStart       bool
	SensitiveMarkerPolicy    string
}

type Project struct {
	ID                    string
	Aliases               []string
	DisplayName           string
	Description           string
	RootPath              string
	Enabled               bool
	Classification        string
	GraphNamespace        string
	GraphStorage          string
	DigestMode            string
	UpdatePolicy          string
	WorkspaceMode         string
	Include               []string
	Exclude               []string
	FollowSymlinks        bool
	MaxFileBytes          int64
	MaxChunkBytes         int
	SensitiveMarkerPolicy string
	Integrations          IntegrationConfig
	GitOperations         *GitOperations
	Verification          *Verification
	WorkflowChains        []WorkflowChain
}

type WorkflowChain struct {
	ChainRef             string
	Enabled              bool
	InputKind            string
	InputPattern         string
	ContextProvider      string
	ContextMode          string
	DefaultTitleTemplate string
	GitOpsMode           string
	Stages               []WorkflowChainStage
}

type WorkflowChainStage struct {
	StageRef                 string
	WorkflowRef              string
	Trigger                  string
	DependsOn                []string
	AutomationRefTemplate    string
	RequiredStatusBeforeNext string
}

type IntegrationConfig struct {
	Jira       *JiraIntegration
	Confluence *ConfluenceIntegration
}

type AtlassianCredentialRefs struct {
	EmailEnv        string
	EmailFile       string
	APITokenEnv     string
	APITokenFile    string
	CredentialsFile string
}

type IntegrationPolling struct {
	IngestionEnabled    bool
	InitialFullSync     string
	IncrementalInterval time.Duration
	EmptyPollSleep      time.Duration
	MaxIdleSleep        time.Duration
	OverlapWindow       time.Duration
	InitialPageSize     int
	IncrementalPageSize int
}

type JiraIntegration struct {
	Enabled           bool
	SiteURL           string
	CloudID           string
	AuthMode          string
	CredentialRefs    AtlassianCredentialRefs
	ReadTimeout       time.Duration
	MaxResults        int
	Polling           IntegrationPolling
	ProjectKeys       []string
	DefaultFields     []string
	AllowedFields     []string
	IncludeRichFields bool
	IncludeComments   bool
	JQLExtraFilter    string
}

type ConfluenceIntegration struct {
	Enabled            bool
	SiteURL            string
	CloudID            string
	AuthMode           string
	CredentialRefs     AtlassianCredentialRefs
	ReadTimeout        time.Duration
	MaxResults         int
	Polling            IntegrationPolling
	SpaceKeys          []string
	BodyRepresentation string
	IncludeBody        bool
	IncludeComments    bool
	IncludeLabels      bool
	IncludeProperties  bool
	RootPageIDs        []string
	CQLExtraFilter     string
}

func Load() (Config, error) {
	configPath, explicitConfigPath := configPathFromEnv()
	return load(configPath, explicitConfigPath)
}

func LoadPath(configPath string) (Config, error) {
	if strings.TrimSpace(configPath) == "" {
		return Config{}, errors.New("config path must not be empty")
	}
	return load(configPath, true)
}

func load(configPath string, explicitConfigPath bool) (Config, error) {
	cfg := defaultConfig(configPath)

	fileCfg, err := loadFileConfig(configPath)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist) && !explicitConfigPath:
		case errors.Is(err, fs.ErrNotExist):
			return Config{}, errors.New("MIVIA_CONFIG_PATH points to a missing config file")
		case explicitConfigPath:
			return Config{}, fmt.Errorf("MIVIA_CONFIG_PATH config is invalid: %w", err)
		default:
			return Config{}, fmt.Errorf("default local config is invalid: %w", err)
		}
	} else if cfg, err = fileCfg.applyTo(cfg); err != nil {
		if explicitConfigPath {
			return Config{}, fmt.Errorf("MIVIA_CONFIG_PATH config is invalid: %w", err)
		}
		return Config{}, fmt.Errorf("default local config is invalid: %w", err)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}
	cfg.resolveAutoSettings(runtime.NumCPU())

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func defaultConfig(configPath string) Config {
	return Config{
		ConfigPath:  configPath,
		CPUCount:    0,
		HTTPAddr:    defaultHTTPAddr,
		LadybugPath: defaultLadybugPath,
		SQLitePath:  defaultSQLitePath,
		SQLite: SQLite{
			WALEnabled:               true,
			BusyTimeout:              defaultSQLiteBusyTimeout,
			Synchronous:              defaultSQLiteSynchronous,
			CheckpointAfterIngestion: true,
		},
		Debug:             Debug{},
		Logging:           Logging{},
		MaxRequestBytes:   defaultMaxRequestBytes,
		RequestTimeout:    defaultRequestTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		Ingestion:         defaultIngestion(),
		Workspace:         Workspace{Enabled: false},
		Workflows:         Workflows{Enabled: false},
		DurableWorkflows: DurableWorkflows{
			Enabled:         true,
			ShadowMode:      true,
			Backend:         durableWorkflowBackendMemory,
			SQLitePath:      defaultDurableWorkflowSQLitePath,
			WorkerEnabled:   true,
			MaxParallelRuns: 1,
		},
		Automation:    defaultAutomation(),
		GitOperations: defaultGitOperations(),
		Verification:  Verification{},
		Projects:      nil,
	}
}

func defaultAutomation() Automation {
	return Automation{
		Enabled:                   false,
		RunnerEnabled:             false,
		RequireCodexWhenAvailable: true,
		AllowManualRunner:         false,
		RunnerExecution:           "in_process",
		QueueDepth:                16,
		PollInterval:              defaultAutomationPollInterval,
		GlobalWorkerCount:         1,
		PerProjectWorkerLimit:     1,
		PerAgentWorkerLimit:       1,
		MaxParallelTasks:          1,
		DefaultMaxRuntime:         30 * time.Minute,
		CodexBinaryPath:           "",
		Agents:                    nil,
		WorkPlanStatusTrigger:     WorkPlanStatusTrigger{Enabled: false, Statuses: []string{"active"}},
	}
}

func defaultGitOperations() GitOperations {
	return GitOperations{
		Enabled:                      false,
		CommitAfterTask:              true,
		PushAfterTask:                false,
		DraftPRAfterPush:             false,
		RequireCleanBeforeTask:       true,
		CleanupWorktreeAfterPlanDone: true,
		RemoteName:                   "origin",
		BranchPrefix:                 "",
		CommitAuthorName:             "Mivia Automation",
		CommitAuthorEmailEnv:         "MIVIA_GIT_AUTHOR_EMAIL",
		GitHubCLIPath:                "gh",
		Conventions:                  defaultGitOpsConventions(),
	}
}

func defaultGitOpsConventions() GitOpsConventions {
	return GitOpsConventions{
		CommitType:               "chore",
		BranchTemplate:           "",
		RequireTicketRef:         false,
		TicketRefPattern:         "",
		TicketURLTemplate:        "",
		AllowedTypes:             []string{"feat", "fix", "docs", "chore", "refactor", "test", "perf", "build", "ci", "revert"},
		DefaultChangeType:        "chore",
		CommitSummaryTemplate:    "complete {{work_task_id}}",
		PullRequestTitleTemplate: "{{commit_subject}}",
		PullRequestBodyTemplate:  "## What changed\n{{what_changed}}\n\n## How verified\n{{how_verified}}\n\n## Tests\n{{tests}}",
		WhatChangedTemplate:      "Completed automation work task {{work_task_id}} for project {{project_id}}.",
		HowVerifiedTemplate:      "Project ID: {{project_id}}\nWork Plan ID: {{work_plan_id}}\nWork Task ID: {{work_task_id}}\nAutomation ID: {{automation_id}}\nAutomation Run ID: {{automation_run_id}}\nOperator ID: {{operator_id}}\nReview refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}",
		TestsTemplate:            "{{test_results}}",
	}
}

func defaultIngestion() Ingestion {
	return Ingestion{
		ContentGraphEnabled:      false,
		LiveUpdatesEnabled:       false,
		ASTExtractionEnabled:     true,
		ExtractorCacheEnabled:    true,
		DebounceInterval:         defaultIngestionDebounceInterval,
		MaxFileBytes:             0,
		MaxChunkBytes:            defaultIngestionMaxChunkBytes,
		QueueDepth:               defaultIngestionQueueDepth,
		WorkerCount:              0,
		GlobalWorkerCount:        0,
		PerProjectWorkerLimit:    0,
		LivePathPriority:         true,
		MaxWatchedDirectoryCount: 0,
		TaskWarnAfter:            defaultIngestionTaskWarnAfter,
		FullScanBatchSize:        defaultIngestionFullScanBatchSize,
		InitialScanOnStart:       false,
		SensitiveMarkerPolicy:    defaultSensitiveMarkerPolicy,
	}
}

func applyEnvOverrides(cfg *Config) error {
	var err error
	cfg.HTTPAddr = getenv("MIVIA_HTTP_ADDR", cfg.HTTPAddr)
	if cfg.CPUCount, err = getenvAutoInt("MIVIA_CPU_COUNT", cfg.CPUCount); err != nil {
		return err
	}
	cfg.LadybugPath = getenv("MIVIA_LADYBUG_PATH", cfg.LadybugPath)
	cfg.SQLitePath = getenv("MIVIA_SQLITE_PATH", cfg.SQLitePath)
	if cfg.Debug.Enabled, err = getenvBool("MIVIA_DEBUG_ENABLED", cfg.Debug.Enabled); err != nil {
		return err
	}
	if cfg.Debug.PprofEnabled, err = getenvBool("MIVIA_DEBUG_PPROF_ENABLED", cfg.Debug.PprofEnabled); err != nil {
		return err
	}
	if cfg.Debug.ExpvarEnabled, err = getenvBool("MIVIA_DEBUG_EXPVAR_ENABLED", cfg.Debug.ExpvarEnabled); err != nil {
		return err
	}
	if cfg.Debug.RuntimeMetricsEnabled, err = getenvBool("MIVIA_DEBUG_RUNTIME_METRICS_ENABLED", cfg.Debug.RuntimeMetricsEnabled); err != nil {
		return err
	}
	if cfg.SQLite.WALEnabled, err = getenvBool("MIVIA_SQLITE_WAL_ENABLED", cfg.SQLite.WALEnabled); err != nil {
		return err
	}
	if cfg.SQLite.BusyTimeout, err = getenvDuration("MIVIA_SQLITE_BUSY_TIMEOUT", cfg.SQLite.BusyTimeout); err != nil {
		return err
	}
	cfg.SQLite.Synchronous = getenv("MIVIA_SQLITE_SYNCHRONOUS", cfg.SQLite.Synchronous)
	if cfg.SQLite.CheckpointAfterIngestion, err = getenvBool("MIVIA_SQLITE_CHECKPOINT_AFTER_INGESTION", cfg.SQLite.CheckpointAfterIngestion); err != nil {
		return err
	}
	if cfg.Logging.FileEnabled, err = getenvBool("MIVIA_LOG_FILE_ENABLED", cfg.Logging.FileEnabled); err != nil {
		return err
	}
	cfg.Logging.FilePath = getenv("MIVIA_LOG_FILE_PATH", cfg.Logging.FilePath)
	if cfg.MaxRequestBytes, err = getenvInt64("MIVIA_MAX_REQUEST_BYTES", cfg.MaxRequestBytes); err != nil {
		return err
	}
	if cfg.RequestTimeout, err = getenvDuration("MIVIA_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return err
	}
	if cfg.ReadHeaderTimeout, err = getenvDuration("MIVIA_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return err
	}
	if cfg.ShutdownTimeout, err = getenvDuration("MIVIA_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	if cfg.Ingestion.ContentGraphEnabled, err = getenvBool("MIVIA_INGESTION_CONTENT_GRAPH_ENABLED", cfg.Ingestion.ContentGraphEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.LiveUpdatesEnabled, err = getenvBool("MIVIA_INGESTION_LIVE_UPDATES_ENABLED", cfg.Ingestion.LiveUpdatesEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.ASTExtractionEnabled, err = getenvBool("MIVIA_INGESTION_AST_EXTRACTION_ENABLED", cfg.Ingestion.ASTExtractionEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.ExtractorCacheEnabled, err = getenvBool("MIVIA_INGESTION_EXTRACTOR_CACHE_ENABLED", cfg.Ingestion.ExtractorCacheEnabled); err != nil {
		return err
	}
	if cfg.Ingestion.DebounceInterval, err = getenvDuration("MIVIA_INGESTION_DEBOUNCE_INTERVAL", cfg.Ingestion.DebounceInterval); err != nil {
		return err
	}
	if cfg.Ingestion.MaxFileBytes, err = getenvInt64("MIVIA_INGESTION_MAX_FILE_BYTES", cfg.Ingestion.MaxFileBytes); err != nil {
		return err
	}
	if cfg.Ingestion.MaxChunkBytes, err = getenvInt("MIVIA_INGESTION_MAX_CHUNK_BYTES", cfg.Ingestion.MaxChunkBytes); err != nil {
		return err
	}
	if cfg.Ingestion.QueueDepth, err = getenvInt("MIVIA_INGESTION_QUEUE_DEPTH", cfg.Ingestion.QueueDepth); err != nil {
		return err
	}
	workerCountOverridden := os.Getenv("MIVIA_INGESTION_WORKER_COUNT") != ""
	if cfg.Ingestion.WorkerCount, err = getenvAutoInt("MIVIA_INGESTION_WORKER_COUNT", cfg.Ingestion.WorkerCount); err != nil {
		return err
	}
	globalWorkerCountOverridden := os.Getenv("MIVIA_INGESTION_GLOBAL_WORKER_COUNT") != ""
	if cfg.Ingestion.GlobalWorkerCount, err = getenvAutoInt("MIVIA_INGESTION_GLOBAL_WORKER_COUNT", cfg.Ingestion.GlobalWorkerCount); err != nil {
		return err
	}
	if workerCountOverridden && !globalWorkerCountOverridden {
		cfg.Ingestion.GlobalWorkerCount = cfg.Ingestion.WorkerCount
	}
	if cfg.Ingestion.PerProjectWorkerLimit, err = getenvAutoInt("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT", cfg.Ingestion.PerProjectWorkerLimit); err != nil {
		return err
	}
	if cfg.Ingestion.LivePathPriority, err = getenvBool("MIVIA_INGESTION_LIVE_PATH_PRIORITY", cfg.Ingestion.LivePathPriority); err != nil {
		return err
	}
	if cfg.Ingestion.MaxWatchedDirectoryCount, err = getenvInt("MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT", cfg.Ingestion.MaxWatchedDirectoryCount); err != nil {
		return err
	}
	if cfg.Ingestion.TaskWarnAfter, err = getenvDuration("MIVIA_INGESTION_TASK_WARN_AFTER", cfg.Ingestion.TaskWarnAfter); err != nil {
		return err
	}
	if cfg.Ingestion.FullScanBatchSize, err = getenvInt("MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE", cfg.Ingestion.FullScanBatchSize); err != nil {
		return err
	}
	if cfg.Ingestion.InitialScanOnStart, err = getenvBool("MIVIA_INGESTION_INITIAL_SCAN_ON_START", cfg.Ingestion.InitialScanOnStart); err != nil {
		return err
	}
	cfg.Ingestion.SensitiveMarkerPolicy = getenv("MIVIA_INGESTION_SENSITIVE_MARKER_POLICY", cfg.Ingestion.SensitiveMarkerPolicy)
	if cfg.Workspace.Enabled, err = getenvBool("MIVIA_WORKSPACE_ENABLED", cfg.Workspace.Enabled); err != nil {
		return err
	}
	if cfg.DurableWorkflows.Enabled, err = getenvBool("MIVIA_DURABLE_WORKFLOWS_ENABLED", cfg.DurableWorkflows.Enabled); err != nil {
		return err
	}
	if cfg.DurableWorkflows.ShadowMode, err = getenvBool("MIVIA_DURABLE_WORKFLOWS_SHADOW_MODE", cfg.DurableWorkflows.ShadowMode); err != nil {
		return err
	}
	cfg.DurableWorkflows.Backend = getenv("MIVIA_DURABLE_WORKFLOWS_BACKEND", cfg.DurableWorkflows.Backend)
	cfg.DurableWorkflows.SQLitePath = getenv("MIVIA_DURABLE_WORKFLOWS_SQLITE_PATH", cfg.DurableWorkflows.SQLitePath)
	if cfg.DurableWorkflows.WorkerEnabled, err = getenvBool("MIVIA_DURABLE_WORKFLOWS_WORKER_ENABLED", cfg.DurableWorkflows.WorkerEnabled); err != nil {
		return err
	}
	if cfg.DurableWorkflows.MaxParallelRuns, err = getenvInt("MIVIA_DURABLE_WORKFLOWS_MAX_PARALLEL_RUNS", cfg.DurableWorkflows.MaxParallelRuns); err != nil {
		return err
	}
	if cfg.Automation.Enabled, err = getenvBool("MIVIA_AUTOMATION_ENABLED", cfg.Automation.Enabled); err != nil {
		return err
	}
	if cfg.Automation.RunnerEnabled, err = getenvBool("MIVIA_AUTOMATION_RUNNER_ENABLED", cfg.Automation.RunnerEnabled); err != nil {
		return err
	}
	cfg.Automation.RunnerExecution = getenv("MIVIA_AUTOMATION_RUNNER_EXECUTION", cfg.Automation.RunnerExecution)
	cfg.Automation.CodexBinaryPath = getenv("MIVIA_AUTOMATION_CODEX_BINARY_PATH", cfg.Automation.CodexBinaryPath)
	if cfg.Automation.WorkPlanStatusTrigger.Enabled, err = getenvBool("MIVIA_AUTOMATION_WORK_PLAN_STATUS_TRIGGER_ENABLED", cfg.Automation.WorkPlanStatusTrigger.Enabled); err != nil {
		return err
	}
	if rawStatuses := os.Getenv("MIVIA_AUTOMATION_WORK_PLAN_STATUS_TRIGGER_STATUSES"); strings.TrimSpace(rawStatuses) != "" {
		cfg.Automation.WorkPlanStatusTrigger.Statuses = splitCSV(rawStatuses)
	}
	if cfg.GitOperations.Enabled, err = getenvBool("MIVIA_GIT_OPS_ENABLED", cfg.GitOperations.Enabled); err != nil {
		return err
	}
	if cfg.GitOperations.CommitAfterTask, err = getenvBool("MIVIA_GIT_OPS_COMMIT_AFTER_TASK", cfg.GitOperations.CommitAfterTask); err != nil {
		return err
	}
	if cfg.GitOperations.PushAfterTask, err = getenvBool("MIVIA_GIT_OPS_PUSH_AFTER_TASK", cfg.GitOperations.PushAfterTask); err != nil {
		return err
	}
	if cfg.GitOperations.DraftPRAfterPush, err = getenvBool("MIVIA_GIT_OPS_DRAFT_PR_AFTER_PUSH", cfg.GitOperations.DraftPRAfterPush); err != nil {
		return err
	}
	if cfg.GitOperations.RequireCleanBeforeTask, err = getenvBool("MIVIA_GIT_OPS_REQUIRE_CLEAN_BEFORE_TASK", cfg.GitOperations.RequireCleanBeforeTask); err != nil {
		return err
	}
	if cfg.GitOperations.CleanupWorktreeAfterPlanDone, err = getenvBool("MIVIA_GIT_OPS_CLEANUP_WORKTREE_AFTER_PLAN_DONE", cfg.GitOperations.CleanupWorktreeAfterPlanDone); err != nil {
		return err
	}
	cfg.GitOperations.RemoteName = getenv("MIVIA_GIT_OPS_REMOTE_NAME", cfg.GitOperations.RemoteName)
	cfg.GitOperations.BranchPrefix = getenv("MIVIA_GIT_OPS_BRANCH_PREFIX", cfg.GitOperations.BranchPrefix)
	cfg.GitOperations.BranchNamePattern = getenv("MIVIA_GIT_OPS_BRANCH_NAME_PATTERN", cfg.GitOperations.BranchNamePattern)
	cfg.GitOperations.CommitAuthorName = getenv("MIVIA_GIT_OPS_COMMIT_AUTHOR_NAME", cfg.GitOperations.CommitAuthorName)
	cfg.GitOperations.CommitAuthorEmailEnv = getenv("MIVIA_GIT_OPS_COMMIT_AUTHOR_EMAIL_ENV", cfg.GitOperations.CommitAuthorEmailEnv)
	cfg.GitOperations.CommitAuthorEmailFile = getenv("MIVIA_GIT_OPS_COMMIT_AUTHOR_EMAIL_FILE", cfg.GitOperations.CommitAuthorEmailFile)
	if cfg.GitOperations.SignCommits, err = getenvBool("MIVIA_GIT_OPS_SIGN_COMMITS", cfg.GitOperations.SignCommits); err != nil {
		return err
	}
	cfg.GitOperations.SSHPrivateKeyPath = getenv("MIVIA_GIT_OPS_SSH_PRIVATE_KEY_PATH", cfg.GitOperations.SSHPrivateKeyPath)
	cfg.GitOperations.SSHPublicKeyPath = getenv("MIVIA_GIT_OPS_SSH_PUBLIC_KEY_PATH", cfg.GitOperations.SSHPublicKeyPath)
	cfg.GitOperations.SSHKnownHostsPath = getenv("MIVIA_GIT_OPS_SSH_KNOWN_HOSTS_PATH", cfg.GitOperations.SSHKnownHostsPath)
	cfg.GitOperations.GitHubTokenEnv = getenv("MIVIA_GIT_OPS_GITHUB_TOKEN_ENV", cfg.GitOperations.GitHubTokenEnv)
	cfg.GitOperations.GitHubTokenFile = getenv("MIVIA_GIT_OPS_GITHUB_TOKEN_FILE", cfg.GitOperations.GitHubTokenFile)
	cfg.GitOperations.GitHubCLIPath = getenv("MIVIA_GIT_OPS_GITHUB_CLI_PATH", cfg.GitOperations.GitHubCLIPath)
	cfg.GitOperations.Conventions.CommitType = getenv("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_TYPE", cfg.GitOperations.Conventions.CommitType)
	cfg.GitOperations.Conventions.CommitScope = getenv("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SCOPE", cfg.GitOperations.Conventions.CommitScope)
	cfg.GitOperations.Conventions.BranchTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_BRANCH_TEMPLATE", cfg.GitOperations.Conventions.BranchTemplate)
	if rawTypes := os.Getenv("MIVIA_GIT_OPS_CONVENTIONS_ALLOWED_TYPES"); strings.TrimSpace(rawTypes) != "" {
		cfg.GitOperations.Conventions.AllowedTypes = splitCSV(rawTypes)
	}
	if cfg.GitOperations.Conventions.RequireTicketRef, err = getenvBool("MIVIA_GIT_OPS_CONVENTIONS_REQUIRE_TICKET_REF", cfg.GitOperations.Conventions.RequireTicketRef); err != nil {
		return err
	}
	cfg.GitOperations.Conventions.TicketRefPattern = getenv("MIVIA_GIT_OPS_CONVENTIONS_TICKET_REF_PATTERN", cfg.GitOperations.Conventions.TicketRefPattern)
	cfg.GitOperations.Conventions.TicketURLTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_TICKET_URL_TEMPLATE", cfg.GitOperations.Conventions.TicketURLTemplate)
	cfg.GitOperations.Conventions.DefaultChangeType = getenv("MIVIA_GIT_OPS_CONVENTIONS_DEFAULT_CHANGE_TYPE", cfg.GitOperations.Conventions.DefaultChangeType)
	cfg.GitOperations.Conventions.CommitSummaryTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SUMMARY_TEMPLATE", cfg.GitOperations.Conventions.CommitSummaryTemplate)
	cfg.GitOperations.Conventions.PullRequestTitleTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_PULL_REQUEST_TITLE_TEMPLATE", cfg.GitOperations.Conventions.PullRequestTitleTemplate)
	cfg.GitOperations.Conventions.PullRequestBodyTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_PULL_REQUEST_BODY_TEMPLATE", cfg.GitOperations.Conventions.PullRequestBodyTemplate)
	cfg.GitOperations.Conventions.WhatChangedTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_WHAT_CHANGED_TEMPLATE", cfg.GitOperations.Conventions.WhatChangedTemplate)
	cfg.GitOperations.Conventions.HowVerifiedTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_HOW_VERIFIED_TEMPLATE", cfg.GitOperations.Conventions.HowVerifiedTemplate)
	cfg.GitOperations.Conventions.TestsTemplate = getenv("MIVIA_GIT_OPS_CONVENTIONS_TESTS_TEMPLATE", cfg.GitOperations.Conventions.TestsTemplate)
	if cfg.AgentActivity.RetainRawPayloads, err = getenvBool("MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS", cfg.AgentActivity.RetainRawPayloads); err != nil {
		return err
	}
	return nil
}

func (cfg *Config) resolveAutoSettings(maxCPU int) {
	if maxCPU <= 0 {
		maxCPU = 1
	}
	if cfg.CPUCount <= 0 {
		cfg.CPUCount = maxCPU
	}
	if cfg.Ingestion.WorkerCount <= 0 {
		cfg.Ingestion.WorkerCount = defaultIngestionWorkerCount
	}
	if cfg.Ingestion.GlobalWorkerCount <= 0 {
		cfg.Ingestion.GlobalWorkerCount = defaultIngestionGlobalWorkerCount
	}
	if cfg.Ingestion.PerProjectWorkerLimit <= 0 {
		cfg.Ingestion.PerProjectWorkerLimit = defaultIngestionPerProjectLimit
	}
	if cfg.SQLitePath == ":memory:" {
		cfg.SQLite.WALEnabled = false
	}
}

func (cfg Config) Validate() error {
	if cfg.CPUCount <= 0 {
		return errors.New("MIVIA_CPU_COUNT must be \"auto\" or a positive integer")
	}
	if cfg.HTTPAddr == "" {
		return errors.New("MIVIA_HTTP_ADDR must not be empty")
	}
	if !isLocalAddress(cfg.HTTPAddr) {
		return fmt.Errorf("%w: MIVIA_HTTP_ADDR must bind to localhost until authn/authz is approved", ErrNonLoopbackBind)
	}
	if cfg.LadybugPath == "" {
		return errors.New("MIVIA_LADYBUG_PATH must not be empty")
	}
	if cfg.SQLitePath == "" {
		return errors.New("MIVIA_SQLITE_PATH must not be empty")
	}
	if err := cfg.Debug.Validate(); err != nil {
		return err
	}
	if err := cfg.SQLite.Validate(); err != nil {
		return err
	}
	if err := cfg.Logging.Validate(); err != nil {
		return err
	}
	if cfg.MaxRequestBytes <= 0 {
		return errors.New("MIVIA_MAX_REQUEST_BYTES must be positive")
	}
	if cfg.RequestTimeout <= 0 {
		return errors.New("MIVIA_REQUEST_TIMEOUT must be positive")
	}
	if cfg.ReadHeaderTimeout <= 0 {
		return errors.New("MIVIA_READ_HEADER_TIMEOUT must be positive")
	}
	if cfg.ShutdownTimeout <= 0 {
		return errors.New("MIVIA_SHUTDOWN_TIMEOUT must be positive")
	}
	if err := cfg.Ingestion.Validate(); err != nil {
		return err
	}
	if err := cfg.Workflows.Validate(); err != nil {
		return err
	}
	if err := cfg.DurableWorkflows.Validate(); err != nil {
		return err
	}
	if cfg.AgentActivity.RetainRawPayloads && !cfg.Debug.Enabled {
		return errors.New("MIVIA_AGENT_ACTIVITY_RETAIN_RAW_PAYLOADS requires MIVIA_DEBUG_ENABLED")
	}
	if err := cfg.Automation.Validate(); err != nil {
		return err
	}
	if err := cfg.GitOperations.Validate(); err != nil {
		return err
	}
	if err := cfg.Verification.Validate("MIVIA_VERIFICATION"); err != nil {
		return err
	}
	for _, project := range cfg.Projects {
		if project.GitOperations != nil {
			if err := project.GitOperations.validate("project "+project.ID+" git_operations", true); err != nil {
				return err
			}
		}
		if project.Verification != nil {
			if err := project.Verification.Validate("project " + project.ID + " verification"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (verification Verification) Validate(prefix string) error {
	for i, command := range verification.BootstrapCommands {
		if err := validateSafeVerifierCommand(fmt.Sprintf("%s_BOOTSTRAP_COMMANDS[%d]", prefix, i), command); err != nil {
			return err
		}
	}
	for i, command := range verification.AlwaysBeforePR {
		if err := validateSafeVerifierCommand(fmt.Sprintf("%s_ALWAYS_BEFORE_PR[%d]", prefix, i), command); err != nil {
			return err
		}
	}
	for i, command := range verification.AutofixCommands {
		if err := validateSafeVerifierCommand(fmt.Sprintf("%s_AUTOFIX_COMMANDS[%d]", prefix, i), command); err != nil {
			return err
		}
	}
	for i, generated := range verification.GeneratedArtifacts {
		if len(generated.Paths) == 0 {
			return fmt.Errorf("%s_GENERATED_ARTIFACTS[%d]_PATHS must not be empty", prefix, i)
		}
		for j, path := range generated.Paths {
			if err := validateSafeVerifierPath(fmt.Sprintf("%s_GENERATED_ARTIFACTS[%d]_PATHS[%d]", prefix, i, j), path); err != nil {
				return err
			}
		}
		if err := validateSafeVerifierCommand(fmt.Sprintf("%s_GENERATED_ARTIFACTS[%d]_COMMAND", prefix, i), generated.Command); err != nil {
			return err
		}
	}
	for key, value := range verification.Env {
		if err := validateSafeVerifierEnv(fmt.Sprintf("%s_ENV[%s]", prefix, key), key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateSafeVerifierCommand(name string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s must not contain control characters", name)
	}
	if len(value) > 1000 {
		return fmt.Errorf("%s must be <= 1000 characters", name)
	}
	return nil
}

func validateSafeVerifierPath(name string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s must be a safe project-relative path or glob", name)
	}
	return nil
}

func validateSafeVerifierEnv(name, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%s key must not be empty", name)
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
		return fmt.Errorf("%s key must be a valid environment variable name", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s value must not contain control characters", name)
	}
	if len(value) > 2000 {
		return fmt.Errorf("%s value must be <= 2000 characters", name)
	}
	return nil
}

func (logging Logging) Validate() error {
	if logging.FileEnabled && strings.TrimSpace(logging.FilePath) == "" {
		return errors.New("MIVIA_LOG_FILE_PATH must not be empty when MIVIA_LOG_FILE_ENABLED is true")
	}
	return nil
}

func (debug Debug) Validate() error {
	if !debug.Enabled && (debug.PprofEnabled || debug.ExpvarEnabled || debug.RuntimeMetricsEnabled) {
		return errors.New("MIVIA_DEBUG_ENABLED must be true before enabling debug diagnostics")
	}
	return nil
}

func (sqlite SQLite) Validate() error {
	if sqlite.BusyTimeout <= 0 {
		return errors.New("MIVIA_SQLITE_BUSY_TIMEOUT must be positive")
	}
	switch strings.ToUpper(strings.TrimSpace(sqlite.Synchronous)) {
	case "OFF", "NORMAL", "FULL", "EXTRA":
	default:
		return errors.New("MIVIA_SQLITE_SYNCHRONOUS must be OFF, NORMAL, FULL, or EXTRA")
	}
	return nil
}

func (ingestion Ingestion) Validate() error {
	if ingestion.LiveUpdatesEnabled && !ingestion.ContentGraphEnabled {
		return errors.New("MIVIA_INGESTION_LIVE_UPDATES_ENABLED requires MIVIA_INGESTION_CONTENT_GRAPH_ENABLED")
	}
	if ingestion.ContentGraphEnabled && !ingestion.ASTExtractionEnabled {
		return errors.New("MIVIA_INGESTION_AST_EXTRACTION_ENABLED must be true when content graph is enabled")
	}
	if ingestion.ASTExtractionEnabled && !ingestion.ExtractorCacheEnabled {
		return errors.New("MIVIA_INGESTION_EXTRACTOR_CACHE_ENABLED must be true when AST extraction is enabled")
	}
	if ingestion.DebounceInterval <= 0 {
		return errors.New("MIVIA_INGESTION_DEBOUNCE_INTERVAL must be positive")
	}
	if ingestion.MaxFileBytes < 0 {
		return errors.New("MIVIA_INGESTION_MAX_FILE_BYTES must be non-negative")
	}
	if ingestion.MaxChunkBytes <= 0 {
		return errors.New("MIVIA_INGESTION_MAX_CHUNK_BYTES must be positive")
	}
	if ingestion.QueueDepth <= 0 {
		return errors.New("MIVIA_INGESTION_QUEUE_DEPTH must be positive")
	}
	if ingestion.WorkerCount <= 0 {
		return errors.New("MIVIA_INGESTION_WORKER_COUNT must be \"auto\" or a positive integer")
	}
	if ingestion.GlobalWorkerCount <= 0 {
		return errors.New("MIVIA_INGESTION_GLOBAL_WORKER_COUNT must be \"auto\" or a positive integer")
	}
	if ingestion.PerProjectWorkerLimit <= 0 || ingestion.PerProjectWorkerLimit > ingestion.GlobalWorkerCount {
		return errors.New("MIVIA_INGESTION_PER_PROJECT_WORKER_LIMIT must be \"auto\" or a positive integer <= MIVIA_INGESTION_GLOBAL_WORKER_COUNT")
	}
	if ingestion.MaxWatchedDirectoryCount < 0 {
		return errors.New("MIVIA_INGESTION_MAX_WATCHED_DIRECTORY_COUNT must be non-negative")
	}
	if ingestion.TaskWarnAfter <= 0 {
		return errors.New("MIVIA_INGESTION_TASK_WARN_AFTER must be positive")
	}
	if ingestion.FullScanBatchSize <= 0 {
		return errors.New("MIVIA_INGESTION_FULL_SCAN_BATCH_SIZE must be positive")
	}
	if ingestion.SensitiveMarkerPolicy != sensitiveMarkerPolicySkipFile {
		return fmt.Errorf("MIVIA_INGESTION_SENSITIVE_MARKER_POLICY must be %q", sensitiveMarkerPolicySkipFile)
	}
	return nil
}

func (automation Automation) Validate() error {
	if automation.RunnerEnabled && !automation.Enabled {
		return errors.New("MIVIA_AUTOMATION_RUNNER_ENABLED requires MIVIA_AUTOMATION_ENABLED")
	}
	switch strings.TrimSpace(automation.RunnerExecution) {
	case "in_process", "external", "managed":
	default:
		return errors.New("MIVIA_AUTOMATION_RUNNER_EXECUTION must be in_process, external, or managed")
	}
	if automation.QueueDepth <= 0 {
		return errors.New("MIVIA_AUTOMATION_QUEUE_DEPTH must be positive")
	}
	if automation.PollInterval <= 0 {
		return errors.New("MIVIA_AUTOMATION_POLL_INTERVAL must be positive")
	}
	if automation.GlobalWorkerCount <= 0 {
		return errors.New("MIVIA_AUTOMATION_GLOBAL_WORKER_COUNT must be positive")
	}
	if automation.PerProjectWorkerLimit <= 0 || automation.PerProjectWorkerLimit > automation.GlobalWorkerCount {
		return errors.New("MIVIA_AUTOMATION_PER_PROJECT_WORKER_LIMIT must be a positive integer <= MIVIA_AUTOMATION_GLOBAL_WORKER_COUNT")
	}
	if automation.PerAgentWorkerLimit <= 0 || automation.PerAgentWorkerLimit > automation.PerProjectWorkerLimit {
		return errors.New("MIVIA_AUTOMATION_PER_AGENT_WORKER_LIMIT must be a positive integer <= MIVIA_AUTOMATION_PER_PROJECT_WORKER_LIMIT")
	}
	if automation.MaxParallelTasks <= 0 {
		return errors.New("MIVIA_AUTOMATION_MAX_PARALLEL_TASKS must be positive")
	}
	if automation.DefaultMaxRuntime <= 0 {
		return errors.New("MIVIA_AUTOMATION_DEFAULT_MAX_RUNTIME must be positive")
	}
	if strings.ContainsAny(automation.CodexBinaryPath, "\r\n\x00") {
		return errors.New("MIVIA_AUTOMATION_CODEX_BINARY_PATH must not contain control characters")
	}
	if err := automation.WorkPlanStatusTrigger.Validate(); err != nil {
		return err
	}
	for _, agent := range automation.Agents {
		if err := agent.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (gitops GitOperations) Validate() error {
	return gitops.validate("MIVIA_GIT_OPS", true)
}

func (gitops GitOperations) validate(prefix string, allowEmptyBranchPrefix bool) error {
	if gitops.PushAfterTask && !gitops.CommitAfterTask {
		return fmt.Errorf("%s_PUSH_AFTER_TASK requires %s_COMMIT_AFTER_TASK", prefix, prefix)
	}
	if gitops.DraftPRAfterPush && !gitops.PushAfterTask {
		return fmt.Errorf("%s_DRAFT_PR_AFTER_PUSH requires %s_PUSH_AFTER_TASK", prefix, prefix)
	}
	if gitops.PostPRChecks.Enabled && !gitops.DraftPRAfterPush {
		return fmt.Errorf("%s_POST_PR_CHECKS_ENABLED requires %s_DRAFT_PR_AFTER_PUSH", prefix, prefix)
	}
	if gitops.PostPRChecks.IntervalSeconds < 0 {
		return fmt.Errorf("%s_POST_PR_CHECKS_INTERVAL_SECONDS must be non-negative", prefix)
	}
	if err := validateSafeGitToken(prefix+"_REMOTE_NAME", gitops.RemoteName); err != nil {
		return err
	}
	if err := validateSafeBranchPrefix(prefix+"_BRANCH_PREFIX", gitops.BranchPrefix, allowEmptyBranchPrefix); err != nil {
		return err
	}
	if err := validateSafeBranchNamePattern(prefix+"_BRANCH_NAME_PATTERN", gitops.BranchNamePattern); err != nil {
		return err
	}
	for name, value := range map[string]string{
		prefix + "_COMMIT_AUTHOR_NAME":       gitops.CommitAuthorName,
		prefix + "_COMMIT_AUTHOR_EMAIL_ENV":  gitops.CommitAuthorEmailEnv,
		prefix + "_COMMIT_AUTHOR_EMAIL_FILE": gitops.CommitAuthorEmailFile,
		prefix + "_GITHUB_TOKEN_ENV":         gitops.GitHubTokenEnv,
		prefix + "_GITHUB_TOKEN_FILE":        gitops.GitHubTokenFile,
		prefix + "_GITHUB_CLI_PATH":          gitops.GitHubCLIPath,
	} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	if strings.TrimSpace(gitops.CommitAuthorEmailEnv) != "" && strings.TrimSpace(gitops.CommitAuthorEmailFile) != "" {
		return fmt.Errorf("%s_COMMIT_AUTHOR_EMAIL_ENV and %s_COMMIT_AUTHOR_EMAIL_FILE are mutually exclusive", prefix, prefix)
	}
	if strings.TrimSpace(gitops.GitHubTokenEnv) != "" && strings.TrimSpace(gitops.GitHubTokenFile) != "" {
		return fmt.Errorf("%s_GITHUB_TOKEN_ENV and %s_GITHUB_TOKEN_FILE are mutually exclusive", prefix, prefix)
	}
	for name, value := range map[string]string{
		prefix + "_SSH_PRIVATE_KEY_PATH":     gitops.SSHPrivateKeyPath,
		prefix + "_SSH_PUBLIC_KEY_PATH":      gitops.SSHPublicKeyPath,
		prefix + "_SSH_KNOWN_HOSTS_PATH":     gitops.SSHKnownHostsPath,
		prefix + "_COMMIT_AUTHOR_EMAIL_FILE": gitops.CommitAuthorEmailFile,
		prefix + "_GITHUB_TOKEN_FILE":        gitops.GitHubTokenFile,
	} {
		if err := validateOptionalAbsolutePath(name, value); err != nil {
			return err
		}
	}
	hasSSHPrivateKey := strings.TrimSpace(gitops.SSHPrivateKeyPath) != ""
	hasSSHKnownHosts := strings.TrimSpace(gitops.SSHKnownHostsPath) != ""
	if gitops.SignCommits && !hasSSHPrivateKey {
		return fmt.Errorf("%s_SIGN_COMMITS requires %s_SSH_PRIVATE_KEY_PATH", prefix, prefix)
	}
	if hasSSHPrivateKey != hasSSHKnownHosts {
		if !hasSSHPrivateKey {
			return fmt.Errorf("%s_SSH_PRIVATE_KEY_PATH is required when SSH known_hosts is configured", prefix)
		}
		return fmt.Errorf("%s_SSH_KNOWN_HOSTS_PATH is required when SSH private key is configured", prefix)
	}
	if gitops.DraftPRAfterPush && strings.TrimSpace(gitops.GitHubTokenEnv) == "" && strings.TrimSpace(gitops.GitHubTokenFile) == "" && strings.TrimSpace(gitops.GitHubCLIPath) == "" {
		return errors.New("draft PR creation requires GitHub CLI auth or a GitHub token env/file reference")
	}
	if err := gitops.Conventions.Validate(); err != nil {
		return err
	}
	if err := gitops.DirtyScopeRecovery.Validate(prefix); err != nil {
		return err
	}
	return nil
}

func (recovery DirtyScopeRecovery) Validate(prefix string) error {
	for _, value := range recovery.AllowedSupportPathspecs {
		path := strings.TrimSpace(filepath.ToSlash(value))
		if err := validateSafeVerifierPath(prefix+"_DIRTY_SCOPE_RECOVERY_ALLOWED_SUPPORT_PATHSPECS", path); err != nil {
			return err
		}
		switch {
		case path == ".git" || strings.HasPrefix(path, ".git/"):
			return fmt.Errorf("%s_DIRTY_SCOPE_RECOVERY_ALLOWED_SUPPORT_PATHSPECS must not include .git", prefix)
		case path == ".mivia-worktrees" || strings.HasPrefix(path, ".mivia-worktrees/"):
			return fmt.Errorf("%s_DIRTY_SCOPE_RECOVERY_ALLOWED_SUPPORT_PATHSPECS must not include .mivia-worktrees", prefix)
		case path == ".ai/tasks" || strings.HasPrefix(path, ".ai/tasks/"):
			return fmt.Errorf("%s_DIRTY_SCOPE_RECOVERY_ALLOWED_SUPPORT_PATHSPECS must not include .ai/tasks", prefix)
		}
	}
	return nil
}

func (conventions GitOpsConventions) Validate() error {
	if !isSafeGitOpsChangeType(conventions.CommitType) {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_TYPE must be a safe Conventional Commit type")
	}
	if strings.TrimSpace(conventions.CommitScope) != "" && !regexpMustMatch(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`, conventions.CommitScope) {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SCOPE must be a safe Conventional Commit scope")
	}
	if strings.TrimSpace(conventions.BranchTemplate) != "" {
		if strings.ContainsAny(conventions.BranchTemplate, "\r\n\x00") {
			return errors.New("MIVIA_GIT_OPS_CONVENTIONS_BRANCH_TEMPLATE must be single-line")
		}
		if err := validateGitOpsTemplate("MIVIA_GIT_OPS_CONVENTIONS_BRANCH_TEMPLATE", conventions.BranchTemplate); err != nil {
			return err
		}
	}
	if conventions.RequireTicketRef && strings.TrimSpace(conventions.TicketRefPattern) == "" {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_TICKET_REF_PATTERN must not be empty when ticket refs are required")
	}
	if strings.TrimSpace(conventions.TicketRefPattern) != "" {
		if len(strings.TrimSpace(conventions.TicketRefPattern)) > 256 || strings.ContainsAny(conventions.TicketRefPattern, "\r\n\x00") {
			return errors.New("MIVIA_GIT_OPS_CONVENTIONS_TICKET_REF_PATTERN must be a safe single-line RE2 pattern")
		}
		if _, err := regexp.Compile(strings.TrimSpace(conventions.TicketRefPattern)); err != nil {
			return fmt.Errorf("MIVIA_GIT_OPS_CONVENTIONS_TICKET_REF_PATTERN must compile as a RE2 pattern: %w", err)
		}
	}
	if strings.TrimSpace(conventions.TicketURLTemplate) != "" {
		if strings.ContainsAny(conventions.TicketURLTemplate, "\x00") {
			return errors.New("MIVIA_GIT_OPS_CONVENTIONS_TICKET_URL_TEMPLATE must not contain unsafe control characters")
		}
		if err := validateGitOpsTicketURLTemplate(conventions.TicketURLTemplate); err != nil {
			return err
		}
	}
	if len(conventions.AllowedTypes) == 0 {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_ALLOWED_TYPES must not be empty")
	}
	allowedTypes := make(map[string]bool, len(conventions.AllowedTypes))
	for _, value := range conventions.AllowedTypes {
		changeType := strings.TrimSpace(value)
		if !isSafeGitOpsChangeType(changeType) {
			return errors.New("MIVIA_GIT_OPS_CONVENTIONS_ALLOWED_TYPES must contain only safe Conventional Commit types")
		}
		allowedTypes[changeType] = true
	}
	if !isSafeGitOpsChangeType(conventions.DefaultChangeType) {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_DEFAULT_CHANGE_TYPE must be a safe Conventional Commit type")
	}
	if !allowedTypes[strings.TrimSpace(conventions.DefaultChangeType)] {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_DEFAULT_CHANGE_TYPE must be included in allowed_types")
	}
	if !allowedTypes[strings.TrimSpace(conventions.CommitType)] {
		return errors.New("MIVIA_GIT_OPS_CONVENTIONS_COMMIT_TYPE must be included in allowed_types")
	}
	for name, value := range map[string]string{
		"MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SUMMARY_TEMPLATE":     conventions.CommitSummaryTemplate,
		"MIVIA_GIT_OPS_CONVENTIONS_PULL_REQUEST_TITLE_TEMPLATE": conventions.PullRequestTitleTemplate,
		"MIVIA_GIT_OPS_CONVENTIONS_PULL_REQUEST_BODY_TEMPLATE":  conventions.PullRequestBodyTemplate,
		"MIVIA_GIT_OPS_CONVENTIONS_WHAT_CHANGED_TEMPLATE":       conventions.WhatChangedTemplate,
		"MIVIA_GIT_OPS_CONVENTIONS_HOW_VERIFIED_TEMPLATE":       conventions.HowVerifiedTemplate,
		"MIVIA_GIT_OPS_CONVENTIONS_TESTS_TEMPLATE":              conventions.TestsTemplate,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", name)
		}
		if strings.Contains(value, "\x00") {
			return fmt.Errorf("%s must not contain NUL characters", name)
		}
		if err := validateGitOpsTemplate(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"MIVIA_GIT_OPS_CONVENTIONS_COMMIT_SUMMARY_TEMPLATE":     conventions.CommitSummaryTemplate,
		"MIVIA_GIT_OPS_CONVENTIONS_PULL_REQUEST_TITLE_TEMPLATE": conventions.PullRequestTitleTemplate,
	} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s must be single-line", name)
		}
	}
	return nil
}

func isSafeGitOpsChangeType(value string) bool {
	return regexpMustMatch(`^[a-z][a-z0-9-]*$`, value)
}

func validateGitOpsTemplate(name, value string) error {
	return validateGitOpsTemplateWithAllowedPlaceholders(name, value, gitOpsTemplateAllowedPlaceholders())
}

func validateGitOpsTicketURLTemplate(value string) error {
	allowed := gitOpsTemplateAllowedPlaceholders()
	delete(allowed, "ticket_url")
	delete(allowed, "commit_subject")
	delete(allowed, "what_changed")
	delete(allowed, "how_verified")
	delete(allowed, "tests")
	return validateGitOpsTemplateWithAllowedPlaceholders("MIVIA_GIT_OPS_CONVENTIONS_TICKET_URL_TEMPLATE", value, allowed)
}

func gitOpsTemplateAllowedPlaceholders() map[string]bool {
	return map[string]bool{
		"project_id":        true,
		"work_plan_id":      true,
		"work_task_id":      true,
		"work_task_ref":     true,
		"work_task_title":   true,
		"branch_name":       true,
		"ticket_ref":        true,
		"ticket_url":        true,
		"slug":              true,
		"automation_id":     true,
		"automation_run_id": true,
		"operator_id":       true,
		"review_refs":       true,
		"verifier_refs":     true,
		"test_results":      true,
		"commit_subject":    true,
		"change_type":       true,
		"what_changed":      true,
		"how_verified":      true,
		"tests":             true,
	}
}

func validateGitOpsTemplateWithAllowedPlaceholders(name, value string, allowed map[string]bool) error {
	for offset := 0; ; {
		start := strings.Index(value[offset:], "{{")
		if start < 0 {
			return nil
		}
		start += offset
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return fmt.Errorf("%s has an unclosed placeholder", name)
		}
		end += start + 2
		placeholder := strings.TrimSpace(value[start+2 : end])
		if !allowed[placeholder] {
			return fmt.Errorf("%s uses unknown placeholder %q", name, placeholder)
		}
		offset = end + 2
	}
}

func regexpMustMatch(pattern, value string) bool {
	return regexp.MustCompile(pattern).MatchString(strings.TrimSpace(value))
}

func validateSafeGitToken(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if strings.ContainsAny(trimmed, "\r\n\x00 ~^:?*[\\") || strings.HasPrefix(trimmed, "-") {
		return fmt.Errorf("%s must be a safe git token", name)
	}
	return nil
}

func validateSafeBranchPrefix(name, value string, allowEmpty bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%s must not be empty", name)
	}
	if strings.ContainsAny(trimmed, "\r\n\x00 ~^:?*[\\") || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "-") || strings.Contains(trimmed, "//") || strings.Contains(trimmed, "..") {
		return fmt.Errorf("%s must be a safe branch prefix", name)
	}
	return nil
}

func validateSafeBranchNamePattern(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > 512 || strings.ContainsAny(trimmed, "\r\n\x00") {
		return fmt.Errorf("%s must be a safe single-line RE2 pattern", name)
	}
	if _, err := regexp.Compile(trimmed); err != nil {
		return fmt.Errorf("%s must compile as a RE2 pattern: %w", name, err)
	}
	return nil
}

func validateOptionalAbsolutePath(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.ContainsAny(trimmed, "\r\n\x00") {
		return fmt.Errorf("%s must not contain control characters", name)
	}
	if !filepath.IsAbs(trimmed) {
		return fmt.Errorf("%s must be an absolute path", name)
	}
	return nil
}

func (trigger WorkPlanStatusTrigger) Validate() error {
	allowed := map[string]bool{
		"planned":      true,
		"active":       true,
		"blocked":      true,
		"needs_review": true,
		"done":         true,
		"failed":       true,
		"cancelled":    true,
		"superseded":   true,
	}
	for _, status := range trigger.Statuses {
		trimmed := strings.TrimSpace(status)
		if trimmed == "" {
			return errors.New("MIVIA_AUTOMATION_WORK_PLAN_STATUS_TRIGGER_STATUSES must not contain empty statuses")
		}
		if !allowed[trimmed] {
			return fmt.Errorf("MIVIA_AUTOMATION_WORK_PLAN_STATUS_TRIGGER_STATUSES contains unsupported status %q", status)
		}
	}
	return nil
}

func (workflows Workflows) Validate() error {
	for _, path := range workflows.DefinitionPaths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return errors.New("MIVIA_WORKFLOWS_DEFINITION_PATHS must not contain empty paths")
		}
		if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, ":") || strings.Contains(trimmed, "//") || strings.Contains(trimmed, "..") {
			return errors.New("MIVIA_WORKFLOWS_DEFINITION_PATHS must contain safe relative paths")
		}
	}
	return nil
}

func (durable DurableWorkflows) Validate() error {
	switch strings.TrimSpace(durable.Backend) {
	case durableWorkflowBackendMemory, durableWorkflowBackendSQLite:
	default:
		return errors.New("MIVIA_DURABLE_WORKFLOWS_BACKEND must be memory or sqlite")
	}
	if err := validateDurableWorkflowSQLitePath(durable.SQLitePath); err != nil {
		return err
	}
	if durable.WorkerEnabled && !durable.Enabled {
		return errors.New("MIVIA_DURABLE_WORKFLOWS_WORKER_ENABLED requires MIVIA_DURABLE_WORKFLOWS_ENABLED")
	}
	if durable.MaxParallelRuns <= 0 {
		return errors.New("MIVIA_DURABLE_WORKFLOWS_MAX_PARALLEL_RUNS must be positive")
	}
	return nil
}

func validateDurableWorkflowSQLitePath(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("MIVIA_DURABLE_WORKFLOWS_SQLITE_PATH must not be empty")
	}
	if filepath.IsAbs(trimmed) || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, ":") || strings.ContainsAny(trimmed, "\x00\r\n") {
		return errors.New("MIVIA_DURABLE_WORKFLOWS_SQLITE_PATH must be a safe data/ relative SQLite path")
	}
	clean := filepath.ToSlash(filepath.Clean(trimmed))
	if clean == "." || clean != trimmed || clean == "data" || !strings.HasPrefix(clean, "data/") || strings.Contains(clean, "../") || strings.HasPrefix(clean, "../") {
		return errors.New("MIVIA_DURABLE_WORKFLOWS_SQLITE_PATH must be a safe data/ relative SQLite path")
	}
	if filepath.Ext(clean) != ".sqlite" {
		return errors.New("MIVIA_DURABLE_WORKFLOWS_SQLITE_PATH must end with .sqlite")
	}
	return nil
}

func (agent AutomationAgent) Validate() error {
	if strings.TrimSpace(agent.ID) == "" {
		return errors.New("automation agents require id")
	}
	if strings.TrimSpace(agent.Purpose) == "" {
		return fmt.Errorf("automation agent %q requires purpose", agent.ID)
	}
	if agent.MaxRuntime < 0 {
		return fmt.Errorf("automation agent %q max_runtime must be non-negative", agent.ID)
	}
	if agent.MaxRetries < 0 {
		return fmt.Errorf("automation agent %q max_retries must be non-negative", agent.ID)
	}
	for _, command := range agent.AllowedCommands {
		if strings.TrimSpace(command.Command) == "" {
			return fmt.Errorf("automation agent %q allowed_commands require command", agent.ID)
		}
	}
	return nil
}

func configPathFromEnv() (string, bool) {
	value := os.Getenv("MIVIA_CONFIG_PATH")
	if value == "" {
		return defaultConfigPath, false
	}
	return value, true
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func getenvInt64(key string, fallback int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func getenvInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func getenvAutoInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	return parseAutoIntString(key, raw)
}

func parseAutoIntString(name string, raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "auto") {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be %q or a positive integer", name, "auto")
	}
	return parsed, nil
}

func getenvBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return value, nil
}

func getenvDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return value, nil
}

func isLocalAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
