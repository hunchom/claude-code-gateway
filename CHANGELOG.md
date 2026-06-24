# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `systemd` and `launchd` service units in `deploy/`, with the `.p12` password
  kept out of version control (macOS Keychain wrapper; Linux root-only env file).
- Integration tests for the proxy: routing, transparent passthrough, and
  count_tokens supported→passthrough with capability learning. Every package is
  now covered by tests.

### Changed
- The local count_tokens path now retries transient upstream failures (network
  errors and 5xx except 501) with exponential backoff. The streaming
  `/v1/messages` path is intentionally never retried.

## [0.1.0] - 2026-06-24

### Added
- Transparent reverse proxy to any Anthropic-compatible endpoint (LiteLLM,
  Bedrock gateway, `api.anthropic.com`) with streaming (SSE) flushes; every
  non-`count_tokens` request is forwarded byte-for-byte so client upgrades don't
  break the connection.
- Mutual TLS using a client certificate extracted from a password-protected
  PKCS#12 bundle, plus a CA bundle embedded into the binary via `go:embed`.
- Auto-detecting `count_tokens`: probes the upstream, caches the result, and
  rechecks every 6 hours and on every launch. Falls back to local counting with
  `ai-tokenizer` (a warm, self-healing Node worker pool) when unsupported.
- Per-request tokenizer model resolution, tolerant of vendor/region prefixes,
  date stamps, and Bedrock version suffixes.
- Dimension-based image token estimation (`width × height / 750`) with a flat
  fallback.
- `ccgate` commands: `run`, `claude`, `setup`, `doctor` (with a live upstream
  count_tokens probe), and `version`.
- Operator endpoints `/healthz` and `/_ccgate/status`.
- Cross-platform release tooling (GoReleaser) producing linux/darwin/windows
  binaries on amd64 and arm64.

[Unreleased]: https://github.com/hunchom/claude-code-gateway/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/hunchom/claude-code-gateway/releases/tag/v0.1.0
