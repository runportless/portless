# Reproduction bundles implementation plan

Status: proposed; not implemented.

Created: 2026-08-30.

## Goal

Make Portless the shortest path from a local failure to a portable diagnosis
that another developer or a CI job can inspect and, when all required inputs
are present, reproduce in an isolated environment.

A reproduction bundle is one versioned file with the extension
.portless. It combines a frozen public topology, source revision metadata,
sanitized effective configuration, selected traffic evidence, related
experiments, bounded logs and timeline history, integrity metadata, and
eventually portable resource snapshots and a declarative replay recipe.

The feature builds on the existing project declaration export, versioned
recording export, trace projection, service configuration, structured logs,
timeline, faults, mocks, provider bindings, and private runtime ownership
boundaries. It must not serialize the current Environment API object directly:
that object intentionally contains machine-local paths, PIDs, listener
addresses, and runtime state which do not belong in a portable artifact.

## User promise

Portless must answer four separate questions without overstating fidelity:

1. What happened?
2. Which topology, revisions, configuration, and interventions were involved?
3. What information was omitted, redacted, truncated, or could not be captured?
4. Can this bundle be run here, and if not, exactly which requirements are
   missing?

Every bundle is inspectable. A bundle is runnable only when its manifest says
so and the importing Portless installation independently verifies every source,
resource, replay, safety, and compatibility requirement.

## Delivery summary

The implementation is deliberately incremental:

| Milestone | Outcome | Public claim |
| --- | --- | --- |
| A. Thin bundle | Safe-by-default export, checksum verification, and offline CLI inspection | Portable diagnosis |
| B. Browser flow | Preview and download from a stopped recording or complete trace | Portable diagnosis |
| C. Fidelity capture | Exact source state and revisioned intervention snapshots | Reproduction-ready evidence |
| D. Isolated import | Preview-first creation of a new project/environment from a bundle | Runnable when sources are available |
| E. Replay and data | Portable resource dumps, recorded simulations, replay, comparison, and CI | Repeatable reproduction |

Milestones A and B are the first release. The UI and CLI must say
diagnostic rather than runnable until Milestone D is complete. Milestone D must
not ship before optional project declaration import and exact source
materialization have stable contracts. Full deterministic replay also depends
on traffic replay, resource snapshots, and the scenario runner.

## Terminology and ownership

- The product artifact is a **reproduction bundle**.
- The CLI command is **reproduce**.
- The daemon API resource is **reproduction-bundles**.
- The Go domain package is portless-daemon/reproduction.
- A **thin** bundle contains metadata and bounded diagnostic evidence but no
  source patch or resource data.
- A **sealed** bundle additionally contains explicitly selected source or data
  blobs. Sealed bundles are deferred until Milestone C or E, depending on the
  blob type.
- **Inspect** parses and presents a bundle without changing Portless state.
- **Verify** checks structure, limits, declared features, and every digest. It
  does not establish that the author is trusted.
- **Run** imports a verified bundle into a new isolated project/environment and
  executes only its bounded declarative recipe.

The CLI surface belongs in portless-cli/traffic because reproduction composes
recordings, traces, faults, mocks, and replay. It remains a top-level command in
the Traffic help group. The package must communicate with the daemon only
through the typed API client.

The daemon owns collection and export in portless-daemon/reproduction.
A nested portless-daemon/reproduction/format package owns the pure,
standard-library archive reader, writer, limits, and structural validation. It
may depend only on the Go standard library and API contract types. This pure
package is the one narrow daemon-domain package the CLI may import for offline
inspect and verify; an architecture test must enforce that it cannot reach the
database, control plane, runtime, proxy, network, or host process APIs.

## Product decisions

### One inspectable standard archive

The .portless file is a ZIP64 archive with the media type:

~~~text
application/vnd.portless.reproduction+zip
~~~

ZIP is selected because Go supports it in the standard library, developers can
inspect it without Portless, browsers can download it directly, and optional
large blobs can be added later without inventing a container format.

The archive is deterministic for a fixed logical input:

- entries are written in lexical path order;
- JSON uses stable field order, UTF-8, UTC RFC3339Nano timestamps, and a final
  newline;
- JSON Lines entries are chronological and use one stable object shape;
- entry modes are 0600, symlinks are never emitted, and directory entries are
  omitted;
- entry modification times use the manifest creation time truncated to ZIP's
  supported precision;
- compression settings are fixed by the format implementation; and
- integrity entries are generated after every content entry is finalized.

Creation time and the producer build are intentional inputs, so two separate
exports are not expected to have the same bundle ID.

### Thin is the default

The first release excludes these values unless the user explicitly includes
them:

- HTTP request and response bodies;
- decoded database, cache, or messaging content;
- repeated header values other than a small non-sensitive allowlist;
- query values;
- service log messages;
- remote target URLs;
- Git patches and untracked files;
- resource data; and
- transform source code.

Thin bundles retain the diagnostic shape: methods, normalized paths, status,
duration, byte counts, protocols, service and edge identities, trace
correlation, operation names, outcomes, source revisions, safe configuration
classifications, and the existence of omitted data.

Explicit payload or log inclusion does not make data safe. It changes the
safety report to review-required and prints a warning before the file is
written.

### Inspection never starts the daemon

The following commands operate only on the supplied local file and therefore
work when the daemon is stopped or incompatible:

~~~text
portless reproduce inspect checkout-timeout.portless
portless reproduce verify checkout-timeout.portless
~~~

They must not read project checkouts, resolve the current environment, open a
browser, or perform network access.

### Import never trusts executable content

A bundle is untrusted input even when all checksums match. Checksums establish
integrity, not author identity.

The first runnable recipe is declarative. It may select a new environment,
restore a plugin-owned snapshot, enable a captured fault or mock, issue a
bounded HTTP request, replay a supported recorded request, assert a response or
trace property, and disable what it enabled. It cannot contain shell commands,
arbitrary URLs, executable hooks, JavaScript, TypeScript, environment-variable
expansion, or host filesystem reads.

