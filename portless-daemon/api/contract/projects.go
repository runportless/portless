package contract

// SourceInput identifies a source tree to discover and attach to a project.
type SourceInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type DiscoverProjectRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type ProjectMutation struct {
	Project     Project     `json:"project"`
	Environment Environment `json:"environment"`
	Warnings    []string    `json:"warnings"`
}

type CreateProjectRequest struct {
	Name    string        `json:"name"`
	Sources []SourceInput `json:"sources"`
}

type AddProjectSourceRequest struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Environment string `json:"environment"`
}

type ProjectSourceMutation struct {
	ProjectMutation
	ConfigurationRequired []string `json:"configurationRequired"`
}

type RenameProjectRequest struct {
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
}

type ProjectList struct {
	Projects []Project `json:"projects"`
	Total    int       `json:"total"`
}
