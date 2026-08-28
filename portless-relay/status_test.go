package relay

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectHelperBuildComparesInstalledHelperWithCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helperBuildID, currentBuildID, current, err := inspectHelperBuild(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !current || helperBuildID == "" || helperBuildID != currentBuildID {
		t.Fatalf("current executable comparison = %q, %q, %v", helperBuildID, currentBuildID, current)
	}

	staleHelper := filepath.Join(t.TempDir(), "portless-relay")
	if err := os.WriteFile(staleHelper, []byte("older Portless helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	helperBuildID, currentBuildID, current, err = inspectHelperBuild(staleHelper)
	if err != nil {
		t.Fatal(err)
	}
	if current || helperBuildID == "" || currentBuildID == "" || helperBuildID == currentBuildID {
		t.Fatalf("stale helper comparison = %q, %q, %v", helperBuildID, currentBuildID, current)
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