If future scenarios allow approved commands, importing a bundle must still
leave those steps disabled until the user inspects and separately authorizes
them. A general scenario permission must never silently authorize commands
carried by an untrusted bundle.

### Bundle topology is evidence, not executable configuration

Service commands, environment values, working directories, image hints, and
health checks in a bundle describe the exporting environment. Import never
executes those values directly.

For each user-supplied checkout, the importing build runs its current trusted
static discovery, then compares the discovered source model with the bundle.
Only the locally discovered command and configuration may start. A material
topology, command, resource, or health-check difference fails with a structured
diff before any runtime starts. Likewise, managed resource plans come from the
current built-in plugin rather than an image or command embedded in the
archive.

This rule is what prevents a checksum-valid malicious bundle from becoming a
command-execution format. A later reviewed source patch is application code and
therefore requires a separate explicit source-content authorization before
materialization or start.

### Run always targets new state

The first run implementation creates a new project and environment. It never:

- changes an existing checkout;
- applies a patch to an existing worktree;
- restores into an existing volume;
- reuses a remote credential from the exporting machine;
- overwrites an existing project or environment;
- enables production or unknown remote providers; or
- sends a captured mutating request to a remote target.

Name conflicts return safe suggestions. Cleanup uses existing preview-first
project, environment, and volume ownership rules.

### Fidelity is explicit and multidimensional

The manifest records one status for each dimension:

~~~text
exact | partial | reference | unavailable
~~~

Dimensions are topology, sources, configuration, traffic, interventions, logs,
data, and replay. The overall bundle never reduces those values to a
misleading single exact boolean.

Examples:

- a clean Git checkout at an exact commit is exact source metadata, while a
  dirty checkout without a patch is partial;
- a PostgreSQL snapshot referenced by digest but not embedded is reference;
- logs outside retention are partial;
- a changed mock definition without a capture-time revision is partial; and
- a bundle with no replay recipe has replay unavailable and is not runnable.

## User workflows

### Save from a recording

The preferred first-release workflow uses a stopped bounded recording:

~~~bash
portless reproduce save checkout-timeout \
  --recording checkout-timeout
~~~

Optional flags:

~~~text
--trace <number>             focus the report on one trace inside the recording
--include-payloads           include payloads already retained by the recording
--include-logs               include bounded service log messages around evidence
--log-window <duration>      default 30s before and after selected evidence
--description <text>         human explanation stored in the manifest
-o, --output <path>          default <name>.portless
--force                      replace an existing output file
--dry-run                    print the export plan without creating a file
~~~

The recording must be stopped. Payload inclusion cannot recover payloads that
the original recording did not retain. The API reports this as unavailable
rather than silently exporting empty bodies.

### Save from a live trace

For a failure which was not recorded:

~~~bash
portless reproduce save checkout-timeout --trace 142
~~~

The trace must be complete and non-provisional. The export plan freezes its
current first and last exchange sequences and timestamps. Export returns a
revision conflict if the trace has left the bounded live window or changed
before assembly. The CLI reruns the plan only after telling the user; it does
not silently select a different trace.

### Human save output

Before writing the archive, human output summarizes:

- bundle name and destination;
- source environment and evidence selection;
- service, edge, exchange, trace, log, and timeline counts;
- source revision and dirty-state coverage;
- payload, log, remote-target, patch, and snapshot inclusion;
- redaction counts by category;
- every truncation or unavailable input;
- estimated uncompressed and archive sizes; and
- whether the resulting artifact is diagnostic or runnable.

Because a thin export is non-destructive, it does not prompt for confirmation.
Explicit sensitive inclusion flags are the consent boundary. JSON output emits
the plan for --dry-run and a concise action result with path, bundle ID, size,
and safety state after save. Binary archive bytes are never mixed with JSON
output, and save does not support stdout in the first release.

### Inspect and verify

Human inspect output has these sections:

1. Summary and bundle identity.
2. Producer and format compatibility.
3. Origin project/environment and selected failure.
4. Sources and revisions.
5. Topology and provider bindings.
6. Evidence counts and time range.
7. Experiments and capture-time fidelity.
8. Omitted, redacted, truncated, and sensitive content.
9. Requirements and runnable blockers.
10. Integrity result.

With --json, inspect emits one ReproductionInspection object. Verify emits a
smaller result suitable for CI and exits nonzero for structural, limit,
feature, or digest failures.

Inspect verifies before presenting content. A separate --no-verify bypass is
not provided.

### Browser export

Milestone B adds:

- **Create reproduction bundle** to a stopped recording drawer;
- the same action to a complete trace drawer;
- a preview dialog showing the same plan as the CLI;
- unchecked **Include captured payloads** and **Include service logs**
  controls with persistent warning copy;
- a disabled download action until preview succeeds; and
- a browser download named after the validated bundle name.

Bundles are files, not daemon-retained artifacts, so the first UI has no bundle
list and no new SSE topic. The browser revokes object URLs after download and
does not retain archive bytes in React state after completion.

### Isolated run

Milestone D adds:

~~~bash
portless reproduce run checkout-timeout.portless
~~~

The first invocation is preview-only and reports proposed public names, source
requirements, resource changes, missing secrets, remote bindings, recipe
steps, expected results, and cleanup ownership. Execution requires --yes:

~~~bash
portless reproduce run checkout-timeout.portless \
  --project store-checkout-timeout \
  --environment local \
  --source checkout=../checkout \
  --source orders=../orders \
  --yes
~~~

Run verifies each supplied checkout against the required Git revision before
creating state. A mismatch fails with exact remediation. Source fetch and
worktree creation are separate later options; the first version never clones,
fetches, resets, checks out, or patches a repository.

## Archive layout

The first format uses these paths:

~~~text
manifest.json
summary.md
topology/project.json
topology/environment.json
topology/bindings.json
sources/<source>/source.json
configuration/services/<service>.json
evidence/selection.json
evidence/recording.json
evidence/exchanges.jsonl
evidence/traces.jsonl
evidence/logs/<service>.jsonl
evidence/timeline.jsonl
experiments/faults.json
experiments/mocks/<profile>.json
safety/redactions.json
safety/requirements.json
integrity/checksums.sha256
~~~

