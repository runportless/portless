package discovery_test

import (
	"github.com/runportless/portless/portless-daemon/projects/discovery"
	"os"
	"path/filepath"
	"testing"
)

func TestConfinedDiscoveryRejectsAncestorsAndSymlinkEscapes(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "child")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"name":"shop","scripts":{"start":"node server.js"},"dependencies":{"express":"5.1.0"}}`)
	if err := os.WriteFile(filepath.Join(parent, "package.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := discovery.NewDefault(discovery.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DiscoverWithin(t.Context(), root, root); err == nil {
		t.Fatal("scanned unauthorized ancestor")
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DiscoverWithin(t.Context(), root, root); err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "package.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DiscoverWithin(t.Context(), filepath.Join(root, "escape"), root); err == nil {
		t.Fatal("followed descendant symlink outside root")
	}
	old := root + "-original"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DiscoverWithin(t.Context(), root, root); err == nil {
		t.Fatal("followed replaced authorization root")
	}
}
