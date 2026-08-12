package cli

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

type colorPreference string

const (
	colorAuto   colorPreference = "auto"
	colorAlways colorPreference = "always"
	colorNever  colorPreference = "never"
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
	Color colorPreference `json:"color"`
}

type colorConfigOutput struct {
	Preference colorPreference `json:"preference"`
	Effective  bool            `json:"effective"`
	Source     string          `json:"source"`
	Reason     string          `json:"reason"`
	Path       string          `json:"path"`
}

func parseColorPreference(value string) (colorPreference, error) {
	preference := colorPreference(strings.ToLower(strings.TrimSpace(value)))
	switch preference {
	case colorAuto, colorAlways, colorNever:
		return preference, nil
	default:
		return "", usageError("color must be auto, always, or never")
	}
}

func (c *CLI) loadPreferences() error {
	c.colorPreference = colorAuto
	c.colorSource = "default"
	path := c.preferencesPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect CLI preferences: %w", err)
	}
	if err := ensurePreferencesDirectory(c.paths.Root); err != nil {
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
	preference, err := parseColorPreference(string(preferences.Color))
	if err != nil {
		return fmt.Errorf("decode CLI preferences: %w", err)
	}
	c.colorPreference = preference
	c.colorSource = "saved"
	return nil
}

func (c *CLI) saveColorPreference(preference colorPreference) error {
	if err := ensurePreferencesDirectory(c.paths.Root); err != nil {
		return err
	}
	content, err := json.MarshalIndent(cliPreferences{Color: preference}, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	temporary, err := os.CreateTemp(c.paths.Root, ".preferences-*")
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
	path := c.preferencesPath()
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
	c.colorPreference = preference
	c.colorSource = "saved"
	return nil
}

func (c *CLI) resetPreferences() error {
	path := c.preferencesPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		c.colorPreference = colorAuto
		c.colorSource = "default"
		return c.printPreferencesReset(false)
	}
	if err != nil {
		return fmt.Errorf("inspect CLI preferences: %w", err)
	}
	if err := ensurePreferencesDirectory(c.paths.Root); err != nil {
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
	c.colorPreference = colorAuto
	c.colorSource = "default"
	return c.printPreferencesReset(true)
}

func (c *CLI) printPreferencesReset(removed bool) error {
	status := "already-default"
	if removed {
		status = "reset"
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "reset", Path: c.preferencesPath(), Status: status})
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

func (c *CLI) preferencesPath() string {
	return filepath.Join(c.paths.Root, preferencesFile)
}

func (c *CLI) colorConfig() colorConfigOutput {
	effective, reason := c.colorDecision(c.Out)
	return colorConfigOutput{
		Preference: c.colorPreference,
		Effective:  effective,
		Source:     c.colorSource,
		Reason:     reason,
		Path:       c.preferencesPath(),
	}
}

func (c *CLI) printColorConfig() error {
	status := c.colorConfig()
	if c.jsonOutput {
		return writeJSON(c.Out, status)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Color"))
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %-12s %s (%s)\n", "Preference:", status.Preference, status.Source)
	effective := "disabled"
	if status.Effective {
		effective = c.success(c.Out, "enabled")
	}
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Effective:", effective)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Reason:", status.Reason)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Config:", status.Path)
	return nil
}

func (c *CLI) colorDecision(writer io.Writer) (bool, string) {
	if c.jsonOutput {
		return false, "JSON output"
	}
	if c.completionOutput {
		return false, "shell completion"
	}
	if c.noColor {
		return false, "--no-color"
	}
	if value, present := os.LookupEnv("NO_COLOR"); present && value != "" {
		return false, "NO_COLOR is set"
	}
	switch c.colorPreference {
	case colorAlways:
		return true, "saved preference is always"
	case colorNever:
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

func (c *CLI) styled(writer io.Writer, sequence, value string) string {
	enabled, _ := c.colorDecision(writer)
	if !enabled || value == "" {
		return value
	}
	return sequence + value + ansiReset
}

func (c *CLI) heading(writer io.Writer, value string) string {
	return c.styled(writer, ansiBoldCyan, value)
}

func (c *CLI) accent(writer io.Writer, value string) string {
	return c.styled(writer, ansiCyan, value)
}

func (c *CLI) muted(writer io.Writer, value string) string {
	return c.styled(writer, ansiDim, value)
}

func (c *CLI) success(writer io.Writer, value string) string {
	return c.styled(writer, ansiGreen, value)
}

func (c *CLI) warning(writer io.Writer, value string) string {
	return c.styled(writer, ansiYellow, value)
}

func (c *CLI) failure(writer io.Writer, value string) string {
	return c.styled(writer, ansiRed, value)
}

func (c *CLI) state(writer io.Writer, value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "healthy", "ready", "running", "active", "succeeded", "passed", "pass", "selected":
		return c.success(writer, value)
	case "degraded", "starting", "stopping", "warning", "warn", "outdated", "installed; service stopped", "running; daemon unavailable":
		return c.warning(writer, value)
	case "failed", "fail", "unhealthy", "incompatible", "error":
		return c.failure(writer, value)
	case "stopped", "disabled", "missing", "unknown", "skipped", "skip", "not installed", "not-installed":
		return c.muted(writer, value)
	case "info", "informational":
		return c.accent(writer, value)
	default:
		return value
	}
}

func boolFlagRequested(args []string, name string) bool {
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

func isCompletionRequest(args []string) bool {
	for _, argument := range args {
		if argument == "completion" || argument == "__complete" || argument == "__completeNoDesc" {
			return true
		}
	}
	return false
}

func isConfigResetRequest(args []string) bool {
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

func (c *CLI) usageTemplate() string {
	heading := func(value string) string { return c.heading(c.Out, value) }
	return heading("Usage:") + `{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

` + heading("Aliases:") + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + heading("Examples:") + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

` + heading("Available Commands:") + `{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

` + heading("Additional Commands:") + `{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + heading("Flags:") + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + heading("Global Flags:") + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

` + heading("Additional help topics:") + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
}