summary.md is generated only from the already sanitized portable DTOs. Inspect
does not print or render arbitrary Markdown from an untrusted archive.

Later optional paths are reserved:

~~~text
sources/<source>/working-tree.patch
data/index.json
blobs/sha256/<digest>
replay/recipe.json
replay/expected.json
experiments/transforms/<name>.json
signatures/<name>.json
~~~

Reserved paths do not imply support. A manifest feature list identifies which
optional semantics are required to inspect or run the bundle.

Public names are converted through one archive-component encoder even though
current name validation is restrictive. Writers never concatenate unchecked
names into archive paths. Readers compare encoded paths with the public name
declared inside each entry.

### Manifest

The root manifest is a versioned BundleManifest with this logical shape:

~~~json
{
  "formatVersion": "1.0.0",
  "kind": "portless.reproduction",
  "name": "checkout-timeout",
  "description": "Checkout fails when inventory exceeds its timeout",
  "createdAt": "2026-08-30T15:04:05Z",
  "producer": {
    "version": "0.1.0",
    "buildId": "distribution-build-id",
    "apiVersion": "12.9.0"
  },
  "origin": {
    "project": "store",
    "environment": "local",
    "projectRevision": 4,
    "environmentRevision": 12
  },
  "focus": {
    "kind": "recording",
    "recording": "checkout-timeout",
    "trace": 142
  },
  "profile": "thin",
  "timeRange": {
    "startedAt": "2026-08-30T15:00:00Z",
    "completedAt": "2026-08-30T15:00:03Z"
  },
  "fidelity": {
    "topology": "exact",
    "sources": "partial",
    "configuration": "exact",
    "traffic": "exact",
    "interventions": "partial",
    "logs": "unavailable",
    "data": "unavailable",
    "replay": "unavailable"
  },
  "features": {
    "required": ["traffic.http", "traffic.tcp"],
    "optional": ["logs"]
  },
  "safety": {
    "reviewRequired": false,
    "reasons": []
  },
  "runnable": {
    "value": false,
    "blockers": ["No replay recipe", "No resource snapshot"]
  }
}
~~~

The example version numbers are illustrative. Implementation increments the
then-current daemon API minor version and records the actual producer values.

Format version and daemon API version are independent. Format compatibility is
based on the bundle major version and declared required features, never on an
exact producer build match.

Known fields have strict types and bounds. Optional future metadata belongs in
an explicit extensions object whose keys are tied to declared optional
features. Readers do not silently accept arbitrary new semantics merely because
the format minor version is newer.

### Project and environment entries

topology/project.json embeds the current versioned portable project
declaration rather than a raw ProjectModel. The bundle manifest records that
nested schema version.

topology/environment.json is a new bundle-specific DTO containing:

- public project and environment names;
- primary service;
- public service definitions;
- directed connections and application protocol classifications;
- provider kinds and public source names;
- resource plugin type and selected engine version;
- remote classification and write policy;
- launch mode only when it affects the diagnosis; and
- configuration issues relevant at capture time.

It excludes:

- private project or environment keys;
- source paths;
- dashboard and endpoint addresses;
- PIDs, process groups, supervisor sockets, private run keys, and owner IDs;
- public or private listener IPs and ports;
- generated credentials;
- daemon instance identity; and
- runtime/container ownership labels.

bindings.json carries sanitized provider choices separately so future import
can apply stricter policies without decoding runtime state.

### Source entries

Each source entry records:

- public source name;
- VCS kind;
- repository root identity without its absolute path;
- exact commit object ID when available;
- branch or detached state as informational metadata;
- clean, tracked-dirty, untracked, or unknown worktree state;
- submodule requirements without local paths;
- a sanitized remote hint only when explicitly included;
- discovered services owned by the source; and
- source fidelity plus reasons.

The daemon invokes Git directly with an argument vector, a bounded context, a
scrubbed output capture, and no shell. It never executes hooks or project code.
Remote URLs have userinfo, query, and fragment removed. Thin export omits the
remote by default.

Non-Git directories remain valid but receive source fidelity unavailable for
exact materialization. The first release does not hash an entire source tree
as a substitute for a revision.

Working-tree patches are not in Milestone A. When added:

- inclusion is explicit per source;
- Git emits a full-index binary patch without invoking a shell;
- untracked files require a separate reviewed content manifest;
- patch bytes are never automatically described as sanitized;
- the safety report always requires review;
- import applies only to a new worktree after git apply --check; and
- any mismatch aborts before a service is started.

### Configuration entries

One service configuration entry contains a source-relative working directory,
tokenized command, health check, port-environment name, and effective
environment values with provenance and classification.

Secret values become requirements such as PAYMENT_TOKEN rather than values.
Absolute paths are translated to a validated source-relative path or omitted
with partial fidelity. Known generated provider secrets are re-redacted across
every textual entry even if an earlier boundary already redacted them.

### Evidence entries

selection.json freezes:

- recording name or trace number;
- first and last exchange sequence;
- evidence start and completion time;
- recording revision/state and capture policy;
- log and timeline cutoffs;
- requested inclusion flags; and
- the export-plan fingerprint.

exchanges.jsonl is ascending by sequence. The default projection excludes
payloads and risky values but retains omission and truncation metadata.
traces.jsonl contains complete stable trace projections derived from the frozen
exchange set rather than references into the daemon's live window.

For a recording, every retained exchange is primary evidence. Supplementary
logs and timeline entries may be bounded; their entries and the manifest state
whether older or newer content was omitted. Primary traffic evidence is never
silently shortened to fit an archive limit. Export fails with remediation to
choose a narrower recording or omit payloads.

### Experiments

The bundle includes only faults and mock profiles referenced by selected
evidence, plus an optional explicitly selected saved definition. Definitions
are stored disabled regardless of their captured active state. The replay
recipe records when they would be enabled.

Until capture-time intervention revisioning exists, the manifest reports
intervention fidelity partial when the current definition could have changed
after the selected exchange. Milestone C resolves this with exact immutable
recording snapshots described below.

