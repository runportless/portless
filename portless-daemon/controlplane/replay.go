package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/replay"
)

// PrepareTrafficReplay freezes a specifically identified live exchange without application I/O.
func (s *Service) PrepareTrafficReplay(ctx context.Context, project, environment string, input contract.PrepareTrafficReplayRequest) (contract.TrafficReplayWorkspace, error) {
	if input.Sequence <= 0 || input.StartedAt.IsZero() {
		return contract.TrafficReplayWorkspace{}, &replay.Error{Status: http.StatusBadRequest, Code: "INVALID_REPLAY_BASELINE", Message: "a live exchange sequence and its start time are required"}
	}
	if _, err := s.Environment(ctx, project, environment); err != nil {
		return contract.TrafficReplayWorkspace{}, err
	}
	baseline, ok := s.traffic.Exchange(model.EnvironmentSelector(project, environment), input.Sequence)
	if !ok {
		return contract.TrafficReplayWorkspace{}, &replay.Error{Status: http.StatusNotFound, Code: "REPLAY_BASELINE_UNAVAILABLE", Message: "the original exchange is no longer in the live traffic window"}
	}
	if !baseline.StartedAt.Equal(input.StartedAt) {
		return contract.TrafficReplayWorkspace{}, &replay.Error{Status: http.StatusConflict, Code: "REPLAY_BASELINE_CHANGED", Message: "the captured exchange belongs to another traffic window; reopen its details"}
	}
	return s.replays.Create(ctx, baseline)
}

// UpdateTrafficReplay prepares a revised request and its current destination policy.
func (s *Service) UpdateTrafficReplay(ctx context.Context, project, environment string, number int64, input contract.UpdateTrafficReplayDraftRequest) (contract.TrafficReplayWorkspace, error) {
	return s.replays.Update(ctx, project, environment, number, input)
}

// RunTrafficReplay admits one reviewed request and returns its duplicate-safe receipt.
func (s *Service) RunTrafficReplay(ctx context.Context, project, environment string, number int64, input contract.RunTrafficReplayRequest) (contract.TrafficReplayWorkspace, error) {
	return s.replays.Run(ctx, project, environment, number, input)
}

// TrafficReplay inspects a bounded workspace without executing application requests.
func (s *Service) TrafficReplay(project, environment string, number int64, expected contract.TrafficReplayIdentity, result bool) (contract.TrafficReplayWorkspace, error) {
	return s.replays.Get(project, environment, number, expected, result)
}

// TrafficReplayStatus returns scoped replay admission metadata without payloads.
func (s *Service) TrafficReplayStatus(project, environment string, number int64, expected contract.TrafficReplayIdentity) (contract.TrafficReplayStatus, error) {
	return s.replays.Status(project, environment, number, expected)
}

// DeleteTrafficReplay releases workspace payloads while preserving duplicate-suppression receipts.
func (s *Service) DeleteTrafficReplay(project, environment string, number int64, expected contract.TrafficReplayIdentity) error {
	return s.replays.Delete(project, environment, number, expected)
}

// TouchTrafficReplay records user activity without changing the request or contacting its destination.
func (s *Service) TouchTrafficReplay(project, environment string, number int64, expected contract.TrafficReplayIdentity) error {
	return s.replays.Touch(project, environment, number, expected)
}

func (s *Service) resolveReplayTarget(ctx context.Context, project, environment, source, target string) (replay.Target, error) {
	if model.ValidateProjectName(project) != nil || model.ValidateEnvironmentName(environment) != nil {
		return replay.Target{}, &replay.Error{Status: http.StatusBadRequest, Code: "INVALID_REPLAY_DESTINATION", Message: "select an environment in the original project"}
	}
	current, err := s.Environment(ctx, project, environment)
	if err != nil {
		return replay.Target{}, &replay.Error{Status: http.StatusNotFound, Code: "REPLAY_DESTINATION_UNAVAILABLE", Message: "the selected environment is unavailable"}
	}
	service := runtimeFor(current, target)
	if service.Kind != model.ServiceProcess || service.Status != model.ServiceReady {
		return replay.Target{}, &replay.Error{Status: http.StatusConflict, Code: "REPLAY_DESTINATION_UNAVAILABLE", Message: "the target HTTP service must be ready before replay"}
	}
	if current.Status == model.EnvironmentStopping || current.Status == model.EnvironmentRecovering {
		return replay.Target{}, &replay.Error{Status: http.StatusConflict, Code: "REPLAY_DESTINATION_UNAVAILABLE", Message: "wait for the selected environment lifecycle operation to finish"}
	}
	if source != "external" {
		found := false
		for _, edge := range current.Connections {
			if edge.Source == source && edge.Target == target && edge.Protocol == model.ProtocolHTTP {
				found = true
				break
			}
		}
		if !found {
			return replay.Target{}, &replay.Error{Status: http.StatusConflict, Code: "REPLAY_EDGE_UNAVAILABLE", Message: "the selected environment does not contain the original HTTP source-to-target connection"}
		}
	}
	binding := bindingForEnvironment(current, target)
	destination := contract.TrafficReplayDestination{Environment: environment, Provider: string(binding.Provider), URL: "http://" + target + "." + environment + "." + project + ".localhost"}
	if binding.Remote != nil {
		destination.Classification = string(binding.Remote.Classification)
		destination.WritePolicy = string(binding.Remote.WritePolicy)
	}
	generation, err := s.proxy.ReplayTarget(model.EnvironmentSelector(project, environment), target, binding)
	if err != nil {
		return replay.Target{}, &replay.Error{Status: http.StatusConflict, Code: "REPLAY_DESTINATION_UNAVAILABLE", Message: "the selected service endpoint is not available"}
	}
	versionData, err := json.Marshal(struct {
		Revision    int64
		Binding     model.ComponentBinding
		Connections []model.Connection
	}{current.Revision, binding, current.Connections})
	if err != nil {
		return replay.Target{}, &replay.Error{Status: http.StatusServiceUnavailable, Code: "REPLAY_DESTINATION_UNAVAILABLE", Message: "the selected service configuration could not be verified"}
	}
	version := sha256.Sum256(versionData)
	return replay.Target{Destination: destination, Generation: generation, Version: hex.EncodeToString(version[:])}, nil
}

func (s *Service) executeReplay(ctx context.Context, scope, source, target string, draft contract.TrafficReplayDraft, generation uint64, provenance model.TrafficReplay) (model.TrafficExchange, string, error) {
	return s.proxy.ReplayHTTP(ctx, scope, source, target, draft.Method, draft.RequestTarget, draft.Headers, draft.Body, generation, provenance)
}
