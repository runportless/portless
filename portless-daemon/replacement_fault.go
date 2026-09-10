//go:build !e2e

package daemon

import (
	"context"
	"github.com/runportless/portless/portless-daemon/database"
)

func replacementStartupFault(context.Context, string, *database.Store) error { return nil }