### Safety and requirements

redactions.json contains category, field class, entry path, and count. It never
contains the removed value or a reversible hash of that value.

requirements.json lists:

- required Portless format features;
- required source commits and whether a clean checkout is sufficient;
- missing secret names without values;
- resource plugin types and compatible engine versions;
- required snapshot digests;
- remote provider rebind requirements;
- replay capabilities; and
- explicit reasons the bundle cannot currently run.

### Integrity

integrity/checksums.sha256 contains one sorted SHA-256 line for every other
archive entry, including manifest.json, and excludes itself. The bundle ID is
the SHA-256 digest of the exact checksum file bytes.

Verification rejects:

- a missing, extra, duplicate, or differently cased entry;
- a checksum mismatch;
- an entry not declared by the format or feature list;
- a required feature unknown to this build;
- malformed or non-canonical paths;
- absolute paths, parent traversal, backslashes, NULs, or empty components;
- symlinks, devices, or executable file modes;
- an unsupported format major version; or
- any configured compressed, uncompressed, entry-count, nesting, or JSON
  depth limit.

A valid checksum does not mark a bundle trusted. Optional signatures are a
separate future feature and must identify exactly which checksum file was
signed.

## Limits

Milestone A defines constants in the format package and publishes them in
format documentation:

- 4,096 entries;
- 1 MiB manifest;
- 32 MiB per JSON or JSON Lines entry;
- 128 MiB compressed thin archive;
- 256 MiB total uncompressed thin content;
- 64 MiB maximum compression expansion for any individual entry;
- 255 UTF-8 bytes per archive path;
- 100,000 primary exchanges, subject to the archive byte limit;
- 10,000 supplementary log lines;
- 1,000 supplementary timeline events; and
- bounded JSON nesting and string lengths in all untrusted manifest fields.

The reader checks compressed size before allocation, counts bytes while
decompressing, and stops at the first exceeded limit. It never relies only on
ZIP directory metadata.

Sealed snapshot limits are designed separately because valid database exports
can exceed thin limits. Raising limits is a format capability, not an
environment variable or hidden flag.

## Safety and redaction model

### Default projection

By default:

- HTTP header names are retained but values are retained only for an explicit
  non-sensitive allowlist;
- query names are retained and values become an omitted marker;
- HTTP and decoded TCP content is removed while byte counts, content types,
  operation names, outcomes, and truncation state remain;
- log metadata may be included, but log messages require --include-logs;
- remote provider classification and write policy remain, but target URL does
  not;
- trace and span identifiers remain because they are needed for correlation;
  and
- application paths remain because they are essential evidence, but the safety
  report warns that path segments may contain application data.

The bundle sanitizer re-applies current fixed credential-header and protocol
authentication redaction. It also replaces every known generated provider
secret using longest-value-first matching across textual entries.

### Explicit sensitive content

--include-payloads and --include-logs:

- reject payload inclusion when the original capture did not retain payloads;
- include whatever log generations remain in the requested window and report
  partial or unavailable fidelity rather than inventing missing lines;
- set safety.reviewRequired;
- add exact reasons and affected entry paths;
- cause the CLI and browser to show a warning before download; and
- remain bounded by the original capture policy and bundle limits.

Portless must not claim it can identify every secret in arbitrary application
bodies, SQL, Redis values, messaging payloads, URLs, or logs.

### Remote targets

Thin bundles never contain remote URLs by default. A later
--include-remote-targets option may include a normalized URL after stripping
userinfo, query, and fragment, but it always requires review.

Import converts every remote provider to an unresolved requirement. The user
must explicitly rebind it. Production and unknown classifications remain
unsupported for bundle run. QA and development targets default to read-only,
and replay of a mutating remote request is rejected in the first release.

### Untrusted archive handling

Inspection and import:

- open the file read-only;
- never extract the archive wholesale;
- stream and validate one expected entry at a time;
- never preserve archive permissions or timestamps onto the host;
- write optional blobs only under a newly created private staging directory;
- use digest-derived filenames rather than archive-provided filenames;
- fsync and atomically publish staged state where persistence matters;
- remove incomplete staging on cancellation or failure; and
- log only bundle ID, public name, sizes, and error categories.

Error messages never echo arbitrary embedded content.

CLI presentation replaces control characters, ANSI escapes, bidi controls, and
invalid Unicode with visible escaped forms before writing untrusted names or
descriptions to a terminal. Browser presentation uses React text nodes only;
it never inserts archive Markdown or HTML through an unsafe rendering path.

## Freeze and consistency semantics

Export is a two-step typed API flow:

1. Plan calculates the exact selection, cutoffs, safety state, and fingerprint.
2. Export repeats validation and assembles the archive only if the fingerprint
   still matches.

The plan fingerprint covers:

- project and environment revisions;
- recording state and capture limits, or live trace first/last sequence;
- selected evidence sequence bounds;
- source binding scan timestamps;
- exact inspected Git commit, dirty-state, and submodule-requirement digests;
- provider binding modification timestamps;
- referenced fault and mock revisions;
- log/timeline cutoff values; and
- inclusion options.

The fingerprint is a public-data digest, not an authorization token. The
authenticated export request supplies it as expectedPlanFingerprint.

Database evidence is read through one read transaction added in
portless-daemon/database/reproductions.go. The database returns a frozen DTO;
it does not build archives or import the reproduction package. Live trace
selection is copied under the traffic store lock before the database snapshot.
If the two snapshots cannot satisfy the planned bounds, export returns 409.

Logs live outside SQLite. The log reader gains an explicit inclusive time
window and upper count bound. It reads only generations already retained when
the plan cutoff was calculated. Rotation during export is reported as partial
logs rather than retried against a different window.

The daemon builds the complete archive in a private file under
PORTLESS_HOME/tmp before sending HTTP headers. Cancellation closes and removes
the file. Successful transfer also removes it. No bundle row or retained copy
is created in Milestones A and B.

## API contract

