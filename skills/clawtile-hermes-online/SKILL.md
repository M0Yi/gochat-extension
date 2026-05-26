---
name: clawtile-hermes-online
description: "Keep Hermes Agent connected to ClawTile online, process completed recordings, and write summaries back through ClawTile Agent REST/MCP endpoints. Use when the user asks to connect Hermes Agent, keep ClawTile online, summarize new ClawTile recordings automatically, or configure a pure-skills GoChat/ClawTile bridge without the OpenClaw TypeScript channel plugin."
---

# ClawTile Hermes Online

Use this skill when the user wants Hermes Agent to work with ClawTile without installing the OpenClaw TypeScript channel plugin.

This skill provides two integration paths:

- **MCP tool mode**: Hermes connects to ClawTile's `/api/agent/mcp` endpoint and can call `list_recordings`, `get_transcript`, `write_summary`, and `mark_summary_state` on demand.
- **Online bridge mode**: a small shell bridge keeps an SSE connection open to `/api/agent/events`, runs `hermes -z` for each completed recording, and writes the generated summary back to ClawTile.

## Requirements

- Hermes Agent installed and configured (`hermes --version` should work).
- A ClawTile agent bearer token (`ct_a_...`) from the ClawTile mini-program pairing flow.
- `curl`, `python3`, and a working network path to the ClawTile server.

Default server:

```bash
https://voinko.com
```

## MCP Tool Mode

Add the ClawTile Agent MCP endpoint to Hermes:

```bash
hermes mcp add clawtile-agent --url https://voinko.com/api/agent/mcp --auth header
```

When prompted for the API key / bearer token, paste the ClawTile agent token. Hermes stores it in `~/.hermes/.env` and writes the MCP server config to `~/.hermes/config.yaml`.

After adding it, start a new Hermes session and use the ClawTile MCP tools to process recordings end-to-end:

1. Call `list_recordings` with `status=completed` and `summary_state=none`.
2. For each result, call `get_transcript`.
3. Generate a concise Chinese summary and action items.
4. Call `write_summary` with `recording_id`, `summary`, `action_items`, `source_label`, and `model`.

Do not stop after listing or summarizing in chat. A ClawTile recording is only handled after `write_summary` succeeds.

## Online Bridge Mode

Run the bridge in the foreground:

```bash
CLAWTILE_TOKEN="ct_a_xxx" \
CLAWTILE_BASE="https://voinko.com" \
bash ~/.hermes/skills/clawtile-hermes-online/scripts/clawtile_hermes_bridge.sh
```

Useful environment variables:

- `CLAWTILE_TOKEN`: required bearer token.
- `CLAWTILE_BASE`: server base URL. May be either `https://host` or `https://host/api/agent`.
- `HERMES_BIN`: Hermes executable path, default `hermes`.
- `HERMES_MODEL`: optional model override passed to `hermes -z --model`.
- `BRIDGE_LOG`: log path, default `~/.clawtile-hermes-bridge.log`.
- `MAX_TRANSCRIPT_CHARS`: transcript limit passed into one Hermes run, default `20000`.

On macOS, install it as a LaunchAgent:

```bash
CLAWTILE_TOKEN="ct_a_xxx" \
bash ~/.hermes/skills/clawtile-hermes-online/scripts/install_launchd.sh
```

Then inspect logs:

```bash
tail -f ~/.clawtile-hermes-bridge.log
```

Unload the LaunchAgent:

```bash
launchctl unload -w ~/Library/LaunchAgents/com.clawtile.hermes-bridge.plist
```

## Summary Format

The bridge asks Hermes for strict JSON:

```json
{
  "summary": "中文纪要...",
  "action_items": [
    {
      "text": "事项",
      "owner": "",
      "due": "",
      "priority": "normal"
    }
  ]
}
```

If Hermes returns plain text instead of JSON, the bridge falls back to using the whole output as the summary and writes no action items.

## Guardrails

- Keep summaries derived from the fetched transcript, not from event titles alone.
- Mark a recording `failed` only after the transcript fetch, Hermes call, or summary write actually fails.
- Do not expose the bearer token in chat output or logs.
- Default output language is Chinese unless the transcript is clearly in another language.
