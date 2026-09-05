package traffic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/runportless/portless/portless-cli/command"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/model"
)

func (c *Commands) followTraffic(ctx context.Context, client *apiclient.Client, environment model.Environment, options trafficOptions, seen map[int64]struct{}, jsonOutput bool) error {
	topic := "traffic.exchange"
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
	replay, err := client.TrafficExchanges(ctx, environment.Project, environment.Name, trafficQuery(options, last))
	if err != nil {
		return err
	}
	for index := len(replay.Exchanges) - 1; index >= 0; index-- {
		exchange := replay.Exchanges[index]
		if _, exists := seen[exchange.Sequence]; exists {
			continue
		}
		seen[exchange.Sequence] = struct{}{}
		if jsonOutput {
			if err := command.WriteJSONLine(c.Out, exchange); err != nil {
				return err
			}
		} else {
			c.printTraffic(exchange)
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
			var exchange model.TrafficExchange
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &exchange) == nil && matchesTrafficOptions(exchange, options) {
				if _, exists := seen[exchange.Sequence]; exists {
					continue
				}
				seen[exchange.Sequence] = struct{}{}
				if jsonOutput {
					if err := command.WriteJSONLine(c.Out, exchange); err != nil {
						return err
					}
				} else {
					c.printTraffic(exchange)
				}
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return scanner.Err()
}

func (c *Commands) printTrafficList(environment model.Environment, protocol string, exchanges []model.TrafficExchange) {
	title := strings.ToUpper(protocol) + " traffic"
	if protocol == "all" {
		title = "Traffic exchanges"
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, title), environment.Project, environment.Name)
	if len(exchanges) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No matching traffic captured."))
		return
	}
	if protocol == "http" {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "SEQ    METHOD  PATH               CODE  TIME    EDGE"))
	} else if protocol == "tcp" {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "SEQ    PROTOCOL    OPERATION          TIME    EDGE                         RESULT"))
	} else {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "SEQ    PROTO  REQUEST / OPERATION     RESULT  TIME    EDGE"))
	}
	for _, exchange := range exchanges {
		c.printTraffic(exchange)
	}
}

func (c *Commands) printTraffic(event model.TrafficExchange) {
	fault := ""
	if event.Fault != "" {
		fault = " fault=" + event.Fault
	}
	mock := ""
	if event.MockScenario != "" {
		mock = " mock=" + event.MockScenario
		if event.MockRoute != "" {
			mock += "/" + event.MockRoute
		}
	}
	if event.Protocol != model.ProtocolHTTP {
		result := "ok"
		if event.Error != "" || event.TCP != nil && event.TCP.Outcome == model.TrafficTCPOutcomeError {
			result = c.Failure(c.Out, event.Error)
			if event.Error == "" {
				result = c.Failure(c.Out, "error")
			}
		} else if event.TCP != nil && event.TCP.Outcome == model.TrafficTCPOutcomeOneWay {
			result = "sent"
		} else if event.TCP != nil && event.TCP.Outcome == model.TrafficTCPOutcomeIncomplete {
			result = c.Warning(c.Out, "incomplete")
		}
		if event.Fault != "" {
			result = c.Warning(c.Out, "fault="+event.Fault)
		}
		fmt.Fprintf(c.Out, "#%-5d %-11s %-18s %5dms %-28s %s\n", event.Sequence, tcpProtocolName(event), tcpOperationName(event), event.DurationMS, event.Source+":"+event.Target, result)
		return
	}
	status := fmt.Sprintf("%4d", event.Status)
	switch {
	case event.Status >= 500:
		status = c.Failure(c.Out, status)
	case event.Status >= 400:
		status = c.Warning(c.Out, status)
	case event.Status >= 200 && event.Status < 400:
		status = c.Success(c.Out, status)
	}
	if fault != "" {
		fault = c.Warning(c.Out, fault)
	}
	method := event.Method
	path := event.RequestTarget
	if path == "" {
		path = event.Path
	}
	if method == "" {
		method = strings.ToUpper(string(event.Protocol))
	}
	if path == "" {
		path = "session"
	}
	fmt.Fprintf(c.Out, "#%-5d %-7s %-18s %s %5dms %s:%s%s%s\n", event.Sequence, method, path, status, event.DurationMS, event.Source, event.Target, fault, mock)
}