Add portless-daemon/api/contract/reproductions.go with bundle-plan API types.
Do not expose archive-internal types through ordinary JSON endpoints unless
they are also genuinely part of the daemon wire contract.

Principal request:

~~~text
ReproductionSelection
  name
  description
  evidenceKind: recording | trace
  recording?
  focusTrace?
  trace?
  includePayloads
  includeLogs
  logWindow
~~~

Principal responses:

~~~text
ReproductionPlan
  project
  environment
  selection
  contents and counts
  source coverage
  fidelity
  safety
  requirements
  limits
  estimatedBytes
  planFingerprint

ReproductionExportResult
  name
  filename
  bundleID
  compressedBytes
  uncompressedBytes
  safety
  fidelity
  runnable

ReproductionInspection
  manifest summary
  integrity result
  contents
  fidelity
  safety
  requirements
  runnable blockers
~~~

Milestone A endpoints:

~~~text
POST /api/v1/environments/{projectName}/{environmentName}/reproduction-bundles/plan
POST /api/v1/environments/{projectName}/{environmentName}/reproduction-bundles/export
~~~

Plan consumes and returns JSON. Export consumes JSON containing the same
selection and expectedPlanFingerprint, then returns the bundle media type with
Content-Length, a sanitized Content-Disposition filename, and an ETag based on
the bundle ID.

Structured errors include:

- reproduction_recording_active;
- reproduction_trace_provisional;
- reproduction_evidence_changed;
- reproduction_source_unavailable;
- reproduction_sensitive_data_unavailable;
- reproduction_limit_exceeded; and
- reproduction_export_failed.

Each error includes public subjects and actionable remediation without private
paths or ownership identifiers.

The API semantic minor version changes with the new endpoints. OpenAPI defines
the JSON schemas and binary response. Browser POSTs use the existing
session/CSRF boundary. events.md states explicitly that bundle plan/export
creates no SSE topic. Successful assembly adds one durable
reproduction.bundle.created timeline event with the summary
Bundle assembled for export. It contains only bundle name, bundle ID, evidence
kind/name, counts, inclusion flags, actor, and safety state. It never claims
that the browser or CLI completed its local save, and it never stores the
client output path or bundle contents.

### Typed API client

Add:

- PlanReproduction for the JSON plan;
- ExportReproductionTo for a specialized bounded binary download into an
  io.Writer; and
- later, PlanReproductionImport and ImportReproduction.

ExportReproductionTo owns request creation internally, validates status and
content type, decodes structured JSON errors before streaming, enforces the
compressed response limit, and returns response metadata. It does not expose a
raw HTTP transport method.

The CLI writes to a 0600 temporary sibling file, verifies the completed
archive, fsyncs it, and atomically renames it to the requested output. --force
replaces only the exact validated destination after successful download; a
failed export never damages the existing file.

## Daemon implementation

### Package map

~~~text
portless-daemon/reproduction/
  assembly.go       bundle collection and entry construction
  plan.go           selection freezing, counts, fidelity, and requirements
  sanitize.go       portable DTO mapping and second-pass redaction
  sources.go        bounded Git metadata inspection
  summary.go        generated summary.md
  format/
    manifest.go     archive-internal versioned types
    limits.go       reader and writer limits
    paths.go        canonical path validation and name encoding
    writer.go       deterministic archive writer and checksums
    reader.go       streaming verify and inspection
    testdata/       valid and malicious golden archives

portless-daemon/controlplane/reproductions.go
portless-daemon/database/reproductions.go
portless-daemon/api/contract/reproductions.go
portless-daemon/api/client/reproductions.go
portless-daemon/api/server/reproductions.go
~~~

controlplane coordinates authorized project/environment access, traffic-store
freezing, database snapshot reads, log reads, source inspection, timeline
audit, and the reproduction assembler. The reproduction package does not own
runtime lifecycle or query SQLite directly.

### Assembly sequence

1. Validate public names and selection.
2. Resolve the environment and verify project/environment revisions.
3. Freeze the recording or complete trace bounds.
4. Read the database reproduction snapshot.
5. Read bounded logs through the frozen cutoff.
6. Inspect each source with a shared deadline and bounded parallelism.
7. Map all data into portable DTOs.
8. Apply the default projection or explicit inclusion policy.
9. Re-redact known secrets across text fields.
10. Calculate fidelity, safety, requirements, and runnable blockers.
11. Write sorted archive entries and checksums to a private temporary file.
12. Re-open the result with the same format reader and verify it.
13. Calculate final bundle ID and size metadata.
14. Record the safe bundle-assembled timeline audit.
15. Return the completed file to the server for download.

No response bytes are sent before step 12 succeeds.

### Persistence

Milestones A and B add no bundle tables. Add only the read-side query needed to
freeze export data.

Milestone C adds capture-time intervention history:

~~~text
recording_interventions
  environment_key
  recording_name
  kind
  public_name
  revision
  definition_digest
  definition_json
  first_sequence
  primary key(environment_key, recording_name, kind, public_name, revision)
~~~

Traffic exchanges gain a stable list of AppliedIntervention references with
kind, public name, revision, and definition digest. Fault rules already have a
revision. Mock profiles gain a monotonic revision which changes whenever the
profile or one of its routes changes.

The proxy carries the exact safe intervention snapshot alongside its internal
capture envelope. When an exchange is persisted to an active recording, the
database inserts the traffic event and any previously unseen intervention
snapshot in the same transaction. The public traffic API exposes only the
references. Bundle export reads the immutable snapshots. This work should be
coordinated with traffic transforms so all fault, mock, and transform
interventions use one reference model rather than three incompatible fields.

Milestone D adds a retained import record owned by the new environment:

~~~text
reproduction_imports
  environment_key
  bundle_id
  format_version
  state
  manifest_json
  safety_json
  staged_bytes
  created_at
  completed_at
~~~

Optional staged blobs live under the environment's private data root and are
included in storage diagnostics, reset, project forget, and uninstall cleanup.
Public APIs never expose that path.

## CLI implementation

Add reproduction command construction and behavior to portless-cli/traffic:

