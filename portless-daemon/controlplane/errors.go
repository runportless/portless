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

var ErrNotFound = errors.New("resource not found")

const (
	ErrorRequestFailed      ErrorKind = "request-failed"
	ErrorNotFound           ErrorKind = "not-found"
	ErrorRevisionConflict   ErrorKind = "revision-conflict"
	ErrorNameTaken          ErrorKind = "name-taken"
	ErrorAlreadyExists      ErrorKind = "already-exists"
	ErrorActiveEnvironments ErrorKind = "active-environments"
	ErrorConfiguration      ErrorKind = "configuration"
	ErrorIncompatibleState  ErrorKind = "incompatible-state"
	ErrorRuntimeInUse       ErrorKind = "runtime-in-use"
	ErrorRuntimeUnavailable ErrorKind = "runtime-unavailable"
)

type ErrorClassification struct {
	Kind               ErrorKind
	Suggestions        []string
	ActiveEnvironments []string
	Issues             []model.ConfigurationIssue
}

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

func IsNotFound(err error) bool {
	return ClassifyError(err).Kind == ErrorNotFound
}
