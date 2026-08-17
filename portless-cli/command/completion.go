package command

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/spf13/cobra"
)

const (
	// CompletionProjects identifies project-name completion candidates.
	CompletionProjects = "projects"
	// CompletionEnvironments identifies project/environment completion candidates.
	CompletionEnvironments = "environments"
	// CompletionServices identifies service-name completion candidates.
	CompletionServices = "services"
	// CompletionConnections identifies source:target connection completion candidates.
	CompletionConnections = "connections"
	// CompletionRecordings identifies recording-name completion candidates.
	CompletionRecordings = "recordings"
	// CompletionFaults identifies fault-rule-name completion candidates.
	CompletionFaults = "faults"
	// CompletionTraffic identifies captured-traffic-sequence completion candidates.
	CompletionTraffic = "traffic"
	// CompletionSources identifies project-source-name completion candidates.
	CompletionSources = "sources"
)

// Complete returns a Cobra completion function for a Portless resource type.
// It performs case-insensitive prefix filtering and never requests file-name
// completion from the shell.
func (c *Context) Complete(resource string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		values := c.CompletionValues(cmd.Context(), resource)
		filtered := make([]string, 0, len(values))
		for _, value := range values {
			if strings.HasPrefix(strings.ToLower(value), strings.ToLower(toComplete)) {
				filtered = append(filtered, value)
			}
		}
		return filtered, cobra.ShellCompDirectiveNoFileComp
	}
}

// CompletionValues queries the existing daemon for sorted completion
// candidates. It uses a short timeout, caches results for the invocation, and
// returns no candidates when the daemon or selected environment is unavailable.
func (c *Context) CompletionValues(parent context.Context, resource string) []string {
	if parent == nil {
		parent = context.Background()
	}
	if c.CompletionCache == nil {
		c.CompletionCache = make(map[string][]string)
	}
	cacheKey := resource + "\x00" + c.EnvironmentOverride
	if values, ok := c.CompletionCache[cacheKey]; ok {
		return values
	}
	ctx, cancel := context.WithTimeout(parent, 900*time.Millisecond)
	defer cancel()
	client, _, err := c.Daemon.ConnectExisting(ctx)
	if err != nil {
		return nil
	}
	var values []string
	switch resource {
	case CompletionProjects:
		response, listErr := client.ListProjects(ctx, 1000)
		if listErr != nil {
			return nil
		}
		for _, project := range response.Projects {
			values = append(values, project.Name)
		}
	case CompletionEnvironments:
		response, listErr := client.ListEnvironments(ctx, "", 1000)
		if listErr != nil {
			return nil
		}
		for _, environment := range response.Environments {
			values = append(values, model.EnvironmentSelector(environment.Project, environment.Name))
		}
	default:
		environment, resolveErr := c.ResolveEnvironment(ctx, client, "")
		if resolveErr != nil {
			return nil
		}
		switch resource {
		case CompletionServices:
			for _, service := range environment.Services {
				values = append(values, service.Name)
			}
		case CompletionConnections:
			for _, connection := range environment.Connections {
				values = append(values, connection.Source+":"+connection.Target)
			}
		case CompletionSources:
			for _, source := range environment.Sources {
				values = append(values, source.Name)
			}
		case CompletionRecordings:
			response, listErr := client.ListRecordings(ctx, environment.Project, environment.Name, 1000)
			if listErr != nil {
				return nil
			}
			for _, item := range response.Recordings {
				values = append(values, item.Name)
			}
		case CompletionFaults:
			response, listErr := client.ListFaults(ctx, environment.Project, environment.Name, 1000)
			if listErr != nil {
				return nil
			}
			for _, item := range response.Faults {
				values = append(values, item.Name)
			}
		case CompletionTraffic:
			seen := make(map[int64]struct{})
			for _, protocol := range []string{"http", "tcp"} {
				response, trafficErr := client.Traffic(ctx, environment.Project, environment.Name, contract.TrafficQuery{Protocol: protocol, Limit: 1000})
				if trafficErr != nil {
					continue
				}
				for _, item := range response.Traffic {
					if _, exists := seen[item.Sequence]; !exists {
						seen[item.Sequence] = struct{}{}
						values = append(values, strconv.FormatInt(item.Sequence, 10))
					}
				}
			}
		}
	}
	if resource == CompletionTraffic {
		sort.Slice(values, func(i, j int) bool {
			left, _ := strconv.ParseInt(values[i], 10, 64)
			right, _ := strconv.ParseInt(values[j], 10, 64)
			return left > right
		})
	} else {
		sort.Strings(values)
	}
	c.CompletionCache[cacheKey] = values
	return values
}

// CompleteEnvironmentNames returns environment-name completions, restricted
// to the project selected by --env when one is present.
func (c *Context) CompleteEnvironmentNames() cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		selectors := c.CompletionValues(cmd.Context(), CompletionEnvironments)
		project := ""
		if c.EnvironmentOverride != "" {
			project, _, _ = model.ParseEnvironmentSelector(c.EnvironmentOverride)
		}
		values := make([]string, 0, len(selectors))
		for _, selector := range selectors {
			candidateProject, environment, err := model.ParseEnvironmentSelector(selector)
			if err == nil && (project == "" || candidateProject == project) && strings.HasPrefix(environment, toComplete) {
				values = append(values, environment)
			}
		}
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}
