package administration

import (
	"context"
	"fmt"
	"strings"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-cli/doctor"
)

func (c *Commands) doctor(ctx context.Context, scope doctor.Scope, jsonOutput bool) error {
	uid, _ := c.Local.UserIDs()
	report, err := c.Local.Diagnose(ctx, c.Paths, scope, uid)
	if err != nil {
		return err
	}
	if jsonOutput {
		if err := command.WriteJSON(c.Out, report); err != nil {
			return err
		}
	} else {
		printDoctorReport(c, report)
	}
	if report.Summary.Failed > 0 {
		return &command.ReportedError{}
	}
	return nil
}

func printDoctorReport(c *Commands, report doctor.Report) {
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Portless doctor"))
	component := ""
	for _, check := range report.Checks {
		if check.Component != component {
			component = check.Component
			fmt.Fprintln(c.Out)
			fmt.Fprintln(c.Out, c.Heading(c.Out, doctorComponentName(component)))
		}
		status := strings.ToUpper(string(check.Status))
		fmt.Fprintf(c.Out, "  %s  %s\n", c.State(c.Out, fmt.Sprintf("%-4s", status)), check.Summary)
		if check.Detail != "" {
			fmt.Fprintln(c.Out, "        "+check.Detail)
		}
		if check.Remediation != "" {
			fmt.Fprintln(c.Out, "        "+c.Warning(c.Out, "fix:")+" "+check.Remediation)
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
