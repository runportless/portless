package administration

import (
	"bytes"
	"os"
	"testing"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-cli/doctor"
	"github.com/runportless/portless/portless-daemon/control"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func newTestCommands(t *testing.T) (*Commands, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	context := &command.Context{Out: out, Err: errOut, Paths: paths, Daemon: control.New(paths)}
	context.Local.UserIDs = func() (int, int) { return os.Getuid(), os.Getgid() }
	context.Local.Diagnose = doctor.Run
	return New(context), out, errOut
}
