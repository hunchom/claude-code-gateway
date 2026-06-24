# PROGRESS — claude-code-gateway

Living status file for the autonomous build loop. **Each loop fire: read this,
pick the top unchecked item under "Next", implement it, build+test, commit+push,
then update this file.** Keep the repo green (build + vet + gofmt + tests).

## Goal (from the operator)

A robust wrapper around Claude Code that:
1. Extracts `user-cert.pem` / `user-key.pem` from a password-protected `.p12`.
2. Has a baked-in TLS (CA) bundle.
3. Configurable API endpoint and model.
4. Forwards requests perfectly and as fast as possible.
5. Handles token counting perfectly, in full compliance with the Anthropic API spec.
6. Auto-detects whether the upstream implements `count_tokens`; if not, serves it
   locally. Caches the result, rechecks every 6h, and re-probes on every launch.
7. Stays robust across Claude Code upgrades (don't break the LiteLLM connection).
8. Published on a public GitHub profile.

## Status: MVP complete, building, smoke-tested, published

- `go build ./...` ✅  `go vet ./...` ✅  `gofmt -l` clean ✅  `go test ./...` ✅
- Unit tests: config precedence/validation, state round-trip, counttokens
  (convert + `decide()` state machine + path match), mtls (`.p12` round-trip
  proving the PKCS#8 extraction is a valid keypair).
- Local `count_tokens` smoke test ✅ → `{"input_tokens":19}` via the Node pool.
- mtls: replaced deprecated `pkcs12.ToPEM` with `DecodeChain` + PKCS#8 PEM (correct output).
- Per-request tokenizer model resolution (sidecar, data-driven against installed table).
- Published: https://github.com/hunchom/claude-code-gateway

## Architecture

- `main.go` — CLI: `run`, `claude`, `setup`, `doctor`, `version`. Embeds `certs/ca`.
- `internal/config` — defaults → YAML → `CCGW_*` env; validation. Secret (p12 pw) env-only.
- `internal/mtls` — `.p12` → `tls.Certificate` (go-pkcs12); PEM extraction; upstream `tls.Config`.
- `internal/state` — atomic JSON cache of learned upstream capability.
- `internal/proxy` — transparent `httputil.ReverseProxy` (Rewrite API, `FlushInterval=-1`
  for SSE, Host/SNI fix, no `X-Forwarded-*`). Routes `count_tokens` to the service.
- `internal/counttokens` — capability decide/probe/cache + Anthropic→ai-tokenizer
  conversion + warm Node worker pool. Pool is self-healing and **zombie-free**
  (each worker reaped by a `cmd.Wait()` goroutine; liveness via generation-scoped
  `atomic.Pointer[atomic.Bool]`).
- `internal/counttokens/sidecar.go` — embedded Node NDJSON sidecar (`ai-tokenizer`),
  written + `npm install`ed on first local count.

## Key decisions

- **Transparent by default**: only `/v1/messages/count_tokens` is ever inspected;
  everything else streams through untouched → upgrade-proof.
- **Probe on real traffic**: capability is learned from the first real request
  (correct model + auth), not a synthetic probe with a guessed model.
- **Lazy tokenizer pool**: Node is only required when the upstream lacks count_tokens.
- Module path `github.com/hunchom/claude-code-gateway`; binary `ccgate`; MIT.

## Next (prioritized — do the top item, then re-evaluate)

- [x] **Unit tests** — config, state, counttokens (convert + decide + path), mtls
      (p12 round-trip). CI runs `go test ./...`.
- [x] **Model→tokenizer-key mapping** — per-request resolution in the sidecar
      against the installed ai-tokenizer table (tolerant of vendor/region
      prefixes, date stamps, bedrock `-v1:0` suffixes); falls back to
      `tokenizer_model`. Smoke-tested across 6 id shapes.
- [x] **`/healthz` + `/_ccgate/status`** — liveness + JSON status (mode, upstream,
      learned capability, checked_at). Outside the Anthropic namespace; tested.
- [x] **Live `doctor` probe** — `doctor [--model]` sends a real count_tokens
      request upstream and classifies it via the shared `Classify` (read-only,
      no cache mutation). Auth from `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`.
- [x] **Image token accuracy** — images estimated via width*height/750 from
      decoded dimensions (png/jpeg/gif); flat `image_tokens` as fallback. Tested.
- [x] **Release engineering** — `.goreleaser.yaml` (6 targets), `release` workflow
      on tag push, CI/release/license/godoc badges in README, `v0.1.0` tagged.
      Cross-compile validated for all targets.
- [x] **Service units** — hardened `systemd` unit (DynamicUser, StateDirectory,
      root-only env file) + `launchd` agent (Keychain password wrapper) in
      `deploy/`, with a deploy guide. plist + wrapper validated.
- [x] **Passthrough resilience** — count_tokens forward/probe retries transient
      failures (network + 5xx except 501) with exponential backoff; safe because
      the body is buffered + idempotent. Streaming `/v1/messages` intentionally
      not retried. Tested (retry-to-success + no-retry-on-501).

### Wave 2 (initial roadmap complete — keep perfecting)

- [x] **Proxy integration tests** — `internal/proxy` covered: `/healthz`,
      `/_ccgate/status`, transparent passthrough (path/query/method/body/headers),
      and count_tokens supported→passthrough with capability learning. Every
      package now has tests. (missing→local needs Node; in the smoke test.)
- [x] **Repo polish for public** — `CHANGELOG.md` (Keep a Changelog), `SECURITY.md`
      (private advisory reporting + no-secrets posture), `CONTRIBUTING.md`
      (build/test/PR flow), linked from README.
- [ ] **Container image** — Dockerfile (distroless/static) + wire into GoReleaser
      so tagged releases also publish an image. Node optional layer for local mode.
      (Deferred: least aligned with the local-launcher use; needs node-in-image for
      local counting + multi-arch CI that can't be validated on this box. Do last.)
- [x] **Config polish** — `count_timeout` (CCGW_COUNT_TIMEOUT) drives the upstream
      count_tokens client; optional `model_map` (YAML) overrides request-model →
      ai-tokenizer-key for custom LiteLLM aliases. Tested.
- [x] **setup/doctor cert detail** — `doctor` and `setup` print the client cert
      subject + NotAfter, warning when expired or expiring within 14 days.
- [x] **Fuzz tests** — `FuzzConvertToSDK` hardens the count_tokens conversion
      (2.9M execs, zero panics). The model-id normalizer lives in the JS sidecar
      (not Go), so it isn't Go-fuzzable here.

### Wave 3 (robustness)

- [x] **Never hard-fail count_tokens** — when the local tokenizer is unavailable
      (no Node) or a count errors, return a conservative heuristic estimate
      (`X-Ccgate-Count: heuristic`) instead of an error, so a session never breaks.
- [ ] **Calibration** — `ccgate calib --model M` compares local counts against an
      upstream that implements count_tokens (e.g. api.anthropic.com) over sample
      payloads and reports mean/max % error, so the operator can trust the estimate.
- [ ] **Richer status** — add tokenizer pool readiness + Node presence to
      `/_ccgate/status` and `doctor`.
- [ ] **Container image** — still deferred; see note above.

## How to build / test

```sh
make build            # -> ./ccgate
go test ./...
# local count smoke test:
CCGW_COUNT_TOKENS=local CCGW_UPSTREAM=http://127.0.0.1:9 CCGW_LISTEN=127.0.0.1:8799 ./ccgate run &
curl -s -XPOST localhost:8799/v1/messages/count_tokens -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}'
```
