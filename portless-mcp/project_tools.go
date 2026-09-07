package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"sort"
	"strings"
	"time"
)

type projectInput struct {
	Project string `json:"project"`
}
type projectListInput struct {
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}
type exportProjectInput struct {
	projectInput
	Offset            int       `json:"offset,omitempty"`
	Limit             int       `json:"limit,omitempty" jsonschema:"UTF-8 byte count; default and maximum 32768"`
	ExpectedRevision  int64     `json:"expectedRevision,omitempty"`
	ExpectedCreatedAt time.Time `json:"expectedCreatedAt,omitempty"`
}

func (r *runtime) registerProjectInspectionTools(server *mcp.Server) {
	registerTool(r, server, readTool("portless_list_projects", "List safe project summaries with only environments visible in the immutable startup scope."), r.listProjects)
	registerTool(r, server, readTool("portless_get_project", "Inspect safe shared logical topology, named sources, and visible environments. Executable configuration and hidden checkout paths are excluded."), r.getProject)
	registerTool(r, server, readTool("portless_export_project", "Read identity-bound chunks of a safe project declaration. Reassemble all UTF-8 chunks before parsing; redactions document omitted configuration, including executable arguments and discovered values."), r.exportProject)
}

func (r *runtime) selectProject(ctx context.Context, name string, sharedMutation bool) (selectedEnvironment, error) {
	if err := contract.ValidateProjectName(name); err != nil {
		return selectedEnvironment{}, codedError{code: "INVALID_PROJECT", message: err.Error()}
	}
	api, err := r.gateway.client(ctx)
	if err != nil {
		return selectedEnvironment{}, err
	}
	allowed := r.config.AllEnvironments || (r.config.Project != "" && strings.EqualFold(r.config.Project, name))
	if !allowed && !sharedMutation && r.config.Project == "" && !r.config.AllEnvironments {
		if r.config.Environment != "" {
			project, _, err := parseEnvironmentSelector(r.config.Environment)
			if err != nil {
				return selectedEnvironment{}, err
			}
			allowed = strings.EqualFold(project, name)
		} else {
			visible, err := api.EnvironmentsForPath(ctx, r.config.WorkspaceRoot)
			if err != nil {
				return selectedEnvironment{}, err
			}
			for _, environment := range visible.Environments {
				if strings.EqualFold(environment.Project, name) {
					allowed = true
					break
				}
			}
		}
	}
	if !allowed {
		return selectedEnvironment{}, codedError{code: "SCOPE_DENIED", message: "project access requires an authorized project; shared topology changes require project or installation scope"}
	}
	return selectedEnvironment{client: api, project: name}, nil
}

