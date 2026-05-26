#!/usr/bin/env bash
set -euo pipefail

: "${CLAWTILE_TOKEN:?CLAWTILE_TOKEN env var is required (Bearer ct_a_xxx)}"
: "${CLAWTILE_BASE:=https://voinko.com}"
: "${HERMES_BIN:=hermes}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "install_launchd.sh only supports macOS launchd. Run clawtile_hermes_bridge.sh directly on this platform." >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BRIDGE_SCRIPT="${SCRIPT_DIR}/clawtile_hermes_bridge.sh"
HERMES_PATH="$(command -v "$HERMES_BIN" 2>/dev/null || true)"
if [[ -z "$HERMES_PATH" ]]; then
  HERMES_PATH="$HERMES_BIN"
fi

PLIST_DIR="${HOME}/Library/LaunchAgents"
PLIST_PATH="${PLIST_DIR}/com.clawtile.hermes-bridge.plist"
mkdir -p "$PLIST_DIR"

cat > "$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.clawtile.hermes-bridge</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>${BRIDGE_SCRIPT}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>${HOME}</string>
    <key>PATH</key>
    <string>${HOME}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>HERMES_BIN</key>
    <string>${HERMES_PATH}</string>
    <key>CLAWTILE_TOKEN</key>
    <string>${CLAWTILE_TOKEN}</string>
    <key>CLAWTILE_BASE</key>
    <string>${CLAWTILE_BASE}</string>
    <key>BRIDGE_LOG</key>
    <string>${HOME}/.clawtile-hermes-bridge.log</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
    <key>NetworkState</key>
    <true/>
  </dict>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>${HOME}/.clawtile-hermes-bridge.stdout</string>
  <key>StandardErrorPath</key>
  <string>${HOME}/.clawtile-hermes-bridge.stderr</string>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
EOF

launchctl unload -w "$PLIST_PATH" >/dev/null 2>&1 || true
launchctl load -w "$PLIST_PATH"
echo "Installed and started ${PLIST_PATH}"
echo "Logs: ${HOME}/.clawtile-hermes-bridge.log"
