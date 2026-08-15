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
