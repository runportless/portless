package model

import "time"

// LogicalServiceMetadata describes reusable topology without executable configuration.
type LogicalServiceMetadata struct {
	Name      string              `json:"name"`
	Kind      ServiceKind         `json:"kind"`
	Framework string              `json:"framework,omitempty"`
	Resource  *ResourceDefinition `json:"resource,omitempty"`
	Required  bool                `json:"required"`
}

// ProjectMetadata is safe logical topology and public environment state.
type ProjectMetadata struct {
	Name           string                   `json:"name"`
	Revision       int64                    `json:"revision"`
	PrimaryService string                   `json:"primaryService,omitempty"`
	CreatedAt      time.Time                `json:"createdAt"`
	UpdatedAt      time.Time                `json:"updatedAt"`
	ServiceCount   int                      `json:"serviceCount"`
	SourceCount    int                      `json:"sourceCount"`
	Sources        []ProjectSource          `json:"sources,omitempty"`
	Services       []LogicalServiceMetadata `json:"services,omitempty"`
	Connections    []Connection             `json:"connections,omitempty"`
	Environments   []EnvironmentSummary     `json:"environments"`
}

// ProjectDeclaration is a portable safe declaration with explicit configuration omissions.
type ProjectDeclaration struct {
	SchemaVersion int       `json:"schemaVersion"`
	Project       string    `json:"project"`
	Revision      int64     `json:"revision"`
	CreatedAt     time.Time `json:"createdAt"`
	Redactions    []string  `json:"redactions"`
	ProjectModel
}
