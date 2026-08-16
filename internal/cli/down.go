package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/model"
)

const downAllInventoryLimit = 1000

type downAllFailure struct {
	Environment string `json:"environment"`
	Stage       string `json:"stage"`
	Error       string `json:"error"`
}

type downAllOutput struct {
	Action        string            `json:"action"`
	All           bool              `json:"all"`
	Wait          bool              `json:"wait"`
	RemoveVolumes bool              `json:"removeVolumes"`
	Targets       int               `json:"targets"`
	Operations    []model.Operation `json:"operations"`
	Failures      []downAllFailure  `json:"failures"`
}

type pendingDown struct {
	environment model.Environment
	operation   model.Operation
}

func (c *CLI) startDown(ctx context.Context, client *apiclient.Client, environment model.Environment, removeVolumes bool) (model.Operation, error) {
	operation, err := client.DownEnvironment(ctx, environment.Project, environment.Name, removeVolumes)
	if err != nil {
		return model.Operation{}, err
	}
	if operation.Project == "" {
		operation.Project = environment.Project
	}
	if operation.Environment == "" {
		operation.Environment = environment.Name
	}
	return operation, nil
}

func (c *CLI) waitDown(ctx context.Context, client *apiclient.Client, operation model.Operation, timeout time.Duration, suppressEvents bool) (model.Operation, error) {
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	completed, err := c.waitOperation(waitContext, client, operation, suppressEvents)
	if err != nil {
		return operation, err
	}
	if completed.State != "succeeded" {
		message := completed.Error
		if message == "" {
			message = fmt.Sprintf("down operation %d finished in state %q", completed.Number, completed.State)
		}
		return completed, errors.New(message)
	}
	return completed, nil
}

func (c *CLI) downAll(ctx context.Context, client *apiclient.Client, options downOptions) error {
	targets, err := loadDownAllTargets(ctx, client, options.volumes)
	if err != nil {
		return err
	}
	result := downAllOutput{
		Action: "down", All: true, Wait: options.wait, RemoveVolumes: options.volumes, Targets: len(targets),
		Operations: []model.Operation{}, Failures: []downAllFailure{},
	}

	pending := make([]pendingDown, 0, len(targets))
	for _, environment := range targets {
		operation, startErr := c.startDown(ctx, client, environment, options.volumes)
		if startErr != nil {
			result.Failures = append(result.Failures, newDownAllFailure(environment, "request", startErr))
			continue
		}
		pending = append(pending, pendingDown{environment: environment, operation: operation})
	}

	for _, attempt := range pending {
		operation := attempt.operation
		if options.wait {
			var waitErr error
			operation, waitErr = c.waitDown(ctx, client, operation, options.timeout, true)
			if waitErr != nil {
				stage := "wait"
				if operation.State != "" && operation.State != "running" {
					stage = "operation"
				}
				result.Failures = append(result.Failures, newDownAllFailure(attempt.environment, stage, waitErr))
			}
		}
		result.Operations = append(result.Operations, operation)
	}

	if err := c.printDownAll(result); err != nil {
		return err
	}
	if len(result.Failures) > 0 {
		return &reportedCommandError{}
	}
	return nil
}

func loadDownAllTargets(ctx context.Context, client *apiclient.Client, includeStopped bool) ([]model.Environment, error) {
	response, err := client.ListEnvironments(ctx, "", downAllInventoryLimit)
	if err != nil {
		return nil, err
	}
	if response.Total > len(response.Environments) {
		return nil, fmt.Errorf("environment inventory exceeds %d entries; Portless refused a partial machine-wide shutdown", downAllInventoryLimit)
	}
	targets := make([]model.Environment, 0, len(response.Environments))
	for _, environment := range response.Environments {
		if includeStopped || environment.Status != model.EnvironmentStopped {
			targets = append(targets, environment)
		}
	}
	sort.Slice(targets, func(left, right int) bool {
		return model.EnvironmentSelector(targets[left].Project, targets[left].Name) < model.EnvironmentSelector(targets[right].Project, targets[right].Name)
	})
	return targets, nil
}

func newDownAllFailure(environment model.Environment, stage string, err error) downAllFailure {
	return downAllFailure{
		Environment: model.EnvironmentSelector(environment.Project, environment.Name),
		Stage:       stage,
		Error:       err.Error(),
	}
}

func (c *CLI) printDownAll(result downAllOutput) error {
	if c.jsonOutput {
		if err := writeJSON(c.Out, result); err != nil {
			return err
		}
		if len(result.Failures) > 0 {
			message := fmt.Sprintf("down failed for %d of %d environments", len(result.Failures), result.Targets)
			return writeJSON(c.Err, errorOutput{Error: errorDetail{Code: "DOWN_INCOMPLETE", Message: message}})
		}
		return nil
	}
	if result.Targets == 0 {
		fmt.Fprintln(c.Out, "All environments are already stopped.")
		return nil
	}
	failed := make(map[string]struct{}, len(result.Failures))
	for _, failure := range result.Failures {
		failed[failure.Environment] = struct{}{}
	}
	for _, operation := range result.Operations {
		selector := model.EnvironmentSelector(operation.Project, operation.Environment)
		if _, found := failed[selector]; found {
			continue
		}
		if result.Wait {
			fmt.Fprintf(c.Out, "%s  %s\n", selector, c.state(c.Out, "stopped"))
			continue
		}
		fmt.Fprintf(c.Out, "%s  %s operation %d %s\n", selector, operation.Type, operation.Number, c.state(c.Out, operation.State))
	}
	for _, failure := range result.Failures {
		fmt.Fprintf(c.Err, "%s %s failed during %s: %s\n", c.failure(c.Err, "portless:"), failure.Environment, failure.Stage, failure.Error)
	}
	return nil
}
