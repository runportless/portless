//go:build e2e

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/runportless/portless/portless-daemon/database"
)

// Linked only in the isolated failure-test image. Normal E2E and production
// builds cannot activate these startup failures through an environment value.
var replacementFailureEnabled string

func replacementStartupFault(ctx context.Context, root string, store *database.Store) error {
	if replacementFailureEnabled != "true" {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(root, ".e2e-replacement-failure"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	switch strings.TrimSpace(string(content)) {
	case "crash":
		os.Exit(73)
	case "deadline":
		<-ctx.Done()
		return ctx.Err()
	case "state":
		// Simulate a candidate migration followed by failed reconciliation. The
		// old binary must receive its original database, including runtime records.
		_, err = store.DB().ExecContext(ctx, "ALTER TABLE environments RENAME TO rejected_environments")
		return err
	}
	return nil
}
