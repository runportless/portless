package controlplane

import (
	"errors"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/projects/compiler"
	"github.com/portless-run/portless/portless-daemon/runtime/container"
)

// ErrorKind is the application-level classification exposed to transport
// adapters. It keeps HTTP handlers independent of storage, compiler, and
// runtime implementation errors.
type ErrorKind string

// ErrNotFound indicates that a requested control-plane resource does not exist.
var ErrNotFound = errors.New("resource not found")

const (
	// ErrorRequestFailed is the fallback classification for an unsuccessful request.
	ErrorRequestFailed ErrorKind = "request-failed"
	// ErrorNotFound identifies a missing resource.
	ErrorNotFound ErrorKind = "not-found"
	// ErrorRevisionConflict identifies an optimistic-concurrency conflict.
	ErrorRevisionConflict ErrorKind = "revision-conflict"
	// ErrorNameTaken identifies a project or resource name collision.
	ErrorNameTaken ErrorKind = "name-taken"
	// ErrorAlreadyExists identifies an attempt to recreate an existing resource.
	ErrorAlreadyExists ErrorKind = "already-exists"
	// ErrorActiveEnvironments indicates that active environments block an operation.
	ErrorActiveEnvironments ErrorKind = "active-environments"
	// ErrorConfiguration identifies an invalid project or environment model.
	ErrorConfiguration ErrorKind = "configuration"
	// ErrorIncompatibleState identifies persisted state from an unsupported schema.
	ErrorIncompatibleState ErrorKind = "incompatible-state"
	// ErrorRuntimeInUse indicates that active resources prevent a runtime change.
	ErrorRuntimeInUse ErrorKind = "runtime-in-use"
	// ErrorRuntimeUnavailable indicates that no usable container runtime is available.
	ErrorRuntimeUnavailable ErrorKind = "runtime-unavailable"
)

// ErrorClassification translates implementation errors into transport-safe details.
type ErrorClassification struct {
	Kind               ErrorKind
	Suggestions        []string
	ActiveEnvironments []string
	Issues             []model.ConfigurationIssue
}

// ClassifyError maps a control-plane error to its public classification.
func ClassifyError(err error) ErrorClassification {
	classification := ErrorClassification{Kind: ErrorRequestFailed}
	var conflict NameConflictError
	var active database.ActiveProjectEnvironmentsError
	var configuration compiler.ConfigurationError
	switch {
	case errors.As(err, &conflict):
		classification.Kind = ErrorNameTaken
		classification.Suggestions = append([]string(nil), conflict.Suggestions...)
	case errors.Is(err, ErrNotFound), errors.Is(err, database.ErrNotFound):
		classification.Kind = ErrorNotFound
	case errors.Is(err, database.ErrConflict):
		classification.Kind = ErrorRevisionConflict
	case errors.Is(err, database.ErrNameTaken):
		classification.Kind = ErrorNameTaken
	case errors.Is(err, database.ErrAlreadyExists):
		classification.Kind = ErrorAlreadyExists
	case errors.As(err, &active):
		classification.Kind = ErrorActiveEnvironments
		classification.ActiveEnvironments = append([]string(nil), active.Environments...)
	case errors.As(err, &configuration):
		classification.Kind = ErrorConfiguration
		classification.Issues = append([]model.ConfigurationIssue(nil), configuration.Issues...)
	case errors.Is(err, database.ErrIncompatibleState):
		classification.Kind = ErrorIncompatibleState
	case errors.As(err, &RuntimeInUseError{}):
		classification.Kind = ErrorRuntimeInUse
	case container.IsUnavailable(err):
		classification.Kind = ErrorRuntimeUnavailable
	}
	return classification
}

// IsNotFound reports whether err represents a missing control-plane resource.
func IsNotFound(err error) bool {
	return ClassifyError(err).Kind == ErrorNotFound
}
