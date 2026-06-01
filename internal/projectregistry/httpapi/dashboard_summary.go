package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

const (
	dashboardFilesPageSize    = 50
	dashboardSymbolsPageSize  = 50
	dashboardHeadingsPageSize = 50
	dashboardGitPageSize      = 25
)

type dashboardSummary struct {
	Project       projectregistry.ProjectMetadata `json:"project"`
	ContextHealth any                             `json:"context_health,omitempty"`
	LatestRun     *projectingestion.RunMetadata   `json:"latest_run,omitempty"`
	Graph         dashboardGraphSummary           `json:"graph"`
	Workspace     *dashboardWorkspaceSummary      `json:"workspace,omitempty"`
	Integrations  *dashboardIntegrationSummary    `json:"integrations,omitempty"`
	Limits        dashboardSummaryLimits          `json:"limits"`
	Warnings      []string                        `json:"warnings,omitempty"`
	CheckedAt     time.Time                       `json:"checked_at"`
}

type dashboardGraphSummary struct {
	Files       dashboardFileSummary                   `json:"files"`
	Symbols     dashboardSymbolSummary                 `json:"symbols"`
	Headings    dashboardHeadingSummary                `json:"headings"`
	ASTCoverage []projectingestion.ASTCoverageMetadata `json:"ast_coverage,omitempty"`
	SearchIndex any                                    `json:"search_index,omitempty"`
}

type dashboardFileSummary struct {
	SampledCount    int              `json:"sampled_count"`
	SampleTruncated bool             `json:"sample_truncated"`
	ByStatus        map[string]int   `json:"by_status"`
	ByExtension     []dashboardCount `json:"by_extension"`
	BySkippedReason []dashboardCount `json:"by_skipped_reason,omitempty"`
	TotalSizeBytes  int64            `json:"total_size_bytes"`
	Sample          []fileSample     `json:"sample,omitempty"`
}

type dashboardSymbolSummary struct {
	SampledCount    int              `json:"sampled_count"`
	SampleTruncated bool             `json:"sample_truncated"`
	ByKind          []dashboardCount `json:"by_kind"`
	ByPackage       []dashboardCount `json:"by_package,omitempty"`
	Sample          []symbolSample   `json:"sample,omitempty"`
}

type dashboardHeadingSummary struct {
	SampledCount    int              `json:"sampled_count"`
	SampleTruncated bool             `json:"sample_truncated"`
	ByLevel         []dashboardCount `json:"by_level,omitempty"`
	Sample          []headingSample  `json:"sample,omitempty"`
}

type dashboardWorkspaceSummary struct {
	Branch            string           `json:"branch,omitempty"`
	HeadOIDShort      string           `json:"head_oid_short,omitempty"`
	SampledDirtyCount int              `json:"sampled_dirty_count"`
	ByStatus          []dashboardCount `json:"by_status,omitempty"`
	Truncated         bool             `json:"truncated"`
	Sample            []gitSample      `json:"sample,omitempty"`
}

type dashboardIntegrationSummary struct {
	Providers []providerStatusSummary                 `json:"providers,omitempty"`
	Counts    []projectintegrations.ProviderItemCount `json:"counts,omitempty"`
}

type dashboardSummaryLimits struct {
	FilesPageSize    int `json:"files_page_size"`
	SymbolsPageSize  int `json:"symbols_page_size"`
	HeadingsPageSize int `json:"headings_page_size"`
	GitPageSize      int `json:"git_page_size"`
}

type dashboardCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type fileSample struct {
	RelativePath string `json:"relative_path,omitempty"`
	Extension    string `json:"extension,omitempty"`
	Status       string `json:"status,omitempty"`
	Present      bool   `json:"present"`
	SizeBytes    int64  `json:"size_bytes"`
}

type symbolSample struct {
	Name        string `json:"name,omitempty"`
	Kind        string `json:"kind,omitempty"`
	PackageName string `json:"package,omitempty"`
	FileID      string `json:"file_id,omitempty"`
}

type headingSample struct {
	Level  int    `json:"level"`
	FileID string `json:"file_id,omitempty"`
}

type gitSample struct {
	RelativePath string `json:"relative_path,omitempty"`
	Status       string `json:"status,omitempty"`
}

