package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	relayinstall "github.com/portless-run/portless/internal/relay/install"
)

func TestLocalDependenciesIsolateRootAndRelayInspection(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "apps", "checkout")
	root := filepath.Dir(filepath.Dir(checkout))
	rootLookups, relayInspections := 0, 0
	application, err := newWithDependencies(&bytes.Buffer{}, &bytes.Buffer{}, t.TempDir(), localDependencies{
		workingDirectory: func() (string, error) { return checkout, nil },
		findProjectRoot: func(_ context.Context, start string) (string, error) {
			rootLookups++
			if start != checkout {
				t.Fatalf("root lookup started at %q, want %q", start, checkout)
			}
			return root, nil
		},
		inspectRelay: func(context.Context) (relayinstall.InstallationStatus, error) {
			relayInspections++
			return relayinstall.InstallationStatus{Platform: "fixture"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := application.currentSourceRoot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expectedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != expectedRoot || rootLookups != 1 {
		t.Fatalf("resolved root = %q, lookups = %d", resolved, rootLookups)
	}
	if err := application.relayStatus(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if relayInspections != 1 {
		t.Fatalf("relay inspections = %d, want 1", relayInspections)
	}
}
