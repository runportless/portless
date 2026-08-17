package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mattn/go-isatty"
)

// ColorPreference controls when the CLI emits ANSI color sequences.
type ColorPreference string

const (
	// ColorAuto enables color only for an interactive terminal.
	ColorAuto ColorPreference = "auto"
	// ColorAlways enables color for human-readable output even when redirected.
	ColorAlways ColorPreference = "always"
	// ColorNever disables color for every invocation.
	ColorNever ColorPreference = "never"
)

const (
	ansiReset       = "\x1b[0m"
	ansiDim         = "\x1b[2m"
	ansiRed         = "\x1b[31m"
	ansiGreen       = "\x1b[32m"
	ansiYellow      = "\x1b[33m"
	ansiCyan        = "\x1b[36m"
	ansiBoldCyan    = "\x1b[1;36m"
	preferencesFile = "preferences.json"
)

type cliPreferences struct {
	Color ColorPreference `json:"color"`
}

type colorConfigOutput struct {
	Preference ColorPreference `json:"preference"`
	Effective  bool            `json:"effective"`
	Source     string          `json:"source"`
	Reason     string          `json:"reason"`
	Path       string          `json:"path"`
}

// ParseColorPreference normalizes and validates a persisted or user-supplied
// color preference.
func ParseColorPreference(value string) (ColorPreference, error) {
	preference := ColorPreference(strings.ToLower(strings.TrimSpace(value)))
	switch preference {
	case ColorAuto, ColorAlways, ColorNever:
		return preference, nil
	default:
		return "", UsageError("color must be auto, always, or never")
	}
}

// LoadPreferences loads the current user's CLI preferences into the context.
// Missing preferences select defaults; existing paths are ownership-checked
// and repaired to private permissions before they are read.
func (c *Context) LoadPreferences() error {
	c.ColorPreference = ColorAuto
	c.ColorSource = "default"
	path := c.PreferencesPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect CLI preferences: %w", err)
	}
	if err := ensurePreferencesDirectory(c.Paths.Root); err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("CLI preferences path %s must be a regular file", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("CLI preferences file %s belongs to UID %d, expected UID %d", path, stat.Uid, os.Geteuid())
	}
	if info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("protect CLI preferences: %w", err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open CLI preferences: %w", err)
	}
	defer file.Close()
	var preferences cliPreferences
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	if err := decoder.Decode(&preferences); err != nil {
		return fmt.Errorf("decode CLI preferences: %w", err)
	}
	preference, err := ParseColorPreference(string(preferences.Color))
	if err != nil {
		return fmt.Errorf("decode CLI preferences: %w", err)
	}
	c.ColorPreference = preference
	c.ColorSource = "saved"
	return nil
}

// SaveColorPreference atomically persists preference in a private file and
// updates the active context.
func (c *Context) SaveColorPreference(preference ColorPreference) error {
	if err := ensurePreferencesDirectory(c.Paths.Root); err != nil {
		return err
	}
	content, err := json.MarshalIndent(cliPreferences{Color: preference}, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	temporary, err := os.CreateTemp(c.Paths.Root, ".preferences-*")
	if err != nil {
		return fmt.Errorf("create temporary CLI preferences: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect temporary CLI preferences: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write CLI preferences: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync CLI preferences: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close CLI preferences: %w", err)
	}
	path := c.PreferencesPath()
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace non-file CLI preferences path %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect existing CLI preferences: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save CLI preferences: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect CLI preferences: %w", err)
	}
	c.ColorPreference = preference
	c.ColorSource = "saved"
	return nil
}

// ResetPreferences removes the current user's preference file and restores
// the in-memory defaults. Repeated resets are successful.
func (c *Context) ResetPreferences() error {
	path := c.PreferencesPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		c.ColorPreference = ColorAuto
		c.ColorSource = "default"
		return c.PrintPreferencesReset(false)
	}
	if err != nil {
		return fmt.Errorf("inspect CLI preferences: %w", err)
	}
	if err := ensurePreferencesDirectory(c.Paths.Root); err != nil {
		return err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refusing to remove non-file CLI preferences path %s", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("CLI preferences path %s belongs to UID %d, expected UID %d", path, stat.Uid, os.Geteuid())
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("reset CLI preferences: %w", err)
	}
	c.ColorPreference = ColorAuto
	c.ColorSource = "default"
	return c.PrintPreferencesReset(true)
}

// PrintPreferencesReset writes the human-readable or JSON result of a
// preference reset.
func (c *Context) PrintPreferencesReset(removed bool) error {
	status := "already-default"
	if removed {
		status = "reset"
	}
	if c.JSONOutput {
		return WriteJSON(c.Out, ActionOutput{Action: "reset", Path: c.PreferencesPath(), Status: status})
	}
	if removed {
		fmt.Fprintln(c.Out, "CLI preferences reset to defaults.")
	} else {
		fmt.Fprintln(c.Out, "CLI preferences are already at defaults.")
	}
	return nil
}

func ensurePreferencesDirectory(path string) error {
	if path == "" || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("refusing to prepare a broad preferences directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create preferences directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect preferences directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("preferences directory %s must be a real directory", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("preferences directory %s belongs to UID %d, expected UID %d", path, stat.Uid, os.Geteuid())
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect preferences directory: %w", err)
	}
	return nil
}

// PreferencesPath returns the preference-file path for this CLI installation.
func (c *Context) PreferencesPath() string {
	return filepath.Join(c.Paths.Root, preferencesFile)
}

