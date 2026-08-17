package projects

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbsoluteSourcePathUsesCLIWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	actual, err := absoluteSourcePath("checkout")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("absoluteSourcePath = %q, want %q", actual, expected)
	}
}
