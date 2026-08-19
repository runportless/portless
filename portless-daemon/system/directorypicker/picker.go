// Package directorypicker opens the operating system's native directory
// chooser for the authenticated Portless control plane.
package directorypicker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const macOSPickerScript = `on run argv
	set promptText to item 1 of argv
	set initialPath to item 2 of argv
	if initialPath is "" then
		set selectedFolder to choose folder with prompt promptText
	else
		set selectedFolder to choose folder with prompt promptText default location POSIX file initialPath
	end if
	return POSIX path of selectedFolder
end run`

type commandRunner func(context.Context, string, ...string) ([]byte, int, error)

type selector struct {
	platform string
	lookPath func(string) (string, error)
	run      commandRunner
}

// Select opens the platform-native directory chooser. The returned canceled
// value is true when the user dismisses the chooser without selecting a path.
func Select(ctx context.Context, prompt, initialPath string) (path string, canceled bool, err error) {
	return selector{platform: runtime.GOOS, lookPath: exec.LookPath, run: runCommand}.selectDirectory(ctx, prompt, initialPath)
}

func (s selector) selectDirectory(ctx context.Context, prompt, initialPath string) (string, bool, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = "Choose a directory"
	}
	initialPath = usableInitialDirectory(initialPath)
	switch s.platform {
	case "darwin":
		return s.selectMacOS(ctx, prompt, initialPath)
	case "linux":
		return s.selectLinux(ctx, prompt, initialPath)
	default:
		return "", false, fmt.Errorf("native directory selection is not supported on %s", s.platform)
	}
}

func (s selector) selectMacOS(ctx context.Context, prompt, initialPath string) (string, bool, error) {
	output, exitCode, err := s.run(ctx, "osascript", "-e", macOSPickerScript, prompt, initialPath)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		message := strings.ToLower(string(output))
		if exitCode == 1 && (strings.Contains(message, "-128") || strings.Contains(message, "user canceled")) {
			return "", true, nil
		}
		return "", false, commandError("open the macOS directory chooser", output, err)
	}
	path, err := selectedDirectory(output)
	return path, false, err
}

func (s selector) selectLinux(ctx context.Context, prompt, initialPath string) (string, bool, error) {
	if executable, err := s.lookPath("zenity"); err == nil {
		arguments := []string{"--file-selection", "--directory", "--title=" + prompt}
		if initialPath != "" {
			arguments = append(arguments, "--filename="+initialPath+string(os.PathSeparator))
		}
		return s.runLinuxPicker(ctx, executable, arguments...)
	}
	if executable, err := s.lookPath("kdialog"); err == nil {
		arguments := []string{"--getexistingdirectory", initialPath, prompt}
		return s.runLinuxPicker(ctx, executable, arguments...)
	}
	return "", false, errors.New("no native directory chooser is available; install zenity or kdialog")
}

func (s selector) runLinuxPicker(ctx context.Context, executable string, arguments ...string) (string, bool, error) {
	output, exitCode, err := s.run(ctx, executable, arguments...)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		if exitCode == 1 && strings.TrimSpace(string(output)) == "" {
			return "", true, nil
		}
		return "", false, commandError("open the Linux directory chooser", output, err)
	}
	path, err := selectedDirectory(output)
	return path, false, err
}

func runCommand(ctx context.Context, executable string, arguments ...string) ([]byte, int, error) {
	output, err := exec.CommandContext(ctx, executable, arguments...).CombinedOutput()
	if err == nil {
		return output, 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return output, exitError.ExitCode(), err
	}
	return output, -1, err
}

func selectedDirectory(output []byte) (string, error) {
	selected := filepath.Clean(strings.TrimSpace(string(output)))
	if selected == "." || !filepath.IsAbs(selected) {
		return "", errors.New("directory chooser returned a non-absolute path")
	}
	info, err := os.Stat(selected)
	if err != nil {
		return "", fmt.Errorf("inspect selected directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("selected path %q is not a directory", selected)
	}
	return selected, nil
}

func usableInitialDirectory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	cleaned := filepath.Clean(value)
	info, err := os.Stat(cleaned)
	if err != nil || !info.IsDir() {
		return ""
	}
	return cleaned
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
