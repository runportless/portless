//go:build darwin || linux

package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayLifecycleLockSerializesOperations(t *testing.T) {
	details := platformInstallation{
		LifecycleLockPath: filepath.Join(t.TempDir(), "relay.lock"),
		ArtifactUID:       os.Geteuid(), ArtifactGID: os.Getegid(),
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- withRelayLifecycleLock(context.Background(), details, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first lifecycle operation did not acquire the lock")
	}

	secondEntered := false
	secondContext, cancelSecond := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelSecond()
	err := withRelayLifecycleLock(secondContext, details, func() error {
		secondEntered = true
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || secondEntered {
		t.Fatalf("contending lifecycle operation err=%v entered=%v", err, secondEntered)
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := withRelayLifecycleLock(context.Background(), details, func() error {
		secondEntered = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !secondEntered {
		t.Fatal("lifecycle lock was not released")
	}
}

func TestRelayLifecycleLockRejectsSymlinksAndUnsafeModes(t *testing.T) {
	directory := t.TempDir()
	details := platformInstallation{
		LifecycleLockPath: filepath.Join(directory, "relay.lock"),
		ArtifactUID:       os.Geteuid(), ArtifactGID: os.Getegid(),
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, details.LifecycleLockPath); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRelayLifecycleLock(context.Background(), details); err == nil {
		t.Fatal("symlink lifecycle lock was accepted")
	}
	if err := os.Remove(details.LifecycleLockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(details.LifecycleLockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRelayLifecycleLock(context.Background(), details); err == nil || !strings.Contains(err.Error(), "mode-0600") {
		t.Fatalf("unsafe lifecycle lock mode was accepted: %v", err)
	}
}
