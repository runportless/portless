# Automatic environment checkouts

Status: accepted

Starting two environments should not require learning Git worktree commands.
Both CLI and UI startup therefore use the daemon's shared source preparation
step before acquiring local source leases. A source conflict creates a detached
worktree from the containing repository's current commit and copies its current
filesystem contents. Dirty tracked files, tracked deletions, untracked files,
and ignored dependencies are preserved without changing the original index,
branch, or files. No branch names or commits are created for the user.

`portless-daemon/projects/worktrees` owns bounded Git and filesystem operations
and depends only on the standard library. Only `controlplane` consumes it.
Discovery remains static and read-only; neither discovery nor the CLI, web,
API server, or MCP adapter creates worktrees. Architecture guards enforce this
dependency direction. No additional HTTP endpoint or worktree-specific user
interaction is necessary. API 13.1.0 adds this startup behavior and preparation
events without changing the request or response shapes.

The control plane serializes source preparation and lease acquisition separately
from its general state lock. Overlapping source directories conflict. A
repository is copied once per new environment, preserving every bound source's
relative directory. Local provider handoffs, individual starts, and debugger
starts use the same preparation; runtime recovery only verifies existing
leases and never moves running or unverified processes.

Source relocation persists the compiled launch paths and source definitions in
one revision-checked database transaction before any service starts. It does
not rediscover topology or clear runtime generations, proxy records, or provider
bindings. Both the control plane and database reject moving an active source.
An explicit CLI context selection survives automatic relocation and may select
any environment from another checkout of the same project. Subsequent starts
use the saved paths, including after daemon replacement.

Copies use root-confined filesystem handles, independent regular files, and no
hard links. Git hooks, filesystem monitors, recursive submodule commands, and
checkout filters do not run during preparation. Internal absolute symlinks are
made relative to the copy. External symlinks are retained without being followed;
they still reference the same external dependencies. Sockets, FIFOs, and device
files are not copied. Preparation has a five-minute budget and a per-repository
limit of one million entries and 10 GiB. A missing Git executable, repository
with no commits, nested repository/submodule, copy failure, or budget exhaustion
fails the tracked start with an actionable error. A single environment still
starts without Git or any configuration file.

Successful worktrees are saved under the private installation's `worktrees`
directory. Stop, forget, and reset retain them; full uninstall refuses to erase
them until they have been moved or removed explicitly. Only a just-created,
unpublished worktree can be rolled back automatically after preparation fails.
If a later database save fails or the daemon crashes during preparation, files
are retained rather than risking loss of edits. Garbage collection of retained
worktrees is outside this change.