type providerStatusSummary struct {
	Provider             projectintegrations.Provider `json:"provider"`
	Configured           bool                         `json:"configured"`
	Enabled              bool                         `json:"enabled"`
	AuthMode             string                       `json:"auth_mode,omitempty"`
	CredentialSource     string                       `json:"credential_source,omitempty"`
	AllowlistKind        string                       `json:"allowlist_kind,omitempty"`
	AllowlistCount       int                          `json:"allowlist_count"`
	IngestionEnabled     bool                         `json:"ingestion_enabled"`
	SourcePersisted      bool                         `json:"source_persisted"`
	SourceAllowlistCount int                          `json:"source_allowlist_count,omitempty"`
	LastRunStatus        string                       `json:"last_run_status,omitempty"`
	LastRunItemsSeen     int                          `json:"last_run_items_seen,omitempty"`
}

func getDashboardSummaryHandler(registry *projectregistry.Registry, ingestion projectingestion.API, workspace projectworkspace.API, integrations *projectintegrations.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("id"))
		project, ok := registry.Get(projectID)
		if !ok {
			writeResult(w, nil, projectregistry.ErrProjectNotFound, http.StatusOK)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), dashboardSummaryTimeout)
		defer cancel()

		summary := dashboardSummary{
			Project: projectregistry.MetadataForProject(project),
			Limits: dashboardSummaryLimits{
				FilesPageSize:    dashboardFilesPageSize,
				SymbolsPageSize:  dashboardSymbolsPageSize,
				HeadingsPageSize: dashboardHeadingsPageSize,
				GitPageSize:      dashboardGitPageSize,
			},
			CheckedAt: time.Now().UTC(),
		}

		if health, err := projectreliability.NewServiceFromAPIs(registry, ingestion, workspace, projectreliability.Options{}).ContextHealth(ctx, projectID); err == nil {
			summary.ContextHealth = health
		} else {
			summary.Warnings = append(summary.Warnings, "context_health_unavailable")
		}
		if latest, err := ingestion.LatestRunMetadata(ctx, projectID); err == nil {
			summary.LatestRun = &latest
		} else {
			summary.Warnings = append(summary.Warnings, "latest_ingestion_unavailable")
		}
		if search, err := ingestion.SearchIndexHealth(ctx, projectID); err == nil {
			summary.Graph.SearchIndex = search
		}
		summary.Graph.Files = dashboardFiles(ctx, ingestion, projectID, &summary.Warnings)
		summary.Graph.Symbols = dashboardSymbols(ctx, ingestion, projectID, &summary.Warnings)
		summary.Graph.Headings = dashboardHeadings(ctx, ingestion, projectID, &summary.Warnings)
		if ast, err := ingestion.ListASTQueries(ctx, projectID); err == nil {
			summary.Graph.ASTCoverage = ast.Coverage
		} else {
			summary.Warnings = append(summary.Warnings, "ast_coverage_unavailable")
		}
		if workspace != nil {
			summary.Workspace = dashboardWorkspace(ctx, workspace, projectID, &summary.Warnings)
		}
		if integrations != nil {
			summary.Integrations = dashboardIntegrations(ctx, integrations, projectID, &summary.Warnings)
		}

		httpserver.WriteJSON(w, http.StatusOK, summary)
	})
}

func dashboardFiles(ctx context.Context, ingestion projectingestion.API, projectID string, warnings *[]string) dashboardFileSummary {
	result := dashboardFileSummary{
		ByStatus: make(map[string]int),
	}
	byExtension := map[string]int{}
	bySkippedReason := map[string]int{}
	page, err := ingestion.ListFiles(ctx, projectID, projectingestion.FileStateFilter{}, projectingestion.Pagination{PageSize: dashboardFilesPageSize})
	if err != nil {
		*warnings = append(*warnings, "files_unavailable")
		return result
	}
	for _, file := range page.Files {
		result.SampledCount++
		result.ByStatus[emptyKey(file.Status)]++
		byExtension[emptyKey(file.Extension)]++
		if file.SkippedReason != "" {
			bySkippedReason[file.SkippedReason]++
		}
		result.TotalSizeBytes += file.SizeBytes
		if len(result.Sample) < 12 {
			result.Sample = append(result.Sample, fileSample{
				RelativePath: file.RelativePath,
				Extension:    file.Extension,
				Status:       file.Status,
				Present:      file.Present,
				SizeBytes:    file.SizeBytes,
			})
		}
	}
	result.SampleTruncated = page.NextPageToken != ""
	result.ByExtension = sortedCounts(byExtension, 12)
	result.BySkippedReason = sortedCounts(bySkippedReason, 12)
	return result
}

