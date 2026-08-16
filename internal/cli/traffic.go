package cli

import (
	"context"
	"strconv"

	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) traffic(ctx context.Context, options trafficOptions) error {
	if options.protocol != "http" && options.protocol != "tcp" {
		return usageError("--protocol must be http or tcp")
	}
	if err := validLimit(options.limit, 1000); err != nil {
		return err
	}
	if options.edge != "" {
		if source, target, err := parseEdge(options.edge); err != nil || source == "" || target == "" {
			return usageError("--edge must use source:target")
		}
	}
	client, environment, err := c.current(ctx)
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
		if c.jsonOutput {
			return writeJSON(c.Out, map[string]any{"project": environment.Project, "environment": environment.Name, "traffic": traffic})
		}
		c.printTrafficList(environment, options.protocol, traffic)
		return nil
	}
	if c.jsonOutput {
		for _, event := range traffic {
			if err := writeJSONLine(c.Out, event); err != nil {
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
	return c.followTraffic(ctx, client, environment, options, seen, c.jsonOutput)
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

func (c *CLI) showTraffic(ctx context.Context, sequenceValue string) error {
	sequence, err := strconv.ParseInt(sequenceValue, 10, 64)
	if err != nil || sequence <= 0 {
		return usageError("traffic sequence must be a positive integer")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	event, err := client.TrafficEvent(ctx, environment.Project, environment.Name, sequence)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, event)
	}
	c.printTrafficDetail(event)
	return nil
}
