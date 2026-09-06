# Tracing performance

Status: implemented and validated, 2026-09-05. WebSocket message capture remains
deferred. The approved implementation steps are retained below; measured results
and validation are recorded at the end.

The proposed fix is to make capture inexpensive, build traces from indexed
metadata in bounded batches, and reuse the resulting projection for reads.
The correlation rules, retained history limits, and request-detail contents
remain the product contract.

1. **Establish the baseline and the specific causes.**

   A temporary Go benchmark exercised the current traffic package with 250,
   1,000, and 5,000 retained exchanges. The mixed workload contains HTTP roots,
   HTTP dependencies, and TCP dependency spans, using both explicit trace
   context and inferred parents. Setup populates the retained window directly
   outside the timed section. These are body-free, in-process measurements on
   an Apple M1 Max with Go 1.26.4, using three measured iterations per case.

   | Store operation | 250 retained | 1,000 retained | 5,000 retained |
   | --- | ---: | ---: | ---: |
   | Build mixed trace projection | 0.56 ms | 9.02 ms | 195.30 ms |
   | Append one exchange to a full window | 0.71 ms | 8.60 ms | 193.97 ms |
   | List up to 100 traces | 0.60 ms | 9.27 ms | 218.07 ms |
   | Open one trace | 0.57 ms | 9.05 ms | 196.59 ms |

   At 5,000 entries, append/list/detail each allocate approximately 12 MiB per
   operation, even though the fixture contains no captured bodies. These numbers
   establish the scale of the problem; they are not measurements of complete
   browser interactions or application request latency. Longer, repeated
   benchmarks and profiles are part of implementation validation.

   The code explains the growth:

   - `traffic/store.go:addExchange` copies the retained slice and runs
     `buildTraces` while holding the single store mutex. Proxy capture calls
     this synchronously. Work for one environment can block other environments.
   - `traffic/traces.go:inferenceCandidates` scans the entire window for every
     exchange needing inference. At 5,000 entries this can mean approximately
     25 million candidate checks for one projection.
   - `Store.Traces` and `Store.Trace` rebuild the whole projection again. Opening
     one trace does work for all retained traces.
   - The API asks for up to 5,000 complete traces, filters them, then discards
     spans for a summary response. Payload-bearing headers/message structures
     are cloned before that discarded work is known to be unnecessary.
   - The browser polls snapshots every five seconds and can also fetch trace
     detail for each incoming trace event when a trace is expanded or an edge
     filter is active. These requests repeat the same expensive computation.

2. **Make capture append to a bounded store with short locks.**

   Replace the per-environment growing/copied slice with a fixed-capacity ring
   and a sequence-to-entry index. Append and oldest-entry eviction become
   amortized constant-time operations. Retain the existing 5,000-entry and
   64 MiB payload bounds and exact sequence high-water behavior. A large new
   payload may evict several entries, but each entry is removed only once.

   Keep retained exchanges immutable after the initial defensive copy. Store
   lightweight correlation metadata alongside sequence references, so tracing
   does not clone headers, bodies, or decoded TCP messages. Detail requests
   make defensive copies only for the exchanges they return.

   Organize mutable state by environment. Use the global map lock only to find
   environment state; use short per-environment critical sections for append,
   lookup, counters, and revision changes. No trace construction, event delivery,
   database work, or network I/O runs under those locks.

   Preserve the atomic relationship between completing an HTTP request and
   removing it from the active-request set. Begin, complete, and abandon must
   all invalidate the appropriate provisional trace state.

   **Why this helps:** the application capture path no longer pays for copying
   the complete history or constructing every trace. Exchange lookup becomes
   direct, and a busy environment no longer serializes unrelated environments
   behind trace computation.

3. **Replace all-pairs inference with a time-ordered index.**

   Keep the existing exact trace/span-ID lookup and exact-context precedence.
   For inference, process metadata in stable `(startedAt, sequence)` order and
   maintain candidate parents by target service and active time interval.

   Use an expiry heap plus an active set per target service. A child's source
   identifies the only relevant target set. Remove parents whose completion
   time is before the child's start, and inspect at most two eligible candidates:
   zero means no inferred parent, one means a unique inferred parent, and two
   already establish ambiguity. Do not allocate all matching parent pairs.

   Activate all exchanges with the same start timestamp before evaluating that
   timestamp's children. Exclude the child itself. Preserve the current inclusive
   start/completion boundaries and deterministic duplicate-context behavior.
   These details matter for very short requests and out-of-order completion.

   Reuse the stable global order when collecting component members. Calculate
   span depths once with cycle-safe memoization rather than walking the same
   ancestor chain separately for every span. Preserve the current cycle result
   and transaction grouping; use ranked/path-compressed component merging where
   component bookkeeping needs it.

   The intended full-projection cost is approximately O(N log N), including
   sorting and expiry management, with O(N) working metadata. The existing
   all-pairs parent search is O(N²). A fivefold increase in retained history
   should no longer produce roughly a twentyfold increase in projection time.

   **Why this helps:** it removes the dominant repeated comparisons while
   preserving the same candidate rule. This also bounds an ambiguity-heavy
   workload without storing a quadratic number of possible relationships.

