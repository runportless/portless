package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type projectCleanupInput struct {
	Project string `json:"project"`
	cleanupInput
}
type sourceCleanupInput struct {
	Project string `json:"project"`
	Source  string `json:"source"`
	cleanupInput
}
type environmentCleanupInput struct {
	Environment string `json:"environment"`
	cleanupInput
}
type checkoutCleanupInput struct {
	Environment string `json:"environment"`
	Source      string `json:"source"`
	cleanupInput
}
type configurationCleanupView struct {
	Preview     *contract.ConfigurationPreview `json:"preview,omitempty"`
	Applied     bool                           `json:"applied"`
	Project     string                         `json:"project"`
	Environment string                         `json:"environment,omitempty"`
	Source      string                         `json:"source,omitempty"`
}

func (r *runtime) registerConfigurationCleanupTools(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_delete_project_source", "Preview project-wide logical source removal, including affected services and environments. Apply requires confirm:true and the unchanged preview identity. Files remain on disk.", true), r.deleteProjectSource)
	registerTool(r, server, mutationTool("portless_remove_source_checkout", "Preview removal of one stopped environment's checkout association. Apply requires confirm:true and an unchanged preview. The logical source and filesystem checkout remain.", true), r.removeSourceCheckout)
	registerTool(r, server, mutationTool("portless_forget_environment", "Preview removal of a stopped environment and retained Portless data. Apply requires confirm:true and an unchanged preview. Does not stop services or delete checkout files or volumes.", true), r.forgetEnvironment)
	registerTool(r, server, mutationTool("portless_forget_project", "Preview every environment and retained artifact removed when forgetting a stopped project. Apply requires confirm:true and the unchanged preview. Requires project or installation scope.", true), r.forgetProject)
}
func configurationCleanup(ctx context.Context, selected selectedEnvironment, source string, input cleanupInput) (configurationCleanupView, error) {
	result := configurationCleanupView{Project: selected.project, Environment: selected.environment, Source: source}
	if source != "" {
		if err := contract.ValidateSourceName(source); err != nil {
			return result, err
		}
	}
	apply, err := input.apply()
	if err != nil {
		return result, err
	}
	if !apply {
		preview, err := selected.client.PreviewConfiguration(ctx, selected.project, selected.environment, source)
		result.Preview = &preview
		return result, err
	}
	if input.Expected.StateDigest == "" {
		return result, codedError{code: "PREVIEW_REQUIRED", message: "expected must contain the complete configuration preview identity"}
	}
	api := selected.client.WithResourceVersion(*input.Expected)
	switch {
	case source != "" && selected.environment == "":
		_, err = api.DeleteProjectSource(ctx, selected.project, source)
	case source != "":
		_, err = api.RemoveSourceCheckout(ctx, selected.project, selected.environment, source)
	case selected.environment != "":
		err = api.ForgetEnvironment(ctx, selected.project, selected.environment)
	default:
		err = api.ForgetProject(ctx, selected.project)
	}
	result.Applied = err == nil
	return result, err
}
func (r *runtime) deleteProjectSource(ctx context.Context, _ *mcp.CallToolRequest, input sourceCleanupInput) (*mcp.CallToolResult, scopedResult[configurationCleanupView], error) {
	if err := contract.ValidateSourceName(input.Source); err != nil {
		return nil, scopedResult[configurationCleanupView]{}, r.toolError(err)
	}
	return projectCall(ctx, r, input.Project, true, true, func(ctx context.Context, selected selectedEnvironment) (configurationCleanupView, error) {
		return configurationCleanup(ctx, selected, input.Source, input.cleanupInput)
	})
}
func (r *runtime) forgetProject(ctx context.Context, _ *mcp.CallToolRequest, input projectCleanupInput) (*mcp.CallToolResult, scopedResult[configurationCleanupView], error) {
	return projectCall(ctx, r, input.Project, true, true, func(ctx context.Context, selected selectedEnvironment) (configurationCleanupView, error) {
		return configurationCleanup(ctx, selected, "", input.cleanupInput)
	})
}
func (r *runtime) removeSourceCheckout(ctx context.Context, _ *mcp.CallToolRequest, input checkoutCleanupInput) (*mcp.CallToolResult, scopedResult[configurationCleanupView], error) {
	if err := contract.ValidateSourceName(input.Source); err != nil {
		return nil, scopedResult[configurationCleanupView]{}, r.toolError(err)
	}
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (configurationCleanupView, error) {
		return configurationCleanup(ctx, selected, input.Source, input.cleanupInput)
	})
}
func (r *runtime) forgetEnvironment(ctx context.Context, _ *mcp.CallToolRequest, input environmentCleanupInput) (*mcp.CallToolResult, scopedResult[configurationCleanupView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (configurationCleanupView, error) {
		return configurationCleanup(ctx, selected, "", input.cleanupInput)
	})
}
