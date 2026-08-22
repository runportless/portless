package command

import (
	"os"
	"testing"

	"github.com/runportless/portless/portless-daemon/control"
	"github.com/spf13/cobra"
)

func TestDynamicCompletionNeverStartsAStoppedDaemon(t *testing.T) {
	context, _, _ := newTestContext(t, t.TempDir())
	context.Daemon = control.New(context.Paths)
	root := &cobra.Command{Use: "portless"}
	values, directive := context.Complete(CompletionServices)(root, nil, "")
	if len(values) != 0 {
		t.Fatalf("completion returned values without a daemon: %#v", values)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("completion directive = %v, want no file completion", directive)
	}
	if _, err := os.Stat(context.Paths.Control); !os.IsNotExist(err) {
		t.Fatalf("dynamic completion contacted or started the daemon: %v", err)
	}
}
