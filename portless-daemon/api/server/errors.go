package server

import (
	"net/http"

	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/controlplane"
)

func (s *Server) writeError(writer http.ResponseWriter, err error, subject map[string]any) {
	status := http.StatusBadRequest
	apiError := contract.APIError{Code: "REQUEST_FAILED", Message: err.Error(), Subject: subject}
	classification := controlplane.ClassifyError(err)
	switch classification.Kind {
	case controlplane.ErrorNameTaken:
		status = http.StatusConflict
		apiError.Code = "PROJECT_NAME_TAKEN"
		if len(classification.Suggestions) > 0 {
			apiError.Details = map[string]any{"suggestions": classification.Suggestions}
		}
	case controlplane.ErrorNotFound:
		status = http.StatusNotFound
		apiError.Code = "RESOURCE_NOT_FOUND"
	case controlplane.ErrorRevisionConflict:
		status = http.StatusConflict
		apiError.Code = "REVISION_CONFLICT"
	case controlplane.ErrorAlreadyExists:
		status = http.StatusConflict
		apiError.Code = "RESOURCE_ALREADY_EXISTS"
	case controlplane.ErrorActiveEnvironments:
		status = http.StatusConflict
		apiError.Code = "ACTIVE_ENVIRONMENTS"
		apiError.Details = map[string]any{"activeEnvironments": nonNil(classification.ActiveEnvironments)}
	case controlplane.ErrorConfiguration:
		status = http.StatusConflict
		apiError.Code = "CONFIGURATION_INVALID"
		apiError.Details = map[string]any{"issues": nonNil(classification.Issues)}
	case controlplane.ErrorIncompatibleState:
		status = http.StatusConflict
		apiError.Code = "INCOMPATIBLE_STATE"
		apiError.Remediation = []contract.Remediation{{Label: "Reset incompatible application state", Command: "portless reset --force --yes"}}
	case controlplane.ErrorRuntimeInUse:
		status = http.StatusConflict
		apiError.Code = "CONTAINER_RUNTIME_IN_USE"
	case controlplane.ErrorRuntimeUnavailable:
		status = http.StatusServiceUnavailable
		apiError.Code = "CONTAINER_RUNTIME_UNAVAILABLE"
	}
	writeAPIError(writer, status, apiError)
}
