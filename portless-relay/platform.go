package relay

import "context"

type relayServiceState uint8

const (
	relayServiceUnknown relayServiceState = iota
	relayServiceStopped
	relayServiceRunning
)

type uninstallSpec struct {
	loopbackAddresses []string
}

type hostPlatform interface {
	installation() platformInstallation
	install(context.Context, SetupRequest) error
	restart(context.Context) error
	uninstall(context.Context, uninstallSpec) error
	prepareRuntime(context.Context) error
	serviceState(context.Context) (relayServiceState, error)
	loopbackPoolStatus() (bool, string, error)
}
