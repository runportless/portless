package portlessmcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupCapabilitiesAndSourceConfinement(t *testing.T) {
	for _, config := range []Config{{Environment: "billing/local", Project: "billing"}, {Project: "billing", AllEnvironments: true}, {AllowReplay: true}, {Project: "Bad Name"}} {
		if _, err := validateConfig(config); err == nil {
			t.Fatalf("accepted invalid startup: %#v", config)
		}
	}
	root := t.TempDir()
	sibling := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "service"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	config, err := validateConfig(Config{WorkspaceRoot: root, AllowConfiguration: true})
	if err != nil {
		t.Fatal(err)
	}
	r := &runtime{config: config}
	if _, _, err := r.sourcePath("service"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sibling, filepath.Join(root, "outside")} {
		if _, _, err := r.sourcePath(path); err == nil || !strings.Contains(err.Error(), "SOURCE_ROOT_REQUIRED") {
			t.Fatalf("source escape accepted: %s %v", path, err)
		}
	}
	config, err = validateConfig(Config{Project: "billing", AllowConfiguration: true, AllowedSourceRoots: []string{root, sibling}})
	if err != nil {
		t.Fatal(err)
	}
	r.config = config
	if _, _, err := r.sourcePath(sibling); err != nil {
		t.Fatal(err)
	}
}
