package contract

// SourceInput identifies a source tree to discover and attach to a project.
type SourceInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DiscoverProjectRequest identifies a source path and optional project name to
// discover.
type DiscoverProjectRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// ProjectMutation returns a project, its initial environment, and warnings.
type ProjectMutation struct {
	Project     Project     `json:"project"`
	Environment Environment `json:"environment"`
	Warnings    []string    `json:"warnings"`
}

// CreateProjectRequest defines a logical project from named source trees.
type CreateProjectRequest struct {
	Name    string        `json:"name"`
	Sources []SourceInput `json:"sources"`
}

// AddProjectSourceRequest defines a source to attach and the environment whose
// bindings should be updated.
type AddProjectSourceRequest struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Environment string `json:"environment"`
}

// ProjectSourceMutation returns updated project state and services requiring
// explicit provider configuration.
type ProjectSourceMutation struct {
	ProjectMutation
	ConfigurationRequired []string `json:"configurationRequired"`
}

// ProjectSourceDeletion returns the project-wide topology remaining after a
// logical source is deleted.
type ProjectSourceDeletion struct {
	Project            Project       `json:"project"`
	Environments       []Environment `json:"environments"`
	RemovedServices    []string      `json:"removedServices"`
	RemovedConnections []Connection  `json:"removedConnections"`
}

// RenameProjectRequest supplies a new project name and expected revision.
type RenameProjectRequest struct {
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
}

// ProjectList is a bounded collection of projects and its total size.
type ProjectList struct {
	Projects []Project `json:"projects"`
	Total    int       `json:"total"`
}
