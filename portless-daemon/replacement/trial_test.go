package replacement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func TestTrialChild(t *testing.T) {
	mode := os.Getenv("PORTLESS_TEST_TRIAL")
	if mode == "" {
		return
	}
	layout, err := installation.ResolveLayout(os.Getenv("PORTLESS_TEST_HOME"))
	if err != nil {
		os.Exit(71)
	}
	lease, err := AcquireLease(layout, 3)
	if err != nil {
		os.Exit(72)
	}
	defer lease.Close()
	switch mode {
	case "exit":
		os.Exit(73)
	case "hang":
		time.Sleep(30 * time.Second)
	case "invalid":
		file := os.NewFile(4, "ready")
		_, _ = file.WriteString("{\"protocol\":\"1.0.0\",\"pid\":1}\n")
		time.Sleep(30 * time.Second)
	case "ready":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := AwaitCommit(ctx, strings.Repeat("a", 64), "trial-id"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(layout.Root, "committed"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTrialCommitsOnlyReadyOwnedChildAndReapsFailures(t *testing.T) {
	for _, mode := range []string{"ready", "exit", "hang", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			layout, err := installation.ResolveLayout(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireLease(layout, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			timeout := 3 * time.Second
			if mode == "hang" {
				timeout = 300 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err = Trial(ctx, TrialOptions{
				Executable: executable, Arguments: []string{"-test.run=^TestTrialChild$"},
				Environment: append(os.Environ(), "PORTLESS_TEST_TRIAL="+mode, "PORTLESS_TEST_HOME="+layout.Root),
				Lease:       lease, Receipt: contract.DaemonRestart{RestartID: "trial-id", TargetBuildID: strings.Repeat("a", 64)},
				Stdout: os.Stderr, Stderr: os.Stderr,
			})
			if mode == "ready" {
				if err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(time.Second)
				for {
					if _, err := os.Stat(filepath.Join(layout.Root, "committed")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("child did not receive commit")
					}
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				if err == nil {
					t.Fatal("failed child was committed")
				}
				var unsafe *UnreapedError
				if errors.As(err, &unsafe) {
					t.Fatal(err)
				}
				if mode == "hang" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline error = %v", err)
				}
			}
			// A reaped child must not have unlocked the guardian's shared lease.
			if other, err := AcquireLease(layout, 0); err == nil {
				other.Close()
				t.Fatal("trial released the installation lease")
			}
		})
	}
}

func TestRetainedBuildIntegrityAndQuarantine(t *testing.T) {
	layout, _ := installation.ResolveLayout(t.TempDir())
	source := filepath.Join(t.TempDir(), "portless")
	if err := os.WriteFile(source, []byte("working image"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildID, _ := installation.BuildIDForPath(source)
	staged, err := StageBuild(context.Background(), layout, source, buildID)
	if err != nil {
		t.Fatal(err)
	}
	rejected := strings.Repeat("b", 64)
	if err := WriteRecovery(layout, Recovery{BuildID: buildID, Restart: contract.DaemonRestartStatus{Outcome: "rolled-back", RestartID: "trial", TargetBuildID: rejected}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	launched, err := LaunchBuild(context.Background(), layout, source, rejected)
	if err != nil || launched != staged {
		t.Fatalf("quarantined launch = %q, %v", launched, err)
	}
	if err := os.WriteFile(staged, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchBuild(context.Background(), layout, source, rejected); err == nil {
		t.Fatal("tampered recovery image was accepted")
	}
	if _, err := BuildPath(layout, "../../unowned"); err == nil {
		t.Fatal("invalid image path accepted")
	}
}

func TestFailedGuardianExecRetainsExclusiveCloseOnExecLease(t *testing.T) {
	layout, _ := installation.ResolveLayout(t.TempDir())
	lease, err := AcquireLease(layout, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	image := filepath.Join(t.TempDir(), "invalid-image")
	if err := os.WriteFile(image, []byte("invalid executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildID, err := installation.BuildIDForPath(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExecGuardian(image, buildID, nil, os.Environ(), lease); err == nil {
		t.Fatal("invalid guardian executed")
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, lease.Fd(), syscall.F_GETFD, 0)
	if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("lease could leak to a child after failed exec: flags=%d err=%v", flags, errno)
	}
	if other, err := AcquireLease(layout, 0); err == nil {
		other.Close()
		t.Fatal("failed exec released ownership")
	}
}
