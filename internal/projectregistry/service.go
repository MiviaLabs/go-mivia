package projectregistry

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

var projectIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Options struct {
	LadybugPath                  string
	SQLitePath                   string
	ContentGraphEnabled          bool
	LiveUpdatesEnabled           bool
	ContentGraphApprovalAccepted bool
}

func NewRegistry(configProjects []config.Project, options Options) (*Registry, error) {
	projects := make([]Project, 0, len(configProjects))
	seenIDs := make(map[string]struct{}, len(configProjects))
	seenNamespaces := make(map[string]struct{}, len(configProjects))

	for i, configProject := range configProjects {
		project, err := normalizeProject(configProject, options)
		if err != nil {
			return nil, fmt.Errorf("project[%d]: %w", i, err)
		}
		if _, ok := seenIDs[project.ID]; ok {
			return nil, fmt.Errorf("project[%d].id %q is duplicated", i, project.ID)
		}
		seenIDs[project.ID] = struct{}{}
		if _, ok := seenNamespaces[project.GraphNamespace]; ok {
			return nil, fmt.Errorf("project[%d].graph_namespace %q is duplicated", i, project.GraphNamespace)
		}
		seenNamespaces[project.GraphNamespace] = struct{}{}
		project.Aliases = projectLookupAliases(project)
		projects = append(projects, project)
	}

	byID := make(map[string]Project, len(projects))
	for _, project := range projects {
		registerProjectLookup(byID, project.ID, project)
		for _, alias := range project.Aliases {
			registerProjectLookup(byID, alias, project)
		}
	}
	return &Registry{projects: cloneProjects(projects), byID: byID}, nil
}

func registerProjectLookup(byID map[string]Project, id string, project Project) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if _, exists := byID[id]; !exists {
		byID[id] = cloneProject(project)
	}
	lowerID := strings.ToLower(id)
	if _, exists := byID[lowerID]; !exists {
		byID[lowerID] = cloneProject(project)
	}
}

func projectLookupAliases(project Project) []string {
	aliases := append([]string(nil), project.Aliases...)
	if project.Enabled && project.CanonicalRootPath != "" {
		if modulePath := goModulePath(filepath.Join(project.CanonicalRootPath, "go.mod")); modulePath != "" {
			aliases = append(aliases, modulePath)
		}
	}
	return normalizeProjectAliases(aliases)
}

func normalizeProjectAliases(aliases []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || strings.ContainsRune(alias, '\x00') {
			continue
		}
		key := strings.ToLower(alias)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, alias)
	}
	return normalized
}

func goModulePath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		modulePath := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		if strings.ContainsRune(modulePath, '\x00') {
			return ""
		}
		return modulePath
	}
	return ""
}

func normalizeProject(configProject config.Project, options Options) (Project, error) {
	project := Project{
		ID:                    strings.TrimSpace(configProject.ID),
		Aliases:               append([]string(nil), configProject.Aliases...),
		DisplayName:           strings.TrimSpace(configProject.DisplayName),
		Description:           strings.TrimSpace(configProject.Description),
		RootPath:              configProject.RootPath,
		Enabled:               configProject.Enabled,
		Classification:        strings.TrimSpace(configProject.Classification),
		GraphNamespace:        strings.TrimSpace(configProject.GraphNamespace),
		GraphStorage:          strings.TrimSpace(configProject.GraphStorage),
		DigestMode:            strings.TrimSpace(configProject.DigestMode),
		UpdatePolicy:          strings.TrimSpace(configProject.UpdatePolicy),
		WorkspaceMode:         strings.TrimSpace(configProject.WorkspaceMode),
		Include:               append([]string(nil), configProject.Include...),
		Exclude:               append([]string(nil), configProject.Exclude...),
		FollowSymlinks:        configProject.FollowSymlinks,
		MaxFileBytes:          configProject.MaxFileBytes,
		MaxChunkBytes:         configProject.MaxChunkBytes,
		SensitiveMarkerPolicy: strings.TrimSpace(configProject.SensitiveMarkerPolicy),
		Integrations:          integrationMetadata(configProject.Integrations),
	}

	if project.Classification == "" {
		project.Classification = ClassificationInternal
	}
	if project.GraphNamespace == "" {
		project.GraphNamespace = project.ID
	}
	if project.GraphStorage == "" {
		project.GraphStorage = GraphStoragePersistent
	}
	if project.DigestMode == "" {
		project.DigestMode = DigestModeMetadataOnly
	}
	if project.UpdatePolicy == "" {
		project.UpdatePolicy = UpdatePolicyManual
	}
	if project.WorkspaceMode == "" {
		project.WorkspaceMode = WorkspaceModeDisabled
	}
	if project.SensitiveMarkerPolicy == "" {
		project.SensitiveMarkerPolicy = SensitiveMarkerPolicySkipFile
	}

	if err := validateProject(project, options); err != nil {
		return Project{}, err
	}
	cleanRootPath, canonicalRootPath, err := validateRootPath(project.RootPath, project.Enabled)
	if err != nil {
		return Project{}, err
	}
	project.RootPath = cleanRootPath
	project.CanonicalRootPath = canonicalRootPath
	project.Exclude = mergeExcludePatterns(project.RootPath, project.Exclude, options.LadybugPath, options.SQLitePath)
	if err := validatePatterns("exclude", project.Exclude); err != nil {
		return Project{}, err
	}
	project.ValidationStatus = ValidationStatusValid
	return project, nil
}

