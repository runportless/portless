package control

import (
	"context"
	"fmt"
	"time"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func (m *Manager) connect(ctx context.Context) (*apiclient.Client, identity.Record, error) {
	paths := m.layout
	record, err := m.ensureDaemon(ctx)
	if err != nil {
		return nil, identity.Record{}, err
	}
	token, err := installation.ReadPrivateTextFile(paths.AuthToken)
	if err != nil {
		return nil, identity.Record{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	return apiclient.New(fmt.Sprintf("http://127.0.0.1:%d", record.Port), token, m.hooks.HTTPClient(30*time.Second)), record, nil
}

// connectExisting verifies and connects to an already-running daemon. It never
// starts, replaces, repairs, or otherwise mutates daemon state.
func (m *Manager) connectExisting(ctx context.Context) (*apiclient.Client, identity.Record, error) {
	paths := m.layout
	record, err := m.checkDaemon(ctx)
	if err != nil {
		return nil, identity.Record{}, err
	}
	token, err := installation.ReadPrivateTextFile(paths.AuthToken)
	if err != nil {
		return nil, identity.Record{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	return apiclient.New(fmt.Sprintf("http://127.0.0.1:%d", record.Port), token, m.hooks.HTTPClient(2*time.Second)), record, nil
}
