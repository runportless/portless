# Mock route preview

Status: implemented and verified on 2026-09-05.

## Outcome

A developer can test a sample HTTP request against the route they are editing,
including unsaved changes, and inspect the expected mock response without
saving, enabling the scenario, replacing a provider, or contacting an application.
Preview occupies the existing right pane. The route list stays visible and the
request tester and response viewer retain the flat layout of the editor.

## Starting behavior

- The right pane owns route drafts; switching routes retains unsaved drafts in
  the scenario workspace. Saving and activation are separate operations.
- POST `/api/v1/environments/{project}/{environment}/mocks/{scenario}/preview`
  received a bare `MockRequest` and evaluated the saved scenario.
- The Go matcher already validates definitions, resolves service/method/path/query
  precedence, ignores disabled routes, and returns an unmatched 501 result.
- Scenario activation does not control matcher preview. Request headers and body
  are validated by the existing API but do not participate in matching.
- The current result describes configured responses rather than every HTTP
  transport header. It reports delay without sleeping.

## User journey and layout

1. Add PREVIEW alongside DISCARD/CANCEL and SAVE ROUTE in the editor footer.
   Opening it preserves every draft field and the selected Request/Response
   configuration tab.
2. The right pane switches to a request tester. Prefill service and method from
   the selected draft; replace path parameters with readable sample values and
   prefill required query values. These are request inputs, separate from the
   route definition. A reset action restores these suggested inputs.
3. Provide service, method, concrete request path, and optional query parameters.
   Query input uses editable Name/Value rows, including repeated names and
   empty values, with an inline blank row and per-row removal. Reject full URLs, fragments, and unresolved path parameters;
   the path field accepts paths only and query values have their own field.
4. RUN PREVIEW explicitly evaluates the current input. Enter submits that form.
   Show RUNNING… and suppress duplicate runs. Leaving the route aborts pending
   work; a late result must never replace another route's result.
5. Show matched route (including when another route wins), HTTP status, and
   configured delay. No match is a normal 501 result, not an API error. Explain
   when the edited route is disabled. Preview works while the scenario is disabled.
6. BODY and HEADERS use accessible tabs. Show JSON formatted when valid, offer
   raw text, and render all content as text. Empty bodies/headers have useful
   empty states. The viewer fills available height and scrolls independently.
7. EDIT returns to the unchanged draft. Retain request inputs and results during
   Edit/Preview switches for this route. Mark results outdated when request,
   selected draft, or saved scenario routes change; require an explicit rerun.
8. Clearly label scope: current route draft plus saved routes in this scenario.
   Other retained route drafts are not silently included. Preview must not
   change active routing even when editing an enabled scenario.
9. Use shared theme variables, focus styling, structured ActionErrorNotice,
   semantic status announcements, and responsive pane layouts. Do not restore
   the removed Edit Route/name heading or maximize control.

## API contract

Replace the bare request with one `PreviewMockRequest` envelope:

```json
{
  "request": {
    "service": "inventory",
    "method": "GET",
    "path": "/inventory/sku-123",
    "query": { "warehouse": ["central"] }
  },
  "originalRoute": "lookup",
  "draft": {
    "name": "lookup",
    "service": "inventory",
    "method": "GET",
    "path": "/inventory/{sku}",
    "status": 503,
    "body": "{\"available\":false}",
    "enabled": true
  }
}
```

- `request` is required. `draft` is optional for CLI saved-scenario previews.
- `originalRoute` is present only when replacing an existing route for preview;
  it requires a draft and must identify an existing saved route. The draft may
  have a new unique name. Missing/deleted originals fail explicitly.
- A new draft omits `originalRoute` and appends to the scenario copy. Duplicate
  names remain validation errors, rather than implicitly replacing another route.
- Canonicalize and validate request and draft services using project topology.
  Compile the complete temporary scenario so ambiguity, invalid paths, duplicate
  identities, and all existing size limits are enforced before evaluation.
- Preserve draft enabled state and other routes' saved enabled states.
- Make no database mutation, runtime change, network request, event, recording,
  or timeline entry. Do not log request/draft payloads.
- Keep wire types in `api/contract`; adapt to domain values at the server.
  Change API version from 13.2.0 to 14.0.0 because the request shape breaks.
  Update the typed client, server, CLI, web types, OpenAPI, and tests together.
  Keep only the new wire format; no compatibility decoder or CLI aliases.