// ColorConfig returns the configured color preference and its effective state
// for the current output writer.
func (c *Context) ColorConfig() colorConfigOutput {
	effective, reason := c.ColorDecision(c.Out)
	return colorConfigOutput{
		Preference: c.ColorPreference,
		Effective:  effective,
		Source:     c.ColorSource,
		Reason:     reason,
		Path:       c.PreferencesPath(),
	}
}

// PrintColorConfig writes the active color configuration in the selected
// output format.
func (c *Context) PrintColorConfig() error {
	status := c.ColorConfig()
	if c.JSONOutput {
		return WriteJSON(c.Out, status)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Color"))
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %-12s %s (%s)\n", "Preference:", status.Preference, status.Source)
	effective := "disabled"
	if status.Effective {
		effective = c.Success(c.Out, "enabled")
	}
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Effective:", effective)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Reason:", status.Reason)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Config:", status.Path)
	return nil
}

// ColorDecision reports whether color is enabled for writer and explains the
// highest-priority rule that made the decision.
func (c *Context) ColorDecision(writer io.Writer) (bool, string) {
	if c.JSONOutput {
		return false, "JSON output"
	}
	if c.CompletionOutput {
		return false, "shell completion"
	}
	if c.NoColor {
		return false, "--no-color"
	}
	if value, present := os.LookupEnv("NO_COLOR"); present && value != "" {
		return false, "NO_COLOR is set"
	}
	switch c.ColorPreference {
	case ColorAlways:
		return true, "saved preference is always"
	case ColorNever:
		return false, "saved preference is never"
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false, "TERM is dumb"
	}
	if isTerminalWriter(writer) {
		return true, "output is an interactive terminal"
	}
	return false, "output is not an interactive terminal"
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

// Styled wraps value in an ANSI sequence when color is enabled for writer.
func (c *Context) Styled(writer io.Writer, sequence, value string) string {
	enabled, _ := c.ColorDecision(writer)
	if !enabled || value == "" {
		return value
	}
	return sequence + value + ansiReset
}

// Heading styles value as a CLI heading when color is enabled.
func (c *Context) Heading(writer io.Writer, value string) string {
	return c.Styled(writer, ansiBoldCyan, value)
}

// Accent styles value with the CLI accent color when color is enabled.
func (c *Context) Accent(writer io.Writer, value string) string {
	return c.Styled(writer, ansiCyan, value)
}

// Muted styles value as secondary text when color is enabled.
func (c *Context) Muted(writer io.Writer, value string) string {
	return c.Styled(writer, ansiDim, value)
}

// Success styles value with the CLI success color when color is enabled.
func (c *Context) Success(writer io.Writer, value string) string {
	return c.Styled(writer, ansiGreen, value)
}

// Warning styles value with the CLI warning color when color is enabled.
func (c *Context) Warning(writer io.Writer, value string) string {
	return c.Styled(writer, ansiYellow, value)
}

// Failure styles value with the CLI failure color when color is enabled.
func (c *Context) Failure(writer io.Writer, value string) string {
	return c.Styled(writer, ansiRed, value)
}

// State applies the semantic success, warning, failure, muted, or informational
// style associated with a textual state value.
func (c *Context) State(writer io.Writer, value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "healthy", "development", "ready", "attached", "running", "active", "succeeded", "passed", "pass", "selected":
		return c.Success(writer, value)
	case "degraded", "starting", "stopping", "warning", "warn", "outdated", "installed; service stopped", "running; daemon unavailable":
		return c.Warning(writer, value)
	case "failed", "fail", "unhealthy", "incompatible", "error":
		return c.Failure(writer, value)
	case "stopped", "disabled", "missing", "unknown", "skipped", "skip", "not installed", "not-installed":
		return c.Muted(writer, value)
	case "info", "informational":
		return c.Accent(writer, value)
	default:
		return value
	}
}

// BoolFlagRequested resolves the last explicit value of a boolean long flag
// before the argument separator.
func BoolFlagRequested(args []string, name string) bool {
	requested := false
	flag := "--" + name
	for _, argument := range args {
		if argument == "--" {
			break
		}
		switch {
		case argument == flag:
			requested = true
		case strings.HasPrefix(argument, flag+"="):
			if value, err := strconv.ParseBool(strings.TrimPrefix(argument, flag+"=")); err == nil {
				requested = value
			}
		}
	}
	return requested
}

// IsCompletionRequest reports whether args invoke public or internal shell
// completion handling.
func IsCompletionRequest(args []string) bool {
	for _, argument := range args {
		if argument == "completion" || argument == "__complete" || argument == "__completeNoDesc" {
			return true
		}
	}
	return false
}

// IsConfigResetRequest reports whether args target config reset, which must
// remain usable even when the saved preference file is malformed.
func IsConfigResetRequest(args []string) bool {
	positionals := make([]string, 0, 2)
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		positionals = append(positionals, argument)
		if len(positionals) == 2 {
			break
		}
	}
	return len(positionals) == 2 && positionals[0] == "config" && positionals[1] == "reset"
}

// UsageTemplate returns the Cobra usage template with headings styled through
// the active presentation settings.
func (c *Context) UsageTemplate() string {
	Heading := func(value string) string { return c.Heading(c.Out, value) }
	return Heading("Usage:") + `{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

` + Heading("Aliases:") + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + Heading("Examples:") + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

` + Heading("Available Commands:") + `{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

` + Heading("Additional Commands:") + `{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + Heading("Flags:") + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + Heading("Global Flags:") + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

` + Heading("Additional help topics:") + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
}