~~~text
command_tree.go
reproductions.go
reproduction_output.go
reproduction_files.go
reproduction_test.go
~~~

The command tree becomes:

~~~text
portless reproduce
├── save <name>
├── inspect <file>
├── verify <file>
└── run <file>      # Milestone D
~~~

An incomplete reproduce command displays help without starting the daemon.
save resolves the current environment normally; inspect and verify do not.
Shell completion supplies recording names and trace numbers for save flags and
uses ordinary file completion only for inspect, verify, and run.

Human and JSON behavior belongs beside the commands. Offline file reading uses
the pure format package and never the daemon API. The CLI composition root only
mounts the new command and receives no feature logic.

Update command-contract tests, grouped help tests, portless-cli/COMMANDS.md,
and completion coverage.

## Web implementation

Add:

~~~text
portless-web/src/api/contracts/reproductions.ts
portless-web/src/features/environment/reproductions/
  CreateReproductionDialog.tsx
  ReproductionPlanSummary.tsx
  reproductionDownload.ts
  reproductionPresentation.ts
  *.test.tsx
~~~

The dialog:

- inherits the recording or trace identity from its launch point;
- accepts bundle name and description;
- shows payload and log inclusion unchecked;
- loads a plan whenever selection changes, with stale responses ignored;
- presents fidelity, review reasons, omissions, and limits before download;
- disables mutually exclusive controls and download while planning/exporting;
- renders failures through the shared structured error component;
- keeps keyboard focus contained and returns focus to the invoking action; and
- revokes any temporary browser URL after download or close.

Do not add a permanent environment Reproductions tab while bundles are not
retained. Add command-palette actions only when they can preserve the selected
recording or trace context.

After web source changes, regenerate tracked dist assets with make web. Build
the full executable and restart the normal daemon before handing off a locally
visible UI implementation, following the repository's existing restart safety
rules.

## Milestone C: source and intervention fidelity

This milestone makes diagnostic bundles suitable inputs for later execution.

### Source fidelity

Implement:

- exact Git commit and clean/dirty state;
- submodule commit requirements;
- optional sanitized remote hints;
- explicit reviewed patch inclusion;
- source-relative service and working directories; and
- import verification against user-supplied checkout paths.

Do not automatically clone or modify a source. A later source materializer may
create new worktrees only after a separate preview and with paths chosen by the
user.

### Intervention fidelity

Implement AppliedIntervention and recording_interventions before calling
captured faults or mocks exact. Bundle inspection must distinguish:

- observed result only;
- current definition matched by digest;
- immutable capture-time definition; and
- missing definition.

All imported definitions remain disabled outside the replay step.

### Sealed source profile

Add profile sealed-source only after:

- patch and untracked-file inventory limits exist;
- binary patch handling is tested;
- safety review identifies every included source path;
- import can materialize a new worktree without touching an existing checkout;
  and
- malicious patch and symlink cases have regression coverage.

## Milestone D: isolated import and run

### Import plan

Run starts by inspecting locally, then sends a bounded import-plan request with
the bundle and proposed public names/source mappings. The daemon independently
re-verifies the archive.

Milestone D adds:

~~~text
POST /api/v1/reproduction-bundle-imports/plan
POST /api/v1/reproduction-bundle-imports
~~~

Both accept bounded multipart input with exactly one bundle part and one JSON
options part. Options contain proposed public names, source-name-to-path
mappings, secret requirement names, and the expected plan fingerprint; source
paths never enter the bundle. The server rejects duplicate parts, unexpected
fields, unknown content types, and any body beyond the configured import
limit. It stages the archive under a new private temporary directory rather
than asking the daemon to open a client path.

Plan validates and deletes staging without mutation. Execute independently
re-verifies the re-uploaded bundle, requires the expected bundle ID and plan
fingerprint, completes all static discovery/comparison checks before mutation,
and returns the newly created environment-scoped operation. The browser may
use the same endpoints after a local file picker; the CLI continues to perform
its first structural inspection offline.

The plan reports:

- target project and environment;
- name conflicts and suggestions;
- source revision matches;
- commands which will eventually run as normal local providers;
- containers and volumes which will be created;
- missing resource plugins or engine versions;
- unresolved secret names;
- remote providers converted to unresolved bindings;
- experiment definitions to import disabled;
- replay steps;
- expected comparisons;
- cleanup ownership; and
- a fingerprint covering the complete plan.

### Import execution

Execution:

1. Re-verifies the bundle and plan fingerprint.
2. Runs current trusted static discovery over each supplied source, compares it
   with the portable declaration, and aborts on a material difference.
3. Creates a new stopped project/environment atomically from the newly
   discovered model and supplied source paths.
4. Copies only validated optional blobs into the new private environment root.
5. Imports disabled mocks and faults. Transform source remains excluded until
   its separate code capability is explicitly designed.
6. Creates owned empty resource volumes or restores compatible snapshots.
7. Creates an environment-scoped durable import operation.
8. Starts the environment only after all static requirements pass, using
   locally discovered commands rather than bundle commands.
9. Runs the declarative replay recipe.
10. Captures a new bounded comparison recording.
11. Stores the result in the operation and timeline.

If failure occurs before any runtime starts, roll back the new project and
environment. After runtime or volume creation, perform ordinary ownership-
verified cleanup. If cleanup cannot be proven complete, retain the failed
environment with exact remediation instead of erasing evidence or guessing.

### Imported remote bindings

Remote bindings never activate automatically. The new environment receives an
unresolved provider requirement. The user may later bind a development or QA
target through the existing provider flow. Bundle replay remains local-only in
the first run release.

## Milestone E: resource data, replay, and CI

### Resource snapshots

Extend the resource plugin contract with optional bounded snapshot
capabilities. A plugin snapshot describes:

- plugin and engine version;
- logical resource service;
- schema or format version;
- consistency method;
- creation time;
- uncompressed size and digest;
- compatibility range;
- whether credentials or application secrets may be embedded; and
- export, inspect, and restore behavior.