func dashboardSymbols(ctx context.Context, ingestion projectingestion.API, projectID string, warnings *[]string) dashboardSymbolSummary {
	result := dashboardSymbolSummary{}
	byKind := map[string]int{}
	byPackage := map[string]int{}
	page, err := ingestion.ListSymbols(ctx, projectID, projectingestion.SymbolFilter{}, projectingestion.Pagination{PageSize: dashboardSymbolsPageSize})
	if err != nil {
		*warnings = append(*warnings, "symbols_unavailable")
		return result
	}
	for _, symbol := range page.Symbols {
		result.SampledCount++
		byKind[emptyKey(symbol.Kind)]++
		if symbol.PackageName != "" {
			byPackage[symbol.PackageName]++
		}
		if len(result.Sample) < 12 {
			result.Sample = append(result.Sample, symbolSample{Name: symbol.Name, Kind: symbol.Kind, PackageName: symbol.PackageName, FileID: symbol.FileID})
		}
	}
	result.SampleTruncated = page.NextPageToken != ""
	result.ByKind = sortedCounts(byKind, 12)
	result.ByPackage = sortedCounts(byPackage, 12)
	return result
}

func dashboardHeadings(ctx context.Context, ingestion projectingestion.API, projectID string, warnings *[]string) dashboardHeadingSummary {
	result := dashboardHeadingSummary{}
	byLevel := map[string]int{}
	page, err := ingestion.ListHeadings(ctx, projectID, "", projectingestion.Pagination{PageSize: dashboardHeadingsPageSize})
	if err != nil {
		*warnings = append(*warnings, "headings_unavailable")
		return result
	}
	for _, heading := range page.Headings {
		result.SampledCount++
		byLevel[strconv.Itoa(heading.Level)]++
		if len(result.Sample) < 8 {
			result.Sample = append(result.Sample, headingSample{Level: heading.Level, FileID: heading.FileID})
		}
	}
	result.SampleTruncated = page.NextPageToken != ""
	result.ByLevel = sortedCounts(byLevel, 8)
	return result
}

func dashboardWorkspace(ctx context.Context, workspace projectworkspace.API, projectID string, warnings *[]string) *dashboardWorkspaceSummary {
	status, err := workspace.GitStatus(ctx, projectID, projectworkspace.GitStatusOptions{IncludeUntracked: true, PageSize: dashboardGitPageSize})
	if err != nil {
		*warnings = append(*warnings, "workspace_git_unavailable")
		return nil
	}
	byStatus := map[string]int{}
	result := &dashboardWorkspaceSummary{
		Branch:            status.Branch,
		HeadOIDShort:      status.HeadOIDShort,
		SampledDirtyCount: len(status.Entries),
		Truncated:         status.Truncated,
	}
	for _, entry := range status.Entries {
		byStatus[emptyKey(entry.Status)]++
		if len(result.Sample) < 12 {
			result.Sample = append(result.Sample, gitSample{RelativePath: entry.RelativePath, Status: entry.Status})
		}
	}
	result.ByStatus = sortedCounts(byStatus, 12)
	return result
}

func dashboardIntegrations(ctx context.Context, integrations *projectintegrations.Service, projectID string, warnings *[]string) *dashboardIntegrationSummary {
	result := &dashboardIntegrationSummary{}
	providers, err := integrations.ListProviders(projectID)
	if err != nil {
		*warnings = append(*warnings, "integrations_unavailable")
		return result
	}
	for _, provider := range providers {
		status, err := integrations.Status(ctx, projectID, provider.Provider)
		if err != nil {
			*warnings = append(*warnings, "integration_status_unavailable")
			continue
		}
		item := providerStatusSummary{
			Provider:             status.Provider,
			Configured:           status.Configured,
			Enabled:              status.Enabled,
			AuthMode:             status.AuthMode,
			CredentialSource:     status.CredentialSource,
			AllowlistKind:        status.AllowlistKind,
			AllowlistCount:       status.AllowlistCount,
			IngestionEnabled:     status.IngestionEnabled,
			SourcePersisted:      status.SourcePersisted,
			SourceAllowlistCount: status.SourceAllowlistCount,
		}
		if status.LastRun != nil {
			item.LastRunStatus = string(status.LastRun.Status)
			item.LastRunItemsSeen = status.LastRun.ItemsSeen
		}
		result.Providers = append(result.Providers, item)
	}
	if counts, err := integrations.Counts(ctx, projectID); err == nil {
		result.Counts = counts.Counts
	}
	return result
}

func sortedCounts(values map[string]int, limit int) []dashboardCount {
	counts := make([]dashboardCount, 0, len(values))
	for key, count := range values {
		counts = append(counts, dashboardCount{Key: key, Count: count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return counts[i].Key < counts[j].Key
	})
	if limit > 0 && len(counts) > limit {
		return counts[:limit]
	}
	return counts
}

func emptyKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
