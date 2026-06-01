package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

type configCheckReport struct {
	Command        string                 `json:"command"`
	Status         string                 `json:"status"`
	ConfigPath     redactedValue          `json:"config_path"`
	ErrorCategory  string                 `json:"error_category,omitempty"`
	ErrorMessage   string                 `json:"error_message,omitempty"`
	Effective      *redactedConfigSummary `json:"effective,omitempty"`
	RedactionModel string                 `json:"redaction_model"`
}

type redactedConfigSummary struct {
	HTTPAddr      redactedValue            `json:"http_addr"`
	LadybugPath   redactedValue            `json:"ladybug_path"`
	SQLitePath    redactedValue            `json:"sqlite_path"`
	Logging       redactedLoggingSummary   `json:"logging"`
	Ingestion     redactedIngestionSummary `json:"ingestion"`
	Workspace     redactedWorkspaceSummary `json:"workspace"`
	AgentActivity redactedActivitySummary  `json:"agent_activity"`
	Projects      []redactedProjectSummary `json:"projects"`
}

type redactedLoggingSummary struct {
	FileEnabled bool          `json:"file_enabled"`
	FilePath    redactedValue `json:"file_path"`
}

type redactedIngestionSummary struct {
	ContentGraphEnabled      bool   `json:"content_graph_enabled"`
	LiveUpdatesEnabled       bool   `json:"live_updates_enabled"`
	ASTExtractionEnabled     bool   `json:"ast_extraction_enabled"`
	ExtractorCacheEnabled    bool   `json:"extractor_cache_enabled"`
	QueueDepth               int    `json:"queue_depth"`
	WorkerCount              int    `json:"worker_count"`
	GlobalWorkerCount        int    `json:"global_worker_count"`
	PerProjectWorkerLimit    int    `json:"per_project_worker_limit"`
	MaxWatchedDirectoryCount int    `json:"max_watched_directory_count"`
	FullScanBatchSize        int    `json:"full_scan_batch_size"`
	InitialScanOnStart       bool   `json:"initial_scan_on_start"`
	SensitiveMarkerPolicy    string `json:"sensitive_marker_policy"`
}

type redactedWorkspaceSummary struct {
	Enabled bool `json:"enabled"`
}

type redactedActivitySummary struct {
	RetainRawPayloads bool `json:"retain_raw_payloads"`
}

type redactedProjectSummary struct {
	ID                    string                     `json:"id"`
	AliasCount            int                        `json:"alias_count"`
	Enabled               bool                       `json:"enabled"`
	Classification        string                     `json:"classification"`
	RootPath              redactedValue              `json:"root_path"`
	GraphNamespace        string                     `json:"graph_namespace"`
	GraphStorage          string                     `json:"graph_storage"`
	DigestMode            string                     `json:"digest_mode"`
	UpdatePolicy          string                     `json:"update_policy"`
	WorkspaceMode         string                     `json:"workspace_mode"`
	IncludeCount          int                        `json:"include_count"`
	ExcludeCount          int                        `json:"exclude_count"`
	FollowSymlinks        bool                       `json:"follow_symlinks"`
	SensitiveMarkerPolicy string                     `json:"sensitive_marker_policy"`
	Integrations          redactedIntegrationSummary `json:"integrations"`
}

type redactedIntegrationSummary struct {
	Jira       *redactedAtlassianIntegrationSummary `json:"jira,omitempty"`
	Confluence *redactedAtlassianIntegrationSummary `json:"confluence,omitempty"`
}

type redactedAtlassianIntegrationSummary struct {
	Enabled           bool          `json:"enabled"`
	SiteURL           redactedValue `json:"site_url"`
	CloudIDPresent    bool          `json:"cloud_id_present"`
	AuthMode          string        `json:"auth_mode"`
	CredentialRefs    redactedValue `json:"credential_refs"`
	IngestionEnabled  bool          `json:"ingestion_enabled"`
	InitialFullSync   string        `json:"initial_full_sync"`
	ProjectKeyCount   int           `json:"project_key_count,omitempty"`
	SpaceKeyCount     int           `json:"space_key_count,omitempty"`
	IncludeComments   bool          `json:"include_comments"`
	IncludeRichFields bool          `json:"include_rich_fields,omitempty"`
	IncludeBody       bool          `json:"include_body,omitempty"`
}