PostgreSQL uses a logical pg_dump-style export rather than a raw volume. MySQL,
Valkey, and future resources use their own consistent portable mechanisms.
Snapshot creation must complete before bundle assembly and restore only into a
new owned volume.

Opaque resource blobs always require review. The manifest never labels them
sanitized merely because Portless cannot inspect them.

### Recorded dependency simulation

Traffic replay supplies deterministic temporary simulations for captured
downstream HTTP responses. The simulation is edge-scoped, bounded to selected
request matchers, disabled outside the recipe, and never becomes a target-only
shortcut.

When a dependency cannot be simulated, requirements.json explains whether a
local checkout, mock, resource snapshot, or explicit remote rebind is needed.

### Replay and comparison

The bounded recipe may:

1. start the isolated environment;
2. restore data;
3. enable named captured interventions;
4. start a comparison recording;
5. send the captured root HTTP request locally;
6. wait for completion under a fixed deadline;
7. compare status, selected headers, body shape or digest, error class,
   latency budget, and trace shape;
8. disable interventions; and
9. leave the environment running for inspection unless --ephemeral was
   explicitly selected.

Comparison distinguishes exact, equivalent, different, and not-comparable.
Timing is a range, not byte-for-byte determinism.

Replay addresses the new environment through logical service and edge
identities. It discards captured Host and absolute URL routing, rebuilds the
target from the new public environment identity, and generates fresh trace
context while retaining an explicit comparison link to the captured trace.

### CI

Add:

~~~bash
portless reproduce verify checkout-timeout.portless --json
portless reproduce run checkout-timeout.portless --ci --json
~~~

CI mode:

- never opens a browser or prompts;
- requires all source mappings and secret providers explicitly;
- rejects review-required content unless an explicit policy allows it;
- emits a machine-readable result and stable exit classification;
- writes a fresh comparison bundle on failure when requested; and
- cleans up only resources created by that run.

CI support must not introduce a hosted account requirement.

## MCP boundary

Do not add MCP reproduction tools in Milestones A or B. Bundle creation can
export sensitive traffic and filesystem artifacts, while inspect accepts an
untrusted arbitrary file. Existing MCP traffic capabilities are not sufficient
authorization for either.

A later read-only tool may summarize an already verified bundle only if MCP
receives a separately named reproduction capability and a path constrained to
the immutable server startup scope. MCP does not receive run, source
materialization, remote rebind, snapshot restore, or arbitrary bundle export
in the initial design.

## Testing

### Format unit tests

Create golden valid bundles for:

- minimal thin HTTP recording;
- HTTP and decoded TCP trace;
- partial source fidelity;
- payload/log review-required state;
- unknown optional feature; and
- supported unknown minor format fields.

Create malicious fixtures for:

- absolute and parent-traversal paths;
- backslashes, NULs, empty components, and Unicode path ambiguity;
- duplicate and case-colliding entries;
- symlink and executable modes;
- missing, extra, and mismatched checksum entries;
- unsupported major version;
- unknown required feature;
- oversized manifest, entry, entry count, and aggregate content;
- forged ZIP sizes and compression bombs;
- deeply nested or huge JSON values;
- malformed UTF-8 and JSON Lines; and
- a valid archive whose internal public name disagrees with its path.

Test deterministic bytes for a fixed manifest time and input. Add fuzz targets
for canonical paths, manifest decoding, checksum parsing, and bounded ZIP
inspection with the malicious fixtures as seeds.

### Sanitizer tests

Cover:

- every fixed credential header;
- repeated header values;
- sensitive query-name values;
- URL userinfo/query/fragment removal;
- generated provider secret replacement with overlapping values;
- HTTP and decoded TCP payload omission;
- redacted authentication protocol fields;
- source path and working-directory relativization;
- remote provider requirements;
- log review-required behavior;
- redaction reports which never contain originals; and
- no private keys, PIDs, runtime ports, sockets, ownership labels, or daemon
  tokens in any archive entry.

### Daemon tests

Cover:

- stopped recording and complete trace planning;
- active recording and provisional trace rejection;
- project/environment revision conflicts;
- evidence leaving the live window between plan and export;
- consistent database cutoffs under concurrent mutations;
- log rotation during export;
- cancellation and private temporary-file cleanup;
- source inspection deadline and non-Git fallback;
- archive limit errors before response headers;
- content type, disposition, length, and ETag;
- safe timeline audit content;
- authenticated CLI/browser actor attribution; and
- no bundle persistence in storage diagnostics before Milestone D.

### Client and CLI tests

Cover:

- specialized binary streaming without exposing raw transport;
- structured error decoding;
- compressed-byte limit enforcement;
- atomic 0600 file creation;
- --force preserving the old file on failure;
- human and JSON plan/save output;
- inspect and verify with the daemon stopped;
- incomplete-command help without daemon startup;
- corrupt and unsupported bundle exit behavior;
- no binary output mixed with JSON; and
- completion for recordings, traces, and files.

### Web tests

Vitest covers:

- plan loading and stale-response suppression;
- unchecked sensitive controls;
- warning and review-required presentation;
- disabled download while busy;
- structured errors;
- object URL cleanup;
- focus containment and restoration;
- invocation from recording and trace drawers; and
- no duplicate request while export is pending.

Playwright covers a real stopped recording and complete trace export, validates
the downloaded filename and ZIP media type, and passes the download to the
compiled CLI verify command.

### End-to-end tests

Add a non-destructive CLI E2E journey using the isolated temporary Portless
home:

1. Start store-lite.
2. Create and stop a bounded recording.
3. Generate a faulted request.
4. Export a thin bundle.
5. Stop the daemon.
6. Inspect and verify offline.
7. Assert the archive contains public topology and evidence.
8. Assert it does not contain the temporary source root, daemon token,
   installation key, PID, runtime port, Authorization value, or generated
   provider secrets.
9. Corrupt one entry and verify rejection.

Milestone D adds an isolated import using fresh source mappings. Milestone E
adds managed-resource snapshot/restore only to the opt-in resource suites.
No reproduction test requires the destructive relay suite.

## Documentation and contract updates

