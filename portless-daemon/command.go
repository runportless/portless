package daemon

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/replacement"
	"github.com/runportless/portless/portless-daemon/runtime/supervisor"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

// BuildInfo identifies the linked Portless version and package provenance.
type BuildInfo struct {
	Version      string
	Distribution string
	Commit       string
}

// Command parses and runs the private daemon process mode.
func Command(args []string, stderr io.Writer, build BuildInfo) int {
	set := flag.NewFlagSet("__daemon", flag.ContinueOnError)
	set.SetOutput(stderr)
	dataDirectory := set.String("data-dir", "", "internal data directory")
	port := set.Int("port", 0, "preferred control port")
	source := set.String("executable", "", "installed executable path")
	trial := set.Bool("replacement", false, "inherited daemon replacement trial")
	guardian := set.Bool("guardian", false, "inherited daemon recovery guardian")
	leaseFD := set.Int("lease-fd", 3, "inherited daemon process lease descriptor")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || (*trial && *guardian) {
		return 2
	}
	layout, err := installation.ResolveLayout(*dataDirectory)
	if err == nil {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err = runDaemonProcess(ctx, layout, *port, *source, *trial, *guardian, *leaseFD, build, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "portless daemon:", err)
		return 1
	}
	return 0
}

func runDaemonProcess(ctx context.Context, layout installation.Layout, port int, source string, trial, guardian bool, leaseFD int, build BuildInfo, stderr io.Writer) error {
	if !trial && !guardian {
		leaseFD = 0
	}
	if (trial || guardian) && leaseFD < 3 {
		return errors.New("invalid inherited daemon process lease")
	}
	lease, err := replacement.AcquireLease(layout, leaseFD)
	if err != nil {
		return err
	}
	defer lease.Close()
	// A receipt is meaningful only on the inherited trial channel. Ordinary
	// startup must not treat ambient environment data as an accepted handoff.
	if !trial && !guardian {
		_ = os.Unsetenv(daemonRestartReceiptEnvironment)
	}
	receipt, err := restartReceiptFromEnvironment()
	_ = os.Unsetenv(daemonRestartReceiptEnvironment)
	if err != nil {
		return err
	}
	if (trial || guardian) && receipt == nil {
		return errors.New("replacement trial is missing its receipt")
	}
	if source == "" {
		source, err = replacement.SourceExecutable()
		if err != nil {
			return err
		}
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	buildID, err := installation.CurrentBuildID()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	stageCtx, stageCancel := context.WithTimeout(ctx, 10*time.Second)
	workingImage, err := replacement.StageBuild(stageCtx, layout, executable, buildID)
	stageCancel()
	if err != nil {
		return fmt.Errorf("retain working daemon build: %w", err)
	}
	recovery, err := replacement.ReadRecovery(layout)
	if err != nil {
		return err
	}
	if recovery != nil && recovery.BuildID != buildID {
		recovery = nil
	}
	config := Config{Layout: layout, PreferredPort: port, Build: build, RestartReceipt: receipt, SourceExecutable: source, RuntimeExecutable: workingImage, Recovery: recovery}
	if trial {
		config.Recovery = nil
		if buildID != receipt.TargetBuildID {
			return errors.New("replacement build does not match its receipt")
		}
		config.StartupDeadline = receipt.DeadlineAt
		config.Candidate = true
		config.BeforeReady = func(readyCtx context.Context) error {
			return replacement.AwaitCommit(readyCtx, buildID, receipt.RestartID)
		}
	}
	var request *replacementExit
	if guardian {
		request = &replacementExit{receipt: *receipt, port: port}
	}
	for {
		if request == nil {
			err := Run(ctx, config)
			if !errors.As(err, &request) || ctx.Err() != nil {
				return err
			}
			environment, err := environmentWithRestartReceipt(os.Environ(), request.receipt)
			if err != nil {
				return err
			}
			arguments := []string{"__daemon", "--data-dir", layout.Root, "--executable", source, "--guardian", "--port", strconv.Itoa(request.port)}
			return replacement.ExecGuardian(workingImage, buildID, arguments, environment, lease)
		}
		config.BeforeReady = nil
		config.Candidate = false
		trialCtx, cancel := context.WithDeadline(ctx, request.receipt.DeadlineAt)
		pruneCtx, pruneCancel := context.WithTimeout(trialCtx, time.Second)
		if pruneErr := replacement.PruneBuilds(pruneCtx, layout, buildID, request.receipt.TargetBuildID); pruneErr != nil {
			slog.Warn("Prune retained daemon builds", "error", pruneErr)
		}
		pruneCancel()
		trialErr := attemptReplacement(trialCtx, layout, source, request, lease, stderr)
		cancel()
		if trialErr == nil {
			return nil
		}
		var unsafe *replacement.UnreapedError
		var blocked *recoveryBlockedError
		if errors.As(trialErr, &unsafe) || errors.As(trialErr, &blocked) || ctx.Err() != nil {
			return trialErr
		}
		slog.Error("Portless daemon replacement failed; restoring previous build", "event", "daemon.restart.rollback", "restart", request.receipt.RestartID, "error", trialErr)
		status := pendingRestartStatus(&request.receipt, "")
		status.Outcome = "rolled-back"
		status.Failure = "The replacement failed startup or runtime recovery. The previous daemon was restored."
		if errors.Is(trialErr, context.DeadlineExceeded) {
			status.Failure = "The replacement exceeded its readiness deadline. The previous daemon was restored."
		}
		config.Recovery = &replacement.Recovery{BuildID: buildID, Restart: *status}
		if err := replacement.WriteRecovery(layout, *config.Recovery); err != nil {
			return fmt.Errorf("quarantine failed daemon build: %w", err)
		}
		config.RestartReceipt = &request.receipt
		config.StartupDeadline = request.receipt.RecoveryDeadlineAt
		config.PreferredPort = request.port
		request = nil
	}
}

type recoveryBlockedError struct{ cause error }

// Error explains a failed state restoration without claiming the daemon recovered.
func (e *recoveryBlockedError) Error() string {
	return "previous daemon state could not be restored; refusing to restart: " + e.cause.Error()
}

// Unwrap preserves the state restoration failure.
func (e *recoveryBlockedError) Unwrap() error { return e.cause }

func attemptReplacement(ctx context.Context, layout installation.Layout, source string, request *replacementExit, lease *os.File, stderr io.Writer) error {
	image, err := replacement.StageBuild(ctx, layout, source, request.receipt.TargetBuildID)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp(layout.Root, ".daemon-replacement-")
	if err != nil {
		return err
	}
	snapshot := filepath.Join(directory, "state.db")
	if err := database.SnapshotForReplacement(ctx, layout.Database, snapshot); err != nil {
		_ = os.RemoveAll(directory)
		return err
	}
	environment, err := environmentWithRestartReceipt(os.Environ(), request.receipt)
	if err == nil {
		arguments := []string{"__daemon", "--data-dir", layout.Root, "--executable", source, "--replacement", "--port", strconv.Itoa(request.port)}
		err = replacement.Trial(ctx, replacement.TrialOptions{Executable: image, Arguments: arguments, Environment: environment, Lease: lease, Receipt: request.receipt, Stdout: os.Stdout, Stderr: stderr, BeforeCommit: func() error { return replacement.ClearRecovery(layout) }})
	}
	var unsafe *replacement.UnreapedError
	if errors.As(err, &unsafe) {
		return err
	}
	if err != nil {
		if restoreErr := database.RestoreAfterReplacement(layout.Database, snapshot); restoreErr != nil {
			return &recoveryBlockedError{cause: errors.Join(err, restoreErr)}
		}
	}
	_ = os.RemoveAll(directory)
	return err
}

// RunnerCommand runs the private service-supervisor mode owned by the daemon.
// Keeping the runner behind the daemon facade prevents the executable entry
// point from depending on runtime implementation packages.
func RunnerCommand(args []string, stderr io.Writer) int {
	return supervisor.Command(args, stderr)
}
