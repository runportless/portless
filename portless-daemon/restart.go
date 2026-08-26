package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

const daemonRestartReceiptEnvironment = "PORTLESS_INTERNAL_DAEMON_RESTART_RECEIPT"

type replacementRequest struct {
	receipt contract.DaemonRestart
	cause   error
}

type replacementCoordinator struct {
	mu        sync.Mutex
	pending   *replacementRequest
	committed bool
	requests  chan replacementRequest
}

func newReplacementCoordinator() *replacementCoordinator {
	return &replacementCoordinator{requests: make(chan replacementRequest, 1)}
}

func (c *replacementCoordinator) prepare(reason, previousInstanceID, targetBuildID string, acceptedAt time.Time, activeEnvironments []string, cause error) (contract.DaemonRestart, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending != nil {
		return cloneDaemonRestart(c.pending.receipt), nil
	}
	restartID, err := newInstanceID()
	if err != nil {
		return contract.DaemonRestart{}, fmt.Errorf("create daemon restart identifier: %w", err)
	}
	if acceptedAt.IsZero() {
		acceptedAt = time.Now().UTC()
	} else {
		acceptedAt = acceptedAt.UTC()
	}
	receipt := contract.DaemonRestart{
		Restarting: true, RestartID: restartID, Reason: reason,
		PreviousInstanceID: previousInstanceID, TargetBuildID: targetBuildID,
		AcceptedAt: acceptedAt, DeadlineAt: acceptedAt.Add(contract.DaemonRestartSLA),
		Handoff: true, ActiveEnvironments: append([]string(nil), activeEnvironments...),
	}
	c.pending = &replacementRequest{receipt: receipt, cause: cause}
	return cloneDaemonRestart(receipt), nil
}

func (c *replacementCoordinator) commit(restartID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || c.pending.receipt.RestartID != restartID || c.committed {
		return false
	}
	c.committed = true
	c.requests <- replacementRequest{receipt: cloneDaemonRestart(c.pending.receipt), cause: c.pending.cause}
	return true
}

func cloneDaemonRestart(receipt contract.DaemonRestart) contract.DaemonRestart {
	receipt.ActiveEnvironments = append([]string(nil), receipt.ActiveEnvironments...)
	return receipt
}

type replacementExit struct {
	receipt contract.DaemonRestart
	cause   error
}

// Error returns the replacement trigger as the daemon run error.
func (e *replacementExit) Error() string {
	if e.cause == nil {
		return "Portless daemon replacement requested"
	}
	return e.cause.Error()
}

// Unwrap exposes the replacement sentinel for process-mode dispatch.
func (e *replacementExit) Unwrap() error { return e.cause }

func restartReceiptFromEnvironment() (*contract.DaemonRestart, error) {
	encoded := os.Getenv(daemonRestartReceiptEnvironment)
	if encoded == "" {
		return nil, nil
	}
	var receipt contract.DaemonRestart
	if err := json.Unmarshal([]byte(encoded), &receipt); err != nil {
		return nil, fmt.Errorf("decode daemon restart receipt: %w", err)
	}
	if !receipt.Restarting || receipt.RestartID == "" || receipt.Reason == "" || receipt.PreviousInstanceID == "" || receipt.TargetBuildID == "" || receipt.AcceptedAt.IsZero() || receipt.DeadlineAt.IsZero() || !receipt.DeadlineAt.After(receipt.AcceptedAt) {
		return nil, errors.New("daemon restart receipt is incomplete")
	}
	return &receipt, nil
}

func environmentWithRestartReceipt(environment []string, receipt contract.DaemonRestart) ([]string, error) {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode daemon restart receipt: %w", err)
	}
	prefix := daemonRestartReceiptEnvironment + "="
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		if !strings.HasPrefix(value, prefix) {
			result = append(result, value)
		}
	}
	return append(result, prefix+string(encoded)), nil
}
