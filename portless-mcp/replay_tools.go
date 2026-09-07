package portlessmcp

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type replayKey struct {
	environment string
	number      int64
	identity    contract.TrafficReplayIdentity
}

type prepareReplayInput struct {
	Environment string    `json:"environment"`
	Sequence    int64     `json:"sequence"`
	StartedAt   time.Time `json:"startedAt"`
}

type replayInput struct {
	Environment     string    `json:"environment"`
	Number          int64     `json:"number"`
	CreatedAt       time.Time `json:"createdAt"`
	DaemonStartedAt time.Time `json:"daemonStartedAt"`
}

func (input replayInput) identity() contract.TrafficReplayIdentity {
	return contract.TrafficReplayIdentity{CreatedAt: input.CreatedAt.UTC(), DaemonStartedAt: input.DaemonStartedAt.UTC()}
}

func (input replayInput) validate() error {
	if input.Number <= 0 || input.CreatedAt.IsZero() || input.DaemonStartedAt.IsZero() {
		return codedError{code: "INVALID_REPLAY_IDENTITY", message: "provide the workspace number and both identity timestamps returned by prepare"}
	}
	return nil
}

type updateReplayInput struct {
	replayInput
	Revision uint64                      `json:"revision"`
	Draft    contract.TrafficReplayDraft `json:"draft"`
}

type runReplayInput struct {
	replayInput
	Revision           uint64 `json:"revision"`
	RunNumber          int64  `json:"runNumber"`
	ConfirmRemoteWrite bool   `json:"confirmRemoteWrite,omitempty"`
	WaitSeconds        *int   `json:"waitSeconds,omitempty"`
}

type getReplayInput struct {
	replayInput
	IncludeResult    *bool `json:"includeResult,omitempty"`
	DifferenceOffset int   `json:"differenceOffset,omitempty"`
}

type replayView struct {
	Workspace            contract.TrafficReplayWorkspace `json:"workspace"`
	DisplayTruncated     bool                            `json:"displayTruncated"`
	NextDifferenceOffset int                             `json:"nextDifferenceOffset,omitempty"`
	TimedOutWaiting      bool                            `json:"timedOutWaiting"`
	AdmissionUnknown     bool                            `json:"admissionUnknown"`
}

func (r *runtime) registerReplayTools(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_prepare_replay", "Freeze one live HTTP exchange as a replay workspace without sending application traffic.", false), r.prepareReplay)
	registerTool(r, server, mutationTool("portless_update_replay", "Prepare a complete edited request against an authorized same-project environment without dispatch. Runtime credentials are never echoed.", false), r.updateReplay)
	registerTool(r, server, mutationTool("portless_run_replay", "Send exactly one reviewed HTTP request using its explicit revision and run number. Configured remote writes require review; an uncertain response is recovered by reading receipts, never automatic resend.", true), r.runReplay)
	registerTool(r, server, readTool("portless_get_replay", "Read replay admission receipts and bounded original/latest response comparison. Partial capture or display must not be treated as equality."), r.getReplay)
	registerTool(r, server, mutationTool("portless_close_replay", "Release replay payloads and unused credentials. Already admitted requests continue and retain duplicate-suppression receipts.", false), r.closeReplay)
}

func (r *runtime) prepareReplay(ctx context.Context, _ *mcp.CallToolRequest, input prepareReplayInput) (*mcp.CallToolResult, scopedResult[replayView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (replayView, error) {
		value, err := selected.client.PrepareTrafficReplay(ctx, selected.project, selected.environment, contract.PrepareTrafficReplayRequest{Sequence: input.Sequence, StartedAt: input.StartedAt})
		if err != nil {
			return replayView{}, err
		}
		r.replayMu.Lock()
		r.replays[replayKey{input.Environment, value.Number, value.TrafficReplayIdentity}] = struct{}{}
		r.replayMu.Unlock()
		return r.replayResult(value, 0), nil
	})
}

func (r *runtime) replayStatus(ctx context.Context, selected selectedEnvironment, input replayInput) (contract.TrafficReplayStatus, error) {
	if err := input.validate(); err != nil {
		return contract.TrafficReplayStatus{}, err
	}
	status, err := selected.client.TrafficReplayStatus(ctx, selected.project, selected.environment, input.Number, input.identity())
	if err != nil {
		return status, err
	}
	for _, destination := range status.Destinations {
		if _, err := r.selectEnvironment(ctx, selected.project+"/"+destination); err != nil {
			return contract.TrafficReplayStatus{}, err
		}
	}
	return status, nil
}

