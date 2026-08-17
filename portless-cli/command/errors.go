package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// UsageFailure marks an invalid invocation whose error should be accompanied
// by command usage and mapped to the CLI usage exit code.
type UsageFailure struct {
	Err     error
	Command *cobra.Command
}

// Error returns the underlying usage error message.
func (e *UsageFailure) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying usage error for errors.Is and errors.As.
func (e *UsageFailure) Unwrap() error { return e.Err }

// HelpError marks an invocation that omitted required positional arguments.
// Command identifies the command whose help should be displayed.
type HelpError struct {
	Command *cobra.Command
}

// Error reports that required positional arguments were omitted.
func (e *HelpError) Error() string { return "required arguments were omitted" }

// ReportedError marks a failed command that has already written its structured
// result, preventing the root command from printing a second error.
type ReportedError struct{}

// Error returns the sentinel's diagnostic description.
func (*ReportedError) Error() string { return "command reported failures" }

// UsageError formats an error and marks it as an invalid CLI invocation.
func UsageError(message string, arguments ...any) error {
	return &UsageFailure{Err: fmt.Errorf(message, arguments...)}
}

// UsageArgs wraps a Cobra positional validator so omitted required arguments
// show help while other validation failures use the CLI usage-error path.
func UsageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < requiredArgumentCount(cmd.Use) {
			return &HelpError{Command: cmd}
		}
		if err := validate(cmd, args); err != nil {
			return &UsageFailure{Err: err, Command: cmd}
		}
		return nil
	}
}

func requiredArgumentCount(use string) int {
	count := 0
	for _, field := range strings.Fields(use) {
		if strings.HasPrefix(field, "<") {
			count++
		}
	}
	return count
}

// IsCobraSyntaxError reports whether err is one of Cobra's invocation syntax
// errors that should use the CLI usage exit code.
func IsCobraSyntaxError(err error) bool {
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown flag: ") ||
		strings.HasPrefix(message, "required flag(s) ") ||
		strings.HasPrefix(message, "at least one of the flags in the group ") ||
		strings.Contains(message, " if any flags in the group ") ||
		strings.Contains(message, " were all set")
}

// CommandGroup constructs a parent command that shows its help when invoked
// without a subcommand.
func CommandGroup(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
}

// FixedCompletions returns a Cobra completion function backed by static values
// and disables file-name completion.
func FixedCompletions(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// FirstArg returns the first argument or an empty string when none is present.
func FirstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// ValidLimit validates that limit is between one and maximum, inclusive.
func ValidLimit(limit, maximum int) error {
	if limit < 1 || limit > maximum {
		return UsageError("--limit must be between 1 and %d", maximum)
	}
	return nil
}

// Truncate returns at most the first limit items without copying the slice.
func Truncate[T any](items []T, limit int) []T {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

// ParseEdge parses the public source:target edge selector used by connection,
// traffic, recording, and fault commands.
func ParseEdge(value string) (string, string, error) {
	if value == "" {
		return "", "", nil
	}
	source, target, found := strings.Cut(value, ":")
	if !found || source == "" || target == "" || strings.Contains(target, ":") {
		return "", "", fmt.Errorf("edge must use source:target")
	}
	return source, target, nil
}
