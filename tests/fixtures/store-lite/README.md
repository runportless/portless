# Store-lite E2E fixture

This dependency-free Go fixture gives the CLI and browser suites a real
three-service application without requiring a container runtime:

```text
client -> checkout -> inventory
                   -> orders
```

There is intentionally no Portless declaration. The tests exercise static
discovery, generated connection environment, process supervisors, ingress,
traffic capture, logs, and the embedded UI through the built CLI binary.

Checkout also exposes `/api/orders` and `/auth/login` as bounded request-echo
endpoints. They verify application ingress preserves methods, paths, query
strings, bodies, and headers. The login path is a routing fixture and performs
no authentication. Unregistered paths, including Portless control API and
browser-claim paths, return the application's ordinary 404.

`/browser-policy` and `/browser-policy/frame` provide application-owned browser
headers that allow an inline script and same-origin embedding. The Chromium
suite verifies those policies survive ingress without additional Portless
restrictions; the request-echo endpoints verify absent policies stay absent.

Checkout and orders serve bounded WebSocket echo endpoints at `/ws`, `/api/ws`,
and `/auth/ws`, negotiating `portless-test`. Checkout's `/websocket-dependency`
opens a real WebSocket using `ORDERS_URL` and returns the echoed response.
`/browser-websocket` uses the browser's native WebSocket client to exchange text
and binary messages and close cleanly under an application-owned CSP. The
fixture-only `wstest` package supports unfragmented frames up to one MiB with
finite I/O deadlines; it is not used by the production proxy.
