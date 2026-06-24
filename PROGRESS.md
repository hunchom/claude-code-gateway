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

- `go build ./...` ✅  `go vet ./...` ✅  `gofmt -l` clean ✅
- Local `count_tokens` smoke test ✅ → `{"input_tokens":19}` via the Node pool.
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

- [ ] **Unit tests** — `convert.go` (text/array/tool_use/tool_result/image/pdf/system),
      `config` precedence, `state` round-trip, `Service.decide()` state machine,
      `IsCountTokensPath`. CI already runs `go test ./...`.
- [ ] **Model→tokenizer-key mapping** — map the request's `model` to the right
      ai-tokenizer key (fall back to `tokenizer_model`) so mixed-model sessions
      count correctly. Beware Fable-class new tokenizers (~30% more tokens).
- [ ] **`/healthz` + `/_ccgate/status`** — liveness and a JSON status (mode,
      learned capability, checked_at) without touching Anthropic routes.
- [ ] **Live `doctor` probe** — actually call upstream `count_tokens` and report
      supported/unsupported.
- [ ] **Image token accuracy** — estimate from decoded image dimensions
      (`(w*h)/750`, capped) instead of a flat rate.
- [ ] **Release engineering** — GoReleaser config, cross-compiled binaries,
      `v0.1.0` tag + GitHub release, CI badge in README.
- [ ] **Service units** — `launchd` plist + `systemd` unit examples for `ccgate run`.
- [ ] **Passthrough resilience** — short retry/backoff on transient upstream
      errors before failing a `count_tokens` forward.

## How to build / test

```sh
make build            # -> ./ccgate
go test ./...
# local count smoke test:
CCGW_COUNT_TOKENS=local CCGW_UPSTREAM=http://127.0.0.1:9 CCGW_LISTEN=127.0.0.1:8799 ./ccgate run &
curl -s -XPOST localhost:8799/v1/messages/count_tokens -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}'
```
