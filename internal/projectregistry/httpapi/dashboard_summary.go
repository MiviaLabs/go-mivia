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
	dashboardSymbolsPageSize  = 100
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
	SampledCount       int                               `json:"sampled_count"`
	TotalCount         int                               `json:"total_count"`
	SampleTruncated    bool                              `json:"sample_truncated"`
	ConcentrationBasis dashboardSymbolConcentrationBasis `json:"concentration_basis"`
	ByKind             []dashboardCount                  `json:"by_kind"`
	ByPackage          []dashboardCount                  `json:"by_package,omitempty"`
	ByModule           []dashboardCount                  `json:"by_module,omitempty"`
	ByNamespace        []dashboardCount                  `json:"by_namespace,omitempty"`
	ByAssembly         []dashboardCount                  `json:"by_assembly,omitempty"`
	ByCodeArea         []dashboardCount                  `json:"by_code_area,omitempty"`
	ByPathBucket       []dashboardCount                  `json:"by_path_bucket,omitempty"`
	ByLanguage         []dashboardCount                  `json:"by_language,omitempty"`
	Sample             []symbolSample                    `json:"sample,omitempty"`
}

type dashboardSymbolConcentrationBasis struct {
	PrimaryField     string `json:"primary_field"`
	Source           string `json:"source"`
	Label            string `json:"label"`
	Description      string `json:"description"`
	Denominator      string `json:"denominator"`
	DenominatorCount int    `json:"denominator_count"`
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
	Name         string `json:"name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	PackageName  string `json:"package,omitempty"`
	RelativePath string `json:"relative_path,omitempty"`
	Extension    string `json:"extension,omitempty"`
	FileID       string `json:"file_id,omitempty"`
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

		indexedSymbolCount := -1
		if health, err := dashboardWithTimeout(r.Context(), func(ctx context.Context) (projectreliability.ContextHealth, error) {
			return projectreliability.NewServiceFromAPIs(registry, ingestion, workspace, projectreliability.Options{}).ContextHealth(ctx, projectID)
		}); err == nil {
			summary.ContextHealth = health
			indexedSymbolCount = health.IndexedSymbolCount
		} else {
			summary.Warnings = append(summary.Warnings, "context_health_unavailable")
			if count, countErr := dashboardWithTimeout(r.Context(), func(ctx context.Context) (int, error) {
				return ingestion.IndexedSymbolCount(ctx, projectID)
			}); countErr == nil {
				indexedSymbolCount = count
			} else {
				summary.Warnings = append(summary.Warnings, "symbol_count_unavailable")
			}
		}
		if latest, err := dashboardWithTimeout(r.Context(), func(ctx context.Context) (projectingestion.RunMetadata, error) {
			return ingestion.LatestRunMetadata(ctx, projectID)
		}); err == nil {
			summary.LatestRun = &latest
		} else {
			summary.Warnings = append(summary.Warnings, "latest_ingestion_unavailable")
		}
		if search, err := dashboardWithTimeout(r.Context(), func(ctx context.Context) (projectingestion.SearchIndexHealth, error) {
			return ingestion.SearchIndexHealth(ctx, projectID)
		}); err == nil {
			summary.Graph.SearchIndex = search
		}
		summary.Graph.Files = dashboardFiles(r.Context(), ingestion, projectID, &summary.Warnings)
		summary.Graph.Symbols = dashboardSymbols(r.Context(), ingestion, projectID, indexedSymbolCount, &summary.Warnings)
		summary.Graph.Headings = dashboardHeadings(r.Context(), ingestion, projectID, &summary.Warnings)
		if ast, err := dashboardWithTimeout(r.Context(), func(ctx context.Context) (projectingestion.ASTQueryCatalog, error) {
			return ingestion.ListASTQueries(ctx, projectID)
		}); err == nil {
			summary.Graph.ASTCoverage = ast.Coverage
		} else {
			summary.Warnings = append(summary.Warnings, "ast_coverage_unavailable")
		}
		if workspace != nil {
			summary.Workspace = dashboardWorkspace(r.Context(), workspace, projectID, &summary.Warnings)
		}
		if integrations != nil {
			summary.Integrations = dashboardIntegrations(r.Context(), integrations, projectID, &summary.Warnings)
		}

		httpserver.WriteJSON(w, http.StatusOK, summary)
	})
}

func dashboardWithTimeout[T any](parent context.Context, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(parent, dashboardSectionTimeout)
	defer cancel()
	return fn(ctx)
}

func dashboardFiles(ctx context.Context, ingestion projectingestion.API, projectID string, warnings *[]string) dashboardFileSummary {
	ctx, cancel := context.WithTimeout(ctx, dashboardSectionTimeout)
	defer cancel()
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
		if dashboardFileExtensionCountable(file) {
			byExtension[emptyKey(file.Extension)]++
		}
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

func dashboardFileExtensionCountable(file projectingestion.FileMetadata) bool {
	return file.Status == string(projectingestion.FileStatusEligible) && file.RelativePathOK
}

func dashboardSymbols(ctx context.Context, ingestion projectingestion.API, projectID string, indexedSymbolCount int, warnings *[]string) dashboardSymbolSummary {
	ctx, cancel := context.WithTimeout(ctx, dashboardSectionTimeout)
	defer cancel()
	result := dashboardSymbolSummary{}
	byKind := map[string]int{}
	byPackage := map[string]int{}
	byModule := map[string]int{}
	byNamespace := map[string]int{}
	byAssembly := map[string]int{}
	byCodeArea := map[string]int{}
	byLanguage := map[string]int{}
	moduleCount := 0
	namespaceCount := 0
	assemblyCount := 0
	packageCount := 0
	page, err := ingestion.ListSymbols(ctx, projectID, projectingestion.SymbolFilter{}, projectingestion.Pagination{PageSize: dashboardSymbolsPageSize})
	if err != nil {
		*warnings = append(*warnings, "symbols_unavailable")
		return result
	}
	symbols := page.Symbols
	if page.NextPageToken != "" {
		*warnings = append(*warnings, "symbols_sample_truncated")
	}
	namespaceByFile := csharpNamespaceByFile(symbols)
	assemblyByDir := unityAssemblyByDir(symbols)
	for _, symbol := range symbols {
		result.TotalCount++
		result.SampledCount++
		byKind[emptyKey(symbol.Kind)]++
		if symbol.PackageName != "" {
			byPackage[symbol.PackageName]++
			packageCount++
		}
		if key := symbolModuleKey(symbol); key != "" {
			byModule[key]++
			moduleCount++
		}
		if key := symbolNamespaceKey(symbol, namespaceByFile); key != "" {
			byNamespace[key]++
			namespaceCount++
		}
		if key := symbolAssemblyKey(symbol, assemblyByDir); key != "" {
			byAssembly[key]++
			assemblyCount++
		}
		if key := symbolCodeAreaKey(symbol); key != "" {
			byCodeArea[key]++
		}
		if language := symbolLanguageKey(symbol); language != "" {
			byLanguage[language]++
		}
		if len(result.Sample) < 12 {
			result.Sample = append(result.Sample, symbolSample{
				Name:         symbol.Name,
				Kind:         symbol.Kind,
				PackageName:  symbol.PackageName,
				RelativePath: symbol.RelativePath,
				Extension:    symbol.Extension,
				FileID:       symbol.FileID,
			})
		}
	}
	if indexedSymbolCount >= 0 && indexedSymbolCount > result.TotalCount {
		result.TotalCount = indexedSymbolCount
	}
	result.SampleTruncated = page.NextPageToken != ""
	result.ByKind = sortedCounts(byKind, 12)
	result.ByPackage = sortedCounts(byPackage, 12)
	result.ByModule = sortedCounts(byModule, 12)
	result.ByNamespace = sortedCounts(byNamespace, 12)
	result.ByAssembly = sortedCounts(byAssembly, 12)
	result.ByCodeArea = sortedCounts(byCodeArea, 12)
	result.ByPathBucket = result.ByCodeArea
	result.ByLanguage = sortedCounts(byLanguage, 12)
	result.ConcentrationBasis = symbolConcentrationBasis(result, moduleCount, namespaceCount, assemblyCount, packageCount, indexedSymbolCount >= 0)
	return result
}

func symbolConcentrationBasis(symbols dashboardSymbolSummary, moduleCount int, namespaceCount int, assemblyCount int, packageCount int, totalCountKnown bool) dashboardSymbolConcentrationBasis {
	if symbols.SampleTruncated && !totalCountKnown {
		return dashboardSymbolConcentrationBasis{
			PrimaryField:     "by_code_area",
			Source:           "relative_path_bucket",
			Label:            "Code area concentration",
			Description:      "Share of indexed symbols grouped by repository path bucket because no semantic module, namespace, assembly, or package metadata is available.",
			Denominator:      "indexed_symbols",
			DenominatorCount: symbols.SampledCount,
		}
	}
	candidates := []dashboardSymbolConcentrationBasis{}
	if len(symbols.ByModule) > 0 && moduleCount > 0 {
		candidates = append(candidates, dashboardSymbolConcentrationBasis{
			PrimaryField:     "by_module",
			Source:           "relative_file_module",
			Label:            "Module concentration",
			Description:      "Share of indexed JavaScript and TypeScript symbols grouped by source file module identity; this is not package-manager workspace ownership.",
			Denominator:      "indexed_js_ts_symbols",
			DenominatorCount: moduleCount,
		})
	}
	if len(symbols.ByNamespace) > 0 && namespaceCount > 0 {
		candidates = append(candidates, dashboardSymbolConcentrationBasis{
			PrimaryField:     "by_namespace",
			Source:           "csharp_package_metadata",
			Label:            "Namespace concentration",
			Description:      "Share of indexed C# symbols grouped by namespace metadata assigned during extraction.",
			Denominator:      "indexed_csharp_namespace_symbols",
			DenominatorCount: namespaceCount,
		})
	}
	if len(symbols.ByAssembly) > 0 && assemblyCount > 0 {
		candidates = append(candidates, dashboardSymbolConcentrationBasis{
			PrimaryField:     "by_assembly",
			Source:           "unity_asmdef_directory_or_predefined_path_bucket",
			Label:            "Unity assembly concentration",
			Description:      "Share of indexed Unity C# symbols grouped by the nearest parsed .asmdef name in path ancestry, or by documented Unity predefined assembly buckets inferred from Assets path segments.",
			Denominator:      "indexed_unity_csharp_symbols",
			DenominatorCount: assemblyCount,
		})
	}
	if len(symbols.ByPackage) > 0 && packageCount > 0 {
		candidates = append(candidates, dashboardSymbolConcentrationBasis{
			PrimaryField:     "by_package",
			Source:           "parsed_symbol_package",
			Label:            "Package concentration",
			Description:      "Share of indexed symbols grouped by parsed package metadata.",
			Denominator:      "indexed_symbols_with_package_metadata",
			DenominatorCount: packageCount,
		})
	}
	if len(candidates) > 0 {
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].DenominatorCount > candidates[j].DenominatorCount
		})
		return candidates[0]
	}
	return dashboardSymbolConcentrationBasis{
		PrimaryField:     "by_code_area",
		Source:           "relative_path_bucket",
		Label:            "Code area concentration",
		Description:      "Share of indexed symbols grouped by repository path bucket because no semantic module, namespace, assembly, or package metadata is available.",
		Denominator:      "indexed_symbols",
		DenominatorCount: symbols.TotalCount,
	}
}

func symbolModuleKey(symbol projectingestion.SymbolMetadata) string {
	switch strings.ToLower(strings.TrimSpace(symbol.Extension)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
		return sourceFileModuleKey(symbol.RelativePath)
	default:
		return ""
	}
}

func csharpNamespaceByFile(symbols []projectingestion.SymbolMetadata) map[string]string {
	byFile := map[string]string{}
	for _, symbol := range symbols {
		if strings.ToLower(strings.TrimSpace(symbol.Extension)) != ".cs" || symbol.Kind != string(projectingestion.SymbolKindPackage) {
			continue
		}
		if name := strings.TrimSpace(symbol.Name); name != "" {
			byFile[symbolFileKey(symbol)] = name
		}
	}
	return byFile
}

func unityAssemblyByDir(symbols []projectingestion.SymbolMetadata) map[string]string {
	byDir := map[string]string{}
	for _, symbol := range symbols {
		if symbol.Kind != string(projectingestion.SymbolKindAssembly) {
			continue
		}
		if name := strings.TrimSpace(symbol.Name); name != "" {
			byDir[pathDir(symbol.RelativePath)] = name
		}
	}
	return byDir
}

func symbolNamespaceKey(symbol projectingestion.SymbolMetadata, namespaceByFile map[string]string) string {
	if strings.ToLower(strings.TrimSpace(symbol.Extension)) != ".cs" {
		return ""
	}
	if namespace := strings.TrimSpace(symbol.PackageName); namespace != "" {
		return namespace
	}
	return strings.TrimSpace(namespaceByFile[symbolFileKey(symbol)])
}

func symbolAssemblyKey(symbol projectingestion.SymbolMetadata, assemblyByDir map[string]string) string {
	if symbol.Kind == string(projectingestion.SymbolKindAssembly) {
		return strings.TrimSpace(symbol.Name)
	}
	if strings.ToLower(strings.TrimSpace(symbol.Extension)) != ".cs" {
		return ""
	}
	if name := nearestAssemblyName(symbol.RelativePath, assemblyByDir); name != "" {
		return name
	}
	return unityPredefinedAssemblyName(symbol.RelativePath)
}

func symbolFileKey(symbol projectingestion.SymbolMetadata) string {
	if value := strings.TrimSpace(symbol.FileID); value != "" {
		return value
	}
	return strings.Trim(strings.ReplaceAll(symbol.RelativePath, "\\", "/"), "/")
}

func nearestAssemblyName(relativePath string, assemblyByDir map[string]string) string {
	relativePath = strings.Trim(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	if relativePath == "" {
		return ""
	}
	dir := pathDir(relativePath)
	for {
		if name := strings.TrimSpace(assemblyByDir[dir]); name != "" {
			return name
		}
		if dir == "" {
			return ""
		}
		dir = pathDir(dir)
	}
}

func unityPredefinedAssemblyName(relativePath string) string {
	parts := normalizedPathParts(relativePath)
	assetsIndex := pathPartIndex(parts, "assets")
	if assetsIndex < 0 {
		return ""
	}
	parts = parts[assetsIndex+1:]
	if len(parts) == 0 {
		return ""
	}
	firstpass := hasLeadingUnityFirstpassFolder(parts)
	editor := containsPathPart(parts, "editor")
	switch {
	case firstpass && editor:
		return "Assembly-CSharp-Editor-firstpass"
	case firstpass:
		return "Assembly-CSharp-firstpass"
	case editor:
		return "Assembly-CSharp-Editor"
	default:
		return "Assembly-CSharp"
	}
}

func hasLeadingUnityFirstpassFolder(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	return equalPathPart(parts[0], "plugins") ||
		equalPathPart(parts[0], "standard assets") ||
		equalPathPart(parts[0], "pro standard assets")
}

func normalizedPathParts(relativePath string) []string {
	relativePath = strings.Trim(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	if relativePath == "" {
		return nil
	}
	raw := strings.Split(relativePath, "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func pathPartIndex(parts []string, target string) int {
	for index, part := range parts {
		if equalPathPart(part, target) {
			return index
		}
	}
	return -1
}

func containsPathPart(parts []string, target string) bool {
	return pathPartIndex(parts, target) >= 0
}

func equalPathPart(part string, target string) bool {
	return strings.EqualFold(strings.TrimSpace(part), target)
}

func symbolCodeAreaKey(symbol projectingestion.SymbolMetadata) string {
	if strings.TrimSpace(symbol.RelativePath) != "" {
		if value := filePathBucketKey(symbol.RelativePath, symbol.Extension); value != "" {
			return value
		}
	}
	return ""
}

func sourceFileModuleKey(relativePath string) string {
	relativePath = strings.Trim(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	if relativePath == "" {
		return ""
	}
	extension := pathExtension(relativePath)
	if extension != "" {
		relativePath = strings.TrimSuffix(relativePath, extension)
	}
	switch {
	case strings.HasSuffix(relativePath, "/index"):
		return strings.TrimSuffix(relativePath, "/index")
	case strings.HasSuffix(relativePath, "/main"):
		return strings.TrimSuffix(relativePath, "/main")
	default:
		return relativePath
	}
}

func symbolLanguageKey(symbol projectingestion.SymbolMetadata) string {
	switch strings.ToLower(strings.TrimSpace(symbol.Extension)) {
	case ".go":
		return "Go"
	case ".py":
		return "Python"
	case ".js":
		return "JavaScript"
	case ".jsx":
		return "JSX"
	case ".ts":
		return "TypeScript"
	case ".tsx":
		return "TSX"
	case ".cs":
		return "C#"
	case ".dart":
		return "Dart"
	case ".md", ".mdx":
		return "Markdown"
	case ".yaml", ".yml":
		return "YAML"
	case ".json":
		return "JSON"
	case ".toml":
		return "TOML"
	case ".xml":
		return "XML"
	case ".sh", ".bash", ".zsh":
		return "Shell"
	case "":
		if strings.EqualFold(strings.TrimSpace(symbol.RelativePath), "Dockerfile") || strings.HasSuffix(strings.TrimSpace(symbol.RelativePath), "/Dockerfile") {
			return "Dockerfile"
		}
		return ""
	default:
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(symbol.Extension)), ".")
	}
}

func filePathBucketKey(relativePath string, extension string) string {
	relativePath = strings.Trim(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	if relativePath == "" {
		return emptyKey(extension)
	}
	parts := strings.Split(relativePath, "/")
	if len(parts) == 1 {
		if extension = strings.TrimSpace(extension); extension != "" {
			return "root " + extension
		}
		return "root"
	}
	dirs := parts[:len(parts)-1]
	if len(dirs) == 0 {
		return "root"
	}
	if len(dirs) > 2 {
		dirs = dirs[:2]
	}
	return strings.Join(dirs, "/")
}

func pathExtension(relativePath string) string {
	index := strings.LastIndex(relativePath, ".")
	if index < 0 || strings.Contains(relativePath[index:], "/") {
		return ""
	}
	return relativePath[index:]
}

func pathDir(relativePath string) string {
	relativePath = strings.Trim(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	index := strings.LastIndex(relativePath, "/")
	if index < 0 {
		return ""
	}
	return relativePath[:index]
}

func dashboardHeadings(ctx context.Context, ingestion projectingestion.API, projectID string, warnings *[]string) dashboardHeadingSummary {
	ctx, cancel := context.WithTimeout(ctx, dashboardSectionTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, dashboardSectionTimeout)
	defer cancel()
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
