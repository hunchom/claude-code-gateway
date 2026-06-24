# Security Policy

## Supported versions

| Version | Supported |
| ------- | --------- |
| 0.1.x   | ✅        |

## Reporting a vulnerability

Please report security issues privately via GitHub's
[security advisory](https://github.com/hunchom/claude-code-gateway/security/advisories/new)
form ("Report a vulnerability" under the **Security** tab). Do not open a public
issue for a suspected vulnerability.

Include reproduction steps, affected version/commit, and impact. You can expect
an initial response within a few days.

## Handling of secrets

This project is designed so credentials never leak:

- The PKCS#12 password is read **only** from the environment (`CCGW_P12_PASSWORD`)
  and is never written to disk, logs, or any committed file.
- `.gitignore` excludes certificate material (`*.p12`, `*.pem`, `*.key`, …),
  local config, and real `*.env` files.
- The reverse proxy forwards client credentials (`x-api-key`, `authorization`)
  to the configured upstream only; it adds no third-party destinations.
- mTLS verification uses the system trust store plus the embedded/extra CA
  bundle, with a TLS 1.2 minimum.

If you find a path where a secret could be exposed (a log line, an error
message, a committed artifact), treat it as a vulnerability and report it.
