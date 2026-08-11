# ADR 0003: traffic experiments live on explicit edges

Status: accepted

Portless injects a separate loopback listener for each discovered source/target connection. That makes `gateway → accounts` different from `orders → accounts` even though both reach the same target process.

Live summaries, recordings, and faults are evaluated in this data path. Application ingress is represented as `external → service`. The UI and CLI therefore describe blast radius in terms developers already understand, without a service mesh or external gateway.
