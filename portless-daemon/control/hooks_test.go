package control

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func TestManagerUsesInjectedDaemonStarter(t *testing.T) {
	layout, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("starter called")
	manager := NewWithHooks(layout, Hooks{
		StartDaemon: func(actual installation.Layout) error {
			if actual.Root != layout.Root {
				t.Fatalf("starter layout root = %q, want %q", actual.Root, layout.Root)
			}
			return want
		},
	})
	if _, err := manager.Ensure(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Ensure error = %v, want injected starter error", err)
	}
}

func TestForcedStopUsesInjectedProcessOperations(t *testing.T) {
	layout, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := installation.EnsurePrivateDirectory(layout.Root); err != nil {
		t.Fatal(err)
	}
	record := identity.Record{PID: 4242, Port: 7331, APIVersion: "1.0.0", TokenPath: layout.AuthToken, InstanceID: "fixture"}
	if err := identity.Write(layout, record); err != nil {
		t.Fatal(err)
	}
	alive, verified, signaled := true, false, false
	manager := NewWithHooks(layout, Hooks{
		ProcessAlive: func(pid int) (bool, error) {
			if pid != record.PID {
				t.Fatalf("liveness PID = %d, want %d", pid, record.PID)
			}
			return alive, nil
		},
		VerifyProcess: func(_ context.Context, actual installation.Layout, candidate identity.Record) error {
			verified = actual.Root == layout.Root && candidate.PID == record.PID
			return nil
		},
		SignalProcess: func(pid int, signal os.Signal) error {
			if pid != record.PID || signal != syscall.SIGTERM {
				t.Fatalf("unexpected signal %v for PID %d", signal, pid)
			}
			signaled = true
			alive = false
			return nil
		},
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	result, err := manager.Stop(context.Background(), StopOptions{Force: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Stopped || !result.Forced || !result.Legacy || !verified || !signaled {
		t.Fatalf("forced stop result = %#v, verified=%t signaled=%t", result, verified, signaled)
	}
	if _, err := identity.Read(layout); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control record still exists after injected stop: %v", err)
	}
}
