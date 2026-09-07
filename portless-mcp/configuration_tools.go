package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"strings"
	"time"
)

type discoverProjectInput struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}
type sourceInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type createProjectInput struct {
	Project string        `json:"project"`
	Sources []sourceInput `json:"sources"`
}
type addProjectSourceInput struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Source      string `json:"source"`
	Path        string `json:"path"`
}
type checkoutInput struct {
	Environment string `json:"environment"`
	Source      string `json:"source"`
	Path        string `json:"path"`
}
type cloneEnvironmentInput struct {
	Environment string `json:"environment"`
	Name        string `json:"name"`
}
type renameProjectInput struct {
	Project          string `json:"project"`
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type rescanEnvironmentInput struct {
	Environment                 string `json:"environment"`
	ExpectedProjectRevision     int64  `json:"expectedProjectRevision"`
	ExpectedEnvironmentRevision int64  `json:"expectedEnvironmentRevision"`
}
type bindingInput struct {
	Environment    string       `json:"environment"`
	Service        string       `json:"service"`
	Binding        bindingDraft `json:"binding"`
	IdempotencyKey string       `json:"idempotencyKey,omitempty"`
	WaitSeconds    *int         `json:"waitSeconds,omitempty"`
}
type bindingDraft struct {
	Provider contract.ProviderKind  `json:"provider"`
	Source   string                 `json:"source,omitempty"`
	Remote   *contract.RemoteTarget `json:"remote,omitempty"`
	Mock     *contract.MockTarget   `json:"mock,omitempty"`
}

type configurationReceipt struct {
	Project                    string    `json:"project"`
	Environment                string    `json:"environment,omitempty"`
	ProjectRevision            int64     `json:"projectRevision,omitempty"`
	EnvironmentRevision        int64     `json:"environmentRevision,omitempty"`
	CreatedAt                  time.Time `json:"createdAt,omitzero"`
	Status                     string    `json:"status,omitempty"`
	ServiceCount               int       `json:"serviceCount"`
	SourceCount                int       `json:"sourceCount"`
	IssueCount                 int       `json:"issueCount"`
	Warnings                   []string  `json:"warnings"`
	WarningsTruncated          bool      `json:"warningsTruncated"`
	ConfigurationRequired      []string  `json:"configurationRequired,omitempty"`
	ConfigurationRequiredCount int       `json:"configurationRequiredCount,omitempty"`
	ClonedFrom                 string    `json:"clonedFrom,omitempty"`
	ScopeConsequence           string    `json:"scopeConsequence,omitempty"`
}

func configurationMutationResult(project contract.Project, env contract.Environment, warnings []string) configurationReceipt {
	result := configurationReceipt{Project: project.Name, ProjectRevision: project.Revision, Environment: env.Name, EnvironmentRevision: env.Revision, CreatedAt: env.CreatedAt, Status: string(env.Status), ServiceCount: len(env.Services), SourceCount: len(env.Sources), IssueCount: len(env.Issues), Warnings: []string{}}
	if result.Project == "" {
		result.Project = env.Project
	}
	budget := 16 << 10
	for _, warning := range warnings {
		if len(result.Warnings) >= 100 || budget == 0 {
			result.WarningsTruncated = true
			break
		}
		value, truncated := truncateUTF8(warning, min(2048, budget))
		result.Warnings = append(result.Warnings, value)
		budget -= len(value)
		result.WarningsTruncated = result.WarningsTruncated || truncated
	}
	return result
}

func (r *runtime) registerConfigurationTools(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_discover_project", "Discover an authorized checkout and persist a stopped project, or resolve an existing environment. Reads are confined to startup source roots. Does not start services.", false), r.discoverProject)
	registerTool(r, server, mutationTool("portless_create_project", "Create a logical project and stopped local environment from named authorized source trees. Workspace scope requires a source associated with the startup checkout.", false), r.createProject)
	registerTool(r, server, mutationTool("portless_add_project_source", "Discover and add one logical source across a project. Requires project or installation scope and stopped environments; reports environments needing configuration.", false), r.addProjectSource)
	registerTool(r, server, mutationTool("portless_clone_environment", "Create a stopped environment from existing topology, bindings and mocks. Requires project or installation scope. Does not start processes or prepare worktrees.", false), r.cloneEnvironment)
	registerTool(r, server, mutationTool("portless_rename_project", "Rename a stopped project at the reviewed revision. Requires project or installation scope. A fixed project scope does not follow the new name.", true), r.renameProject)
	registerTool(r, server, mutationTool("portless_set_source_checkout", "Set an authorized source checkout for one stopped environment, returning configuration warnings. Does not change other environments.", false), r.setSourceCheckout)
	registerTool(r, server, mutationTool("portless_rescan_environment", "Rescan registered sources at reviewed project and environment revisions. Shared topology changes require project or installation scope and stopped environments.", true), r.rescanEnvironment)
	r.registerConfigurationCleanupTools(server)
	if r.config.AllowLifecycle {
		registerTool(r, server, mutationTool("portless_change_service_binding", "Change one registered service provider through a durable operation. Requires configuration and lifecycle permissions. Remote bindings require explicit classification and write policy.", true), r.changeServiceBinding)
	}
}

func (r *runtime) createWithSources(ctx context.Context, name string, call func(context.Context, *client.Client, string) (contract.ProjectMutation, error)) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	var output scopedResult[configurationReceipt]
	release, err := r.enter(ctx, true)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 130*time.Second)
	defer cancel()
	if r.config.Environment != "" {
		return nil, output, r.toolError(codedError{code: "SCOPE_DENIED", message: "creation requires workspace, project, or installation scope"})
	}
	if r.config.Project != "" {
		if name != "" && !strings.EqualFold(name, r.config.Project) {
			return nil, output, r.toolError(codedError{code: "SCOPE_DENIED", message: "project is outside startup scope"})
		}
		name = r.config.Project
	}
	api, err := r.gateway.client(ctx)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	value, err := call(ctx, api, name)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	// Discovery may resolve an existing project. Verify before returning any data;
	// the daemon enforces required project/association constraints before writes.
	if _, err = r.selectEnvironment(ctx, value.Environment.Project+"/"+value.Environment.Name); err != nil {
		return nil, output, r.toolError(err)
	}
	output = scopedResult[configurationReceipt]{Project: value.Project.Name, Environment: value.Environment.Name, UntrustedData: true, Result: configurationMutationResult(value.Project, value.Environment, value.Warnings)}
	return nil, output, nil
}

