# claude-code-gateway (`ccgate`)

A transparent gateway and launcher that lets [Claude Code](https://docs.anthropic.com/en/docs/claude-code) talk to **any Anthropic-compatible endpoint** — a self-hosted [LiteLLM](https://github.com/BerriAI/litellm) proxy, an Amazon Bedrock gateway, or `api.anthropic.com` — over **mutual TLS**, while transparently filling in the `count_tokens` endpoint when the upstream doesn't implement it.

It is built to be **invisible** and **upgrade-proof**: every request is forwarded byte-for-byte, so new fields and behaviours introduced by future Claude Code releases pass straight through. The only request it ever inspects is `/v1/messages/count_tokens`.

```
  Claude Code ──HTTP──▶  ccgate  ──mTLS / HTTP2──▶  LiteLLM / Bedrock / api.anthropic.com
                          │
                          └─ /v1/messages/count_tokens
                               ├─ upstream supports it?  → forward
                               └─ upstream missing it?   → count locally (ai-tokenizer)
```

## Why

Some gateways (notably LiteLLM in front of Bedrock) proxy `/v1/messages` but **do not implement `/v1/messages/count_tokens`**. Claude Code calls that endpoint constantly to manage its context window and trigger auto-compaction. Without it, the client degrades or errors.

`ccgate` solves three problems at once:

1. **mTLS** — extract a client certificate from a password-protected `.p12` and present it upstream, with a CA bundle baked into the binary.
2. **Token counting** — auto-detect whether the upstream supports `count_tokens`; if not, answer locally with [`ai-tokenizer`](https://www.npmjs.com/package/ai-tokenizer) and return the spec-compliant `{"input_tokens": N}`.
3. **Robustness across upgrades** — forward everything else untouched, so upgrading Claude Code never breaks the connection to your endpoint.

## Features

- **Transparent reverse proxy** with streaming (SSE) flushes — no buffering, no header rewriting, no `X-Forwarded-*`.
- **Mutual TLS** from a password-protected PKCS#12 bundle (modern cipher support via `go-pkcs12`).
- **Embedded CA bundle** — drop a `*.pem` into `certs/ca/` and rebuild to trust a private CA with zero host configuration.
- **Auto-detecting `count_tokens`** — probes the upstream, caches the result to disk, rechecks every 6 hours and on every launch.
- **Local token counting** via a warm pool of `ai-tokenizer` Node workers (parallel, self-healing, zombie-free).
- **Single static binary** plus a small Node sidecar that is installed automatically on first use.
- **`ccgate claude …`** launches Claude Code with `ANTHROPIC_BASE_URL` already pointed at the gateway.

## Install

```sh
go install github.com/hunchom/claude-code-gateway@latest   # installs as "claude-code-gateway"
# or build locally:
git clone https://github.com/hunchom/claude-code-gateway
cd claude-code-gateway
make build           # produces ./ccgate
make install         # copies to ~/.local/bin/ccgate
```

Requirements: Go 1.23+ to build; Node.js + npm on the host **only** if the upstream lacks `count_tokens` (the sidecar installs `ai-tokenizer` on first local count).

## Quick start

```sh
# 1. Configure (env vars shown; a YAML file works too — see config.example.yaml)
export CCGW_UPSTREAM="https://litellm.internal.example.com"
export CCGW_P12_PATH="$HOME/secure/client.p12"
export CCGW_P12_PASSWORD="••••••••"          # from your password manager; never committed

# 2. (Optional) sanity-check config, certificate, and connectivity
ccgate doctor

# 3. Launch Claude Code through the gateway
ccgate claude
```

`ccgate claude` binds the proxy, sets `ANTHROPIC_BASE_URL` for the child process, runs `claude` with any arguments you pass, and tears everything down when Claude exits.

Prefer to run the gateway as a long-lived service and point Claude Code at it yourself?

```sh
ccgate run &
export ANTHROPIC_BASE_URL="http://127.0.0.1:8787"
claude
```

## How `count_tokens` detection works

| State (cached in `state.json`) | Behaviour |
| --- | --- |
| `auto`, unknown / stale | **Probe**: forward the real request upstream. `200` with `input_tokens` ⇒ mark *supported*; `404/405/501` or `not_found_error` ⇒ mark *unsupported* and count locally. Ambiguous (auth/5xx/network) ⇒ count locally for this request without caching. |
| `auto`, supported (fresh) | Forward upstream. |
| `auto`, unsupported (fresh) | Count locally. |
| `local` | Always count locally. |
| `passthrough` | Always forward upstream. |

The cached answer is rechecked every `recheck_hours` (default 6) and forcibly re-probed on every `ccgate` launch, so a newly-deployed upstream `count_tokens` is picked up automatically.

### Accuracy

Local counting uses `ai-tokenizer`, which reproduces the model's BPE encoding and adds per-message/-tool structural overhead. The tokenizer is selected **per request** from the request's `model` field, matched against the installed `ai-tokenizer` model table — tolerant of vendor/region prefixes (`us.anthropic.…`), date stamps (`…-20251001`), and Bedrock version suffixes (`…-v1:0`) — and falls back to `tokenizer_model` when the model can't be resolved. It is an excellent estimate for context-window management and auto-compaction triggers, but is not guaranteed to be byte-identical to Anthropic's server-side count. Non-text blocks (images, PDFs) are estimated with the configurable `image_tokens` / `pdf_tokens` flat rates. When the upstream supports `count_tokens`, that exact count is used instead.

## Configuration

Resolved in increasing precedence: **defaults → YAML file (`--config`) → `CCGW_*` environment variables**.

| YAML key | Env var | Default | Meaning |
| --- | --- | --- | --- |
| `listen` | `CCGW_LISTEN` | `127.0.0.1:8787` | Local address Claude Code connects to |
| `upstream` | `CCGW_UPSTREAM` | `https://api.anthropic.com` | Endpoint to forward to |
| `tokenizer_model` | `CCGW_TOKENIZER_MODEL` | `anthropic/claude-sonnet-4.5` | Fallback `ai-tokenizer` key when a request's model can't be resolved |
| `p12_path` | `CCGW_P12_PATH` | — | Client certificate bundle |
| `p12_password` | `CCGW_P12_PASSWORD` | — | `.p12` password (**env only**, never serialized) |
| `ca_bundle` | `CCGW_CA_BUNDLE` | — | Extra CA PEM file (added to the embedded bundle) |
| `count_tokens` | `CCGW_COUNT_TOKENS` | `auto` | `auto` \| `local` \| `passthrough` |
| `recheck_hours` | `CCGW_RECHECK_HOURS` | `6` | Capability recheck cadence |
| `tokenizer_pool` | `CCGW_TOKENIZER_POOL` | `4` | Node worker count |
| `image_tokens` | `CCGW_IMAGE_TOKENS` | `1600` | Flat estimate per image block |
| `pdf_tokens` | `CCGW_PDF_TOKENS` | `3000` | Flat estimate per PDF block |

See [`config.example.yaml`](./config.example.yaml).

## Commands

```
ccgate run                 Run the gateway in the foreground
ccgate claude [args...]    Launch Claude Code through the gateway
ccgate setup               Extract user-cert.pem / user-key.pem from a .p12
ccgate doctor              Diagnose configuration, certificate, and connectivity
ccgate version             Print version
```

## Security

- Secrets (`.p12` password) are accepted **only** from the environment and are never written to disk or logs.
- `.gitignore` excludes `*.p12`, `*.pem`, `*.key`, and local config so certificate material never lands in version control.
- mTLS verification uses the system trust store plus the embedded/extra CA bundle; TLS 1.2 minimum.
- The proxy adds no identifying headers — upstream sees what Claude Code sent.

## License

MIT — see [LICENSE](./LICENSE).
