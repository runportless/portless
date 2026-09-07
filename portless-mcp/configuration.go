package portlessmcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func validateConfig(config Config) (Config, error) {
	scopes := 0
	if config.Environment != "" {
		scopes++
	}
	if config.Project != "" {
		scopes++
		if err := contract.ValidateProjectName(config.Project); err != nil {
			return config, fmt.Errorf("invalid MCP project: %w", err)
		}
	}
	if config.AllEnvironments {
		scopes++
	}
	if scopes > 1 {
		return config, errors.New("--env, --project, and --all-environments are mutually exclusive")
	}
	if config.AllowReplay && !config.AllowSensitiveTraffic {
		return config, errors.New("--allow-replay requires --allow-sensitive-traffic")
	}
	roots := slices.Clone(config.AllowedSourceRoots)
	if scopes == 0 && config.AllowConfiguration && config.WorkspaceRoot != "" {
		roots = append(roots, config.WorkspaceRoot)
	}
	config.AllowedSourceRoots = nil
	for _, value := range roots {
		root, err := canonicalDirectory(value)
		if err != nil {
			return config, fmt.Errorf("MCP source root: %w", err)
		}
		if !slices.Contains(config.AllowedSourceRoots, root) {
			config.AllowedSourceRoots = append(config.AllowedSourceRoots, root)
		}
	}
	return config, nil
}

func canonicalDirectory(value string) (string, error) {
	if value == "" {
		return "", errors.New("directory is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("source path must be a directory")
	}
	return canonical, nil
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func (r *runtime) sourcePath(value string) (path, root string, err error) {
	if !filepath.IsAbs(value) && r.config.WorkspaceRoot != "" {
		value = filepath.Join(r.config.WorkspaceRoot, value)
	}
	path, err = canonicalDirectory(value)
	if err != nil {
		return "", "", codedError{code: "INVALID_SOURCE_PATH", message: "source must identify an existing directory inside an authorized source root"}
	}
	for _, candidate := range r.config.AllowedSourceRoots {
		if withinDirectory(candidate, path) && len(candidate) > len(root) {
			root = candidate
		}
	}
	if root == "" {
		return "", "", codedError{code: "SOURCE_ROOT_REQUIRED", message: "source path is outside the startup --source-root directories"}
	}
	return path, root, nil
}