func (r *runtime) requiredAssociation() string {
	if !r.config.AllEnvironments && r.config.Project == "" && r.config.Environment == "" {
		return r.config.WorkspaceRoot
	}
	return ""
}

func (r *runtime) discoverProject(ctx context.Context, _ *mcp.CallToolRequest, input discoverProjectInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return r.createWithSources(ctx, input.Name, func(ctx context.Context, api *client.Client, name string) (contract.ProjectMutation, error) {
		path, root, err := r.sourcePath(input.Path)
		if err != nil {
			return contract.ProjectMutation{}, err
		}
		if name != "" {
			if err := contract.ValidateProjectName(name); err != nil {
				return contract.ProjectMutation{}, err
			}
		}
		return api.DiscoverProject(ctx, contract.DiscoverProjectRequest{Path: path, Name: name, AllowedRoot: root, RequiredAssociationPath: r.requiredAssociation(), RequiredProject: r.config.Project})
	})
}
func (r *runtime) createProject(ctx context.Context, _ *mcp.CallToolRequest, input createProjectInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return r.createWithSources(ctx, input.Project, func(ctx context.Context, api *client.Client, name string) (contract.ProjectMutation, error) {
		if err := contract.ValidateProjectName(name); err != nil {
			return contract.ProjectMutation{}, err
		}
		if len(input.Sources) == 0 || len(input.Sources) > 100 {
			return contract.ProjectMutation{}, codedError{code: "INVALID_ARGUMENT", message: "sources must contain 1 to 100 named directories"}
		}
		sources := []contract.SourceInput{}
		for _, source := range input.Sources {
			if err := contract.ValidateSourceName(source.Name); err != nil {
				return contract.ProjectMutation{}, err
			}
			path, root, err := r.sourcePath(source.Path)
			if err != nil {
				return contract.ProjectMutation{}, err
			}
			sources = append(sources, contract.SourceInput{Name: source.Name, Path: path, AllowedRoot: root})
		}
		return api.CreateProject(ctx, contract.CreateProjectRequest{Name: name, Sources: sources, RequiredAssociationPath: r.requiredAssociation()})
	})
}
func (r *runtime) addProjectSource(ctx context.Context, _ *mcp.CallToolRequest, input addProjectSourceInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return projectCall(ctx, r, input.Project, true, true, func(ctx context.Context, selected selectedEnvironment) (configurationReceipt, error) {
		if err := contract.ValidateSourceName(input.Source); err != nil {
			return configurationReceipt{}, err
		}
		p, e, err := parseEnvironmentSelector(input.Environment)
		if err != nil {
			return configurationReceipt{}, err
		}
		if !strings.EqualFold(p, selected.project) {
			return configurationReceipt{}, codedError{code: "SCOPE_DENIED", message: "environment must belong to the selected project"}
		}
		path, root, err := r.sourcePath(input.Path)
		if err != nil {
			return configurationReceipt{}, err
		}
		value, err := selected.client.AddProjectSource(ctx, selected.project, contract.AddProjectSourceRequest{Name: input.Source, Environment: e, Path: path, AllowedRoot: root})
		if err != nil {
			return configurationReceipt{}, err
		}
		result := configurationMutationResult(value.Project, value.Environment, value.Warnings)
		result.ConfigurationRequiredCount = len(value.ConfigurationRequired)
		result.ConfigurationRequired = value.ConfigurationRequired[:min(100, len(value.ConfigurationRequired))]
		return result, nil
	})
}
func (r *runtime) cloneEnvironment(ctx context.Context, _ *mcp.CallToolRequest, input cloneEnvironmentInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (configurationReceipt, error) {
		if _, err := r.selectProject(ctx, selected.project, true); err != nil {
			return configurationReceipt{}, err
		}
		if err := contract.ValidateEnvironmentName(input.Name); err != nil {
			return configurationReceipt{}, err
		}
		env, err := selected.client.CloneEnvironment(ctx, contract.CloneEnvironmentRequest{Project: selected.project, From: selected.environment, Name: input.Name})
		result := configurationMutationResult(contract.Project{}, env, nil)
		result.ClonedFrom = selected.environment
		return result, err
	})
}
func (r *runtime) renameProject(ctx context.Context, _ *mcp.CallToolRequest, input renameProjectInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return projectCall(ctx, r, input.Project, true, true, func(ctx context.Context, selected selectedEnvironment) (configurationReceipt, error) {
		if err := contract.ValidateProjectName(input.Name); err != nil {
			return configurationReceipt{}, err
		}
		preview, err := selected.client.PreviewConfiguration(ctx, selected.project, "", "")
		if err != nil {
			return configurationReceipt{}, err
		}
		if input.ExpectedRevision < 1 || preview.Expected.Revision != input.ExpectedRevision {
			return configurationReceipt{}, codedError{code: "RESOURCE_CHANGED", message: "expectedRevision must match the current project"}
		}
		value, err := selected.client.WithResourceVersion(preview.Expected).RenameProject(ctx, selected.project, contract.RenameProjectRequest{Name: input.Name, Revision: input.ExpectedRevision})
		if err != nil {
			return configurationReceipt{}, err
		}
		result := configurationMutationResult(value, contract.Environment{}, nil)
		if r.config.Project != "" {
			result.ScopeConsequence = "Startup scope retains the old project name. Restart this MCP server with --project " + value.Name + " to access the renamed project."
		}
		return result, nil
	})
}
func (r *runtime) setSourceCheckout(ctx context.Context, _ *mcp.CallToolRequest, input checkoutInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (configurationReceipt, error) {
		if err := contract.ValidateSourceName(input.Source); err != nil {
			return configurationReceipt{}, err
		}
		path, root, err := r.sourcePath(input.Path)
		if err != nil {
			return configurationReceipt{}, err
		}
		value, err := selected.client.SetSourceCheckout(ctx, selected.project, selected.environment, input.Source, contract.SetSourceCheckoutRequest{Path: path, AllowedRoot: root})
		return configurationMutationResult(contract.Project{}, value.Environment, value.Warnings), err
	})
}
func (r *runtime) rescanEnvironment(ctx context.Context, _ *mcp.CallToolRequest, input rescanEnvironmentInput) (*mcp.CallToolResult, scopedResult[configurationReceipt], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (configurationReceipt, error) {
		if _, err := r.selectProject(ctx, selected.project, true); err != nil {
			return configurationReceipt{}, err
		}
		preview, err := selected.client.PreviewConfiguration(ctx, selected.project, "", "")
		if err != nil {
			return configurationReceipt{}, err
		}
		match := false
		for _, env := range preview.Environments {
			match = match || (strings.EqualFold(env.Name, selected.environment) && env.Revision == input.ExpectedEnvironmentRevision)
		}
		if input.ExpectedProjectRevision < 1 || preview.Expected.Revision != input.ExpectedProjectRevision || !match {
			return configurationReceipt{}, codedError{code: "RESOURCE_CHANGED", message: "review current project and environment revisions before rescan"}
		}
		value, err := selected.client.WithResourceVersion(preview.Expected).RescanEnvironment(ctx, selected.project, selected.environment)
		return configurationMutationResult(contract.Project{}, value.Environment, value.Warnings), err
	})
}

