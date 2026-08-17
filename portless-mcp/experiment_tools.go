package portlessmcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func (r *runtime) registerTrafficControlTools(server *mcp.Server) {
	mcp.AddTool(server, mutationTool(
		"portless_start_recording",
		"Start a named metadata-only traffic recording with a mandatory finite duration no longer than one hour.",
		false,
	), r.startRecording)
	mcp.AddTool(server, mutationTool(
		"portless_stop_recording",
		"Stop one named recording while retaining its captured events and metadata.",
		false,
	), r.stopRecording)
	mcp.AddTool(server, mutationTool(
		"portless_apply_fault",
		"Create a named scoped fault with a mandatory finite duration no longer than one hour and at least one explicit effect.",
		false,
	), r.applyFault)
	mcp.AddTool(server, mutationTool(
		"portless_disable_fault",
		"Disable one named fault without deleting its audit history.",
		false,
	), r.disableFault)
	mcp.AddTool(server, mutationTool(
		"portless_disable_all_faults",
		"Atomically disable all active fault rules in one scoped environment without deleting history.",
		false,
	), r.disableAllFaults)
}

func (r *runtime) startRecording(ctx context.Context, _ *mcp.CallToolRequest, input startRecordingInput) (*mcp.CallToolResult, recordingOutput, error) {
	var output recordingOutput
	if err := validateArtifactName(input.Recording, "recording"); err != nil {
		return nil, output, r.toolError(err)
	}
	if input.DurationSeconds < 1 || input.DurationSeconds > 3600 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "durationSeconds must be between 1 and 3600"})
	}
	if input.MaxEvents == 0 {
		input.MaxEvents = 10_000
	}
	if input.MaxEvents < 1 || input.MaxEvents > 100_000 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "maxEvents must be between 1 and 100000"})
	}
	if input.Source == "" && input.Target != "" {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "source is required when target is provided"})
	}
	if input.Source != "" {
		if err := validateConnectionSource(input.Source); err != nil {
			return nil, output, r.toolError(err)
		}
	}
	if input.Target != "" {
		if err := validateServiceName(input.Target); err != nil {
			return nil, output, r.toolError(err)
		}
	}
	release, err := r.enter(ctx, true)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	selected, err := r.selectEnvironment(ctx, input.Environment)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	expires := time.Now().UTC().Add(time.Duration(input.DurationSeconds) * time.Second)
	requested := contract.Recording{
		Name: input.Recording, Source: input.Source, Target: input.Target,
		CaptureBodies: false, MaxEvents: input.MaxEvents, ExpiresAt: &expires,
	}
	recording, err := selected.client.StartRecording(ctx, selected.project, selected.environment, requested)
	if err != nil {
		var clientError *apiclient.ClientError
		if !errors.As(err, &clientError) || clientError.Code != "RESOURCE_ALREADY_EXISTS" {
			return nil, output, r.toolError(err)
		}
		recording, err = selected.client.Recording(ctx, selected.project, selected.environment, input.Recording)
		if err != nil || recording.Source != input.Source || recording.Target != input.Target || recording.MaxEvents != input.MaxEvents || recording.Status != "active" {
			if err == nil {
				err = codedError{code: "IDEMPOTENCY_CONFLICT", message: "recording name already exists with different bounds or scope"}
			}
			return nil, output, r.toolError(err)
		}
	}
	output = recordingOutput{Project: selected.project, Environment: selected.environment, Recording: recording}
	if err := r.checkOutput(output); err != nil {
		return nil, recordingOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) stopRecording(ctx context.Context, _ *mcp.CallToolRequest, input recordingInput) (*mcp.CallToolResult, recordingOutput, error) {
	var output recordingOutput
	if err := validateArtifactName(input.Recording, "recording"); err != nil {
		return nil, output, r.toolError(err)
	}
	release, err := r.enter(ctx, true)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	selected, err := r.selectEnvironment(ctx, input.Environment)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	recording, err := selected.client.Recording(ctx, selected.project, selected.environment, input.Recording)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	if recording.Status == "active" {
		recording, err = selected.client.StopRecording(ctx, selected.project, selected.environment, input.Recording)
		if err != nil {
			return nil, output, r.toolError(err)
		}
	}
	output = recordingOutput{Project: selected.project, Environment: selected.environment, Recording: recording}
	if err := r.checkOutput(output); err != nil {
		return nil, recordingOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) applyFault(ctx context.Context, _ *mcp.CallToolRequest, input applyFaultInput) (*mcp.CallToolResult, faultOutput, error) {
	var output faultOutput
	if err := validateArtifactName(input.Fault, "fault"); err != nil {
		return nil, output, r.toolError(err)
	}
	if err := validateConnectionSource(input.Source); err != nil {
		return nil, output, r.toolError(err)
	}
	if err := validateServiceName(input.Target); err != nil {
		return nil, output, r.toolError(err)
	}
	if input.DurationSeconds < 1 || input.DurationSeconds > 3600 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "durationSeconds must be between 1 and 3600"})
	}
	if input.LatencyMS < 0 || input.JitterMS < 0 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "latencyMs and jitterMs cannot be negative"})
	}
	if input.StatusCode != 0 && (input.StatusCode < 400 || input.StatusCode > 599) {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "statusCode must be between 400 and 599"})
	}
	if input.LatencyMS == 0 && input.JitterMS == 0 && input.StatusCode == 0 && !input.Abort {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "at least one latency, jitter, status, or abort effect is required"})
	}
	probability := 1.0
	if input.Probability != nil {
		probability = *input.Probability
	}
	if probability <= 0 || probability > 1 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "probability must be greater than zero and no more than one"})
	}
	release, err := r.enter(ctx, true)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	selected, err := r.selectEnvironment(ctx, input.Environment)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	expires := time.Now().UTC().Add(time.Duration(input.DurationSeconds) * time.Second)
	requested := contract.FaultRule{
		Name: input.Fault, Source: input.Source, Target: input.Target,
		Method: strings.ToUpper(input.Method), Path: input.Path, Probability: probability,
		LatencyMS: input.LatencyMS, JitterMS: input.JitterMS, StatusCode: input.StatusCode,
		Abort: input.Abort, ExpiresAt: &expires,
	}
	fault, err := selected.client.CreateFault(ctx, selected.project, selected.environment, requested)
	if err != nil {
		var clientError *apiclient.ClientError
		if !errors.As(err, &clientError) || clientError.Code != "RESOURCE_ALREADY_EXISTS" {
			return nil, output, r.toolError(err)
		}
		fault, err = selected.client.Fault(ctx, selected.project, selected.environment, input.Fault)
		if err != nil || !sameFault(fault, requested) || !fault.Enabled {
			if err == nil {
				err = codedError{code: "IDEMPOTENCY_CONFLICT", message: "fault name already exists with a different scope or effect"}
			}
			return nil, output, r.toolError(err)
		}
	}
	output = faultOutput{Project: selected.project, Environment: selected.environment, Fault: fault}
	if err := r.checkOutput(output); err != nil {
		return nil, faultOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) disableFault(ctx context.Context, _ *mcp.CallToolRequest, input faultInput) (*mcp.CallToolResult, faultOutput, error) {
	var output faultOutput
	if err := validateArtifactName(input.Fault, "fault"); err != nil {
		return nil, output, r.toolError(err)
	}
	release, err := r.enter(ctx, true)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	selected, err := r.selectEnvironment(ctx, input.Environment)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	fault, err := selected.client.Fault(ctx, selected.project, selected.environment, input.Fault)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	if fault.Enabled {
		if _, err := selected.client.SetFaultEnabled(ctx, selected.project, selected.environment, input.Fault, false); err != nil {
			return nil, output, r.toolError(err)
		}
		fault, err = selected.client.Fault(ctx, selected.project, selected.environment, input.Fault)
		if err != nil {
			return nil, output, r.toolError(err)
		}
	}
	output = faultOutput{Project: selected.project, Environment: selected.environment, Fault: fault}
	if err := r.checkOutput(output); err != nil {
		return nil, faultOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) disableAllFaults(ctx context.Context, _ *mcp.CallToolRequest, input environmentInput) (*mcp.CallToolResult, disabledFaultsOutput, error) {
	var output disabledFaultsOutput
	release, err := r.enter(ctx, true)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	selected, err := r.selectEnvironment(ctx, input.Environment)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	response, err := selected.client.DisableAllFaults(ctx, selected.project, selected.environment)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	output = disabledFaultsOutput{Project: selected.project, Environment: selected.environment, Disabled: response.Disabled}
	if err := r.checkOutput(output); err != nil {
		return nil, disabledFaultsOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func sameFault(left, right contract.FaultRule) bool {
	return left.Source == right.Source && left.Target == right.Target &&
		left.Method == right.Method && left.Path == right.Path && left.Probability == right.Probability &&
		left.LatencyMS == right.LatencyMS && left.JitterMS == right.JitterMS &&
		left.StatusCode == right.StatusCode && left.Abort == right.Abort
}
