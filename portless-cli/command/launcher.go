package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const homebrewLauncherReason = "launcher is managed by Homebrew; run `brew uninstall runportless/tap/portless` after Portless state removal"

// InspectLauncher builds a conservative uninstall plan for the launcher used
// to invoke the current process. A package-managed distribution is preserved
// for its package manager to remove.
func InspectLauncher(distribution string) LauncherPlan {
	invocation, err := ResolveInvocationPath(os.Args[0])
	if err != nil {
		return LauncherPlan{Kind: "unknown", Action: "preserve", Reason: err.Error(), Distribution: distribution}
	}
	executable, err := os.Executable()
	if err != nil {
		return LauncherPlan{Path: invocation, Kind: "unknown", Action: "preserve", Reason: err.Error(), Distribution: distribution}
	}
	return ClassifyLauncher(invocation, executable, RecognizedLauncherDirectories(), distribution)
}

// ResolveInvocationPath converts an argv[0]-style executable name or path into
// an absolute launcher path.
func ResolveInvocationPath(argument string) (string, error) {
	path := argument
	if !strings.ContainsRune(path, filepath.Separator) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return "", fmt.Errorf("locate current CLI launcher: %w", err)
		}
		path = resolved
	}
	return filepath.Abs(path)
}

// ClassifyLauncher determines whether invocation is a verified Portless
// launcher that can be removed without deleting a source-tree build or a
// package-manager-owned launcher.
func ClassifyLauncher(invocation, executable string, installDirectories []string, distribution string) LauncherPlan {
	plan := LauncherPlan{Path: invocation, Kind: "unknown", Action: "preserve", Reason: "launcher identity could not be verified", Distribution: distribution}
	invocation, invocationErr := filepath.Abs(invocation)
	if invocationErr != nil {
		plan.Reason = invocationErr.Error()
		return plan
	}
	plan.Path = filepath.Clean(invocation)
	executable, executableErr := filepath.Abs(executable)
	if executableErr != nil {
		plan.Reason = executableErr.Error()
		return plan
	}
	executable, executableErr = filepath.EvalSymlinks(executable)
	if executableErr != nil {
		plan.Reason = "current executable cannot be resolved: " + executableErr.Error()
		return plan
	}
	plan.Executable = executable
	info, err := os.Lstat(plan.Path)
	if errors.Is(err, os.ErrNotExist) {
		plan.Kind = "missing"
		plan.Action = "not-found"
		plan.Reason = "launcher is already absent"
		return plan
	}
	if err != nil {
		plan.Reason = err.Error()
		return plan
	}
	if info.Mode()&os.ModeSymlink != 0 {
		plan.Kind = "symlink"
		target, err := filepath.EvalSymlinks(plan.Path)
		if err != nil {
			plan.Reason = "symlink target cannot be resolved: " + err.Error()
			return plan
		}
		plan.Target = target
		if SameFile(target, executable) {
			if distribution == "homebrew" {
				plan.Reason = homebrewLauncherReason
				return plan
			}
			plan.Action = "remove"
			plan.Reason = "verified launcher symlink; its target will be preserved"
			return plan
		}
		plan.Reason = "symlink does not target the running Portless executable"
		return plan
	}
	if !info.Mode().IsRegular() {
		plan.Kind = "other"
		plan.Reason = "launcher is not a regular file or symlink"
		return plan
	}
	plan.Kind = "regular-file"
	plan.Target = plan.Path
	if filepath.Base(plan.Path) != "portless" || !SameFile(plan.Path, executable) {
		plan.Reason = "file is not the running portless executable"
		return plan
	}
	if !DirectoryRecognized(filepath.Dir(plan.Path), installDirectories) {
		plan.Reason = "running executable is outside a recognized CLI install directory; source-tree builds are never removed automatically"
		return plan
	}
	if distribution == "homebrew" {
		plan.Reason = homebrewLauncherReason
		return plan
	}
	plan.Action = "remove"
	plan.Reason = "verified installed CLI executable"
	return plan
}

// RemoveLauncher revalidates and removes a launcher approved by plan. It
// refuses removal when the launcher changed after inspection.
func RemoveLauncher(plan LauncherPlan) (bool, error) {
	if plan.Action != "remove" || plan.Path == "" {
		return false, nil
	}
	fresh := ClassifyLauncher(plan.Path, plan.Executable, RecognizedLauncherDirectories(), plan.Distribution)
	if fresh.Action == "not-found" {
		return true, nil
	}
	if fresh.Action != "remove" || fresh.Kind != plan.Kind || (plan.Target != "" && fresh.Target != plan.Target) {
		return false, errors.New("launcher changed after uninstall preflight; it was left in place")
	}
	if err := os.Remove(plan.Path); err != nil {
		return false, err
	}
	return true, nil
}

// RecognizedLauncherDirectories returns conventional binary installation
// directories in which a verified regular Portless launcher may be removed.
func RecognizedLauncherDirectories() []string {
	directories := make([]string, 0, 8)
	if value := os.Getenv("GOBIN"); value != "" {
		directories = append(directories, value)
	}
	if value := os.Getenv("GOPATH"); value != "" {
		for _, root := range filepath.SplitList(value) {
			directories = append(directories, filepath.Join(root, "bin"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		directories = append(directories, filepath.Join(home, "go", "bin"), filepath.Join(home, "bin"), filepath.Join(home, ".local", "bin"))
	}
	directories = append(directories, "/usr/local/bin", "/opt/homebrew/bin")
	return directories
}

// DirectoryRecognized reports whether directory exactly matches one of the
// recognized installation directories after absolute-path normalization.
func DirectoryRecognized(directory string, recognized []string) bool {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return false
	}
	for _, candidate := range recognized {
		candidate, err = filepath.Abs(candidate)
		if err == nil && filepath.Clean(candidate) == filepath.Clean(directory) {
			return true
		}
	}
	return false
}

// SameFile reports whether two paths identify the same existing filesystem
// object.
func SameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
