package portlessmcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scopedResult[T any] struct {
	Project       string `json:"project"`
	Environment   string `json:"environment,omitempty"`
	UntrustedData bool   `json:"untrustedData"`
	Result        T      `json:"result"`
}

func environmentCall[T any](ctx context.Context, r *runtime, selector string, mutation bool, call func(context.Context, selectedEnvironment) (T, error)) (*mcp.CallToolResult, scopedResult[T], error) {
	var output scopedResult[T]
	release, err := r.enter(ctx, mutation)
	if err != nil {
		return nil, output, r.toolError(err)
	}
	defer release()
	duration := readTimeout
	if mutation {
		duration = 130 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	run := func() error {
		selected, err := r.selectEnvironment(callCtx, selector)
		if err != nil {
			return err
		}
		value, err := call(callCtx, selected)
		if err != nil {
			return err
		}
		output = scopedResult[T]{Project: selected.project, Environment: selected.environment, UntrustedData: true, Result: value}
		return nil
	}
	if mutation {
		err = run()
	} else {
		err = r.retryRead(run)
	}
	if err != nil {
		return nil, output, r.toolError(err)
	}
	if err := r.checkOutput(output); err != nil {
		return nil, scopedResult[T]{}, r.toolError(err)
	}
	return nil, output, nil
}

func requireSensitive(r *runtime, requested bool) error {
	if requested && !r.config.AllowSensitiveTraffic {
		return codedError{code: "SENSITIVE_TRAFFIC_REQUIRED", message: "this result requires startup --allow-sensitive-traffic permission"}
	}
	return nil
}