func (c *Commands) printTrafficDetail(event model.TrafficExchange) {
	title := strings.ToUpper(string(event.Protocol))
	if event.Protocol == model.ProtocolTCP {
		title = tcpProtocolName(event)
	}
	fmt.Fprintf(c.Out, "%s #%d\n\n", c.Heading(c.Out, title+" exchange"), event.Sequence)
	fmt.Fprintf(c.Out, "  %-18s %s → %s\n", "Edge:", event.Source, event.Target)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Provider:", command.EmptyAs(string(event.TargetProvider), "unknown"))
	if event.MockScenario != "" {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Mock scenario:", event.MockScenario)
	}
	if event.MockRoute != "" {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Mock route:", event.MockRoute)
	}
	if event.Method != "" {
		requestTarget := event.RequestTarget
		if requestTarget == "" {
			requestTarget = event.Path
		}
		fmt.Fprintf(c.Out, "  %-18s %s %s\n", "Request:", event.Method, requestTarget)
	}
	if event.Status != 0 {
		fmt.Fprintf(c.Out, "  %-18s %d\n", "Status:", event.Status)
	}
	if event.TCP != nil {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Operation:", command.EmptyAs(event.TCP.Operation, "session"))
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Inspection:", event.TCP.Inspection)
		if event.TCP.InspectionReason != "" {
			fmt.Fprintf(c.Out, "  %-18s %s\n", "Inspection detail:", event.TCP.InspectionReason)
		}
		if event.TCP.Outcome != "" {
			fmt.Fprintf(c.Out, "  %-18s %s\n", "Outcome:", event.TCP.Outcome)
		}
	}
	fmt.Fprintf(c.Out, "  %-18s %dms\n", "Duration:", event.DurationMS)
	fmt.Fprintf(c.Out, "  %-18s %d / %d\n", "Bytes in / out:", event.RequestBytes, event.ResponseBytes)
	if event.RequestCapturedBytes != 0 || event.ResponseCapturedBytes != 0 {
		fmt.Fprintf(c.Out, "  %-18s %d / %d\n", "Captured in / out:", event.RequestCapturedBytes, event.ResponseCapturedBytes)
	}
	if event.TraceID != "" {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Trace:", event.TraceID)
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Span:", event.SpanID)
		if event.ParentSpanID != "" {
			fmt.Fprintf(c.Out, "  %-18s %s\n", "Parent span:", event.ParentSpanID)
		}
	}
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Fault:", command.EmptyAs(event.Fault, "none"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Recording:", command.EmptyAs(event.Recording, "none"))
	if event.Error != "" {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Error:", c.Failure(c.Out, event.Error))
	}
	printHeaderMap(c.Out, "Request headers", event.RequestHeaders)
	printHeaderMap(c.Out, "Response headers", event.ResponseHeaders)
	if event.TCP != nil {
		printProtocolMessages(c.Out, "Request messages", event.TCP.RequestMessages)
		printProtocolMessages(c.Out, "Response messages", event.TCP.ResponseMessages)
	}
}

func (c *Commands) printTraceList(environment model.Environment, traces []model.TrafficTrace) {
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, "Traffic traces"), environment.Project, environment.Name)
	if len(traces) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No matching traces captured."))
		return
	}
	fmt.Fprintln(c.Out, c.Muted(c.Out, "TRACE  REQUEST / ROOT                 RESULT  TIME    SPANS  CORRELATION"))
	for _, trace := range traces {
		request := strings.TrimSpace(trace.Method + " " + trace.RequestTarget)
		if request == "" {
			request = trace.Source + ":" + trace.Target
		}
		result := fmt.Sprint(trace.Status)
		if trace.Error {
			result = c.Failure(c.Out, "error")
		} else if trace.Status == 0 {
			result = "ok"
		}
		fmt.Fprintf(c.Out, "#%-5d %-30s %-7s %5dms %5d  %s\n", trace.Number, request, result, trace.DurationMS, trace.SpanCount, trace.Correlation)
	}
}

func (c *Commands) printTrace(trace model.TrafficTrace) {
	fmt.Fprintf(c.Out, "%s #%d\n\n", c.Heading(c.Out, "Traffic trace"), trace.Number)
	fmt.Fprintf(c.Out, "  %-14s %s %s\n", "Root:", trace.Method, trace.RequestTarget)
	fmt.Fprintf(c.Out, "  %-14s %dms\n", "Duration:", trace.DurationMS)
	fmt.Fprintf(c.Out, "  %-14s %s\n", "Correlation:", trace.Correlation)
	fmt.Fprintf(c.Out, "  %-14s %d\n\n", "Spans:", trace.SpanCount)
	for _, span := range trace.Spans {
		exchange := span.Exchange
		operation := strings.TrimSpace(exchange.Method + " " + exchange.RequestTarget)
		if operation == "" {
			operation = tcpProtocolName(exchange) + " " + tcpOperationName(exchange)
		}
		fmt.Fprintf(c.Out, "  %s%s → %s  %-28s %dms  %s\n", strings.Repeat("  ", span.Depth), exchange.Source, exchange.Target, operation, exchange.DurationMS, span.Correlation)
	}
}

func tcpProtocolName(event model.TrafficExchange) string {
	if event.TCP != nil && event.TCP.ApplicationProtocol != "" {
		return strings.ToUpper(string(event.TCP.ApplicationProtocol))
	}
	return "TCP"
}

func tcpOperationName(event model.TrafficExchange) string {
	if event.TCP != nil && event.TCP.Operation != "" {
		return event.TCP.Operation
	}
	return "SESSION"
}

func printProtocolMessages(output io.Writer, label string, messages []model.TrafficMessage) {
	if len(messages) == 0 {
		return
	}
	fmt.Fprintf(output, "\n%s\n", label)
	for _, message := range messages {
		fmt.Fprintf(output, "  %-18s %s (%dms, %d bytes)\n", strings.ToUpper(message.Type)+":", message.Summary, message.OffsetMS, message.WireBytes)
		for _, field := range message.Fields {
			fmt.Fprintf(output, "    %-16s %s\n", field.Name+":", field.Value)
		}
		if message.Content != "" {
			for _, line := range strings.Split(message.Content, "\n") {
				fmt.Fprintf(output, "    %s\n", line)
			}
		}
		if message.Truncated {
			fmt.Fprintf(output, "    [truncated: %d of %d bytes]\n", message.CapturedBytes, message.ContentBytes)
		}
	}
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
