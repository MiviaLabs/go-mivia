package projectregistry

import "errors"

const (
	DigestModeMetadataOnly        = "metadata_only"
	DigestModeContentGraph        = "content_graph"
	UpdatePolicyManual            = "manual"
	UpdatePolicyLive              = "live"
	WorkspaceModeDisabled         = "disabled"
	WorkspaceModeReadOnly         = "read_only"
	WorkspaceModeEdit             = "edit"
	GraphStoragePersistent        = "persistent"
	GraphStorageInMemory          = "in_memory"
	ClassificationInternal        = "internal"
	SensitiveMarkerPolicySkipFile = "skip_file"
	ValidationStatusValid         = "valid"
)

var (
	ErrProjectNotFound = errors.New("project not found")
	ErrInvalidInput    = errors.New("invalid project input")
)

type Project struct {
	ID                    string
	DisplayName           string
	Description           string
	RootPath              string
	CanonicalRootPath     string
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
	Integrations          ProjectIntegrationsMetadata
	ValidationStatus      string
	ValidationError       string
}

type ProjectMetadata struct {
	ID               string                       `json:"id"`
	DisplayName      string                       `json:"display_name"`
	Description      string                       `json:"description,omitempty"`
	Enabled          bool                         `json:"enabled"`
	Classification   string                       `json:"classification"`
	GraphNamespace   string                       `json:"graph_namespace"`
	GraphStorage     string                       `json:"graph_storage"`
	DigestMode       string                       `json:"digest_mode"`
	UpdatePolicy     string                       `json:"update_policy"`
	WorkspaceMode    string                       `json:"workspace_mode"`
	ValidationStatus string                       `json:"validation_status"`
	ValidationError  string                       `json:"validation_error,omitempty"`
	Integrations     *ProjectIntegrationsMetadata `json:"integrations,omitempty"`
}

type ProjectIntegrationsMetadata struct {
	Jira       *ProjectIntegrationProviderMetadata `json:"jira,omitempty"`
	Confluence *ProjectIntegrationProviderMetadata `json:"confluence,omitempty"`
}

type ProjectIntegrationProviderMetadata struct {
	Enabled          bool   `json:"enabled"`
	AuthMode         string `json:"auth_mode,omitempty"`
	CredentialSource string `json:"credential_source,omitempty"`
	ProjectKeyCount  int    `json:"project_key_count,omitempty"`
	SpaceKeyCount    int    `json:"space_key_count,omitempty"`
	IngestionEnabled bool   `json:"ingestion_enabled"`
}

type Registry struct {
	projects []Project
	byID     map[string]Project
}

func (registry *Registry) List() []Project {
	if registry == nil {
		return nil
	}
	return cloneProjects(registry.projects)
}

func (registry *Registry) Get(id string) (Project, bool) {
	if registry == nil {
		return Project{}, false
	}
	project, ok := registry.byID[id]
	return cloneProject(project), ok
}

func cloneProjects(projects []Project) []Project {
	cloned := make([]Project, 0, len(projects))
	for _, project := range projects {
		cloned = append(cloned, cloneProject(project))
	}
	return cloned
}

func cloneProject(project Project) Project {
	project.Include = append([]string(nil), project.Include...)
	project.Exclude = append([]string(nil), project.Exclude...)
	project.Integrations = cloneProjectIntegrationsMetadata(project.Integrations)
	return project
}

func cloneProjectIntegrationsMetadata(metadata ProjectIntegrationsMetadata) ProjectIntegrationsMetadata {
	cloned := ProjectIntegrationsMetadata{}
	if metadata.Jira != nil {
		jira := *metadata.Jira
		cloned.Jira = &jira
	}
	if metadata.Confluence != nil {
		confluence := *metadata.Confluence
		cloned.Confluence = &confluence
	}
	return cloned
}

func MetadataForProject(project Project) ProjectMetadata {
	return ProjectMetadata{
		ID:               project.ID,
		DisplayName:      project.DisplayName,
		Description:      project.Description,
		Enabled:          project.Enabled,
		Classification:   project.Classification,
		GraphNamespace:   project.GraphNamespace,
		GraphStorage:     project.GraphStorage,
		DigestMode:       project.DigestMode,
		UpdatePolicy:     project.UpdatePolicy,
		WorkspaceMode:    project.WorkspaceMode,
		ValidationStatus: project.ValidationStatus,
		ValidationError:  project.ValidationError,
		Integrations:     metadataIntegrations(project.Integrations),
	}
}

func metadataIntegrations(integrations ProjectIntegrationsMetadata) *ProjectIntegrationsMetadata {
	if integrations.Jira == nil && integrations.Confluence == nil {
		return nil
	}
	cloned := cloneProjectIntegrationsMetadata(integrations)
	return &cloned
}

func MetadataForProjects(projects []Project) []ProjectMetadata {
	metadata := make([]ProjectMetadata, 0, len(projects))
	for _, project := range projects {
		metadata = append(metadata, MetadataForProject(project))
	}
	return metadata
}

func ProjectIncludesRelativePath(project Project, relativePath string) bool {
	return includedByPatterns(project.Include, relativePath) && !matchesAnyPattern(project.Exclude, relativePath)
}

func ProjectMayIncludeRelativePath(project Project, relativePath string) bool {
	return mayIncludeByPatterns(project.Include, relativePath) && !matchesAnyPattern(project.Exclude, relativePath)
}

func ProjectExcludesRelativePath(project Project, relativePath string) bool {
	return matchesAnyPattern(project.Exclude, relativePath)
}
