package projectregistry

import "errors"

const (
	DigestModeMetadataOnly        = "metadata_only"
	DigestModeContentGraph        = "content_graph"
	UpdatePolicyManual            = "manual"
	UpdatePolicyLive              = "live"
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
	DigestMode            string
	UpdatePolicy          string
	Include               []string
	Exclude               []string
	FollowSymlinks        bool
	MaxFileBytes          int64
	MaxChunkBytes         int
	SensitiveMarkerPolicy string
	ValidationStatus      string
	ValidationError       string
}

type ProjectMetadata struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description,omitempty"`
	Enabled          bool   `json:"enabled"`
	Classification   string `json:"classification"`
	GraphNamespace   string `json:"graph_namespace"`
	DigestMode       string `json:"digest_mode"`
	UpdatePolicy     string `json:"update_policy"`
	ValidationStatus string `json:"validation_status"`
	ValidationError  string `json:"validation_error,omitempty"`
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
	return project
}

func MetadataForProject(project Project) ProjectMetadata {
	return ProjectMetadata{
		ID:               project.ID,
		DisplayName:      project.DisplayName,
		Description:      project.Description,
		Enabled:          project.Enabled,
		Classification:   project.Classification,
		GraphNamespace:   project.GraphNamespace,
		DigestMode:       project.DigestMode,
		UpdatePolicy:     project.UpdatePolicy,
		ValidationStatus: project.ValidationStatus,
		ValidationError:  project.ValidationError,
	}
}

func MetadataForProjects(projects []Project) []ProjectMetadata {
	metadata := make([]ProjectMetadata, 0, len(projects))
	for _, project := range projects {
		metadata = append(metadata, MetadataForProject(project))
	}
	return metadata
}
