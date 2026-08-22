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
