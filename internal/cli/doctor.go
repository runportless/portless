package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/portless-run/portless/internal/diagnostics"
)

func (c *CLI) doctor(ctx context.Context, scope diagnostics.Scope, jsonOutput bool) error {
	uid, _ := requestingUserIDs()
	report, err := diagnostics.Run(ctx, c.paths, scope, uid)
	if err != nil {
		return err
	}
	if jsonOutput {
		if err := writeJSON(c.Out, report); err != nil {
			return err
		}
	} else {
		printDoctorReport(c, report)
	}
	if report.Summary.Failed > 0 {
		return &reportedCommandError{}
	}
	return nil
}

func printDoctorReport(c *CLI, report diagnostics.Report) {
	fmt.Fprintln(c.Out, c.heading(c.Out, "Portless doctor"))
	component := ""
	for _, check := range report.Checks {
		if check.Component != component {
			component = check.Component
			fmt.Fprintln(c.Out)
			fmt.Fprintln(c.Out, c.heading(c.Out, doctorComponentName(component)))
		}
		status := strings.ToUpper(string(check.Status))
		fmt.Fprintf(c.Out, "  %s  %s\n", c.state(c.Out, fmt.Sprintf("%-4s", status)), check.Summary)
		if check.Detail != "" {
			fmt.Fprintln(c.Out, "        "+check.Detail)
		}
		if check.Remediation != "" {
			fmt.Fprintln(c.Out, "        "+c.warning(c.Out, "fix:")+" "+check.Remediation)
		}
	}
	fmt.Fprintf(c.Out, "\n%d passed, %s, %s, %d failed, %d skipped\n",
		report.Summary.Passed,
		countNoun(report.Summary.Informational, "informational check", "informational checks"),
		countNoun(report.Summary.Warnings, "warning", "warnings"),
		report.Summary.Failed, report.Summary.Skipped)
}

func countNoun(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func doctorComponentName(component string) string {
	switch component {
	case "daemon":
		return "Daemon"
	case "relay":
		return "Relay"
	case "runtime":
		return "Container runtime"
	default:
		return component
	}
}