func (r *runtime) checkReplayPayloadScope(ctx context.Context, selected selectedEnvironment, value contract.TrafficReplayWorkspace) error {
	destinations := []string{}
	if value.Draft != nil {
		destinations = append(destinations, value.Draft.Environment)
	}
	if value.Destination != nil {
		destinations = append(destinations, value.Destination.Environment)
	}
	if value.Result != nil {
		destinations = append(destinations, value.Result.Destination.Environment)
	}
	for _, destination := range destinations {
		if _, err := r.selectEnvironment(ctx, selected.project+"/"+destination); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) updateReplay(ctx context.Context, _ *mcp.CallToolRequest, input updateReplayInput) (*mcp.CallToolResult, scopedResult[replayView], error) {
	if len(input.Draft.Body) > contract.TrafficReplayMaxBodyBytes {
		return nil, scopedResult[replayView]{}, r.toolError(codedError{code: "REPLAY_BODY_TOO_LARGE", message: "replay request body exceeds its execution limit", status: 413})
	}
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (replayView, error) {
		if _, err := r.replayStatus(ctx, selected, input.replayInput); err != nil {
			return replayView{}, err
		}
		if _, err := r.selectEnvironment(ctx, selected.project+"/"+input.Draft.Environment); err != nil {
			return replayView{}, err
		}
		value, err := selected.client.UpdateTrafficReplayDraft(ctx, selected.project, selected.environment, input.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: input.identity(), Revision: input.Revision, Draft: input.Draft})
		if err != nil {
			return replayView{}, err
		}
		if err := r.checkReplayPayloadScope(ctx, selected, value); err != nil {
			return replayView{}, err
		}
		return r.replayResult(value, 0), nil
	})
}

func (r *runtime) getReplay(ctx context.Context, _ *mcp.CallToolRequest, input getReplayInput) (*mcp.CallToolResult, scopedResult[replayView], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (replayView, error) {
		if input.DifferenceOffset < 0 {
			return replayView{}, codedError{code: "INVALID_ARGUMENT", message: "differenceOffset must be non-negative"}
		}
		status, err := r.replayStatus(ctx, selected, input.replayInput)
		if err != nil {
			return replayView{}, err
		}
		include := input.IncludeResult == nil || *input.IncludeResult
		if !include {
			return r.replayResult(replayWorkspaceFromStatus(status), input.DifferenceOffset), nil
		}
		value, err := selected.client.TrafficReplay(ctx, selected.project, selected.environment, input.Number, input.identity(), include)
		if err != nil {
			return replayView{}, err
		}
		if value.Revision != status.Revision {
			return replayView{}, codedError{code: "REPLAY_REVISION_CHANGED", message: "replay changed while reading; inspect it again"}
		}
		if err := r.checkReplayPayloadScope(ctx, selected, value); err != nil {
			return replayView{}, err
		}
		if !status.Closed && include {
			_ = selected.client.TouchTrafficReplay(ctx, selected.project, selected.environment, input.Number, input.identity())
		}
		return r.replayResult(value, input.DifferenceOffset), nil
	})
}

