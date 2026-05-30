package projectregistry

const (
	DigestModeMetadataOnly = "metadata_only"
	UpdatePolicyManual     = "manual"
	ClassificationInternal = "internal"
	ValidationStatusValid  = "valid"
)

type Project struct {
	ID                string
	DisplayName       string
	Description       string
	RootPath          string
	CanonicalRootPath string
	Enabled           bool
	Classification    string
	GraphNamespace    string
	DigestMode        string
	UpdatePolicy      string
	Include           []string
	Exclude           []string
	FollowSymlinks    bool
	ValidationStatus  string
	ValidationError   string
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