4. **Build and cache projections outside the capture path.**

   Give each environment a monotonically increasing revision and a separate
   Clear generation. Appends, evictions, and active-request changes mark its
   projection dirty. A bounded worker pool projects immutable metadata snapshots
   outside store locks, with at most one build in flight per environment.

   Start with a maximum 50 ms batching delay under continuous traffic. A snapshot
   read can request immediate processing. Coalesce dirty notifications by
   environment/revision; do not enqueue one job or spawn one goroutine for each
   exchange. Work arriving during a build schedules the next revision.

   Publish an immutable cache containing summaries, ordered trace numbers,
   trace-to-exchange membership, span relationships, transaction groups, and
   service/edge membership used for filtering. The cache does not retain a
   second set of payloads. Release temporary snapshot references after each
   build; bound worker concurrency and include in-flight snapshots in memory
   measurements.

   Reads capture the revision they need and either use an adequate cached
   projection or await the shared worker with the HTTP request's context. They
   never launch duplicate projection work. Resolve selected detail exchanges
   under a stable read snapshot; if eviction or Clear invalidates that snapshot,
   retry against a newer projection within the request deadline. Never return
   a trace assembled from incompatible revisions.

   Publish changed trace summaries after a batch. Exchange notifications remain
   immediate. Reject completed work from an older Clear generation and never
   replace a newer published revision with an older one. Clear, environment
   disposal, and daemon shutdown cancel obsolete work and release resources.

   **Why this helps:** many exchanges arriving in a burst share one projection
   build. Repeated reads and multiple browser tabs reuse it. Capture latency is
   separated from projection cost, while metadata snapshots still become visible
   promptly and consistently.

   This first fix deliberately uses efficient complete projections per batch.
   A fully incremental graph is unnecessary unless the resulting benchmarks show
   a remaining problem. Such a graph would need to detach and reattach inferred
   children when late parents create ambiguity or eviction removes it, and split
   components after deletion. The indexed batch approach recomputes these cases
   using one clear set of rules.

5. **Serve summaries and individual details directly from the cache.**

   Introduce internal summary queries with the existing service, edge,
   background, and limit filters. Apply those filters to cached metadata before
   materializing the response. Trace list endpoints must not construct span
   trees or clone message content just to discard it.

   A trace detail request looks up that trace's sequence list and materializes
   only its members. Preserve the existing public detail contents, span order,
   root selection, trace number, correlation quality, and transaction groups.
   A genuinely large trace still requires work proportional to its own spans;
   unrelated retained traffic should not determine its cost.

   Add metadata-only exchange reads for list responses and environment request
   statistics, which currently clone full captured exchanges. Keep existing
   detail APIs as the place to obtain captured content.

   Add an explicit projection `revision` and `throughSequence` to trace snapshot
   responses, and the projection revision to trace updates. Use these values to
   reconcile the cache with concurrent notifications and remove obsolete trace
   rows after merges or eviction. Do not infer a complete watermark from the
   maximum sequence in a limited, filtered list.

   Update contract types first, then the typed client, server/control-plane
   adapters, CLI/web consumers, OpenAPI, and event documentation. Proposed API
   version: **13.2.0** for these additive snapshot fields. Recording schema,
   lifecycle protocol, and supervisor protocol stay unchanged. Existing paths,
   query behavior, and HTTP/TCP protocol values remain in place.

   **Why this helps:** listing 100 summaries copies 100 summaries. Opening a
   four-span trace loads four spans. Cached reads avoid both full-history CPU
   work and the current multi-megabyte allocation pattern.

6. **Remove redundant browser fetches and bound presentation buffers.**

   Merge summary updates by projection revision. When the expanded trace changes,
   coalesce its detail refreshes and allow only one in-flight request. If another
   revision arrives during the request, fetch the newest required revision once
   afterward; do not queue every intermediate update.

   For an active edge filter, reconcile against one filtered summary request per
   batch instead of requesting detail for every incoming trace to discover its
   edges. Keep the periodic snapshot as a recovery mechanism, reuse in-flight
   requests, and discard responses from old environments or Clear generations.

   On pause, cap buffered metadata at the retained-window bound and track that
   resynchronization is required if it overflows. Resume from an authoritative
   snapshot rather than accumulating unlimited browser-side updates. Preserve
   selection and scroll position where the selected trace remains retained.

   **Why this helps:** traffic bursts produce bounded network and React work.
   Opening several control-plane tabs no longer multiplies trace construction,
   and leaving Traffic paused cannot grow memory indefinitely.