func projectCall[T any](ctx context.Context, r *runtime, name string, mutation, sharedMutation bool, call func(context.Context, selectedEnvironment) (T, error)) (*mcp.CallToolResult, scopedResult[T], error) {
	var output scopedResult[T]
	release, err := r.enter(ctx, mutation)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	duration := readTimeout
	if mutation {
		duration = 130 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	run := func() error {
		selected, err := r.selectProject(ctx, name, sharedMutation)
		if err != nil {
			return err
		}
		value, err := call(ctx, selected)
		if err != nil {
			return err
		}
		output = scopedResult[T]{Project: selected.project, UntrustedData: true, Result: value}
		return nil
	}
	if mutation {
		err = run()
	} else {
		err = r.retryRead(run)
	}
	if err != nil {
		return nil, scopedResult[T]{}, r.toolError(err)
	}
	if err := r.checkOutput(output); err != nil {
		return nil, scopedResult[T]{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) visibleProjectMetadata(ctx context.Context, selected selectedEnvironment, value contract.ProjectMetadata) (contract.ProjectMetadata, error) {
	if r.config.AllEnvironments || r.config.Project != "" {
		return value, nil
	}
	allowed := map[string]bool{}
	if r.config.Environment != "" {
		_, environment, err := parseEnvironmentSelector(r.config.Environment)
		if err != nil {
			return contract.ProjectMetadata{}, err
		}
		allowed[strings.ToLower(environment)] = true
	} else {
		visible, err := selected.client.EnvironmentsForPath(ctx, r.config.WorkspaceRoot)
		if err != nil {
			return contract.ProjectMetadata{}, err
		}
		for _, environment := range visible.Environments {
			if strings.EqualFold(environment.Project, value.Name) {
				allowed[strings.ToLower(environment.Name)] = true
			}
		}
	}
	filtered := value.Environments[:0]
	for _, environment := range value.Environments {
		if !allowed[strings.ToLower(environment.Name)] {
			continue
		}
		if !allowed[strings.ToLower(environment.ClonedFrom)] {
			environment.ClonedFrom = ""
		}
		filtered = append(filtered, environment)
	}
	value.Environments = filtered
	if len(filtered) == 0 {
		return contract.ProjectMetadata{}, codedError{code: "SCOPE_DENIED", message: "project no longer contains an environment visible to this server"}
	}
	return value, nil
}

func (r *runtime) getProject(ctx context.Context, _ *mcp.CallToolRequest, input projectInput) (*mcp.CallToolResult, scopedResult[contract.ProjectMetadata], error) {
	return projectCall(ctx, r, input.Project, false, false, func(ctx context.Context, selected selectedEnvironment) (contract.ProjectMetadata, error) {
		value, err := selected.client.ProjectMetadata(ctx, selected.project)
		if err != nil {
			return value, err
		}
		return r.visibleProjectMetadata(ctx, selected, value)
	})
}

func (r *runtime) exportProject(ctx context.Context, _ *mcp.CallToolRequest, input exportProjectInput) (*mcp.CallToolResult, scopedResult[contract.ProjectDeclarationChunk], error) {
	return projectCall(ctx, r, input.Project, false, false, func(ctx context.Context, selected selectedEnvironment) (contract.ProjectDeclarationChunk, error) {
		var result contract.ProjectDeclarationChunk
		limit, err := bounded(input.Limit, 32<<10, 32<<10, "limit")
		if err != nil {
			return result, err
		}
		if input.Offset < 0 || (input.Offset > 0 && (input.ExpectedRevision < 1 || input.ExpectedCreatedAt.IsZero())) {
			return result, codedError{code: "INVALID_ARGUMENT", message: "continuation requires a non-negative offset, expectedRevision, and expectedCreatedAt"}
		}
		return selected.client.ProjectDeclarationChunk(ctx, selected.project, contract.ProjectDeclarationQuery{Offset: input.Offset, Limit: limit, ExpectedRevision: input.ExpectedRevision, ExpectedCreatedAt: input.ExpectedCreatedAt})
	})
}

func (r *runtime) listProjects(ctx context.Context, _ *mcp.CallToolRequest, input projectListInput) (*mcp.CallToolResult, scopedResult[contract.ProjectMetadataList], error) {
	var output scopedResult[contract.ProjectMetadataList]
	release, err := r.enter(ctx, false)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	limit, err := bounded(input.Limit, 100, 500, "limit")
	if err != nil {
		return nil, output, r.toolError(err)
	}
	if input.Offset < 0 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "offset must be non-negative"})
	}
	ctx, cancel := r.readContext(ctx)
	defer cancel()
	err = r.retryRead(func() error {
		api, err := r.gateway.client(ctx)
		if err != nil {
			return err
		}
		result := contract.ProjectMetadataList{Projects: []contract.ProjectMetadata{}}
		if r.config.AllEnvironments {
			result, err = api.ListProjectMetadata(ctx, input.Offset, limit)
			if err != nil {
				return err
			}
		} else {
			names := []string{}
			switch {
			case r.config.Project != "":
				names = append(names, r.config.Project)
			case r.config.Environment != "":
				project, _, err := parseEnvironmentSelector(r.config.Environment)
				if err != nil {
					return err
				}
				names = append(names, project)
			default:
				visible, err := api.EnvironmentsForPath(ctx, r.config.WorkspaceRoot)
				if err != nil {
					return err
				}
				seen := map[string]bool{}
				for _, environment := range visible.Environments {
					if !seen[environment.Project] {
						names = append(names, environment.Project)
						seen[environment.Project] = true
					}
				}
			}
			sort.Strings(names)
			result.Total = len(names)
			end := min(len(names), input.Offset+limit)
			start := min(input.Offset, end)
			for _, name := range names[start:end] {
				value, err := api.ProjectMetadata(ctx, name)
				if err != nil {
					return err
				}
				value, err = r.visibleProjectMetadata(ctx, selectedEnvironment{client: api, project: name}, value)
				if err != nil {
					return err
				}
				value.Services = nil
				value.Sources = nil
				value.Connections = nil
				result.Projects = append(result.Projects, value)
			}
			if end < len(names) {
				result.NextOffset = end
			}
		}
		for len(result.Projects) > 1 && r.checkOutput(scopedResult[contract.ProjectMetadataList]{Result: result}) != nil {
			result.Projects = result.Projects[:len(result.Projects)/2]
			result.NextOffset = input.Offset + len(result.Projects)
		}
		output = scopedResult[contract.ProjectMetadataList]{UntrustedData: true, Result: result}
		return nil
	})
	if err != nil {
		return nil, output, r.toolError(err)
	}
	if err := r.checkOutput(output); err != nil {
		return nil, scopedResult[contract.ProjectMetadataList]{}, r.toolError(err)
	}
	return nil, output, nil
}
