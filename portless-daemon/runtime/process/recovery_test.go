package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/runtime/supervisor"
)

func TestInspectPersistedRunClassifiesRecoveryEvidence(t *testing.T) {
	expected := PersistedRun{
		Scope: "store/local", Service: "checkout", Generation: 4, PID: 4102, SupervisorPID: 4101,
		SupervisorSocket: "/tmp/checkout.sock", SupervisorState: "/private/checkout.state.json", PrivateRunKey: "private",
		LaunchMode: model.LaunchManaged,
	}
	ready := supervisor.Status{
		ProtocolVersion: supervisor.ProtocolVersion, Scope: expected.Scope, Service: expected.Service, Generation: expected.Generation,
		PID: expected.PID, SupervisorPID: expected.SupervisorPID, Port: 43123, State: "ready", LaunchMode: model.LaunchManaged,
	}
	terminal := ready
	terminal.State = "stopped"
	unavailable := errors.New("socket is unavailable")

	tests := []struct {
		name            string
		live            supervisor.Status
		liveErr         error
		durable         supervisor.Status
		durableErr      error
		supervisorAlive bool
		groupAlive      bool
		want            RecoveryState
	}{
		{name: "live", live: ready, want: RecoveryLive},
		{name: "durable terminal", liveErr: unavailable, durable: terminal, want: RecoveryTerminal},
		{name: "durable ready and absent", liveErr: unavailable, durable: ready, want: RecoveryGone},
		{name: "supervisor remains", liveErr: unavailable, durable: ready, supervisorAlive: true, want: RecoveryUnverifiable},
		{name: "process group remains", liveErr: unavailable, durable: ready, groupAlive: true, want: RecoveryUnverifiable},
		{name: "durable state missing", liveErr: unavailable, durableErr: errors.New("missing state"), want: RecoveryUnverifiable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspection := inspectPersistedRun(context.Background(), expected, recoveryHooks{
				liveStatus: func(context.Context, string, string) (supervisor.Status, error) { return test.live, test.liveErr },
				durableStatus: func(context.Context, string, string, string) (supervisor.Status, error) {
					return test.durable, test.durableErr
				},
				processAlive:      func(int) (bool, error) { return test.supervisorAlive, nil },
				processGroupAlive: func(int) (bool, error) { return test.groupAlive, nil },
			})
			if inspection.State != test.want {
				t.Fatalf("inspection = %#v, want %s", inspection, test.want)
			}
		})
	}
}

func TestInspectPersistedRunRejectsIdentityAndPIDMismatch(t *testing.T) {
	expected := PersistedRun{
		Scope: "store/local", Service: "checkout", Generation: 4, PID: 4102, SupervisorPID: 4101,
		SupervisorSocket: "/tmp/checkout.sock", SupervisorState: "/private/checkout.state.json", PrivateRunKey: "private",
		LaunchMode: model.LaunchManaged,
	}
	status := supervisor.Status{
		ProtocolVersion: supervisor.ProtocolVersion, Scope: expected.Scope, Service: expected.Service, Generation: expected.Generation,
		PID: 9999, SupervisorPID: expected.SupervisorPID, Port: 43123, State: "ready", LaunchMode: model.LaunchManaged,
	}
	inspection := inspectPersistedRun(context.Background(), expected, recoveryHooks{
		liveStatus: func(context.Context, string, string) (supervisor.Status, error) { return status, nil },
	})
	if inspection.State != RecoveryUnverifiable || inspection.Err == nil {
		t.Fatalf("mismatched PID inspection = %#v", inspection)
	}
}

func TestStopPersistedRunStopsOnlyAuthenticatedLiveSupervisor(t *testing.T) {
	expected := PersistedRun{
		Scope: "store/local", Service: "checkout", Generation: 4, PID: 4102, SupervisorPID: 4101,
		SupervisorSocket: "/tmp/checkout.sock", SupervisorState: "/private/checkout.state.json", PrivateRunKey: "private",
		LaunchMode: model.LaunchManaged,
	}
	ready := supervisor.Status{
		ProtocolVersion: supervisor.ProtocolVersion, Scope: expected.Scope, Service: expected.Service, Generation: expected.Generation,
		PID: expected.PID, SupervisorPID: expected.SupervisorPID, Port: 43123, State: "ready", LaunchMode: model.LaunchManaged,
	}
	stopped := ready
	stopped.State = "stopped"
	stopCalls := 0
	manager := NewManager(nil)
	manager.recovery = recoveryHooks{
		liveStatus: func(context.Context, string, string) (supervisor.Status, error) { return ready, nil },
		stop: func(context.Context, string, string, string) (supervisor.Status, error) {
			stopCalls++
			return stopped, nil
		},
	}
	inspection, err := manager.StopPersistedRun(context.Background(), expected, time.Second)
	manager.Close()
	if err != nil || inspection.State != RecoveryTerminal || stopCalls != 1 {
		t.Fatalf("stop inspection=%#v calls=%d err=%v", inspection, stopCalls, err)
	}
}
