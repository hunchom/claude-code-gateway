# Contributing

Thanks for your interest in improving `ccgate`.

## Development

Requirements: Go 1.23+ (built with 1.26). Node.js + npm are only needed to
exercise the local token-counting path.

```sh
git clone https://github.com/hunchom/claude-code-gateway
cd claude-code-gateway
make build      # -> ./ccgate
go test ./...
go vet ./...
gofmt -l .      # must print nothing
```

CI runs the same checks (`gofmt`, `go vet`, `go build`, `go test`) and must pass.

## Guidelines

- Keep the proxy **transparent**: only `/v1/messages/count_tokens` and the
  operator routes (`/healthz`, `/_ccgate/status`) are ever inspected. Everything
  else must be forwarded unchanged so Claude Code upgrades never break the
  connection. Add tests when you touch routing or forwarding.
- **Never** commit secrets or certificate material. The `.p12` password is read
  only from `CCGW_P12_PASSWORD`; tests must not embed real credentials.
- Match the existing style; run `gofmt`. Add or update tests for behavior changes.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for messages
  (`feat:`, `fix:`, `test:`, `docs:`, `build:`, …).
- Keep pull requests focused and describe the motivation.

## Releasing

Releases are cut by pushing a `vX.Y.Z` tag; the `release` workflow runs GoReleaser
to build and publish cross-platform artifacts. Update `CHANGELOG.md` first.