7. **Prove that faster tracing still produces the same answers.**

   Before replacing the algorithm, add characterization tests for its current
   semantics. Keep a small, straightforward test-only reference implementation
   as an oracle for generated bounded histories. Compare complete normalized
   projections after each relevant transition, not just final span counts.

   | Case | Required result |
   | --- | --- |
   | Children complete before parents | The final trace root, numbering, duration, depths, and correlation stay correct. |
   | A second overlapping parent arrives | A formerly unique inferred parent becomes ambiguous; the child is not silently assigned to either parent. |
   | Exact context arrives late | Exact parentage takes precedence over inferred relationships. |
   | Candidate or root is evicted | Components, public trace numbers, and ambiguity are recalculated from retained inputs. |
   | Active HTTP request begins/completes/abandons | TCP provisional state changes even when there is no new completed exchange on abandon. |
   | Equal timestamps, duplicate context, and cycles | Results are deterministic and match the reference; no recursion overflow or hang. |
   | Database transactions/background operations | Grouping, background filtering, failures, and fault visibility remain unchanged. |
   | Clear during a build or request | Old projection results and browser responses cannot restore cleared rows. |
   | Multiple readers and environments | One shared build per needed revision; unrelated environments can append while another projection is blocked in a test. |
   | Eviction and payload ownership | Retention budgets hold, returned data cannot mutate storage, and caches do not pin evicted payloads indefinitely. |

   Exercise HTTP/TCP combinations, all-exact context, no context, heavy overlap,
   deep chains, unrelated services, payload-bearing traffic, and full windows.
   Include race tests for capture, worker publication, snapshot reads, and Clear.

8. **Use measurable acceptance targets and a staged implementation.**

   On the same machine and representative 5,000-entry benchmark, the initial
   targets are:

   | Operation | Current baseline | Target |
   | --- | ---: | ---: |
   | Capture append | About 194 ms | Under 1 ms, excluding the pre-existing durable recording write. |
   | Cached list of 100 summaries | About 218 ms | Under 2 ms. |
   | Cached four-span trace detail | About 197 ms | Under 1 ms. |
   | Full mixed projection | About 195 ms | Under 10 ms. |
   | Projection allocations | About 10 MiB per mixed build | Substantial reduction; metadata-only and proportional to retained count. |
   | Cache-hit list/detail allocations | About 12 MiB per call | Proportional to returned summaries/spans, not total retained history. |

   These targets were set before implementation; measured results follow below. A cache miss
   can wait for one projection and batching interval; it is not covered by the
   cache-hit latency target. Measure uncached-read latency and live-update lag
   separately, with an initial normal-load target below 100 ms.

   CI should enforce algorithmic and concurrency properties rather than rely
   only on hardware-sensitive millisecond thresholds: cache hits perform no
   projection build; readers share work; append does not build traces; queue
   count is bounded; and the inference algorithm does not enumerate all pairs.
   Capture before/after benchmark results and CPU/allocation profiles on the
   same hardware, including one versus several browser readers.

   | Step | Files / owning area | Exit evidence |
   | --- | --- | --- |
   | A | `traffic/store_test.go`, new focused benchmark/reference tests | Repeatable baseline and correlation characterization. |
   | B | `traffic/traces.go` and focused trace-index helpers | Indexed projection matches the reference across ordinary and adversarial histories; projection benchmark improves. |
   | C | `traffic/store.go` and focused retention/cache helpers | Ring/index, revisions, bounded worker lifecycle, short locks, race coverage, and inexpensive append/cache hits. |
   | D | `api/contract`, typed client, server, control-plane observability/statistics | Summary queries and consistent snapshot metadata; detail contract preserved; API/CLI tests pass. |
   | E | `trafficSnapshot.ts`, `useTrafficStream.ts`, traffic state/component tests | Coalesced requests, revision reconciliation, bounded pause/resume, and preserved user navigation. |
   | F | Documentation, generated web assets, complete validation | Recorded before/after results, real UI journeys, and successful normal daemon restart. |

   Run focused traffic/API/CLI/Vitest checks while implementing. Then run
   `go test -race` for affected traffic packages, `go test ./tests/architecture`,
   `make lint`, `make test`, and the ordinary isolated CLI/UI E2E suites.
   The traffic list, inspection, waterfall, background/transaction, and
   pause/clear journeys must continue to pass. Run `git diff --check`.

   Regenerate tracked assets through Make, build the complete executable, and
   use normal `./bin/portless daemon restart` to load the final UI. Preserve
   active environments and verify their adoption. Destructive relay tests and
   forced daemon replacement are not routine validation for this change.

