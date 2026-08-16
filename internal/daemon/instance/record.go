// Package instance owns the private discovery record shared by the daemon
// process and its authenticated control clients.
package instance

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/portless-run/portless/internal/installation"
)

type Record struct {
	PID              int       `json:"pid"`
	Port             int       `json:"port"`
	ProtocolVersion  string    `json:"protocolVersion,omitempty"`
	APIVersion       string    `json:"apiVersion"`
	InstallationID   string    `json:"installationId,omitempty"`
	InstanceID       string    `json:"instanceId,omitempty"`
	BuildID          string    `json:"buildId,omitempty"`
	State            string    `json:"state,omitempty"`
	HandoffReady     bool      `json:"handoffReady,omitempty"`
	RecoveryProblems []string  `json:"recoveryProblems,omitempty"`
	TokenPath        string    `json:"tokenPath"`
	StartedAt        time.Time `json:"startedAt"`
	ProcessHint      string    `json:"processHint"`
}

func Read(layout installation.Layout) (Record, error) {
	content, err := installation.ReadPrivateTextFile(layout.Control)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal([]byte(content), &record); err != nil {
		return Record{}, err
	}
	if record.Port < 1 || record.Port > 65535 || record.PID <= 0 || record.APIVersion == "" || record.TokenPath == "" {
		return Record{}, errors.New("invalid daemon discovery record")
	}
	return record, nil
}

func Write(layout installation.Layout, record Record) error {
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temporary := layout.Control + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, layout.Control); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func RemoveOwn(layout installation.Layout, instanceID string) {
	record, err := Read(layout)
	if err == nil && record.PID == os.Getpid() && record.InstanceID == instanceID {
		_ = os.Remove(layout.Control)
	}
}

func RemoveMatching(layout installation.Layout, expected Record) {
	current, err := Read(layout)
	if err != nil || current.PID != expected.PID || current.Port != expected.Port || current.InstanceID != expected.InstanceID {
		return
	}
	_ = os.Remove(layout.Control)
}
