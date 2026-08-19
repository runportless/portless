package client

import (
	"context"
	"net/http"
	"strconv"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

// ListProjects returns the daemon's logical projects.
func (c *Client) ListProjects(ctx context.Context, limit int) (contract.ProjectList, error) {
	var result contract.ProjectList
	err := c.do(ctx, http.MethodGet, "/api/v1/projects?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

// Project returns one logical project by name.
func (c *Client) Project(ctx context.Context, name string) (contract.Project, error) {
	var result contract.Project
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+EscapePath(name), nil, &result)
	return result, err
}

// DiscoverProject discovers a project from a source path and persists it.
func (c *Client) DiscoverProject(ctx context.Context, input contract.DiscoverProjectRequest) (contract.ProjectMutation, error) {
	var result contract.ProjectMutation
	err := c.do(ctx, http.MethodPost, "/api/v1/projects/discover", input, &result)
	return result, err
}

// CreateProject creates a logical project from explicitly named sources.
func (c *Client) CreateProject(ctx context.Context, input contract.CreateProjectRequest) (contract.ProjectMutation, error) {
	var result contract.ProjectMutation
	err := c.do(ctx, http.MethodPost, "/api/v1/projects", input, &result)
	return result, err
}

// AddProjectSource attaches and discovers another source tree for project.
func (c *Client) AddProjectSource(ctx context.Context, project string, input contract.AddProjectSourceRequest) (contract.ProjectSourceMutation, error) {
	var result contract.ProjectSourceMutation
	err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+EscapePath(project)+"/sources", input, &result)
	return result, err
}

// DeleteProjectSource removes one logical source and its owned topology from
// every environment in project.
func (c *Client) DeleteProjectSource(ctx context.Context, project, source string) (contract.ProjectSourceDeletion, error) {
	var result contract.ProjectSourceDeletion
	err := c.do(ctx, http.MethodDelete, "/api/v1/projects/"+EscapePath(project)+"/sources/"+EscapePath(source), nil, &result)
	return result, err
}

// ExportProject returns the portable declaration for project.
func (c *Client) ExportProject(ctx context.Context, project string) ([]byte, error) {
	var result []byte
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+EscapePath(project)+"/declaration", nil, &result)
	return result, err
}

// RenameProject changes a project's public name using optimistic concurrency.
func (c *Client) RenameProject(ctx context.Context, project string, input contract.RenameProjectRequest) (contract.Project, error) {
	var result contract.Project
	err := c.do(ctx, http.MethodPatch, "/api/v1/projects/"+EscapePath(project), input, &result)
	return result, err
}

// ForgetProject removes a project whose environments are safe to forget.
func (c *Client) ForgetProject(ctx context.Context, project string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/projects/"+EscapePath(project), nil, nil)
}
