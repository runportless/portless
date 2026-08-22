package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/runportless/portless/portless-relay"
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
		inspectRelay: func(context.Context) (relay.InstallationStatus, error) {
			relayInspections++
			return relay.InstallationStatus{Platform: "fixture"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := application.context.CurrentSourceRoot(context.Background())
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
	if code := application.Run(context.Background(), []string{"relay", "status"}); code != 0 {
		t.Fatalf("relay status returned %d", code)
	}
	if relayInspections != 1 {
		t.Fatalf("relay inspections = %d, want 1", relayInspections)
	}
}
