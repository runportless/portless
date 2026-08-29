# Portless Relay

`portless-relay` owns the narrow machine-wide privilege boundary that makes
Portless HTTP URLs and TCP endpoint names work without user-visible ports. It
installs and runs fixed loopback HTTP and DNS listeners, verifies their
machine-level ownership, and forwards requests to private sockets owned by the
per-user daemon.

The relay is part of the repository's single Go module and the distributed
`portless` executable. It is not a separately built application, a general
reverse proxy, or another control plane. Users manage it through `portless
setup` and the public `portless relay` commands.

## Product boundary

The relay owns:

- the privileged HTTP listener on `127.0.0.1:80`;
- the UDP and TCP DNS listeners on `127.77.0.1:1053`;
- launchd or systemd service integration and scoped resolver configuration;
- the reserved loopback address pool needed for clean TCP endpoints;
- installation receipts, helper integrity and compatibility checks;
- bounded HTTP, DNS, and system-resolver readiness probes; and
- guarded install, repair, restart, and removal behavior.

Adjacent products retain application-aware behavior:

- `portless-daemon` owns HTTP host routing, dynamic `portless.test` records,
  stable TCP endpoint allocation, source-aware proxies, traffic capture, and
  all project and environment state.
- `portless-cli` owns the public relay commands, output, confirmation, and
  administrator-elevation workflow.
- `portless-web` consumes relay status through the daemon API; the relay does
  not serve a separate UI or API.

The relay's TCP role is intentionally limited to DNS and loopback setup. TCP
application traffic does not pass through the privileged relay: after a name
resolves, the client connects directly to a daemon-owned proxy on its assigned
loopback address and service port.

## Data paths

```text
HTTP client
    |
    v
127.0.0.1:80
    |
    v
relay HTTP listener -----> private ingress.sock -----> daemon HTTP ingress

TCP client
    |
    +-- resolve *.portless.test
    |       |
    |       v
    |   system resolver -> 127.77.0.1:1053
    |       |
    |       v
    |   relay DNS listener -> private dns.sock -> daemon DNS
    |       |
    |       v
    |   assigned 127.77.0.2-127.77.0.65 address
    |
    v
daemon-owned source-aware TCP proxy -> target service
```

The HTTP relay forwards the connection byte-for-byte to the daemon. The daemon
then selects a control-plane or service route from the request host.

The DNS relay handles both UDP and TCP queries. It synthesizes fixed loopback
answers for `.localhost` names and sends dynamic `portless.test` queries to the
daemon over a length-prefixed private Unix-socket protocol. A bounded pool of
64 endpoint addresses, `127.77.0.2` through `127.77.0.65`, gives different
public and source-scoped TCP names stable identities even when they use the
same application port.

## Package map

| Path | Responsibility |
| --- | --- |
| `command.go` | Dispatch the fixed private relay runtime and lifecycle modes embedded in the shared executable. |
| `runtime` | Validate the authorized identity, bind HTTP and DNS listeners, drop privileges, and forward bounded traffic. |
| `health` | Probe the relay's HTTP listener, direct DNS listener, private daemon sockets, and host resolver paths. |
| `installation` | Install, inspect, authorize, restart, and remove platform services, resolver files, loopback resources, receipts, and the helper executable. |

The root package is composition only. Runtime data-plane behavior belongs in
`runtime`; machine lifecycle behavior belongs in `installation`; readiness
semantics belong in `health`. These dependency directions are enforced by
`tests/architecture`.

The executable's `__relay`, `__install-relay`, `__restart-relay`, and
`__uninstall-relay` modes are private service protocols. Do not invoke them
directly or treat them as public compatibility contracts.

## Installation and operation

First-run setup installs the relay as part of the normal Portless workflow:

```bash
portless setup
```

The explicit lifecycle commands are:

| Command | Behavior |
| --- | --- |
| `portless relay install` | Install or repair the relay, resolver integration, receipt, and loopback resources. The operation is idempotent. |
| `portless relay status` | Inspect installation ownership, helper integrity and compatibility, generated configuration, service state, endpoint pool, HTTP, DNS, and resolver health. |
| `portless relay restart` | Restart an installed relay only when its receipt proves that it belongs to the requesting user. There is no force mode. |
| `portless relay uninstall` | Remove the verified relay service, helper, resolver integration, receipt, and owned loopback resources. Alias: `relay remove`. |

All data-bearing commands honor the CLI's global `--json` behavior. For
example:

```bash
portless --json relay status
```

Installation and lifecycle commands may request administrator approval because
they change machine-level networking. Removing the relay does not remove
projects, environments, application runtimes, recordings, or the Portless data
directory. Running environments can continue, but their clean HTTP URLs and
TCP names are unavailable until the relay is installed again.

