package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func inspectLauncher() launcherPlan {
	invocation, err := resolveInvocationPath(os.Args[0])
	if err != nil {
		return launcherPlan{Kind: "unknown", Action: "preserve", Reason: err.Error()}
	}
	executable, err := os.Executable()
	if err != nil {
		return launcherPlan{Path: invocation, Kind: "unknown", Action: "preserve", Reason: err.Error()}
	}
	return classifyLauncher(invocation, executable, recognizedLauncherDirectories())
}

func resolveInvocationPath(argument string) (string, error) {
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

func classifyLauncher(invocation, executable string, installDirectories []string) launcherPlan {
	plan := launcherPlan{Path: invocation, Kind: "unknown", Action: "preserve", Reason: "launcher identity could not be verified"}
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
	plan.executable = executable
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
		if sameFile(target, executable) {
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
	if filepath.Base(plan.Path) != "portless" || !sameFile(plan.Path, executable) {
		plan.Reason = "file is not the running portless executable"
		return plan
	}
	if !directoryRecognized(filepath.Dir(plan.Path), installDirectories) {
		plan.Reason = "running executable is outside a recognized CLI install directory; source-tree builds are never removed automatically"
		return plan
	}
	plan.Action = "remove"
	plan.Reason = "verified installed CLI executable"
	return plan
}

func removeLauncher(plan launcherPlan) (bool, error) {
	if plan.Action != "remove" || plan.Path == "" {
		return false, nil
	}
	fresh := classifyLauncher(plan.Path, plan.executable, recognizedLauncherDirectories())
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

func recognizedLauncherDirectories() []string {
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

func directoryRecognized(directory string, recognized []string) bool {
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

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
