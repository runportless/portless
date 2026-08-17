package portlessmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func (r *runtime) registerLifecycleTools(server *mcp.Server) {
	mcp.AddTool(server, mutationTool(
		"portless_start_environment",
		"Start an existing scoped environment, optionally selecting explicit debug services. This never discovers or creates a project.",
		false,
	), r.startEnvironment)
	mcp.AddTool(server, mutationTool(
		"portless_stop_environment",
		"Stop exactly one scoped environment while always preserving managed volumes and retained data.",
		true,
	), r.stopEnvironment)
	mcp.AddTool(server, mutationTool(
		"portless_change_service_state",
		"Start, stop, restart, debug, or manage one explicitly named service. The daemon validates provider and debugger eligibility.",
		true,
	), r.changeServiceState)
}

func (r *runtime) startEnvironment(ctx context.Context, _ *mcp.CallToolRequest, input startEnvironmentInput) (*mcp.CallToolResult, lifecycleOutput, error) {
	var output lifecycleOutput
	for _, service := range input.DebugServices {
		if err := validateServiceName(service); err != nil {
			return nil, output, r.toolError(err)
		}
	}
	if input.Managed && len(input.DebugServices) > 0 {
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "managed startup cannot also select debug services"})
	}
	wait, err := waitDuration(input.WaitSeconds)
	if err != nil {
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
	callerKey, persistedKey, err := prepareIdempotency("start-environment", input.Environment, input.IdempotencyKey)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	operation, err := selected.client.UpEnvironment(ctx, selected.project, selected.environment, contract.UpRequest{
		DebugServices: nonNilStrings(input.DebugServices), Managed: input.Managed,
	}, persistedKey)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	operation, timedOut, err := waitForOperation(ctx, selected.client, operation, wait)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	output = lifecycleOutput{Project: selected.project, Environment: selected.environment, UntrustedData: true, Operation: operation, IdempotencyKey: callerKey, TimedOutWaiting: timedOut}
	if err := r.checkOutput(output); err != nil {
		return nil, lifecycleOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) stopEnvironment(ctx context.Context, _ *mcp.CallToolRequest, input stopEnvironmentInput) (*mcp.CallToolResult, lifecycleOutput, error) {
	var output lifecycleOutput
	wait, err := waitDuration(input.WaitSeconds)
	if err != nil {
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
	callerKey, persistedKey, err := prepareIdempotency("stop-environment", input.Environment, input.IdempotencyKey)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	operation, err := selected.client.DownEnvironment(ctx, selected.project, selected.environment, false, persistedKey)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	operation, timedOut, err := waitForOperation(ctx, selected.client, operation, wait)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	output = lifecycleOutput{Project: selected.project, Environment: selected.environment, UntrustedData: true, Operation: operation, IdempotencyKey: callerKey, TimedOutWaiting: timedOut}
	if err := r.checkOutput(output); err != nil {
		return nil, lifecycleOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func (r *runtime) changeServiceState(ctx context.Context, _ *mcp.CallToolRequest, input serviceStateInput) (*mcp.CallToolResult, lifecycleOutput, error) {
	var output lifecycleOutput
	if err := validateServiceName(input.Service); err != nil {
		return nil, output, r.toolError(err)
	}
	switch input.Action {
	case "start", "stop", "restart", "debug", "manage":
	default:
		return nil, output, r.toolError(codedError{code: "INVALID_ARGUMENT", message: "action must be start, stop, restart, debug, or manage"})
	}
	wait, err := waitDuration(input.WaitSeconds)
	if err != nil {
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
	target := input.Environment + "/" + input.Service
	callerKey, persistedKey, err := prepareIdempotency("service-"+input.Action, target, input.IdempotencyKey)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	operation, err := selected.client.ServiceAction(ctx, selected.project, selected.environment, input.Service, input.Action, persistedKey)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	operation, timedOut, err := waitForOperation(ctx, selected.client, operation, wait)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	output = lifecycleOutput{Project: selected.project, Environment: selected.environment, UntrustedData: true, Operation: operation, IdempotencyKey: callerKey, TimedOutWaiting: timedOut}
	if err := r.checkOutput(output); err != nil {
		return nil, lifecycleOutput{}, r.toolError(err)
	}
	return nil, output, nil
}

func waitDuration(value *int) (time.Duration, error) {
	seconds := 30
	if value != nil {
		seconds = *value
	}
	if seconds < 0 || seconds > 120 {
		return 0, codedError{code: "INVALID_ARGUMENT", message: "waitSeconds must be between 0 and 120"}
	}
	return time.Duration(seconds) * time.Second, nil
}

func waitForOperation(ctx context.Context, api *client.Client, operation contract.Operation, wait time.Duration) (contract.Operation, bool, error) {
	if wait == 0 || operation.State != "running" {
		return operation, false, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	current, err := api.WaitOperation(waitCtx, operation, 200*time.Millisecond, nil)
	if err == nil {
		return current, false, nil
	}
	if waitCtx.Err() == context.DeadlineExceeded {
		return operation, true, nil
	}
	return contract.Operation{}, false, err
}

func prepareIdempotency(tool, target, callerKey string) (string, string, error) {
	if callerKey == "" {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return "", "", fmt.Errorf("generate idempotency key: %w", err)
		}
		callerKey = hex.EncodeToString(value[:])
	}
	if len(callerKey) > 120 {
		return "", "", codedError{code: "INVALID_ARGUMENT", message: "idempotencyKey must be at most 120 characters"}
	}
	for _, character := range callerKey {
		if character < 0x21 || character > 0x7e || strings.ContainsRune("/\\", character) {
			return "", "", codedError{code: "INVALID_ARGUMENT", message: "idempotencyKey must contain visible ASCII without slashes"}
		}
	}
	digest := sha256.Sum256([]byte(tool + "\x00" + target + "\x00" + callerKey))
	return callerKey, "mcp-" + hex.EncodeToString(digest[:]), nil
}