type bindingReceipt struct {
	Operation        contract.Operation `json:"operation"`
	IdempotencyKey   string             `json:"idempotencyKey"`
	TimedOutWaiting  bool               `json:"timedOutWaiting"`
	AdmissionUnknown bool               `json:"admissionUnknown"`
	Binding          *bindingView       `json:"binding,omitempty"`
	Warning          string             `json:"warning,omitempty"`
}

func (r *runtime) changeServiceBinding(ctx context.Context, _ *mcp.CallToolRequest, input bindingInput) (*mcp.CallToolResult, scopedResult[bindingReceipt], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (bindingReceipt, error) {
		var result bindingReceipt
		if err := validateServiceName(input.Service); err != nil {
			return result, err
		}
		binding := contract.ComponentBinding{Service: input.Service, Provider: input.Binding.Provider, Source: input.Binding.Source, Remote: input.Binding.Remote, Mock: input.Binding.Mock}
		wait, err := waitDuration(input.WaitSeconds)
		if err != nil {
			return result, err
		}
		key, persisted, err := prepareIdempotency("change-binding", input.Environment+"/"+input.Service, input.IdempotencyKey)
		if err != nil {
			return result, err
		}
		result.IdempotencyKey = key
		operation, err := selected.client.ChangeBinding(ctx, selected.project, selected.environment, input.Service, binding, persisted)
		if err != nil {
			if uncertainMutation(err) {
				result.AdmissionUnknown = true
				result.Warning = "Admission response was lost. Inspect operations before explicitly retrying with this idempotency key."
				return result, nil
			}
			return result, err
		}
		result.Operation = operation
		current, pending, err := waitForOperation(ctx, selected.client, operation, wait)
		result.TimedOutWaiting = pending
		if err != nil {
			result.TimedOutWaiting = true
			result.Warning = "Waiting ended after admission; inspect the returned operation."
			return result, nil
		}
		result.Operation = current
		env, err := selected.client.Environment(ctx, selected.project, selected.environment)
		if err == nil {
			for _, binding := range environmentResult(env).Bindings {
				if strings.EqualFold(binding.Service, input.Service) {
					result.Binding = &binding
					break
				}
			}
		}
		return result, nil
	})
}
