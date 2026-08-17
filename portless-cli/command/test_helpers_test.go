package command

import (
	"bytes"
	"os"
	"testing"

	"github.com/portless-run/portless/portless-daemon/system/installation"
)

func newTestContext(t *testing.T, root string) (*Context, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	paths, err := installation.ResolveLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	return &Context{Out: out, Err: errOut, Paths: paths}, out, errOut
}

func assertFileMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s permissions = %04o, want %04o", path, actual, expected)
	}
}
