package replacement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

const readyProtocol = "1.0.0"

// ErrLeaseHeld means a running daemon or replacement guardian still owns the
// installation, including intervals without a published discovery record.
var ErrLeaseHeld = errors.New("another Portless daemon owns this installation or is replacing its daemon")

// ExecGuardian re-enters the retained working image before starting a trial.
// This ends every old control-plane goroutine while retaining the exclusive
// lease and the old process identity. It never executes the candidate image.
func ExecGuardian(image, buildID string, arguments, environment []string, lease *os.File) error {
	if err := verifyBuild(image, buildID); err != nil {
		return err
	}
	// Prevent a concurrent service launch from inheriting the lease during the
	// brief interval in which it must survive our own exec.
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	defer syscall.CloseOnExec(int(lease.Fd()))
	if _, _, err := syscall.Syscall(syscall.SYS_FCNTL, lease.Fd(), syscall.F_SETFD, 0); err != 0 {
		return err
	}
	arguments = append(arguments, "--lease-fd", strconv.Itoa(int(lease.Fd())))
	return syscall.Exec(image, append([]string{image}, arguments...), environment)
}

// AcquireLease excludes unrelated daemon launches while the old process drains,
// its child starts, or the old process restores state. The inherited descriptor
// keeps the same lock held across the entire handoff, without a release gap.
func AcquireLease(layout installation.Layout, inheritedFD int) (*os.File, error) {
	if err := installation.EnsurePrivateDirectory(layout.Root); err != nil {
		return nil, err
	}
	path := filepath.Join(layout.Root, "daemon.process.lock")
	var file *os.File
	var err error
	if inheritedFD > 0 {
		file = os.NewFile(uintptr(inheritedFD), path)
		syscall.CloseOnExec(inheritedFD)
	} else {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return nil, err
		}
	}
	if file == nil {
		return nil, errors.New("missing daemon process lease")
	}
	info, err := file.Stat()
	expected, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, expected) {
		file.Close()
		return nil, errors.New("daemon process lease does not match its private lock file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0o600 {
		file.Close()
		return nil, errors.New("daemon process lease has unverified ownership or permissions")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLeaseHeld
		}
		return nil, fmt.Errorf("acquire daemon process lease: %w", err)
	}
	// Only close the descriptor: LOCK_UN would also unlock an inherited lease
	// still held by the child after this guardian exits.
	return file, nil
}

type readyMessage struct {
	Protocol  string `json:"protocol"`
	PID       int    `json:"pid"`
	BuildID   string `json:"buildId"`
	RestartID string `json:"restartId"`
}

// AwaitCommit proves readiness through inherited pipes, then waits for the old
// process to commit. Call it only after reconciliation and listener binding,
// before admitting any application or feature API request.
func AwaitCommit(ctx context.Context, buildID, restartID string) error {
	// ExtraFiles arrive as blocking descriptors. Register nonblocking pipes
	// with Go's poller so the readiness/commit deadline remains enforceable.
	if err := syscall.SetNonblock(4, true); err != nil {
		return err
	}
	if err := syscall.SetNonblock(5, true); err != nil {
		return err
	}
	ready := os.NewFile(4, "daemon-replacement-ready")
	commit := os.NewFile(5, "daemon-replacement-commit")
	syscall.CloseOnExec(4)
	syscall.CloseOnExec(5)
	defer ready.Close()
	defer commit.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := ready.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if err := commit.SetReadDeadline(deadline); err != nil {
			return err
		}
	}
	if err := json.NewEncoder(ready).Encode(readyMessage{Protocol: readyProtocol, PID: os.Getpid(), BuildID: buildID, RestartID: restartID}); err != nil {
		return fmt.Errorf("report replacement readiness: %w", err)
	}
	var accepted [1]byte
	if _, err := io.ReadFull(commit, accepted[:]); err != nil {
		return fmt.Errorf("wait for replacement commit: %w", err)
	}
	if accepted[0] != 1 {
		return errors.New("daemon replacement was not committed")
	}
	return nil
}

// TrialOptions defines the fixed daemon launch and its inherited process lease.
type TrialOptions struct {
	Executable   string
	Arguments    []string
	Environment  []string
	Lease        *os.File
	Receipt      contract.DaemonRestart
	Stdout       io.Writer
	Stderr       io.Writer
	BeforeCommit func() error
}

// UnreapedError means a trial child has not been proven exited; its guardian
// must not restore the database or start another daemon.
type UnreapedError struct{ Cause error }

// Error explains why recovery must fail closed.
func (e *UnreapedError) Error() string {
	return "replacement process exit could not be confirmed; refusing concurrent recovery: " + e.Cause.Error()
}

// Unwrap preserves the original replacement failure.
func (e *UnreapedError) Unwrap() error { return e.Cause }

// Trial starts exactly one owned child, waits for its bounded readiness proof,
// and commits it. Every failure kills and reaps only that directly owned child
// before allowing recovery. Success transfers the inherited lease to the child.
func Trial(ctx context.Context, options TrialOptions) error {
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	commitRead, commitWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer commitRead.Close()
	defer commitWrite.Close()
	command := exec.Command(options.Executable, options.Arguments...)
	command.Env = options.Environment
	command.ExtraFiles = []*os.File{options.Lease, readyWrite, commitRead}
	command.Stdout, command.Stderr = options.Stdout, options.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start replacement daemon: %w", err)
	}
	readyWrite.Close()
	commitRead.Close()
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	ready := make(chan error, 1)
	go func() {
		var message readyMessage
		err := json.NewDecoder(io.LimitReader(readyRead, 4096)).Decode(&message)
		if err == nil && (message.Protocol != readyProtocol || message.PID != command.Process.Pid || message.BuildID != options.Receipt.TargetBuildID || message.RestartID != options.Receipt.RestartID) {
			err = errors.New("replacement readiness proof does not match the owned trial")
		}
		ready <- err
	}()
	var failure error
	select {
	case err := <-exited:
		return fmt.Errorf("replacement daemon exited before readiness: %v", err)
	case <-ctx.Done():
		failure = ctx.Err()
	case failure = <-ready:
		if failure == nil {
			failure = ctx.Err()
			if failure == nil {
				if options.BeforeCommit != nil {
					failure = options.BeforeCommit()
				}
			}
			if failure == nil {
				if deadline, ok := ctx.Deadline(); ok {
					failure = commitWrite.SetWriteDeadline(deadline)
				}
				if failure == nil {
					_, failure = commitWrite.Write([]byte{1})
				}
				if failure == nil {
					return nil
				}
			}
		}
	}
	// os.Process retains the directly spawned child's identity; no PID from a
	// discovery file or runtime record is ever signalled here.
	_ = command.Process.Kill()
	select {
	case <-exited:
		return fmt.Errorf("replacement daemon failed readiness: %w", failure)
	case <-time.After(2 * time.Second):
		return &UnreapedError{Cause: failure}
	}
}
