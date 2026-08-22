package portlessmcp

import (
	"context"
	"fmt"
	"strings"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type selectedEnvironment struct {
	client      *apiclient.Client
	project     string
	environment string
}

func parseEnvironmentSelector(selector string) (string, string, error) {
	project, environment, err := contract.ParseEnvironmentSelector(selector)
	if err != nil {
		return "", "", codedError{code: "INVALID_ENVIRONMENT", message: err.Error()}
	}
	return project, environment, nil
}

func (r *runtime) selectEnvironment(ctx context.Context, selector string) (selectedEnvironment, error) {
	project, environment, err := parseEnvironmentSelector(selector)
	if err != nil {
		return selectedEnvironment{}, err
	}
	client, err := r.gateway.client(ctx)
	if err != nil {
		return selectedEnvironment{}, err
	}

	allowed := false
	switch {
	case r.config.Environment != "":
		allowed = strings.EqualFold(selector, r.config.Environment)
	case r.config.AllEnvironments:
		list, listErr := client.ListEnvironments(ctx, "", 1000)
		if listErr != nil {
			return selectedEnvironment{}, listErr
		}
		allowed = containsEnvironment(list.Environments, project, environment)
	default:
		list, listErr := client.EnvironmentsForPath(ctx, r.config.WorkspaceRoot)
		if listErr != nil {
			return selectedEnvironment{}, listErr
		}
		allowed = containsEnvironment(list.Environments, project, environment)
	}
	if !allowed {
		return selectedEnvironment{}, codedError{
			code:    "SCOPE_DENIED",
			message: fmt.Sprintf("environment %s is outside this MCP server's scope", selector),
			subject: map[string]any{"environment": selector},
		}
	}
	return selectedEnvironment{client: client, project: project, environment: environment}, nil
}

func containsEnvironment(environments []contract.Environment, project, environment string) bool {
	for _, candidate := range environments {
		if strings.EqualFold(candidate.Project, project) && strings.EqualFold(candidate.Name, environment) {
			return true
		}
	}
	return false
}

func (r *runtime) visibleEnvironments(ctx context.Context, limit int) (*apiclient.Client, []contract.Environment, int, error) {
	client, err := r.gateway.client(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	var response contract.EnvironmentList
	switch {
	case r.config.Environment != "":
		project, environment, parseErr := parseEnvironmentSelector(r.config.Environment)
		if parseErr != nil {
			return nil, nil, 0, parseErr
		}
		item, loadErr := client.Environment(ctx, project, environment)
		if loadErr != nil {
			return nil, nil, 0, loadErr
		}
		response = contract.EnvironmentList{Environments: []contract.Environment{item}, Total: 1}
	case r.config.AllEnvironments:
		response, err = client.ListEnvironments(ctx, "", limit)
	default:
		response, err = client.EnvironmentsForPath(ctx, r.config.WorkspaceRoot)
	}
	if err != nil {
		return nil, nil, 0, err
	}
	total := response.Total
	if total == 0 {
		total = len(response.Environments)
	}
	if len(response.Environments) > limit {
		response.Environments = response.Environments[:limit]
	}
	return client, response.Environments, total, nil
}
