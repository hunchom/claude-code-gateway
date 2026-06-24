#!/usr/bin/env bash
# Wrapper for the launchd agent: load the .p12 password from the macOS Keychain
# (so it never lives in the plist) and exec the gateway.
#
# One-time: store the password in the Keychain with
#   security add-generic-password -a "$USER" -s ccgate-p12 -w
set -euo pipefail

if pw="$(security find-generic-password -a "$USER" -s ccgate-p12 -w 2>/dev/null)"; then
  export CCGW_P12_PASSWORD="$pw"
fi

exec "$HOME/.local/bin/ccgate" run
