# GoChat Plugin Installed

Enable it if you did not install with `--enable`:

```bash
hermes plugins enable gochat
```

Then configure the ClawTile Agent MCP endpoint:

```bash
hermes gochat mcp-configure
```

If you have a 6-digit ClawTile mini-program pairing code, use it directly:

```bash
hermes gochat mcp-configure --code 123456 --server https://voinko.com
```

For always-on processing of completed ClawTile recordings:

```bash
hermes gochat bridge-run
```

On macOS:

```bash
hermes gochat bridge-install-launchd
```

Restart Hermes or the Hermes gateway after enabling the plugin.
