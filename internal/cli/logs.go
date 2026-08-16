package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) logs(ctx context.Context, requestedService string, options logsOptions) error {
	if err := validLimit(options.limit, 10_000); err != nil {
		return err
	}
	if options.since < 0 {
		return usageError("--since cannot be negative")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if _, err := logServiceNames(environment, requestedService); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	var cursor time.Time
	initial := true
	for {
		since := ""
		if !cursor.IsZero() {
			since = cursor.Format(time.RFC3339Nano)
		} else if options.since > 0 {
			since = options.since.String()
		}
		response, err := client.Logs(ctx, environment.Project, environment.Name, requestedService, options.limit, since)
		if err != nil {
			return err
		}
		if response.Entries == nil {
			response.Entries = []model.LogEntry{}
		}
		if !options.tail {
			if c.jsonOutput {
				return writeJSON(c.Out, logsOutput{Project: environment.Project, Environment: environment.Name, Entries: response.Entries})
			}
			c.printLogs(environment, response.Entries, requestedService != "", options.timestamps)
			return nil
		}
		for _, entry := range response.Entries {
			if entry.Timestamp.After(cursor) {
				cursor = entry.Timestamp
				seen = make(map[string]struct{})
			}
			key := logEntryKey(entry)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			if c.jsonOutput {
				if err := writeJSONLine(c.Out, entry); err != nil {
					return err
				}
			} else {
				c.printLogEntry(entry, requestedService == "", options.timestamps)
			}
		}
		if initial && len(response.Entries) == 0 && !c.jsonOutput {
			c.printLogs(environment, response.Entries, requestedService != "", options.timestamps)
		}
		initial = false
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func logServiceNames(environment model.Environment, requested string) ([]string, error) {
	if requested != "" {
		for _, service := range environment.Services {
			if strings.EqualFold(service.Name, requested) {
				return []string{service.Name}, nil
			}
		}
		return nil, fmt.Errorf("service %s was not found in %s/%s", requested, environment.Project, environment.Name)
	}
	services := make([]string, 0, len(environment.Services))
	for _, service := range environment.Services {
		services = append(services, service.Name)
	}
	return services, nil
}

func (c *CLI) printLogs(environment model.Environment, entries []model.LogEntry, singleService, timestamps bool) {
	for _, entry := range entries {
		c.printLogEntry(entry, !singleService, timestamps)
	}
	if len(entries) > 0 {
		return
	}
	if singleService {
		fmt.Fprintln(c.Out, "No logs for the selected service.")
		return
	}
	fmt.Fprintf(c.Out, "No logs for %s/%s.\n", environment.Project, environment.Name)
}

func (c *CLI) printLogEntry(entry model.LogEntry, includeService, timestamps bool) {
	prefix := ""
	if timestamps {
		prefix = entry.Timestamp.Local().Format("15:04:05.000") + " "
	}
	if includeService {
		fmt.Fprintf(c.Out, "%s%s %s\n", prefix, c.accent(c.Out, "["+entry.Service+"]"), entry.Message)
		return
	}
	fmt.Fprintln(c.Out, prefix+entry.Message)
}

func logEntryKey(entry model.LogEntry) string {
	return entry.Timestamp.Format(time.RFC3339Nano) + "\x00" + entry.Service + "\x00" + entry.Stream + "\x00" + strconv.FormatInt(entry.Generation, 10) + "\x00" + entry.Message
}
