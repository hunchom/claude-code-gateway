# Running `ccgate` as a service

Example unit files for keeping the gateway running in the background. Both keep
the `.p12` password out of version control and out of the unit file itself.

## Linux (systemd)

```sh
# 1. Install the binary and config
sudo install -m 0755 ccgate /usr/local/bin/ccgate
sudo install -d /etc/ccgate
sudo install -m 0644 config.example.yaml /etc/ccgate/config.yaml   # then edit
sudo install -m 0600 deploy/systemd/ccgate.env.example /etc/ccgate/ccgate.env  # then edit

# 2. Install and start the unit
sudo install -m 0644 deploy/systemd/ccgate.service /etc/systemd/system/ccgate.service
sudo systemctl daemon-reload
sudo systemctl enable --now ccgate.service
systemctl status ccgate.service
journalctl -u ccgate -f
```

Notes:
- The unit runs as a `DynamicUser` with a private state dir at `/var/lib/ccgate`
  (holds `state.json` and the auto-installed `ai-tokenizer` sidecar).
- The `.p12` must be readable by the service (e.g. `/etc/ccgate/client.p12`).
- Local token counting needs `node`/`npm` available and outbound network on
  first run (to install `ai-tokenizer`). Not needed if the upstream implements
  `count_tokens`.

## macOS (launchd)

```sh
# 1. Install the binary and wrapper
install -d ~/.local/bin
make build && install -m 0755 ccgate ~/.local/bin/ccgate
install -m 0755 deploy/launchd/ccgate-launchd.sh ~/.local/bin/ccgate-launchd.sh

# 2. Store the .p12 password in the Keychain (kept out of the plist)
security add-generic-password -a "$USER" -s ccgate-p12 -w

# 3. Configure
mkdir -p ~/.config/claude-code-gateway
cp config.example.yaml ~/.config/claude-code-gateway/config.yaml   # then edit

# 4. Install the agent (replace YOURUSER first)
sed "s/YOURUSER/$USER/g" deploy/launchd/com.hunchom.ccgate.plist \
  > ~/Library/LaunchAgents/com.hunchom.ccgate.plist
launchctl load ~/Library/LaunchAgents/com.hunchom.ccgate.plist
tail -f ~/Library/Logs/ccgate.log
```

Unload with `launchctl unload ~/Library/LaunchAgents/com.hunchom.ccgate.plist`.

## Pointing Claude Code at the service

With the gateway listening on its configured `listen` address:

```sh
export ANTHROPIC_BASE_URL="http://127.0.0.1:8787"
claude
```

(Or just use `ccgate claude …`, which sets this for you and does not require a
long-running service.)
