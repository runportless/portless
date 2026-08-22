package observe

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/model"
)

func (c *Commands) listConnections(ctx context.Context, limit int) error {
	if err := command.ValidLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListConnections(ctx, environment.Project, environment.Name, limit)
	if err != nil {
		return err
	}
	if response.Connections == nil {
		response.Connections = []model.EffectiveConnection{}
	}
	response.Connections = command.Truncate(response.Connections, limit)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Connections) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No connections."))
		return nil
	}
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-30s %-10s %-11s %-12s %-52s %s", "CONNECTION", "PROTOCOL", "PROVIDER", "STATE", "ENDPOINT", "INJECTED AS")))
	for _, connection := range response.Connections {
		endpoint := "inactive"
		if connection.Endpoint != nil {
			endpoint = connection.Endpoint.URL
		}
		fmt.Fprintf(c.Out, "%-30s %-10s %-11s %s %-52s %s\n", connection.Source+":"+connection.Target, connection.Protocol, connection.TargetProvider, c.State(c.Out, fmt.Sprintf("%-12s", connection.TargetStatus)), endpoint, command.EmptyAs(strings.Join(sortedMapKeys(connection.InjectedEnvironment), ", "), "—"))
	}
	return nil
}

func (c *Commands) showConnection(ctx context.Context, edge string) error {
	source, target, err := command.ParseEdge(edge)
	if err != nil || source == "" || target == "" {
		return command.UsageError("connection must use source:target")
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	connection, err := client.Connection(ctx, environment.Project, environment.Name, source, target)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, connection)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Connection: "+source+":"+target))
	fmt.Fprintf(c.Out, "\n  %-18s %s\n", "Protocol:", connection.Protocol)
	fmt.Fprintf(c.Out, "  %-18s %t\n", "Required:", connection.Required)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Target provider:", connection.TargetProvider)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Target state:", c.State(c.Out, string(connection.TargetStatus)))
	endpointURL, listener := "inactive", "inactive"
	if connection.Endpoint != nil {
		endpointURL, listener = connection.Endpoint.URL, command.EmptyAs(connection.Endpoint.Address, "inactive")
	}
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Endpoint:", endpointURL)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Listener:", listener)
	if len(connection.InjectedEnvironment) == 0 {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Injected values:", "none")
	} else {
		for index, key := range sortedMapKeys(connection.InjectedEnvironment) {
			label := ""
			if index == 0 {
				label = "Injected values:"
			}
			fmt.Fprintf(c.Out, "  %-18s %s=%s\n", label, key, connection.InjectedEnvironment[key])
		}
	}
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Runtime target:", command.EmptyAs(connection.RuntimeTarget, "not active"))
	return nil
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func relative(value time.Time) string {
	duration := time.Since(value)
	if duration < 0 {
		return value.Local().Format(time.RFC3339)
	}
	if duration < time.Minute {
		return fmt.Sprintf("%ds ago", int(duration.Seconds()))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
}
