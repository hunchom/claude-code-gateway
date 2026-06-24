# Baked-in CA bundle

Drop any number of PEM-encoded CA certificates (`*.pem`) in this directory and
rebuild. They are embedded into the binary at compile time via `go:embed` and
appended to the system trust store when verifying the upstream TLS connection.

This lets you ship a self-contained binary that trusts a private/internal CA
(for example, the CA in front of a self-hosted LiteLLM endpoint) without
installing anything into the host trust store.

No `.pem` files here is fine — the gateway falls back to the system roots, plus
any file named by `ca_bundle` / `CCGW_CA_BUNDLE` at runtime.

Files in this directory other than `*.pem` (like this README) are ignored.