`portless relay uninstall --force` is a guarded recovery tool for removing
fixed artifacts when the recorded owner differs or cannot be determined. It
does not authorize Portless to remove unverified macOS loopback aliases. Check
`portless relay status` and the [command reference](../portless-cli/COMMANDS.md#relay-and-clean-endpoint-setup)
before using it.

## Platform integration

| Resource | macOS | Linux |
| --- | --- | --- |
| Service manager | launchd, `dev.portless.relay` | systemd, `portless-relay.service` |
| Installed helper | `/Library/PrivilegedHelperTools/dev.portless.relay` | `/usr/local/libexec/portless/portless-relay` |
| Service configuration | `/Library/LaunchDaemons/dev.portless.relay.plist` | `/etc/systemd/system/portless-relay.service` |
| Ownership receipt | `/var/db/portless/relay.json` | `/var/lib/portless/relay.json` |
| Resolver integration | `/etc/resolver/portless.test` and `/etc/resolver/portless.localhost` | `/etc/systemd/resolved.conf.d/portless.conf` |
| Lifecycle lock | `/var/db/portless/relay.lock` | `/var/lib/portless/relay.lock` |
| Endpoint pool | Explicit aliases on `lo0` | The kernel-routed IPv4 `127/8` loopback range |

Linux installation requires an active `systemd-resolved.service`; Portless
fails without changing the machine when that prerequisite is unavailable.
The installed systemd unit also restricts address families, filesystem access,
devices, kernel controls, and retained capabilities.

macOS requires every reserved `127.77.0.x` address to be present on `lo0`.
Portless records the exact managed addresses in the ownership receipt and
refuses to adopt or remove aliases whose ownership cannot be proven.

Other platforms do not currently provide relay installation integration.

## Privilege and ownership model

The installed service starts as root so it can bind port 80 and, on macOS,
provision loopback aliases. It also opens the fixed DNS listeners before
dropping privileges. Before serving traffic it:

1. validates absolute private socket paths ending in `ingress.sock` and
   `dns.sock`, plus a non-root user and group;
2. verifies that those exact values match the root-owned installation receipt;
3. verifies the installed helper's content identity and semantic helper
   version;
4. prepares any platform-owned loopback resources;
5. binds the fixed HTTP, DNS TCP, and DNS UDP listeners; and
6. clears supplementary groups, then drops to the installing user's group and
   user IDs before accepting traffic.

The receipt binds the platform service, owner, daemon socket paths, loopback
manifest, helper path, helper SHA-256 identity, semantic helper version, and
generated configuration path. Status also compares generated service and
resolver files with their deterministic expected contents.

Install, restart, and removal are serialized through a root-owned lifecycle
lock and use bounded execution time. Installation stages root-owned artifacts
atomically, starts the service, and commits only after HTTP, direct DNS, and
system-resolver readiness all succeed. A failed repair restores the prior
artifacts and service state.

Ownership ambiguity fails closed. Portless will not replace another user's
relay, trust a modified helper or receipt, or delete macOS aliases based only
on their address. This is why a damaged installation can require explicit
inspection instead of an automatic cleanup.

## Availability and health

Relay readiness consists of three independent, concurrent checks:

- an HTTP request through port 80 to
  `http://portless.localhost/api/v1/health`, requiring a ready daemon and a
  semantic API version;
- direct DNS queries to `127.77.0.1:1053` for the dynamic `portless.test` zone
  and a fixed `.localhost` name; and
- equivalent lookups through the operating system's default resolver.

Installation waits up to eight seconds for the complete path to become ready.
`portless relay status` reports the checks separately so a service failure,
direct DNS failure, and resolver-configuration failure remain distinguishable.

The relay is intentionally independent of daemon restarts. If HTTP cannot
reach `ingress.sock`, it returns a controlled `503 Service Unavailable` page
with a short retry interval. If a dynamic DNS request cannot reach `dns.sock`,
it returns DNS `SERVFAIL`. Listener concurrency and upstream dials are bounded
so a stopped or overloaded daemon cannot create unbounded privileged work.

## Development

Run supported workflows from the repository root:

```bash
go test ./portless-relay/...
go test ./tests/architecture
make lint
make test
git diff --check
```

Use the complete executable for manual, non-destructive inspection:

```bash
make
./bin/portless relay status
```

Changing relay runtime behavior does not update the installed helper by
building the repository alone. When a change requires existing installations
to refresh the helper, increment `runtime.HelperVersion` as a semantic
compatibility version and exercise the public install or repair workflow. The
receipt's content hash protects the installed copy; the semantic version tells
the current executable whether that copy remains compatible. Rebuilding
unrelated Portless code therefore does not require relay reinstallation.

The ordinary relay unit tests do not alter machine networking. The destructive
end-to-end suites replace the real machine relay, bind port 80 and the DNS
listener, change resolver configuration, and may provision loopback aliases.
Read the [E2E testing guide](../docs/e2e-testing.md#destructive-relay-integration)
and obtain explicit authorization before running either suite.

## Further reading

- [Repository overview and product workflow](../README.md)
- [CLI relay command reference](../portless-cli/COMMANDS.md#relay-and-clean-endpoint-setup)
- [Daemon ownership and runtime model](../portless-daemon/README.md)
- [E2E testing guide](../docs/e2e-testing.md)
- [Architecture guard tests](../tests/architecture/imports_test.go)