- Improve response preview fidelity for mock-owned headers, unmatched JSON,
  and body suppression for HEAD/204/304. Reuse shared mock response construction
  with the runtime where appropriate. Delay remains metadata, with no sleep.
  Do not claim to reproduce transport-added Date/Content-Length headers.

## Implementation ownership and order

1. Write this plan before implementation; preserve unrelated working-tree changes.
2. Backend: add the contract envelope, update typed client, implement temporary
   draft overlay and validation, update server adapter and CLI consumer, and
   implement/test shared mock response semantics. Update OpenAPI and event docs.
3. Frontend: add request/envelope types and callback in MocksPanel, reuse existing
   draft conversion, add preview state/request parser and result viewer under
   `features/mocks`, and integrate the mode toggle/footer in MockRouteEditor.
   Keep API path ownership in the web feature's existing API layer usage.
4. Concurrency: each run captures a serialized input/scenario fingerprint and
   uses an AbortController and finite timeout. Discard obsolete responses and
   clear pending state on cancellation. Preserve old results as visibly outdated.
5. Styling: extend existing CSS beside mock editor rules; retain the full-width
   pane, growing response area, sticky footer, independent scrolling, and both
   themes. Narrow panes stack request controls without horizontal overflow.
6. Update README, deterministic-mock ADR, implementation inventory, and E2E
   documentation for draft scope, side effects, and user workflow.

## Verification

- Go unit/control-plane tests: saved-only request; changed unsaved response;
  new route; disabled route; other-route precedence; ambiguous/duplicate drafts;
  missing or renamed original; invalid service and request; request/response
  bounds; HEAD/204/304 and unmatched response; unchanged scenario and runtime.
- API/client tests: envelope encoding and decoding, draft/original identity,
  structured failures, and existing CLI preview behavior with the new envelope.
- Vitest: concrete sample paths, repeated/empty query values, input validation,
  presentation of JSON/raw/empty content, accessible controls and error state.
- Playwright: create/edit preview without save; saved scenario remains unchanged;
  disabled scenario works; no-match 501; preview another matching route; disabled
  route result; return to edit preserving fields; outdated results; request edits;
  cancellation/late result handling; save then activate existing workflow still
  works. Verify dark/light and narrow/focus layouts with screenshots.
- Run focused checks while iterating, then `make lint`, `make test`,
  `go test ./tests/architecture`, `git diff --check`, and affected CLI/browser
  E2E journeys with isolated homes. Do not run destructive relay suites.
- Build tracked assets through Make, build complete executable and normal
  `./bin/portless daemon restart`. Verify currentBuild and live UI. If restart
  times out, inspect status/logs and allow verified recovery; never force or
  kill an unverified process. Report a concrete blocker if recovery fails.

## Completion record

Implemented the same-pane request tester, preserved draft and request inputs,
explicit execution, Body/Headers views, raw/formatted body display, stale-result
and error handling, cancellation, timeout, and keyboard interactions. Query
disclosure stays open when its input is cleared. The API, typed client, CLI,
OpenAPI, event documentation, and deterministic-mock ADR use the new contract.
Tracked web assets were regenerated through Make.

Validation completed:

- `make lint` and `make test` passed, including all Go packages, architecture,
  349 web tests, and site validation. After the final query-disclosure fix,
  web lint, typecheck/build, and all 349 web tests passed again.
- Seven focused Playwright journeys in `mock-preview.spec.ts` and
  `experiments.spec.ts` passed. All four preview journeys passed again after
  the final query-disclosure fix, including clearing the field without losing
  visibility or focus. Dark/light, focus, and narrow layouts were inspected.
- The isolated CLI journey
  `TestCLIMockScenarioHotSwapKeepsPeerServicesRunning` passed with the new API.
- `git diff --check` passed. The full unrelated E2E suites and opt-in resource
  integrations were not run; validation targeted the affected journeys.
  Machine-destructive relay suites were not run.
- `make` and normal `./bin/portless daemon restart` succeeded. Daemon status
  reported API 14.0.0, `currentBuild: true`, ready runtime and handoff state,
  both existing active environments, and no recovery problems.
- The existing Firefox mocks page was refreshed and Preview was exercised for
  `sold-out` / `get-root`: `GET /` returned a matched 200 response, empty body,
  and expected configured/mock headers. The scenario remained disabled and
  no route changes were saved. The full-height response pane was inspected.
