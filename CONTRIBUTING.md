# Contributing to Portless

For installation and everyday use, start with the [README](README.md).
Read [AGENTS.md](AGENTS.md) for product principles, package ownership, coding
conventions, and validation requirements before making changes.

## Build and test

Building Portless requires Go 1.26 or newer, Node.js 22.12 or newer, npm, and
Make. From the repository root:

```bash
make
make lint
make test
```

`make` installs locked frontend dependencies when needed, builds the embedded
React control plane, and writes `bin/portless`. `make test` type-checks and
tests both web projects, builds their production assets, and runs all Go tests.

`make lint` checks Go formatting and vet diagnostics, runs pinned Staticcheck
and actionlint versions, lints the React/TypeScript sources with Oxlint and
React Hooks rules, and checks repository shell scripts with ShellCheck.
ShellCheck must be installed locally; the remaining lint tools are installed
from their locked project or Makefile versions.

Use `make test-go`, `make test-web`, or `make test-site` for a narrower suite.
`make coverage` runs tests with coverage reporting and writes a summary, raw
profiles, and browsable HTML reports under `coverage/`. CI adds the summary to
the workflow run and retains the complete report as an artifact.

### Web changes

`portless-web/dist` is tracked because the executable embeds it. Regenerate it
with `make web`; never edit it by hand. To see a change in an already running
control plane, build the complete executable and restart the daemon from this
checkout:

```bash
make
./bin/portless daemon restart
```

Refresh the browser after the restart succeeds. If a normal restart is blocked,
inspect `./bin/portless daemon status` and follow its guidance. A forced restart
can interrupt active environments and requires explicit authorization.

The public website is separate from the embedded control plane. Use
`make site` to build it and `make test-site` to validate it. Do not track
`portless-site/dist`, `.astro`, or `node_modules`.

### End-to-end tests

Read the [E2E testing guide](docs/e2e-testing.md) before changing or running
these suites. The ordinary CLI and browser suites use compiled binaries and
isolated Portless homes:

```bash
make install-e2e-browser
make test-e2e
```

CI runs the CLI and Chromium suites as separate required jobs.

Machine-level relay suites require separate explicit authorization. They
temporarily replace the real relay and change machine networking; they are
excluded from routine validation. Do not use reset, uninstall, or commands that
stop a developer's services as incidental tests.

## Repository structure

Portless is one Go module and one distributed executable, with six product
roots:

| Product | Responsibility | Documentation |
| --- | --- | --- |
| `portless-cli` | Commands, selection, output, confirmation, completion, and browser launching. | [CLI README](portless-cli/README.md), [command reference](portless-cli/COMMANDS.md) |
| `portless-daemon` | API, control plane, discovery, state, runtimes, traffic, and embedded UI serving. | [Daemon README](portless-daemon/README.md), [OpenAPI](portless-daemon/api/openapi.yaml), [events](portless-daemon/api/events.md) |
| `portless-relay` | Machine-wide HTTP and DNS relay, installation, health, restart, and removal. | [Relay README](portless-relay/README.md) |
| `portless-web` | React control plane embedded in the executable. | [Embedded assets](portless-web/embedded-assets.md) |
| `portless-site` | Static marketing site published separately at [www.portless.run](https://www.portless.run). | [Website README](portless-site/README.md) |
| `portless-mcp` | Local stdio MCP runtime, scope and capability policy, redaction, and result limits. | [MCP README](portless-mcp/README.md) |

Package ownership and dependency direction are defined in [AGENTS.md](AGENTS.md)
and enforced by `tests/architecture`. Run `go test ./tests/architecture` after
moving packages, adding imports, or exporting declarations.

## Additional references

- [Implementation status and release gates](docs/implementation-status.md)
- [Architecture decisions](docs/architecture/decisions/)
- [Public website development](portless-site/README.md)
- [Release process](docs/releasing.md)
- [API contract](portless-daemon/api/openapi.yaml)
- [Live event contract](portless-daemon/api/events.md)
