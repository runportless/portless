package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type UsageFailure struct {
	Err     error
	Command *cobra.Command
}

func (e *UsageFailure) Error() string { return e.Err.Error() }
func (e *UsageFailure) Unwrap() error { return e.Err }

type HelpError struct {
	Command *cobra.Command
}

func (e *HelpError) Error() string { return "required arguments were omitted" }

type ReportedError struct{}

func (*ReportedError) Error() string { return "command reported failures" }

func UsageError(message string, arguments ...any) error {
	return &UsageFailure{Err: fmt.Errorf(message, arguments...)}
}

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

func IsCobraSyntaxError(err error) bool {
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown flag: ") ||
		strings.HasPrefix(message, "required flag(s) ") ||
		strings.HasPrefix(message, "at least one of the flags in the group ") ||
		strings.Contains(message, " if any flags in the group ") ||
		strings.Contains(message, " were all set")
}

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

func FixedCompletions(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

func FirstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func ValidLimit(limit, maximum int) error {
	if limit < 1 || limit > maximum {
		return UsageError("--limit must be between 1 and %d", maximum)
	}
	return nil
}

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