type redactedValue struct {
	Class   string `json:"class"`
	Present bool   `json:"present"`
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "config" {
		return false, 0
	}
	if len(args) < 2 || args[1] != "check" {
		fmt.Fprintln(stderr, "usage: mivia-server config check --config <path> --redacted-json")
		return true, 2
	}
	return true, runConfigCheck(args[2:], stdout, stderr)
}

func runConfigCheck(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("mivia-server config check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "config file path")
	redactedJSON := flags.Bool("redacted-json", false, "write a redacted JSON validation report")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*configPath) == "" || !*redactedJSON || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: mivia-server config check --config <path> --redacted-json")
		return 2
	}

	cfg, err := config.LoadPath(*configPath)
	if err == nil {
		_, err = projectregistry.NewRegistry(cfg.Projects, projectregistry.Options{
			LadybugPath:                  cfg.LadybugPath,
			SQLitePath:                   cfg.SQLitePath,
			ContentGraphEnabled:          cfg.Ingestion.ContentGraphEnabled,
			LiveUpdatesEnabled:           cfg.Ingestion.LiveUpdatesEnabled,
			ContentGraphApprovalAccepted: true,
		})
	}
	report := configCheckReport{
		Command:        "config check",
		Status:         "valid",
		ConfigPath:     redactPath(*configPath),
		RedactionModel: "classes_only_no_roots_urls_or_credential_refs",
	}
	if err != nil {
		report.Status = "invalid"
		report.ErrorCategory = configErrorCategory(err)
		report.ErrorMessage = configErrorMessage(err)
	} else {
		report.Effective = redactedSummary(cfg)
	}
	if encodeErr := json.NewEncoder(stdout).Encode(report); encodeErr != nil {
		fmt.Fprintf(stderr, "failed to write config check report: %v\n", encodeErr)
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func redactedSummary(cfg config.Config) *redactedConfigSummary {
	projects := make([]redactedProjectSummary, 0, len(cfg.Projects))
	for _, project := range cfg.Projects {
		projects = append(projects, redactedProject(project))
	}
	return &redactedConfigSummary{
		HTTPAddr:    redactURLOrAddr(cfg.HTTPAddr),
		LadybugPath: redactPath(cfg.LadybugPath),
		SQLitePath:  redactPath(cfg.SQLitePath),
		Logging: redactedLoggingSummary{
			FileEnabled: cfg.Logging.FileEnabled,
			FilePath:    redactPath(cfg.Logging.FilePath),
		},
		Ingestion: redactedIngestionSummary{
			ContentGraphEnabled:      cfg.Ingestion.ContentGraphEnabled,
			LiveUpdatesEnabled:       cfg.Ingestion.LiveUpdatesEnabled,
			ASTExtractionEnabled:     cfg.Ingestion.ASTExtractionEnabled,
			ExtractorCacheEnabled:    cfg.Ingestion.ExtractorCacheEnabled,
			QueueDepth:               cfg.Ingestion.QueueDepth,
			WorkerCount:              cfg.Ingestion.WorkerCount,
			GlobalWorkerCount:        cfg.Ingestion.GlobalWorkerCount,
			PerProjectWorkerLimit:    cfg.Ingestion.PerProjectWorkerLimit,
			MaxWatchedDirectoryCount: cfg.Ingestion.MaxWatchedDirectoryCount,
			FullScanBatchSize:        cfg.Ingestion.FullScanBatchSize,
			InitialScanOnStart:       cfg.Ingestion.InitialScanOnStart,
			SensitiveMarkerPolicy:    cfg.Ingestion.SensitiveMarkerPolicy,
		},
		Workspace:     redactedWorkspaceSummary{Enabled: cfg.Workspace.Enabled},
		AgentActivity: redactedActivitySummary{RetainRawPayloads: cfg.AgentActivity.RetainRawPayloads},
		Projects:      projects,
	}
}

func redactedProject(project config.Project) redactedProjectSummary {
	return redactedProjectSummary{
		ID:                    project.ID,
		AliasCount:            len(project.Aliases),
		Enabled:               project.Enabled,
		Classification:        project.Classification,
		RootPath:              redactPath(project.RootPath),
		GraphNamespace:        project.GraphNamespace,
		GraphStorage:          project.GraphStorage,
		DigestMode:            project.DigestMode,
		UpdatePolicy:          project.UpdatePolicy,
		WorkspaceMode:         project.WorkspaceMode,
		IncludeCount:          len(project.Include),
		ExcludeCount:          len(project.Exclude),
		FollowSymlinks:        project.FollowSymlinks,
		SensitiveMarkerPolicy: project.SensitiveMarkerPolicy,
		Integrations: redactedIntegrationSummary{
			Jira:       redactedJiraIntegration(project.Integrations.Jira),
			Confluence: redactedConfluenceIntegration(project.Integrations.Confluence),
		},
	}
}

func redactedJiraIntegration(integration *config.JiraIntegration) *redactedAtlassianIntegrationSummary {
	if integration == nil {
		return nil
	}
	summary := redactedAtlassianIntegration(integration.Enabled, integration.SiteURL, integration.CloudID, integration.AuthMode, integration.CredentialRefs, integration.Polling.IngestionEnabled, integration.Polling.InitialFullSync, integration.IncludeComments)
	summary.ProjectKeyCount = len(integration.ProjectKeys)
	summary.IncludeRichFields = integration.IncludeRichFields
	return summary
}

func redactedConfluenceIntegration(integration *config.ConfluenceIntegration) *redactedAtlassianIntegrationSummary {
	if integration == nil {
		return nil
	}
	summary := redactedAtlassianIntegration(integration.Enabled, integration.SiteURL, integration.CloudID, integration.AuthMode, integration.CredentialRefs, integration.Polling.IngestionEnabled, integration.Polling.InitialFullSync, integration.IncludeComments)
	summary.SpaceKeyCount = len(integration.SpaceKeys)
	summary.IncludeBody = integration.IncludeBody
	return summary
}

func redactedAtlassianIntegration(enabled bool, siteURL string, cloudID string, authMode string, refs config.AtlassianCredentialRefs, ingestionEnabled bool, initialFullSync string, includeComments bool) *redactedAtlassianIntegrationSummary {
	return &redactedAtlassianIntegrationSummary{
		Enabled:          enabled,
		SiteURL:          redactURLOrAddr(siteURL),
		CloudIDPresent:   strings.TrimSpace(cloudID) != "",
		AuthMode:         authMode,
		CredentialRefs:   redactCredentialRefs(refs),
		IngestionEnabled: ingestionEnabled,
		InitialFullSync:  initialFullSync,
		IncludeComments:  includeComments,
	}
}

func redactPath(value string) redactedValue {
	return redactedValue{Class: "path", Present: strings.TrimSpace(value) != ""}
}

func redactURLOrAddr(value string) redactedValue {
	if strings.TrimSpace(value) == "" {
		return redactedValue{Class: "url_or_addr", Present: false}
	}
	return redactedValue{Class: "url_or_addr", Present: true}
}

func redactCredentialRefs(refs config.AtlassianCredentialRefs) redactedValue {
	present := refs.EmailEnv != "" || refs.EmailFile != "" || refs.APITokenEnv != "" || refs.APITokenFile != "" || refs.CredentialsFile != ""
	return redactedValue{Class: "credential_ref", Present: present}
}

func configErrorCategory(err error) string {
	switch {
	case errors.Is(err, config.ErrNonLoopbackBind):
		return "invalid_bind"
	case strings.Contains(err.Error(), "MIVIA_INGESTION_"), strings.Contains(err.Error(), "ingestion"):
		return "invalid_ingestion"
	case strings.Contains(err.Error(), "workspace_mode"), strings.Contains(err.Error(), "MIVIA_WORKSPACE_"):
		return "invalid_workspace"
	case strings.Contains(err.Error(), "missing config file"):
		return "missing_config"
	default:
		return "invalid_config"
	}
}

func configErrorMessage(err error) string {
	switch configErrorCategory(err) {
	case "invalid_bind":
		return "HTTP bind address must be loopback."
	case "invalid_ingestion":
		return "Ingestion settings failed validation."
	case "invalid_workspace":
		return "Workspace settings failed validation."
	case "missing_config":
		return "Config file was not found."
	default:
		return "Config failed validation."
	}
}
