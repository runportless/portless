package controlplane

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
)

func TestDiagnosticsReportsRecoveryAndBoundedStorage(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	initial := app.Diagnostics(ctx, false)
	if initial.Storage != nil || initial.Recovery.Result != "not-run" {
		t.Fatalf("initial diagnostics = %#v", initial)
	}
	if initial.Inventory.Processes != 0 || initial.Inventory.Containers != 0 || initial.Inventory.ProxyListeners != 0 || initial.Inventory.ActiveEnvironments != 0 {
		t.Fatalf("initial inventory = %#v", initial.Inventory)
	}

	if _, err := app.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(data, "environments", "private-environment", "logs", "checkout", "1")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "stdout.jsonl"), []byte("service"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "daemon.log"), []byte("daemon-output"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := app.Diagnostics(ctx, true)
	if result.Recovery.Result != "healthy" || result.Recovery.CompletedAt == nil || result.Recovery.Duration < 0 || result.Recovery.Recovered != 0 || len(result.Recovery.Problems) != 0 {
		t.Fatalf("recovery diagnostics = %#v", result.Recovery)
	}
	if result.Storage == nil {
		t.Fatal("storage diagnostics were not collected")
	}
	storage := result.Storage
	if storage.DatabaseBytes <= 0 || storage.ServiceLogBytes != int64(len("service")) || storage.DaemonLogBytes != int64(len("daemon-output")) {
		t.Fatalf("storage footprint = %#v", storage)
	}
	if storage.RecordingDefaultEventLimit != database.DefaultRecordingEventLimit || storage.RecordingMaximumEventLimit != maximumRecordingEventLimit || storage.RecordingDefaultPayloadLimit != database.DefaultRecordingPayloadLimit || storage.RecordingMaximumPayloadLimit != maximumRecordingPayloadLimit {
		t.Fatalf("recording limits = %#v", storage)
	}
	if storage.TrafficExchangeLimitPerEnvironment <= 0 || storage.TrafficPayloadLimitPerEnvironment <= 0 || storage.ServiceLogGenerationLimit <= 0 || storage.ServiceLogStreamLimitBytes <= 0 || len(storage.Problems) != 0 {
		t.Fatalf("retention diagnostics = %#v", storage)
	}
}
