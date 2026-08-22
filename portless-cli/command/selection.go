package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/model"
)

// Current connects to the daemon and resolves the environment associated with
// the current checkout or invocation override.
func (c *Context) Current(ctx context.Context) (*apiclient.Client, model.Environment, error) {
	return c.CurrentOrNamed(ctx, "")
}

// CurrentOrNamed connects to the daemon and resolves selector, falling back to
// the current checkout when selector and --env are both absent.
func (c *Context) CurrentOrNamed(ctx context.Context, selector string) (*apiclient.Client, model.Environment, error) {
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return nil, model.Environment{}, err
	}
	environment, err := c.ResolveEnvironment(ctx, client, selector)
	if err != nil {
		return nil, model.Environment{}, err
	}
	return client, environment, nil
}

// FindCurrent resolves the environment associated with the current checkout.
func (c *Context) FindCurrent(ctx context.Context, client *apiclient.Client) (model.Environment, error) {
	resolved, err := c.ResolveEnvironmentContext(ctx, client)
	if err != nil {
		return model.Environment{}, err
	}
	return resolved.Environment, nil
}

// ResolveEnvironment applies explicit selection rules and loads the resulting
// environment, or infers one from the current checkout when none is explicit.
func (c *Context) ResolveEnvironment(ctx context.Context, client *apiclient.Client, selector string) (model.Environment, error) {
	effective, err := c.EffectiveEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	if effective != "" {
		return c.LoadEnvironment(ctx, client, effective)
	}
	return c.FindCurrent(ctx, client)
}

// EffectiveEnvironmentSelector combines a command selector with the global
// --env override and rejects invocations that provide both.
func (c *Context) EffectiveEnvironmentSelector(selector string) (string, error) {
	if selector != "" && c.EnvironmentOverride != "" {
		return "", UsageError("an environment was provided twice; use only --env")
	}
	if selector != "" {
		return selector, nil
	}
	return c.EnvironmentOverride, nil
}

// ResolveEnvironmentContext returns the environment selected for the current
// source root together with the path and resolution strategy used.
func (c *Context) ResolveEnvironmentContext(ctx context.Context, client *apiclient.Client) (EnvironmentContextOutput, error) {
	root, err := c.CurrentSourceRoot(ctx)
	if err != nil {
		return EnvironmentContextOutput{}, err
	}
	if c.EnvironmentOverride != "" {
		environment, err := c.LoadEnvironment(ctx, client, c.EnvironmentOverride)
		if err != nil {
			return EnvironmentContextOutput{}, err
		}
		return EnvironmentContextOutput{
			Path: root, Selector: model.EnvironmentSelector(environment.Project, environment.Name),
			Resolution: "flag", Environment: environment,
		}, nil
	}
	response, err := client.EnvironmentContext(ctx, root)
	if err != nil {
		return EnvironmentContextOutput{}, err
	}
	if response.Environment == nil {
		if response.Resolution == "ambiguous" {
			return EnvironmentContextOutput{}, AmbiguousEnvironmentError(response.Candidates)
		}
		return EnvironmentContextOutput{}, errors.New("this checkout is not part of a Portless environment; run `portless up` or `portless project create`")
	}
	environment := *response.Environment
	return EnvironmentContextOutput{
		Path: root, Selector: model.EnvironmentSelector(environment.Project, environment.Name),
		Resolution: response.Resolution, Environment: environment,
	}, nil
}

// EnvironmentsForCurrentPath lists every environment that uses the current
// checkout's source root.
func (c *Context) EnvironmentsForCurrentPath(ctx context.Context, client *apiclient.Client) ([]model.Environment, error) {
	root, err := c.CurrentSourceRoot(ctx)
	if err != nil {
		return nil, err
	}
	response, err := client.EnvironmentsForPath(ctx, root)
	if err != nil {
		return nil, err
	}
	return response.Environments, nil
}

// CurrentSourceRoot discovers the project root containing the working
// directory and returns its symlink-resolved path. When no project marker is
// found, the working directory itself is returned.
func (c *Context) CurrentSourceRoot(ctx context.Context) (string, error) {
	cwd, err := c.Local.WorkingDirectory()
	if err != nil {
		return "", err
	}
	root, err := c.Local.FindProjectRoot(ctx, cwd)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		root = cwd
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	return root, nil
}

// WorkingDirectory returns the absolute current working directory after
// resolving symbolic links when possible.
func WorkingDirectory() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolved
	}
	return filepath.Abs(cwd)
}

// DebugServiceForPath selects the deepest local process service containing
// path. It returns an empty name when no service matches and rejects ties.
func DebugServiceForPath(environment model.Environment, path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	bestName, bestDirectory := "", ""
	for _, service := range environment.Services {
		if service.Kind != model.ServiceProcess || ProviderFor(environment, service.Name) != model.ProviderLocal {
			continue
		}
		for _, candidate := range ServiceDirectories(environment, service) {
			directory, absErr := filepath.Abs(candidate)
			if absErr != nil {
				continue
			}
			relative, relErr := filepath.Rel(directory, path)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			if len(directory) < len(bestDirectory) {
				continue
			}
			if len(directory) == len(bestDirectory) && bestName != "" && !strings.EqualFold(bestName, service.Name) {
				return "", fmt.Errorf("%s contains more than one service; choose one with `portless up --debug <service>`", path)
			}
			bestName, bestDirectory = service.Name, directory
		}
	}
	return bestName, nil
}

// ServiceDirectories returns the filesystem directories that may identify
// service within its bound project source.
func ServiceDirectories(environment model.Environment, service model.Service) []string {
	if service.ServiceDirectory != "" {
		return []string{service.ServiceDirectory}
	}
	var result []string
	binding := model.ComponentBinding{}
	for _, candidate := range environment.Bindings {
		if strings.EqualFold(candidate.Service, service.Name) {
			binding = candidate
			break
		}
	}
	var sourceRoot string
	for _, source := range environment.Sources {
		if strings.EqualFold(source.Name, binding.Source) {
			sourceRoot = source.Path
			break
		}
	}
	if sourceRoot != "" {
		for _, evidence := range service.Evidence {
			if evidence.File == "" {
				continue
			}
			result = append(result, filepath.Join(sourceRoot, filepath.Dir(filepath.FromSlash(evidence.File))))
		}
	}
	return result
}

// LoadEnvironment parses a project/environment selector and retrieves that
// environment from the daemon API.
func (c *Context) LoadEnvironment(ctx context.Context, client *apiclient.Client, selector string) (model.Environment, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	result, err := client.Environment(ctx, project, environment)
	if err != nil {
		return model.Environment{}, err
	}
	return result, nil
}

// AmbiguousEnvironmentError describes competing checkout matches and tells the
// user how to select one explicitly.
func AmbiguousEnvironmentError(environments []model.Environment) error {
	selectors := make([]string, 0, len(environments))
	for _, environment := range environments {
		selectors = append(selectors, model.EnvironmentSelector(environment.Project, environment.Name))
	}
	return fmt.Errorf("this checkout belongs to multiple environments (%s); select one with `portless env select project/environment` or pass `--env project/environment`", strings.Join(selectors, ", "))
}

// EnvironmentResolutionDescription translates an environment-resolution code
// into human-readable CLI output.
func EnvironmentResolutionDescription(resolution string) string {
	switch resolution {
	case "flag":
		return "--env override for this invocation"
	case "selected":
		return "saved selection for this checkout"
	case "inferred":
		return "only environment using this checkout"
	default:
		return resolution
	}
}
