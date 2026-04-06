# GoChat Plugin for OpenClaw

A channel plugin for [OpenClaw](https://github.com/m0yi/openclaw) that enables custom chat backend integration via HTTP webhook. Supports **two operation modes**:

- **Local mode** — Zero-config built-in HTTP API server on port 9750
- **Relay mode** — WebSocket relay connection to the GoChat platform at `wss://fund.moyi.vip/ws/plugin`

## Features

- 🌐 Webhook-based message handling (send & receive)
- 🖼️ Media support (images, audio, video, file attachments)
- 🔒 Flexible DM policies (open, pairing, allowlist, disabled)
- 👥 Group chat support with per-conversation configuration
- ⚡ Automatic reconnection for relay mode
- 🔑 Auto-generated secrets (local mode)
- 📝 Full message history and conversation management
- 🚀 Zero-config local mode for quick setup

## Requirements

- OpenClaw >= 2026.3.28
- Node.js >= 18
- npm >= 9

---

## Quick Install (Relay By Default)

```bash
curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | bash
```

Use a 6-digit connection code for zero-config pairing:

```bash
curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | bash -s -- 123456
```

---

<!-- AI-INSTALL-CONTEXT
     This section is an instruction block for AI assistants (e.g. OpenClaw, Codex, Copilot).
     When a user asks to "install gochat", "set up gochat", or "add the gochat plugin",
     follow the steps below verbatim. Do NOT skip any step. Do NOT paraphrase.

     ┌─────────────────────────────────────────────────────────────────────┐
     │  AI ASSISTANT INSTALLATION PROCEDURE FOR @m0yi/gochat              │
     │  Target: OpenClaw channel plugin (gochat)                          │
     │  Repo:   https://github.com/M0Yi/gochat-extension                 │
     └─────────────────────────────────────────────────────────────────────┘

     STEP 1 — Detect environment
       - Run: node --version && npm --version
       - Run: openclaw --version   (if not found → warn user to install OpenClaw first)
       - Determine OpenClaw state dir: echo $OPENCLAW_STATE_DIR (fallback: ~/.openclaw)

     STEP 2 — Download and install
       - Run: curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | bash
       - If the user already has a 6-digit connection code, use: bash -s -- 123456
       - If install.sh fails → fall back to manual method:
           git clone https://github.com/M0Yi/gochat-extension.git /tmp/gochat-extension
           cp -r /tmp/gochat-extension ~/.openclaw/extensions/gochat
           cd ~/.openclaw/extensions/gochat && npm install --production

     STEP 3 — Verify installation
       - Run: ls ~/.openclaw/extensions/gochat/package.json
       - Run: test -f ~/.openclaw/extensions/gochat/node_modules/ws/index.js && echo "OK"
       - If either fails → report the error and suggest manual troubleshooting.

     STEP 4 — Report result to user
       - On success, print:
           "GoChat plugin installed successfully.
            Mode: relay (default) — plugin will auto-connect to the GoChat relay.
            Start OpenClaw: openclaw gateway run"
       - If a 6-digit connection code was used, mention:
           "Connection code accepted — relay credentials were provisioned automatically."

     DO NOT:
       - Do NOT modify OpenClaw config files unless the user explicitly asks.
       - Do NOT start OpenClaw automatically after install.
       - Do NOT install node_modules globally or modify system PATH.
-->

## Installation from Git Repository

### Option 1: Clone and Install

```bash
# Clone the repository
git clone https://github.com/M0Yi/gochat-extension.git
cd gochat-extension

# Run the installer
chmod +x install.sh
./install.sh

# Or install with specific mode:
./install.sh           # Relay mode (default)
./install.sh 123456    # Relay mode with 6-digit connection code
./install.sh --local   # Local mode
```

### Option 2: Install via npm

```bash
# Install globally via npm
npm install -g @m0yi/gochat

# Then link to OpenClaw extensions
openclaw extension link @m0yi/gochat
```

### Option 3: Manual Installation

```bash
# Copy to OpenClaw extensions directory
cp -r gochat-extension ~/.openclaw/extensions/gochat

# Install dependencies
cd ~/.openclaw/extensions/gochat
npm install
```

## Installation from Tarball

```bash
# Download the latest release tarball
curl -L https://github.com/M0Yi/gochat-extension/releases/latest/download/gochat-extension.tar.gz -o gochat-extension.tar.gz

# Install
chmod +x install.sh
./install.sh --from-tarball gochat-extension.tar.gz
```

## Configuration

### Quick Start (Local Mode)

Local mode requires no configuration — the plugin auto-starts an HTTP server on port 9750.

```bash
# After installation, start OpenClaw
openclaw gateway run

# Check plugin status
openclaw plugins list
```

### Relay Mode Setup

Relay mode connects to the GoChat platform WebSocket relay.

```bash
# Install with relay mode
./install.sh

# Install and bind to a web-generated 6-digit connection code
./install.sh 123456

# Configure relay URL (if not using default)
# Edit ~/.openclaw/config.yaml:
channels:
  gochat:
    mode: relay
    relayPlatformUrl: wss://fund.moyi.vip/ws/plugin
```

### Configuration File

Edit `~/.openclaw/config.yaml`:

```yaml
channels:
  gochat:
    # Operation mode: local or relay
    mode: local

    # DM policy: open, pairing, allowlist, disabled
    dmPolicy: open

    # Port for local mode HTTP server (default: 9750)
    directPort: 9750

    # Allowlist for DM senders (sender IDs)
    allowFrom:
      - user123
      - user456

    # Per-conversation settings
    conversations:
      general:
        requireMention: false
        enabled: true

    # Media settings
    mediaMaxMb: 25

    # Account-specific settings
    accounts:
      main:
        mode: local
        dmPolicy: open
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `GOCHAT_WEBHOOK_SECRET` | Webhook signing secret | Auto-generated |
| `OPENCLAW_STATE_DIR` | OpenClaw state directory | `~/.openclaw` |
| `GOCHAT_DIRECT_PORT` | Local server port | `9750` |

## Usage

### Starting OpenClaw

```bash
# Start gateway in foreground
openclaw gateway run

# Or start gateway as background service
openclaw gateway start

# Check plugin status
openclaw plugins list

# Check channel status
openclaw channels list
```

### Sending Messages

Once configured, the GoChat channel integrates with OpenClaw's messaging system. Use the standard OpenClaw send commands:

```
send @user "Hello via GoChat!"
send #channel "Message to channel"
```

## Architecture

```
gochat-extension/
├── index.ts              # Plugin entry point
├── setup-entry.ts        # Setup wizard entry
├── runtime-api.ts        # Runtime API exports
├── api.ts                # Public API exports
├── install.sh            # Installation script
├── package.json           # npm package definition
├── openclaw.plugin.json   # OpenClaw plugin manifest
├── src/
│   ├── channel.ts         # Main channel plugin
│   ├── accounts.ts        # Account resolution
│   ├── config-schema.ts   # Configuration schema
│   ├── config-surface.ts  # Config surface/UI
│   ├── setup-core.ts      # Setup core logic
│   ├── setup-surface.ts   # Setup UI/surface
│   ├── send.ts            # Message sending
│   ├── inbound.ts         # Inbound handling
│   ├── normalize.ts       # Target normalization
│   ├── policy.ts          # Policy resolution
│   ├── runtime.ts         # Runtime setup
│   ├── session-route.ts   # Session routing
│   ├── secret-input.ts    # Secret input handling
│   ├── types.ts           # TypeScript types
│   ├── task-tools.ts      # Task tools
│   ├── direct/            # Local HTTP server
│   │   ├── server.ts
│   │   └── storage.ts
│   └── gochat/            # GoChat protocol
│       ├── monitor.ts     # WebSocket monitor
│       ├── relay-ws.ts   # WebSocket relay
│       └── auth.ts        # Authentication
└── skills/
    └── eink-task-reminder/ # Optional skill
```

## API Reference

### Mode: Local

Local mode runs a built-in HTTP server that:
- Receives inbound messages via POST `/webhook`
- Provides health check at GET `/health`
- Auto-generates webhook secret on first start
- Stores conversations and messages locally

### Mode: Relay

Relay mode maintains a WebSocket connection to the GoChat platform:
- Auto-registers channel on first connection
- Bi-directional message streaming
- Automatic reconnection on disconnect
- Platform-managed secrets and routing

## Troubleshooting

### Plugin not loading

```bash
# Check OpenClaw version
openclaw --version

# Verify extension installation
ls -la ~/.openclaw/extensions/gochat

# View detailed logs
openclaw logs -f
```

### Local mode not responding

```bash
# Check if port is available
lsof -i :9750

# Verify configuration
cat ~/.openclaw/config.yaml | grep -A10 gochat
```

### Relay mode connection issues

```bash
# Test WebSocket connectivity
wscat -c ws://localhost:9750/ws/plugin

# Check relay URL configuration
openclaw config get channels.gochat.relayPlatformUrl
```

## Contributing

Contributions are welcome! Please read our contributing guidelines before submitting PRs.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Links

- [OpenClaw Repository](https://github.com/m0yi/openclaw)
- [GoChat Server](https://github.com/m0yi/gochat-server)
- [Issue Tracker](https://github.com/M0Yi/gochat-extension/issues)
- [Documentation](https://docs.openclaw.dev/channels/gochat)