When Milestone A is implemented:

- add a stable docs/reproduction-bundle-format.md separate from this proposal;
- update README workflows and Local data and safety;
- update portless-cli/COMMANDS.md and generated help tests;
- update docs/implementation-status.md;
- update portless-daemon/api/openapi.yaml;
- update portless-daemon/api/events.md to document the absence of a new topic
  and the new timeline audit;
- update portless-daemon/README.md package ownership;
- update architecture tests for the reproduction and pure format packages; and
- link the implemented command from recording and traffic documentation.

Each later milestone updates the format feature registry and documentation
before writing a new required feature.

## Rollout and compatibility

The first public format is 1.0.0. This greenfield repository does not write a
legacy draft format or a compatibility adapter.

Readers:

- reject a newer major version;
- accept newer minor versions only when all required features are known;
- ignore unknown optional entries or extension keys only when the manifest
  declares their feature optional and their checksums verify; and
- preserve no unknown data during import because import creates a current
  environment rather than rewriting the bundle.

Writers emit only the current major/minor version. A format semantic version
changes independently from the daemon API semantic version.

Before enabling bundle run by default:

- retain format fuzzing in CI;
- run CLI and UI E2E on macOS and Linux;
- run resource snapshot integration against every supported engine/plugin
  combination;
- exercise daemon cancellation and disk-full failure;
- perform a security review of archive parsing, source patches, redaction,
  snapshot staging, and remote policy; and
- complete a release soak with bundles created by one build and inspected/run
  by the next compatible build.

## Implementation sequence

### Phase 1: format and offline inspection

1. Add contract and format packages.
2. Define manifest, feature registry, limits, canonical paths, checksums, and
   malicious fixtures.
3. Implement reader, verify, and inspection DTO.
4. Add CLI inspect and verify without daemon startup.
5. Add architecture guards and format documentation skeleton.

Exit criterion: a handcrafted supported bundle is deterministically written,
verified, and inspected offline; every malicious fixture fails safely.

### Phase 2: thin plan and export

1. Add database frozen snapshot reads and log-window support.
2. Add source metadata inspection.
3. Add portable topology/configuration/evidence mappers and sanitizer.
4. Add plan and archive assembly.
5. Add API endpoints, typed client streaming, and CLI save.
6. Add timeline audit and OpenAPI/event documentation.
7. Add unit, integration, and CLI E2E coverage.

Exit criterion: a stopped recording or complete trace produces a verified thin
bundle with no known private runtime or secret-bearing fields.

### Phase 3: browser export

1. Add TypeScript contracts and preview dialog.
2. Add recording and trace drawer actions.
3. Add safe download handling and tests.
4. Regenerate embedded web assets.
5. Run the full build and normal daemon restart for local verification.

Exit criterion: the real browser journey downloads a bundle which the compiled
CLI verifies.

### Phase 4: exact source and intervention capture

1. Add revisioned applied-intervention references.
2. Add immutable recording intervention snapshots.
3. Add source patch design and safety gates.
4. Extend fidelity and feature registry.
5. Add migration, API, traffic UI, and recording export updates coherently.

Exit criterion: every intervention used by selected recorded traffic resolves
to an immutable definition, and a clean or sealed source is materializable
without touching an existing checkout.

### Phase 5: isolated import

1. Land project declaration import and target-name planning.
2. Add import inspection, staging, storage accounting, and durable operation.
3. Verify user-supplied source paths and create a new stopped environment.
4. Import experiments disabled and enforce remote unresolved bindings.
5. Add preview-first CLI run and browser file inspection.
6. Add cleanup, reset, forget, and uninstall coverage.

Exit criterion: a diagnostic bundle can create a new isolated stopped
environment, and failure cannot mutate existing source or unowned runtime data.

### Phase 6: snapshots, replay, and CI

1. Add resource plugin snapshot capability and portable blobs.
2. Add recorded dependency simulation and local root-request replay.
3. Add comparison results and bounded recipe execution.
4. Add CI mode and failure-bundle export.
5. Run opt-in resource E2E and release soak.

Exit criterion: a bundle marked runnable can reproduce its captured behavior
from exact sources and compatible data, or return a structured comparison
showing precisely how the current result differs.

## Validation for each implementation phase

During development, run the narrow owning-package tests. Before handing off
each coherent milestone:

~~~bash
gofmt -w <changed Go files>
go test ./portless-daemon/reproduction/... ./portless-daemon/api/... ./portless-daemon/controlplane ./portless-daemon/database ./portless-cli/traffic
go test ./tests/architecture
npm --prefix portless-web run typecheck
npm --prefix portless-web test
make lint
make test
git diff --check
~~~

Run the ordinary isolated E2E suites when the CLI or browser workflow lands.
Run managed-resource E2E only when snapshots land. The reproduction feature
does not authorize machine-level relay tests, resets of a developer's normal
Portless home, or cleanup of unverified processes, containers, volumes,
worktrees, or files.

## Acceptance criteria

Milestones A and B are complete when:

- one .portless file contains versioned public topology and selected bounded
  evidence;
- thin export omits payloads, log messages, remote URLs, patches, snapshots,
  secrets, private keys, paths, PIDs, and private runtime addresses by default;
- every omission, redaction, truncation, and fidelity gap is visible;
- inspect and verify work with the daemon stopped;
- malformed or hostile archives fail within fixed resource limits;
- CLI and browser use the same daemon plan and produce the same manifest;
- export never leaves a partial destination file or retained daemon copy;
- human and JSON output are stable; and
- complete non-destructive validation passes.

Runnable reproduction is complete only when:

- exact source requirements can be satisfied without modifying an existing
  checkout;
- capture-time interventions are immutable and revisioned;
- snapshots restore only into new owned resources;
- remote providers remain unresolved unless explicitly rebound;
- no arbitrary code from the archive executes automatically;
- replay uses the source-aware edge model;
- results compare observed and reproduced behavior;
- failure cleanup remains ownership-verified and fail-closed; and
- the manifest's runnable value is derived from verified capabilities rather
  than user-authored assertion.
