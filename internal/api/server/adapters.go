package server

import (
	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/model"
)

func trafficSummary(event model.TrafficEvent) model.TrafficEvent {
	event.RequestHeaders = nil
	event.ResponseHeaders = nil
	event.RequestBody = ""
	event.ResponseBody = ""
	event.RequestBodyTruncated = false
	event.ResponseBodyTruncated = false
	return event
}

func environmentSubject(project, environment string) map[string]any {
	return map[string]any{"project": project, "environment": environment}
}

func applicationSourceInputs(inputs []contract.SourceInput) []application.SourceInput {
	result := make([]application.SourceInput, 0, len(inputs))
	for _, input := range inputs {
		result = append(result, application.SourceInput{Name: input.Name, Path: input.Path})
	}
	return result
}

func runtimeStatusContract(status application.RuntimeStatus) contract.RuntimeStatus {
	candidates := make([]contract.RuntimeProbe, 0, len(status.Candidates))
	for _, candidate := range status.Candidates {
		candidates = append(candidates, contract.RuntimeProbe{
			Name: contract.RuntimeName(candidate.Name), State: candidate.State,
			Version: candidate.Version, Reason: candidate.Reason,
		})
	}
	return contract.RuntimeStatus{
		Preference: contract.RuntimeName(status.Preference), Selected: contract.RuntimeName(status.Selected),
		State: status.State, Version: status.Version, Reason: status.Reason, Candidates: candidates,
	}
}

func resetPlanContract(plan application.ResetPlan) contract.ResetPlan {
	return contract.ResetPlan{
		Projects: plan.Projects, Environments: plan.Environments,
		ManagedVolumeEnvironments: plan.ManagedVolumeEnvironments,
		ActiveEnvironments:        append([]string(nil), plan.ActiveEnvironments...),
		TopologyIncompatible:      plan.TopologyIncompatible,
	}
}

func prepareResetContract(result application.ResetRuntimeResult) contract.PrepareResetResponse {
	runtimes := make([]contract.RuntimeResetResult, 0, len(result.Runtimes))
	for _, item := range result.Runtimes {
		runtimes = append(runtimes, contract.RuntimeResetResult{
			Runtime: contract.RuntimeName(item.Runtime), Containers: item.Containers,
			Volumes: item.Volumes, Networks: item.Networks,
		})
	}
	return contract.PrepareResetResponse{Processes: result.Processes, Runtimes: runtimes}
}
