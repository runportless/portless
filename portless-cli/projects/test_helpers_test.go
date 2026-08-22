package projects

import (
	"bytes"
	"testing"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func newTestCommands(t *testing.T) (*Commands, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(&command.Context{Out: out, Err: errOut, Paths: paths}), out, errOut
}
