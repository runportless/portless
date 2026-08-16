package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) followTraffic(ctx context.Context, client *apiclient.Client, environment model.Environment, options trafficOptions, seen map[int64]struct{}, jsonOutput bool) error {
	topic := "traffic.http"
	if options.protocol == "tcp" {
		topic = "traffic.tcp"
	}
	body, err := client.OpenEventStream(ctx, environment.Project, environment.Name, topic)
	if err != nil {
		return err
	}
	defer body.Close()
	last := int64(0)
	for sequence := range seen {
		if sequence > last {
			last = sequence
		}
	}
	replay, err := client.Traffic(ctx, environment.Project, environment.Name, trafficQuery(options, last))
	if err != nil {
		return err
	}
	for index := len(replay.Traffic) - 1; index >= 0; index-- {
		event := replay.Traffic[index]
		if _, exists := seen[event.Sequence]; exists {
			continue
		}
		seen[event.Sequence] = struct{}{}
		if jsonOutput {
			if err := writeJSONLine(c.Out, event); err != nil {
				return err
			}
		} else {
			c.printTraffic(event)
		}
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	eventType := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") && eventType == topic {
			var event model.TrafficEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && matchesTrafficOptions(event, options) {
				if _, exists := seen[event.Sequence]; exists {
					continue
				}
				seen[event.Sequence] = struct{}{}
				if jsonOutput {
					if err := writeJSONLine(c.Out, event); err != nil {
						return err
					}
				} else {
					c.printTraffic(event)
				}
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return scanner.Err()
}

func (c *CLI) printTrafficList(environment model.Environment, protocol string, events []model.TrafficEvent) {
	title := strings.ToUpper(protocol) + " traffic"
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, title), environment.Project, environment.Name)
	if len(events) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No "+strings.ToUpper(protocol)+" traffic captured."))
		return
	}
	if protocol == "http" {
		fmt.Fprintln(c.Out, c.muted(c.Out, "SEQ    METHOD  PATH               CODE  TIME    EDGE"))
	} else {
		fmt.Fprintln(c.Out, c.muted(c.Out, "SEQ    PROTOCOL   TIME    EDGE                         RESULT"))
	}
	for _, event := range events {
		c.printTraffic(event)
	}
}

func (c *CLI) printTraffic(event model.TrafficEvent) {
	fault := ""
	if event.Fault != "" {
		fault = " fault=" + event.Fault
	}
	if event.Protocol != model.ProtocolHTTP {
		result := "ok"
		if event.Error != "" {
			result = c.failure(c.Out, event.Error)
		}
		if event.Fault != "" {
			result = c.warning(c.Out, "fault="+event.Fault)
		}
		fmt.Fprintf(c.Out, "#%-5d %-10s %5dms %-28s %s\n", event.Sequence, strings.ToUpper(string(event.Protocol)), event.DurationMS, event.Source+":"+event.Target, result)
		return
	}
	status := fmt.Sprintf("%4d", event.Status)
	switch {
	case event.Status >= 500:
		status = c.failure(c.Out, status)
	case event.Status >= 400:
		status = c.warning(c.Out, status)
	case event.Status >= 200 && event.Status < 400:
		status = c.success(c.Out, status)
	}
	if fault != "" {
		fault = c.warning(c.Out, fault)
	}
	method := event.Method
	path := event.Path
	if method == "" {
		method = strings.ToUpper(string(event.Protocol))
	}
	if path == "" {
		path = "session"
	}
	fmt.Fprintf(c.Out, "#%-5d %-7s %-18s %s %5dms %s:%s%s\n", event.Sequence, method, path, status, event.DurationMS, event.Source, event.Target, fault)
}

func (c *CLI) printTrafficDetail(event model.TrafficEvent) {
	fmt.Fprintf(c.Out, "%s #%d\n\n", c.heading(c.Out, strings.ToUpper(string(event.Protocol))+" traffic"), event.Sequence)
	fmt.Fprintf(c.Out, "  %-18s %s → %s\n", "Edge:", event.Source, event.Target)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Provider:", emptyAs(string(event.TargetProvider), "unknown"))
	if event.Method != "" {
		fmt.Fprintf(c.Out, "  %-18s %s %s\n", "Request:", event.Method, event.Path)
	}
	if event.Status != 0 {
		fmt.Fprintf(c.Out, "  %-18s %d\n", "Status:", event.Status)
	}
	fmt.Fprintf(c.Out, "  %-18s %dms\n", "Duration:", event.DurationMS)
	fmt.Fprintf(c.Out, "  %-18s %d / %d\n", "Bytes in / out:", event.RequestBytes, event.ResponseBytes)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Fault:", emptyAs(event.Fault, "none"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Recording:", emptyAs(event.Recording, "none"))
	if event.Error != "" {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Error:", c.failure(c.Out, event.Error))
	}
	printHeaderMap(c.Out, "Request headers", event.RequestHeaders)
	printHeaderMap(c.Out, "Response headers", event.ResponseHeaders)
}

func parseEdge(value string) (string, string, error) {
	if value == "" {
		return "", "", nil
	}
	source, target, found := strings.Cut(value, ":")
	if !found || source == "" || target == "" || strings.Contains(target, ":") {
		return "", "", errors.New("edge must use source:target")
	}
	return source, target, nil
}

func matchesTraffic(event model.TrafficEvent, selector string) bool {
	if selector == "" {
		return true
	}
	if source, target, found := strings.Cut(selector, ":"); found {
		return event.Source == source && event.Target == target
	}
	return event.Source == selector || event.Target == selector
}

func recordingName(ctx context.Context, client *apiclient.Client, project, environment string, arguments []string) (string, error) {
	if len(arguments) == 1 {
		return arguments[0], nil
	}
	if len(arguments) > 1 {
		return "", errors.New("usage: portless record stop [name]")
	}
	response, err := client.ListRecordings(ctx, project, environment, 1000)
	if err != nil {
		return "", err
	}
	for _, item := range response.Recordings {
		if item.Status == "active" {
			return item.Name, nil
		}
	}
	return "", errors.New("no active recording")
}