func (r *runtime) runReplay(ctx context.Context, _ *mcp.CallToolRequest, input runReplayInput) (*mcp.CallToolResult, scopedResult[replayView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (replayView, error) {
		wait, err := waitDuration(input.WaitSeconds)
		if err != nil {
			return replayView{}, err
		}
		if input.RunNumber <= 0 {
			return replayView{}, codedError{code: "INVALID_REPLAY_RUN", message: "provide the explicit next run number returned by preparation"}
		}
		if _, err := r.replayStatus(ctx, selected, input.replayInput); err != nil {
			return replayView{}, err
		}
		value, runErr := selected.client.RunTrafficReplay(ctx, selected.project, selected.environment, input.Number, contract.RunTrafficReplayRequest{TrafficReplayIdentity: input.identity(), Revision: input.Revision, RunNumber: input.RunNumber, ConfirmRemoteWrite: input.ConfirmRemoteWrite})
		if runErr != nil {
			var apiError *apiclient.ClientError
			if errors.As(runErr, &apiError) && apiError.Status < 500 {
				return replayView{}, runErr
			}
			status, err := r.replayStatus(ctx, selected, input.replayInput)
			if err != nil {
				return replayView{Workspace: contract.TrafficReplayWorkspace{TrafficReplayIdentity: input.identity(), Project: selected.project, Environment: selected.environment, Number: input.Number}, AdmissionUnknown: true}, nil
			}
			value = replayWorkspaceForRun(replayWorkspaceFromStatus(status), input.RunNumber)
			found := false
			for _, receipt := range status.Receipts {
				if receipt.Number == input.RunNumber {
					found = true
					break
				}
			}
			if !found {
				result := r.replayResult(value, 0)
				result.AdmissionUnknown = true
				return result, nil
			}
		}
		value = replayWorkspaceForRun(value, input.RunNumber)
		if err := r.checkReplayPayloadScope(ctx, selected, value); err != nil {
			return replayView{}, err
		}
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for value.Run != nil && value.Run.State == "running" && wait > 0 {
			select {
			case <-waitCtx.Done():
				result := r.replayResult(value, 0)
				result.TimedOutWaiting = true
				return result, nil
			case <-time.After(200 * time.Millisecond):
			}
			status, err := r.replayStatus(waitCtx, selected, input.replayInput)
			if err != nil {
				result := r.replayResult(value, 0)
				result.TimedOutWaiting = true
				return result, nil
			}
			value = replayWorkspaceForRun(replayWorkspaceFromStatus(status), input.RunNumber)
		}
		if value.Run != nil && value.Run.State != "running" {
			full, err := selected.client.TrafficReplay(ctx, selected.project, selected.environment, input.Number, input.identity(), true)
			if err == nil {
				if err := r.checkReplayPayloadScope(ctx, selected, full); err != nil {
					return replayView{}, err
				}
				value = replayWorkspaceForRun(full, input.RunNumber)
			}
		}
		return r.replayResult(value, 0), nil
	})
}

func replayWorkspaceFromStatus(status contract.TrafficReplayStatus) contract.TrafficReplayWorkspace {
	return contract.TrafficReplayWorkspace{TrafficReplayIdentity: status.TrafficReplayIdentity, Project: status.Project, Environment: status.Environment, Number: status.Number, Revision: status.Revision, NextRunNumber: status.NextRunNumber, Run: status.Run, Receipts: status.Receipts}
}

type replayClosed struct {
	Closed               bool `json:"closed"`
	AdmittedRunsContinue bool `json:"admittedRunsContinue"`
}

func (r *runtime) closeReplay(ctx context.Context, _ *mcp.CallToolRequest, input replayInput) (*mcp.CallToolResult, scopedResult[replayClosed], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (replayClosed, error) {
		if _, err := r.replayStatus(ctx, selected, input); err != nil {
			return replayClosed{}, err
		}
		if err := selected.client.DeleteTrafficReplay(ctx, selected.project, selected.environment, input.Number, input.identity()); err != nil {
			return replayClosed{}, err
		}
		r.replayMu.Lock()
		delete(r.replays, replayKey{input.Environment, input.Number, input.identity()})
		r.replayMu.Unlock()
		return replayClosed{Closed: true, AdmittedRunsContinue: true}, nil
	})
}

func (r *runtime) closeReplays() {
	r.replayMu.Lock()
	keys := make([]replayKey, 0, len(r.replays))
	for key := range r.replays {
		keys = append(keys, key)
	}
	r.replays = make(map[replayKey]struct{})
	r.replayMu.Unlock()
	if len(keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		selected, err := r.selectEnvironment(ctx, key.environment)
		if err == nil {
			_ = selected.client.DeleteTrafficReplay(ctx, selected.project, selected.environment, key.number, key.identity)
		}
	}
}

func replayWorkspaceForRun(value contract.TrafficReplayWorkspace, number int64) contract.TrafficReplayWorkspace {
	value.Run = nil
	for _, receipt := range value.Receipts {
		if receipt.Number == number {
			copy := receipt
			value.Run = &copy
			break
		}
	}
	if value.Result != nil && value.Result.RunNumber != number {
		value.Result = nil
	}
	return value
}