func integrationMetadata(integrations config.IntegrationConfig) ProjectIntegrationsMetadata {
	metadata := ProjectIntegrationsMetadata{}
	if integrations.Jira != nil {
		metadata.Jira = &ProjectIntegrationProviderMetadata{
			Enabled:          integrations.Jira.Enabled,
			AuthMode:         strings.TrimSpace(integrations.Jira.AuthMode),
			CredentialSource: credentialSource(integrations.Jira.CredentialRefs),
			ProjectKeyCount:  len(integrations.Jira.ProjectKeys),
			IngestionEnabled: integrations.Jira.Polling.IngestionEnabled,
		}
	}
	if integrations.Confluence != nil {
		metadata.Confluence = &ProjectIntegrationProviderMetadata{
			Enabled:          integrations.Confluence.Enabled,
			AuthMode:         strings.TrimSpace(integrations.Confluence.AuthMode),
			CredentialSource: credentialSource(integrations.Confluence.CredentialRefs),
			SpaceKeyCount:    len(integrations.Confluence.SpaceKeys),
			IngestionEnabled: integrations.Confluence.Polling.IngestionEnabled,
		}
	}
	return metadata
}

func credentialSource(refs config.AtlassianCredentialRefs) string {
	credentialsFile := strings.TrimSpace(refs.CredentialsFile) != ""
	emailEnv := strings.TrimSpace(refs.EmailEnv) != ""
	emailFile := strings.TrimSpace(refs.EmailFile) != ""
	tokenEnv := strings.TrimSpace(refs.APITokenEnv) != ""
	tokenFile := strings.TrimSpace(refs.APITokenFile) != ""
	switch {
	case credentialsFile && !emailEnv && !emailFile && !tokenEnv && !tokenFile:
		return "file"
	case emailEnv && tokenEnv && !emailFile && !tokenFile:
		return "env"
	case emailFile && tokenFile && !emailEnv && !tokenEnv:
		return "file"
	case (emailEnv || emailFile) && (tokenEnv || tokenFile):
		return "mixed"
	default:
		return "none"
	}
}

