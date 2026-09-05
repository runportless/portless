# Application API and authentication routes

Status: implemented and active. Validation results and the existing browser-test
exception are recorded below.

Portless must forward an application's `/api/` and `/auth/` requests to that
application. For example,
`http://checkout.local.billing.localhost/api/orders` must reach `checkout`.
Portless's own `/api/v1/` and `/auth/claim/` handlers belong to the control
host, `portless.localhost`, and the existing supported loopback control hosts.

The defect is in `portless-daemon/api/server/server.go`. `Server.ServeHTTP`
already identifies application hosts before considering control routes, but
then rejects every application path starting with `/api/` or `/auth/` with
HTTP 421. The combined server test explicitly expects that rejection for
`/api/v1/projects`, so the correction needs a replacement assertion that
proves the application receives the request and the control handler does not.

The owner is `portless-daemon/api/server`. The implementation should follow
these steps in one coherent change.

1. **Correct the application-host branch.**

   Remove the two path-prefix checks and their rejection from
   `Server.ServeHTTP`. Once `applicationHost` recognizes the hostname, pass the
   request to the existing `app.ServeIngress` method and return. Determine the
   destination from `request.Host` using the existing hostname validation.

   Application routing must include paths that happen to have the same spelling
   as control routes, such as `/api/v1/projects`, `/api/v1/health`, and
   `/auth/claim/example`. The selected application supplies the response. An
   unavailable application produces the existing ingress failure; it must
   never fall through to the control API, browser-claim handler, or embedded UI.

   The control-host branch continues to handle daemon health, authenticated
   control APIs, browser claims, and the UI. Unknown or malformed hostnames
   continue to receive `421 UNKNOWN_HOST`. Forwarded-host headers must not
   select a different handler.

2. **Add focused server regression tests.**

   Add `portless-daemon/api/server/application_hosts_test.go`. Use the existing
   database and control-plane APIs to configure a single HTTP service backed
   by a loopback `httptest` upstream. Give that upstream a distinctive response
   marker and capture the received method, path, query, and body. Use explicit
   remote classification and a read-write binding for this temporary test
   upstream so POST tests exercise routing rather than remote write policy.

   Cover these cases:

   - Application requests to `/api`, `/api/`, `/api/orders`, `/auth`, `/auth/`,
     and `/auth/login` reach the upstream without Portless authentication.
   - GET requests preserve query strings, including repeated query parameters;
     POST requests preserve their method, application headers, and body.
   - Application requests to `/api/v1/projects`, `/api/v1/health`, and a public
     daemon mutation path receive the upstream's response. A fake daemon
     control records zero calls, including when the request carries a valid
     synthetic test credential for Portless.
   - A real test browser claim sent to the application host reaches the
     upstream without issuing a Portless session cookie. The same claim still
     succeeds once on the control host and then rejects reuse.
   - A valid application hostname with no serving target returns the ingress
     failure, including for `/api/v1/projects`, without returning control data.
   - An unknown hostname remains rejected even with valid test credentials or
     an `X-Forwarded-Host` header naming the control host.
   - Existing hostname normalization remains effective for case and an
     explicit port, and existing loopback control hosts keep their behavior.

   Replace the obsolete application-host 421 assertion in
   `server_test.go` with this focused coverage. Keep the existing control
   authentication, CSRF, claim expiry/reuse, and unknown-host assertions.

   The private `/_portless/daemon/v1/...` lifecycle routes have a separate
   authenticated handler ahead of the API server. Include its existing
   control-host tests in validation; the broad application `/api/` and
   `/auth/` prefix rejection is not that privilege boundary.

