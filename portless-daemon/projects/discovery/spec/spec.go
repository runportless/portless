// Package spec defines the contract between the discovery engine and discovery
// plugins. Plugins only receive a bounded, read-only view of a workspace and
// return candidates; the engine remains responsible for conflict resolution,
// normalization, and validation.
package spec

import (
	"context"
	"path"
	"strings"

	"github.com/portless-run/portless/portless-daemon/model"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
)

type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Plugin   string   `json:"plugin"`
	File     string   `json:"file,omitempty"`
	Message  string   `json:"message"`
}

type Descriptor struct {
	ID           string
	RootMarkers  []string
	Supersedes   []string
	PrimaryOrder int
}

// Workspace deliberately exposes only source-relative paths. Implementations
// must confine reads to Root and reject non-regular files.
type Workspace interface {
	Root() string
	Files() []string
	Exists(relativePath string) bool
	IsDir(relativePath string) bool
	ReadFile(ctx context.Context, relativePath string) ([]byte, error)
}

type Candidate struct {
	// Key identifies the physical service unit claimed by a detector. Framework
	// detectors which inspect the same package/module must return the same key.
	Key               string
	Directory         string
	RunDirectory      string
	Definition        model.ServiceDefinition
	PrimaryPreference int
}

type Findings struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
}

type ServiceDetector interface {
	Descriptor() Descriptor
	Detect(ctx context.Context, workspace Workspace) (Findings, error)
}

type ResolvedService struct {
	Key          string
	Directory    string
	Plugin       string
	Definition   model.ServiceDefinition
	PrimaryOrder int
}

type TopologyFindings struct {
	Connections []model.Connection
	References  []model.ConnectionReference
	Diagnostics []Diagnostic
}

type TopologyAnalyzer interface {
	Descriptor() Descriptor
	Analyze(ctx context.Context, workspace Workspace, services []ResolvedService) (TopologyFindings, error)
}

// CleanRelative validates and canonicalizes a slash-separated workspace path.
func CleanRelative(value string) (string, bool) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" {
		value = "."
	}
	cleaned := path.Clean(value)
	if cleaned == "/" || strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func ServiceName(value string) string {
	value = strings.TrimSuffix(value, "-service")
	value = strings.TrimSuffix(value, "-api")
	return model.NormalizeDNSName(value)
}
