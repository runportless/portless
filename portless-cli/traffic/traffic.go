package traffic

import (
	"context"
	"strconv"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

func (c *Commands) traffic(ctx context.Context, options trafficOptions) error {
	if options.protocol != "all" && options.protocol != "http" && options.protocol != "tcp" {
		return command.UsageError("--protocol must be all, http, or tcp")
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
	response, err := client.TrafficExchanges(ctx, environment.Project, environment.Name, query)
	if err != nil {
		return err
	}
	exchanges := make([]model.TrafficExchange, 0, len(response.Exchanges))
	for index := len(response.Exchanges) - 1; index >= 0; index-- {
		exchanges = append(exchanges, response.Exchanges[index])
	}
	if !options.tail {
		if c.JSONOutput {
			return command.WriteJSON(c.Out, map[string]any{"project": environment.Project, "environment": environment.Name, "exchanges": exchanges})
		}
		c.printTrafficList(environment, options.protocol, exchanges)
		return nil
	}
	if c.JSONOutput {
		for _, exchange := range exchanges {
			if err := command.WriteJSONLine(c.Out, exchange); err != nil {
				return err
			}
		}
	} else {
		c.printTrafficList(environment, options.protocol, exchanges)
	}
	seen := make(map[int64]struct{}, len(exchanges))
	for _, exchange := range exchanges {
		seen[exchange.Sequence] = struct{}{}
	}
	return c.followTraffic(ctx, client, environment, options, seen, c.JSONOutput)
}

func trafficQuery(options trafficOptions, after int64) contract.TrafficExchangeQuery {
	return contract.TrafficExchangeQuery{
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
	exchange, err := client.TrafficExchange(ctx, environment.Project, environment.Name, sequence)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, exchange)
	}
	c.printTrafficDetail(exchange)
	return nil
}

func (c *Commands) traces(ctx context.Context, options traceOptions) error {
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
	response, err := client.TrafficTraces(ctx, environment.Project, environment.Name, contract.TrafficTraceQuery{
		Service: options.service, Edge: options.edge, IncludeBackground: options.includeBackground, Limit: options.limit,
	})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, map[string]any{"project": environment.Project, "environment": environment.Name, "traces": response.Traces})
	}
	c.printTraceList(environment, response.Traces)
	return nil
}

func (c *Commands) showTrace(ctx context.Context, numberValue string) error {
	number, err := strconv.ParseInt(numberValue, 10, 64)
	if err != nil || number <= 0 {
		return command.UsageError("trace number must be a positive integer")
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	trace, err := client.TrafficTrace(ctx, environment.Project, environment.Name, number)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, trace)
	}
	c.printTrace(trace)
	return nil
}
