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

// Severity classifies a discovery diagnostic.
type Severity string

const (
	// SeverityInfo is a non-actionable discovery detail.
	SeverityInfo Severity = "info"
	// SeverityWarning identifies a recoverable discovery concern.
	SeverityWarning Severity = "warning"
)

// Diagnostic is a structured message attributed to a discovery plugin and file.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Plugin   string   `json:"plugin"`
	File     string   `json:"file,omitempty"`
	Message  string   `json:"message"`
}

// Descriptor declares a plugin identity, root markers, precedence, and primary-service rank.
type Descriptor struct {
	ID           string
	RootMarkers  []string
	Supersedes   []string
	PrimaryOrder int
}

// Workspace deliberately exposes only source-relative paths. Implementations
// must confine reads to Root and reject non-regular files.
type Workspace interface {
	// Root returns the canonical absolute workspace root.
	Root() string
	// Files returns sorted source-relative regular-file paths.
	Files() []string
	// Exists reports whether a regular file was present in the indexed snapshot.
	Exists(relativePath string) bool
	// IsDir reports whether a directory was present in the indexed snapshot.
	IsDir(relativePath string) bool
	// ReadFile returns a bounded read of an indexed regular file.
	ReadFile(ctx context.Context, relativePath string) ([]byte, error)
}

// Candidate is a service implementation proposed by a detector.
type Candidate struct {
	// Key identifies the physical service unit claimed by a detector. Framework
	// detectors which inspect the same package/module must return the same key.
	Key               string
	Directory         string
	RunDirectory      string
	Definition        model.ServiceDefinition
	PrimaryPreference int
}

// Findings contains service candidates and diagnostics returned by a detector.
type Findings struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
}

// ServiceDetector discovers application services from a bounded workspace.
type ServiceDetector interface {
	// Descriptor returns the detector's immutable registration metadata.
	Descriptor() Descriptor
	// Detect examines a workspace and returns proposed service candidates.
	Detect(ctx context.Context, workspace Workspace) (Findings, error)
}

// ResolvedService is the winning normalized candidate passed to topology analyzers.
type ResolvedService struct {
	Key          string
	Directory    string
	Plugin       string
	Definition   model.ServiceDefinition
	PrimaryOrder int
}

// TopologyFindings contains dependency edges, unresolved references, and diagnostics.
type TopologyFindings struct {
	Connections []model.Connection
	References  []model.ConnectionReference
	Diagnostics []Diagnostic
}

// TopologyAnalyzer discovers connections among resolved services.
type TopologyAnalyzer interface {
	// Descriptor returns the analyzer's immutable registration metadata.
	Descriptor() Descriptor
	// Analyze examines source configuration for service dependency edges.
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

// ServiceName normalizes a package or module name into a service DNS label.
func ServiceName(value string) string {
	value = strings.TrimSuffix(value, "-service")
	value = strings.TrimSuffix(value, "-api")
	return model.NormalizeDNSName(value)
}
