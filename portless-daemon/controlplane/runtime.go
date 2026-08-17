package controlplane

import "github.com/portless-run/portless/portless-daemon/runtime/container"

type RuntimeName string

type RuntimeProbe struct {
	Name    RuntimeName
	State   string
	Version string
	Reason  string
}

type RuntimeStatus struct {
	Preference RuntimeName
	Selected   RuntimeName
	State      string
	Version    string
	Reason     string
	Candidates []RuntimeProbe
}

type RuntimeResetResult struct {
	Runtime    RuntimeName
	Containers int
	Volumes    int
	Networks   int
}

func applicationRuntimeStatus(status container.Status) RuntimeStatus {
	candidates := make([]RuntimeProbe, 0, len(status.Candidates))
	for _, candidate := range status.Candidates {
		candidates = append(candidates, RuntimeProbe{
			Name: RuntimeName(candidate.Name), State: candidate.State,
			Version: candidate.Version, Reason: candidate.Reason,
		})
	}
	return RuntimeStatus{
		Preference: RuntimeName(status.Preference), Selected: RuntimeName(status.Selected),
		State: status.State, Version: status.Version, Reason: status.Reason, Candidates: candidates,
	}
}

func applicationRuntimeResetResults(results []container.ResetResult) []RuntimeResetResult {
	converted := make([]RuntimeResetResult, 0, len(results))
	for _, result := range results {
		converted = append(converted, RuntimeResetResult{
			Runtime: RuntimeName(result.Runtime), Containers: result.Containers,
			Volumes: result.Volumes, Networks: result.Networks,
		})
	}
	return converted
}