func validateProject(project Project, options Options) error {
	if project.ID == "" {
		return fmt.Errorf("id must not be empty")
	}
	if !projectIDPattern.MatchString(project.ID) {
		return fmt.Errorf("id must match %s", projectIDPattern.String())
	}
	if project.DisplayName == "" {
		return fmt.Errorf("display_name must not be empty")
	}
	if strings.ContainsRune(project.DisplayName, '\x00') ||
		strings.ContainsRune(project.Description, '\x00') ||
		strings.ContainsRune(project.Classification, '\x00') ||
		strings.ContainsRune(project.GraphNamespace, '\x00') {
		return fmt.Errorf("text fields must not contain NUL bytes")
	}
	if project.GraphNamespace == "" {
		return fmt.Errorf("graph_namespace must not be empty")
	}
	if !projectIDPattern.MatchString(project.GraphNamespace) {
		return fmt.Errorf("graph_namespace must match %s", projectIDPattern.String())
	}
	switch project.GraphStorage {
	case GraphStoragePersistent, GraphStorageInMemory:
	default:
		return fmt.Errorf("graph_storage must be %q or %q", GraphStoragePersistent, GraphStorageInMemory)
	}
	switch project.DigestMode {
	case DigestModeMetadataOnly:
	case DigestModeContentGraph:
		if !options.ContentGraphEnabled {
			return fmt.Errorf("digest_mode %q requires content graph to be enabled", DigestModeContentGraph)
		}
		if !options.ContentGraphApprovalAccepted {
			return fmt.Errorf("digest_mode %q requires accepted ADR-0007 owner approvals", DigestModeContentGraph)
		}
	default:
		return fmt.Errorf("digest_mode must be %q or %q", DigestModeMetadataOnly, DigestModeContentGraph)
	}
	switch project.UpdatePolicy {
	case UpdatePolicyManual:
	case UpdatePolicyLive:
		if project.DigestMode != DigestModeContentGraph {
			return fmt.Errorf("update_policy %q requires digest_mode %q", UpdatePolicyLive, DigestModeContentGraph)
		}
		if !options.LiveUpdatesEnabled {
			return fmt.Errorf("update_policy %q requires live updates to be enabled", UpdatePolicyLive)
		}
	default:
		return fmt.Errorf("update_policy must be %q or %q", UpdatePolicyManual, UpdatePolicyLive)
	}
	switch project.WorkspaceMode {
	case WorkspaceModeDisabled:
	case WorkspaceModeReadOnly, WorkspaceModeEdit:
		if project.DigestMode != DigestModeContentGraph {
			return fmt.Errorf("workspace_mode %q requires digest_mode %q", project.WorkspaceMode, DigestModeContentGraph)
		}
	default:
		return fmt.Errorf("workspace_mode must be %q, %q, or %q", WorkspaceModeDisabled, WorkspaceModeReadOnly, WorkspaceModeEdit)
	}
	if project.FollowSymlinks {
		return fmt.Errorf("follow_symlinks must be false until symlink traversal is approved")
	}
	if project.MaxFileBytes < 0 {
		return fmt.Errorf("max_file_bytes must not be negative")
	}
	if project.MaxChunkBytes < 0 {
		return fmt.Errorf("max_chunk_bytes must not be negative")
	}
	if project.SensitiveMarkerPolicy != SensitiveMarkerPolicySkipFile {
		return fmt.Errorf("sensitive_marker_policy must be %q", SensitiveMarkerPolicySkipFile)
	}
	if err := validatePatterns("include", project.Include); err != nil {
		return err
	}
	if err := validatePatterns("exclude", project.Exclude); err != nil {
		return err
	}
	return nil
}

func validateRootPath(rootPath string, enabled bool) (string, string, error) {
	if rootPath == "" {
		return "", "", fmt.Errorf("root_path must not be empty")
	}
	if strings.ContainsRune(rootPath, '\x00') {
		return "", "", fmt.Errorf("root_path must not contain NUL bytes")
	}
	if containsDotDotPathSegment(rootPath) {
		return "", "", fmt.Errorf("root_path must not contain path traversal")
	}
	if !filepath.IsAbs(rootPath) {
		return "", "", fmt.Errorf("root_path must be absolute")
	}
	cleanRootPath := filepath.Clean(rootPath)
	if !enabled {
		return cleanRootPath, "", nil
	}

	info, err := os.Stat(cleanRootPath)
	if err != nil {
		return "", "", fmt.Errorf("root_path must exist: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("root_path must be a directory")
	}
	canonicalRootPath, err := filepath.EvalSymlinks(cleanRootPath)
	if err != nil {
		return "", "", fmt.Errorf("root_path symlink check failed: %w", err)
	}
	canonicalRootPath = filepath.Clean(canonicalRootPath)
	if canonicalRootPath != cleanRootPath {
		return "", "", fmt.Errorf("root_path must not resolve through a symlink")
	}
	return cleanRootPath, canonicalRootPath, nil
}

func containsDotDotPathSegment(pathValue string) bool {
	for _, part := range strings.FieldsFunc(pathValue, func(r rune) bool {
		return r == '/' || r == filepath.Separator
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}
