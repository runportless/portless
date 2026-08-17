package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/model"
)

func InvocationKey(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create operation idempotency key: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(random[:]), nil
}

func (c *Context) WaitOperation(ctx context.Context, client *apiclient.Client, operation model.Operation, jsonOutput bool) (model.Operation, error) {
	seen := 0
	for {
		current, err := client.Operation(ctx, operation.Project, operation.Environment, operation.Number)
		if err != nil {
			return model.Operation{}, err
		}
		operation = current
		for _, event := range operation.Events[seen:] {
			if !jsonOutput {
				fmt.Fprintf(c.Out, "  %-12s %s\n", event.Subject, event.Message)
			}
		}
		seen = len(operation.Events)
		if operation.State != "running" {
			return operation, nil
		}
		select {
		case <-ctx.Done():
			return model.Operation{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