The completion evidence should show lower capture latency, cheap repeat reads,
bounded work under bursts, and unchanged trace results. WebSocket parsing,
message contracts, capture UI, and recording changes remain deferred.


## Implementation results — 2026-09-05

Implemented the ring/index, per-environment locks, metadata-only indexed
correlation, two shared projection workers, revision-aware cached APIs, and
bounded browser refresh/pause handling. HTTP/TCP correlation, captured details,
recordings, and the existing retention bounds are preserved. API version is
13.2.0. No WebSocket message capture was added.

The same Apple M1 Max and Go 1.26.4 were used. The original body-free mixed
fixture is now checked in as `portless-daemon/traffic/benchmark_test.go`.
The updated measurements below are medians of three 300 ms benchmark runs;
initial one-second repeated runs gave similar results. Baseline measurements
used three fixed iterations, and the original CPU profile separately measured
10 complete builds. These are in-process store timings, not browser render or
network latency measurements.

| Operation, 5,000 retained exchanges | Before | After |
| --- | ---: | ---: |
| Full mixed projection | 195.30 ms | 3.020 ms |
| Capture append | 193.97 ms | 0.803 µs |
| Cached list of 100 summaries | 218.07 ms | 4.657 µs |
| Cached four-span detail | 196.59 ms | 0.667 µs |

Mixed projection time scales from 0.140 ms at 250 entries, through 0.623 ms at
1,000, to 3.020 ms at 5,000. The full-window adversarial fixtures also meet the
10 ms target: independent HTTP traces 6.215 ms, heavy overlap 5.709 ms, no
explicit context 2.777 ms, and a deep exact-context chain 2.011 ms.

Mixed projection allocation fell from 10.00 MB to 4.53 MB per build. Cached
list/detail calls allocate 28 KiB / 2.25 KiB, respectively, instead of roughly
12 MiB each. The fixture with an 8 KiB response body per exchange also allocates
4.53 MB per projection; payload data is absent from the projection path.
The allocation profile includes the worker's temporary metadata snapshot and
projection construction. Retention and eviction tests cover byte accounting,
summary payload omission, and defensive detail copies.

A cache miss with one reader completes in 2.885 ms; eight simultaneous readers
complete in 3.068 ms and share exactly one build per changed revision. A
100-exchange burst reaches its trace notification in 64.2 ms with one build
per burst (three runs of ten bursts), meeting the initial normal-load live-lag
target. Saturating multiple environments can add worker queue time; these are
measured workload results rather than a universal latency guarantee.

The original CPU profile attributed 92.6% of sampled CPU time to the all-pairs
parent-candidate search. The new worker profile contains sorting, indexed
projection, allocation, and scheduler work; the all-pairs search is absent.
Profiles and raw logs for this run are under `/private/tmp/portless-tracing-*`;
the original artifacts are in `/private/tmp/portless-trace-performance-review/`.
Reproduce measurements with:

```bash
go test ./portless-daemon/traffic -run '^$' -bench '^BenchmarkTracing($|ColdReaders)' -benchtime=300ms -count=3
go test ./portless-daemon/traffic -run '^$' -bench '^BenchmarkTracingLiveBatch$' -benchtime=10x -count=3
go test ./portless-daemon/traffic -run '^$' -bench '^BenchmarkTracingColdReaders/8$' -benchtime=2s -cpuprofile=/tmp/tracing-cpu.pprof -memprofile=/tmp/tracing-memory.pprof -o /tmp/tracing.test
```

Validation completed:

- Generated correlation histories match the test-only reference after appends
  and eviction; deep chains, cycles, duplicate context, inclusive timestamps,
  transactions, background operations, and provisional state are covered.
- Race tests pass across traffic/proxy, API server, and control plane, including
  concurrent capture/read/Clear, canceled readers, shared work, disposal, and
  shutdown. Cached-reader tests verify that repeat reads do not rebuild.
- Browser tests cover one in-flight expanded-detail request, coalesced filtered
  summaries, delayed pre-Clear/previous-environment responses, revision resets
  on reconnect, and a 6,000-event pause capped at 5,000 entries.
- `make lint`, `make test`, and architecture checks pass. The default isolated
  CLI E2E suite passes; all 49 browser E2E tests pass, including real filtered
  bursts, expanded-detail reuse, pause/resume, Clear, HTTP/TCP waterfall,
  transactions, and the existing WebSocket handshake-only journey.
- `make` regenerated tracked assets and built the executable. Normal daemon
  restart succeeded in 1.736 seconds. Chat and Store remain healthy with the
  same service PIDs; the stopped Store environment remains stopped. The live
  bundle matches the checkout, and live trace snapshots expose the new fields.

Container-backed opt-in example suites and destructive machine-relay suites
were not run; the modified tracing paths are covered by the focused tests and
default isolated suites above.
