package server

import (
	"net/http"
	"strings"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/model"
)

func (s *Server) handleProjects(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodGet:
			if request.URL.Query().Get("view") == "metadata" {
				s.handleProjectMetadata(writer, request, "")
				return
			}
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			projects, err := s.app.Projects(ctx)
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, contract.ProjectList{Projects: limited(nonNil(projects), limit), Total: len(projects)})
		case http.MethodPost:
			var input contract.CreateProjectRequest
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			for _, source := range input.Sources {
				if !requireSourceRoot(writer, request, source.AllowedRoot) {
					return
				}
			}
			project, environment, warnings, err := s.app.CreateProject(ctx, input.Name, applicationSourceInputs(input.Sources), input.RequiredAssociationPath, principal.Actor)
			if err != nil {
				s.writeError(writer, err, map[string]any{"project": input.Name})
				return
			}
			writeJSON(writer, http.StatusCreated, contract.ProjectMutation{Project: project, Environment: environment, Warnings: nonNil(warnings)})
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) == 2 && segments[1] == "discover" {
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		if principal.Session {
			writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "CLI_AUTH_REQUIRED", Message: "source discovery may only be requested by the local CLI"})
			return
		}
		var input contract.DiscoverProjectRequest
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		if !requireSourceRoot(writer, request, input.AllowedRoot) {
			return
		}
		project, environment, warnings, err := s.app.Discover(ctx, input.Path, input.Name, controlplane.DiscoveryPolicy{AllowedRoot: input.AllowedRoot, RequiredAssociationPath: input.RequiredAssociationPath, RequiredProject: input.RequiredProject}, principal.Actor)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, contract.ProjectMutation{Project: project, Environment: environment, Warnings: nonNil(warnings)})
		return
	}
	project := segments[1]
	if err := model.ValidateProjectName(project); err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_PROJECT_NAME", Message: err.Error(), Subject: map[string]any{"project": project}})
		return
	}
	if len(segments) == 2 {
		s.handleProject(writer, request, project, principal)
		return
	}
	if len(segments) == 3 && segments[2] == "declaration" && request.Method == http.MethodGet {
		if request.URL.Query().Get("view") == "chunk" {
			s.handleProjectDeclarationChunk(writer, request, project)
			return
		}
		content, err := s.app.ExportProject(ctx, project)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Disposition", `attachment; filename="portless.project.json"`)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(content)
		return
	}
	if len(segments) == 3 && segments[2] == "sources" {
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		var input contract.AddProjectSourceRequest
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		if err := model.ValidateEnvironmentName(input.Environment); err != nil {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_ENVIRONMENT_NAME", Message: err.Error(), Subject: map[string]any{"project": project, "environment": input.Environment}})
			return
		}
		if !requireSourceRoot(writer, request, input.AllowedRoot) {
			return
		}
		updatedProject, environment, warnings, err := s.app.AddProjectSource(ctx, project, input.Environment, input.Name, input.Path, principal.Actor, input.AllowedRoot)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project, "environment": input.Environment, "source": input.Name})
			return
		}
		environments, err := s.app.Environments(ctx, project)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		var configurationRequired []string
		for _, candidate := range environments {
			if strings.EqualFold(candidate.Name, environment.Name) || len(candidate.Issues) == 0 {
				continue
			}
			configurationRequired = append(configurationRequired, model.EnvironmentSelector(project, candidate.Name))
		}
		writeJSON(writer, http.StatusCreated, contract.ProjectSourceMutation{
			ProjectMutation:       contract.ProjectMutation{Project: updatedProject, Environment: environment, Warnings: nonNil(warnings)},
			ConfigurationRequired: nonNil(configurationRequired),
		})
		return
	}
	if len(segments) == 4 && segments[2] == "sources" {
		if request.Method != http.MethodDelete {
			methodNotAllowed(writer, http.MethodDelete)
			return
		}
		if err := model.ValidateSourceName(segments[3]); err != nil {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_SOURCE_NAME", Message: err.Error(), Subject: map[string]any{"project": project, "source": segments[3]}})
			return
		}
		expected, ok := s.configurationCondition(writer, request, project, "", segments[3])
		if !ok {
			return
		}
		removed, err := s.app.RemoveProjectSource(ctx, project, segments[3], principal.Actor, expected)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project, "source": segments[3]})
			return
		}
		writeJSON(writer, http.StatusOK, contract.ProjectSourceDeletion{
			Project: removed.Project, Environments: nonNil(removed.Environments),
			RemovedServices: nonNil(removed.RemovedServices), RemovedConnections: nonNil(removed.RemovedConnections),
		})
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "project route not found"})
}

func (s *Server) handleProject(writer http.ResponseWriter, request *http.Request, project string, principal auth.Principal) {
	switch request.Method {
	case http.MethodGet:
		if request.URL.Query().Get("view") == "metadata" {
			s.handleProjectMetadata(writer, request, project)
			return
		}
		result, err := s.app.Project(request.Context(), project)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodPatch:
		var input contract.RenameProjectRequest
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		expected, ok := requestResourceVersion(writer, request, request.Header.Get(contract.ClientKindHeader) == string(contract.ClientKindMCP))
		if !ok {
			return
		}
		result, err := s.app.Rename(request.Context(), project, input.Name, input.Revision, principal.Actor, expected)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodDelete:
		expected, ok := s.configurationCondition(writer, request, project, "", "")
		if !ok {
			return
		}
		if err := s.app.Forget(request.Context(), project, expected); err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}
