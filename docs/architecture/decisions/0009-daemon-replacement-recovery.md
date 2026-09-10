# ADR 0009: recover failed daemon replacements

Status: accepted

A daemon that overwrites its own process image cannot recover when the new
image fails to execute, exits during startup, cannot reconcile owned runtimes,
or never becomes ready. Daemon replacement therefore uses a bounded child
trial while the previous process remains available as its guardian.

The daemon composition root coordinates the following sequence:

1. Retain a verified, private, content-addressed working executable. Preserve
   the installed invocation path separately, including package-manager symlinks.
2. Perform the existing runtime handoff audit and coalesce concurrent requests
   into one receipt. Cancel serving contexts, drain HTTP, close runtime managers
   and SQLite, and release the daemon's listeners without stopping applications.
   Re-enter the retained working image as the guardian, ending all old
   control-plane goroutines before taking the snapshot. The candidate is never
   executed in place of the guardian.
3. Under an inherited exclusive process lease, stage the exact requested build
   and create a consistent SQLite snapshot with `VACUUM INTO`. This includes
   committed WAL contents. A snapshot failure skips the trial.
4. Launch exactly one directly owned child. The child may reconcile state and
   bind listeners, but may not serve application or feature API requests yet.
   Reconciliation errors or unverifiable runtimes fail the trial. Readiness is
   proven over inherited pipes using the child PID, build, receipt, and private
   handoff protocol version. The five-second replacement deadline includes
   preparation, reconciliation, and readiness.
5. Commit only a ready child. It starts serving with a new PID and instance ID;
   application process IDs, runtime generations, and directed edge ports stay
   intact. The guardian exits. Clear a prior rejected-build record before commit.
6. On failure, kill and reap only the directly owned trial child. Restore the
   SQLite snapshot before rerunning the previous process's daemon composition.
   Never restore while the child may still be alive, or run against partially
   restored state. Recovery has an additional fifteen seconds to become ready.

The exclusive `daemon.process.lock` descriptor stays open in the guardian and
child across the entire transaction. Closing a descriptor does not explicitly
unlock the shared open-file description. Launch, reset, and uninstall checks
must consider this lease even when the ordinary instance record is absent.

`replacement` owns retained images, the lease, and bounded child trials. It
depends only on the standard library, API contracts, and standard-library
installation primitives. Both daemon composition and client-side `control`
may consume it; neither imports the other. `database` owns consistent snapshots
and restoration. The composition root supplies commit and startup callbacks;
the trial package cannot open SQLite, call the feature API, or control service
runtimes. Architecture tests enforce this direction.

API 21.0.0 receipts include `recoveryDeadlineAt`. Lifecycle protocol 5.0.0 and
public daemon status expose `lastRestart`, including `outcome` (`replaced` or
`rolled-back`) and a safe failure explanation. CLI explicit restart reports
rollback as an error with the recovered status available in JSON. The browser
reconnects and reports recovery without claiming upgrade success.

A private recovery record retains the previous image identity and rejected
build. The executable watcher and automatic CLI connection do not retry that
same failed build. Compatible clients can continue using the recovered daemon;
incompatible clients still fail closed. Explicit restart can retry, and a
different installed build is eligible automatically. The working and latest
trial images are retained; pruning removes only verified private build files.
Recovery never overwrites a Homebrew or source-checkout executable.

This is startup rollback, not a zero-gap upgrade or a post-commit health monitor.
HTTP and TCP connections can drop during the handoff; WebSockets reconnect.
Application data in managed resources is not snapshotted or rewound. If runtime
ownership becomes ambiguous independently of the candidate, the previous daemon
still reports unknown state and refuses duplicate launches. Power loss or death
of the guardian itself may require ordinary crash recovery; the transaction
does not restore snapshots speculatively on a later startup.

Compiled isolated E2E tests cover a successful upgrade, invalid executable,
candidate exit, readiness timeout, candidate schema changes followed by failed
reconciliation, continued CLI operations on the working image, and explicit
retry. Unit tests cover inherited ownership, malformed readiness, build
integrity, WAL snapshots, schema restoration, and rejection of unsafe sidecars.
