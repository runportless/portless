package traffic

import (
	"context"
	"strconv"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (c *Commands) traffic(ctx context.Context, options trafficOptions) error {
	if options.protocol != "http" && options.protocol != "tcp" {
		return command.UsageError("--protocol must be http or tcp")
	}
	if err := command.ValidLimit(options.limit, 1000); err != nil {
		return err
	}
	if options.edge != "" {
		if source, target, err := command.ParseEdge(options.edge); err != nil || source == "" || target == "" {
			return command.UsageError("--edge must use source:target")
		}
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	query := trafficQuery(options, 0)
	response, err := client.Traffic(ctx, environment.Project, environment.Name, query)
	if err != nil {
		return err
	}
	traffic := make([]model.TrafficEvent, 0, len(response.Traffic))
	for index := len(response.Traffic) - 1; index >= 0; index-- {
		traffic = append(traffic, response.Traffic[index])
	}
	if !options.tail {
		if c.JSONOutput {
			return command.WriteJSON(c.Out, map[string]any{"project": environment.Project, "environment": environment.Name, "traffic": traffic})
		}
		c.printTrafficList(environment, options.protocol, traffic)
		return nil
	}
	if c.JSONOutput {
		for _, event := range traffic {
			if err := command.WriteJSONLine(c.Out, event); err != nil {
				return err
			}
		}
	} else {
		c.printTrafficList(environment, options.protocol, traffic)
	}
	seen := make(map[int64]struct{}, len(traffic))
	for _, event := range traffic {
		seen[event.Sequence] = struct{}{}
	}
	return c.followTraffic(ctx, client, environment, options, seen, c.JSONOutput)
}

func trafficQuery(options trafficOptions, after int64) contract.TrafficQuery {
	return contract.TrafficQuery{
		Protocol: options.protocol,
		Service:  options.service,
		Edge:     options.edge,
		After:    after,
		Limit:    options.limit,
	}
}

func (c *Commands) showTraffic(ctx context.Context, sequenceValue string) error {
	sequence, err := strconv.ParseInt(sequenceValue, 10, 64)
	if err != nil || sequence <= 0 {
		return command.UsageError("traffic sequence must be a positive integer")
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	event, err := client.TrafficEvent(ctx, environment.Project, environment.Name, sequence)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, event)
	}
	c.printTrafficDetail(event)
	return nil
}