3. **Verify the behavior through the compiled product.**

   Add small fixture-owned handlers for `/api/orders` and `/auth/login` in
   `tests/fixtures/store-lite/apps/checkout/main.go`. Return recognizable
   application responses and expose enough request information to verify a
   query and a POST body. These are fixture endpoints, not actual login or
   credential-handling implementations.

   Add `tests/e2e/application_routes_test.go` with a dedicated
   `TestCLIApplicationHostPaths` scenario. Start the copied fixture through
   `portless up` in an isolated `PORTLESS_HOME`, then use the existing
   `applicationRequestWithMethod` helper to verify both paths through the real
   daemon. Verify an app-level 404 for a control-shaped path the fixture does
   not implement. Confirm captured traffic identifies the correct
   `external:checkout` edge and original request path. Using a dedicated
   scenario keeps the existing lifecycle test's exact traffic counts meaningful.

   Extend the one-use browser-claim journey in
   `portless-web/e2e/access-navigation.spec.ts`: issue a claim, request its path
   through the application host using the existing `applicationRequest` helper,
   then consume it successfully through the control host in Chromium. Verify
   the application response did not set a Portless session cookie and that
   subsequent claim reuse still fails.

4. **Document the hostname boundary.**

   Update the public endpoint explanation in `README.md`, the API boundary
   explanation in `portless-daemon/README.md`, and the description in
   `portless-daemon/api/openapi.yaml`. State plainly that application `/api/`
   and `/auth/` paths belong to the application selected by the hostname.
   Document the control origin for event URLs in
   `portless-daemon/api/events.md`. Update `docs/e2e-testing.md` and the
   store-lite fixture README to describe the new regression coverage.

   This corrects application ingress rather than changing the control API's
   endpoints, wire types, authentication rules, or event payloads. Keep the API,
   lifecycle, and supervisor protocol versions at their current values. Native
   API clients and browser production source need no contract adaptation.

5. **Validate and make the corrected daemon available.**

   Format changed Go files. Run the focused package and architecture checks:

   ```bash
   go test ./portless-daemon/api/server ./portless-daemon/auth ./portless-daemon/control ./tests/architecture
   make e2e-binary
   PORTLESS_E2E_BINARY="$PWD/bin/portless-e2e" go test -count=1 -tags=e2e ./tests/e2e -run '^TestCLIApplicationHostPaths$' -v
   ```

   Then run the complete supported checks:

   ```bash
   make lint
   make test
   make test-e2e-cli
   make test-e2e-ui
   git diff --check
   ```

   Use a writable temporary `GOCACHE` when running in a restricted execution
   environment. Use the ordinary isolated E2E suites for this change; they
   exercise the real daemon without installing a machine relay.

   Finish by building the complete executable with `make` and performing a
   normal `./bin/portless daemon restart` so the current local installation
   serves the corrected router. If normal handoff is blocked, inspect
   `./bin/portless daemon status` and report the blocker. Forced replacement
   requires the user's explicit authorization under the repository instructions.

The change is complete when the fixture's application API and login paths work
through its Portless hostname, matching control-shaped paths stay with the
application, control-host authentication and one-use claims still work, unknown
hosts remain rejected, and the complete validation passes. Report any skipped
check or blocked daemon restart explicitly.

## Validation results (2026-09-04)

- The new server regressions reproduced HTTP 421 before the routing change and
  passed afterward. Authentication, private lifecycle control, and architecture
  checks also passed.
- `make lint` and `make test` passed using writable temporary Go and Staticcheck
  caches. The complete CLI E2E suite passed, including
  `TestCLIApplicationHostPaths`.
- The complete `make test-e2e` run now passes the CLI suite and all 46 browser
  journeys with no exclusions or test retries, including the application-policy
  regression added after the routing change. The E2E follow-up fixed the
  focus-mode and responsive layout timing races and restores stopped fixture
  services after failed assertions. The three affected journeys also passed
  five consecutive runs each. A temporary deliberate failure after stopping
  the environment confirmed that the subsequent recording journey still passes;
  the diagnostic test was removed before the complete suite ran.
- `make` rebuilt the complete executable. Normal daemon restart succeeded in
  1,673 ms; status confirmed the new build is current and ready, with
  `store/local` active and no handoff or recovery problems.
- Live requests through the installed relay to the checkout application's
  `/api/orders`, `/auth/login`, and `/api/v1/projects` returned HTTP 200 with
  `service: checkout`. The control host's `/api/v1/health` remained ready.
- `git diff --check` passed. No machine-destructive relay suite was run.
