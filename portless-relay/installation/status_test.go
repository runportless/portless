package installation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	systeminstallation "github.com/runportless/portless/portless-daemon/system/installation"
)

func TestValidateInstalledHelperReceiptDetectsContentReplacement(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "portless-relay")
	if err := os.WriteFile(helper, []byte("installed Portless helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	helperBuildID, err := systeminstallation.BuildIDForPath(helper)
	if err != nil {
		t.Fatal(err)
	}
	receipt := installationReceipt{HelperBuildID: helperBuildID}
	if err := validateInstalledHelperReceipt(helper, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("replaced Portless helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateInstalledHelperReceipt(helper, receipt); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("replaced helper passed receipt validation: %v", err)
	}
}

func TestInspectArtifactRejectsUnsafeModeAndSymlink(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "artifact")
	if err := os.WriteFile(artifact, []byte("relay"), 0o600); err != nil {
		t.Fatal(err)
	}
	present, err := inspectArtifact(artifact, 0o644, os.Geteuid(), os.Getegid())
	if !present || err == nil {
		t.Fatalf("unsafe artifact mode was accepted: present=%v err=%v", present, err)
	}
	link := filepath.Join(directory, "artifact-link")
	if err := os.Symlink(artifact, link); err != nil {
		t.Fatal(err)
	}
	present, err = inspectArtifact(link, 0o600, os.Geteuid(), os.Getegid())
	if !present || err == nil {
		t.Fatalf("artifact symlink was accepted: present=%v err=%v", present, err)
	}
	if err := os.Chmod(artifact, os.FileMode(0o644)|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	present, err = inspectArtifact(artifact, 0o644, os.Geteuid(), os.Getegid())
	if !present || err == nil {
		t.Fatalf("artifact with special permission bits was accepted: present=%v err=%v", present, err)
	}
}
