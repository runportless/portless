package cli

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/model"
	"github.com/spf13/cobra"
)

const (
	completionProjects     = "projects"
	completionEnvironments = "environments"
	completionServices     = "services"
	completionConnections  = "connections"
	completionRecordings   = "recordings"
	completionFaults       = "faults"
	completionTraffic      = "traffic"
	completionSources      = "sources"
)

func (c *CLI) complete(resource string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		values := c.completionValues(cmd.Context(), resource)
		filtered := make([]string, 0, len(values))
		for _, value := range values {
			if strings.HasPrefix(strings.ToLower(value), strings.ToLower(toComplete)) {
				filtered = append(filtered, value)
			}
		}
		return filtered, cobra.ShellCompDirectiveNoFileComp
	}
}

func (c *CLI) completionValues(parent context.Context, resource string) []string {
	if parent == nil {
		parent = context.Background()
	}
	if c.completionCache == nil {
		c.completionCache = make(map[string][]string)
	}
	cacheKey := resource + "\x00" + c.environmentOverride
	if values, ok := c.completionCache[cacheKey]; ok {
		return values
	}
	ctx, cancel := context.WithTimeout(parent, 900*time.Millisecond)
	defer cancel()
	client, _, err := bootstrap.ConnectExisting(ctx, c.paths)
	if err != nil {
		return nil
	}
	var values []string
	switch resource {
	case completionProjects:
		var response struct {
			Projects []model.Project `json:"projects"`
		}
		if client.Do(ctx, http.MethodGet, "/api/v1/projects?limit=1000", nil, &response) != nil {
			return nil
		}
		for _, project := range response.Projects {
			values = append(values, project.Name)
		}
	case completionEnvironments:
		var response struct {
			Environments []model.Environment `json:"environments"`
		}
		if client.Do(ctx, http.MethodGet, "/api/v1/environments?limit=1000", nil, &response) != nil {
			return nil
		}
		for _, environment := range response.Environments {
			values = append(values, model.EnvironmentSelector(environment.Project, environment.Name))
		}
	default:
		environment, resolveErr := c.resolveEnvironment(ctx, client, "")
		if resolveErr != nil {
			return nil
		}
		switch resource {
		case completionServices:
			for _, service := range environment.Services {
				values = append(values, service.Name)
			}
		case completionConnections:
			for _, connection := range environment.Connections {
				values = append(values, connection.Source+":"+connection.Target)
			}
		case completionSources:
			for _, source := range environment.Sources {
				values = append(values, source.Name)
			}
		case completionRecordings:
			var response struct {
				Recordings []model.Recording `json:"recordings"`
			}
			if client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/recordings?limit=1000", nil, &response) != nil {
				return nil
			}
			for _, item := range response.Recordings {
				values = append(values, item.Name)
			}
		case completionFaults:
			var response struct {
				Faults []model.FaultRule `json:"faults"`
			}
			if client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/faults?limit=1000", nil, &response) != nil {
				return nil
			}
			for _, item := range response.Faults {
				values = append(values, item.Name)
			}
		case completionTraffic:
			seen := make(map[int64]struct{})
			for _, protocol := range []string{"http", "tcp"} {
				var response struct {
					Traffic []model.TrafficEvent `json:"traffic"`
				}
				if client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/traffic?protocol="+protocol+"&limit=1000", nil, &response) != nil {
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
	if resource == completionTraffic {
		sort.Slice(values, func(i, j int) bool {
			left, _ := strconv.ParseInt(values[i], 10, 64)
			right, _ := strconv.ParseInt(values[j], 10, 64)
			return left > right
		})
	} else {
		sort.Strings(values)
	}
	c.completionCache[cacheKey] = values
	return values
}

func (c *CLI) completeEnvironmentNames() cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		selectors := c.completionValues(cmd.Context(), completionEnvironments)
		project := ""
		if c.environmentOverride != "" {
			project, _, _ = model.ParseEnvironmentSelector(c.environmentOverride)
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
