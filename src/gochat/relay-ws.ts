import crypto from "node:crypto";

import type { WebSocket as WS } from "ws";

export interface RelayStatusPayload {
  type: string;
  version: string;
  agentCount: number;
  status: string;
  uptime: number;
  metadata?: Record<string, string>;
}

export interface RelayWSOptions {
  platformUrl: string;
  channelId: string;
  secret: string;
  onMessage: (message: any) => void | Promise<void>;
  onError?: (error: Error) => void;
  abortSignal?: AbortSignal;
  statusProvider?: () => RelayStatusPayload | null;
}

type WSInstance = InstanceType<typeof WS>;

const MAX_BACKOFF_MS = 30_000;
const INITIAL_BACKOFF_MS = 1_000;
const PING_INTERVAL_MS = 30_000;

function computeHmacSignature(secret: string, channelId: string, ts: number): string {
  const payload = `${ts}.${channelId}`;
  return crypto.createHmac("sha256", secret).update(payload).digest("hex");
}

export function createRelayWSConnection(opts: RelayWSOptions): {
  start: () => Promise<void>;
  send: (data: any) => void;
  sendStatusNow: () => void;
  stop: () => void;
} {
  const { platformUrl, channelId, secret, onMessage, onError, abortSignal, statusProvider } = opts;

  let ws: WSInstance | null = null;
  let stopped = false;
  let backoffMs = INITIAL_BACKOFF_MS;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let pingTimer: ReturnType<typeof setTimeout> | null = null;

  function log(level: "info" | "warn" | "error", msg: string): void {
    const prefix = `[gochat:relay:${channelId}]`;
    if (level === "error") {
      console.error(prefix, msg);
    } else if (level === "warn") {
      console.warn(prefix, msg);
    } else {
      console.log(prefix, msg);
    }
  }

  function cleanup(): void {
    if (pingTimer) {
      clearInterval(pingTimer);
      pingTimer = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  }

  function stop(): void {
    if (stopped) return;
    stopped = true;
    cleanup();
    if (ws) {
      try {
        ws.close(1000, "shutdown");
      } catch {
        // ignore
      }
      ws = null;
    }
  }

  function send(data: any): void {
    if (!ws || ws.readyState !== ws.OPEN) {
      log("warn", `send called but WebSocket not open (readyState=${ws?.readyState}), dropping: type=${typeof data === "object" ? data?.type : "unknown"}`);
      return;
    }
    try {
      const payload = typeof data === "string" ? data : JSON.stringify(data);
      ws.send(payload);
      const dataType = typeof data === "object" ? data?.type : "unknown";
      log("info", `sent to server: type=${dataType} conv=${typeof data === "object" ? data?.conversationId : "?"} text="${(typeof data === "object" ? data?.text : "").substring(0, 80)}"`);
    } catch (err) {
      log("error", `send failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  function sendStatusNow(): void {
    if (!ws || ws.readyState !== ws.OPEN || !statusProvider) {
      return;
    }
    try {
      const sp = statusProvider();
      if (!sp) {
        return;
      }
      ws.send(JSON.stringify({
        type: "status",
        clientType: sp.type,
        version: sp.version,
        agentCount: sp.agentCount,
        status: sp.status,
        uptime: sp.uptime,
        metadata: sp.metadata,
      }));
      log("info", `status pushed: state=${sp.status} agents=${sp.agentCount}`);
    } catch (err) {
      log("warn", `status push failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  async function doConnect(): Promise<void> {
    const WebSocket = (await import("ws")).default;
    const ts = Math.floor(Date.now() / 1000);
    const sig = computeHmacSignature(secret, channelId, ts);
    const separator = platformUrl.includes("?") ? "&" : "?";
    const url = `${platformUrl}${separator}channelId=${encodeURIComponent(channelId)}&ts=${ts}&sig=${sig}`;

    return new Promise<void>((resolve, reject) => {
      if (stopped || abortSignal?.aborted) {
        resolve();
        return;
      }

      const socket = new WebSocket(url) as WSInstance;
      ws = socket;

      socket.on("open", () => {
        backoffMs = INITIAL_BACKOFF_MS;
        log("info", `connected to ${platformUrl}`);
        sendStatusNow();
        resolve();
      });

      socket.on("message", (raw: Buffer | ArrayBuffer | Buffer[], isBinary: boolean) => {
        try {
          const text = typeof raw === "string"
            ? raw
            : Buffer.isBuffer(raw)
              ? raw.toString("utf-8")
              : Array.isArray(raw)
                ? Buffer.concat(raw).toString("utf-8")
                : new TextDecoder().decode(raw as ArrayBuffer);
          const parsed = JSON.parse(text);

          if (parsed.type === "message") {
            log("info", `recv message: conv=${parsed.conversationId || "default"} text="${(parsed.text || "").substring(0, 60)}..."`);
            // Detach message handling from the WS read loop so long-running
            // model execution or block streaming does not starve ping/status traffic.
            void Promise.resolve()
              .then(() => onMessage(parsed))
              .catch((err) => {
                onError?.(err instanceof Error ? err : new Error(String(err)));
              });
          } else if (parsed.type === "runtime.refresh") {
            log("info", `recv runtime refresh request: reason=${parsed.reason || "manual"}`);
            sendStatusNow();
          } else if (parsed.type === "reply") {
            log("info", `recv reply: conv=${parsed.conversationId || "default"} text="${(parsed.text || "").substring(0, 60)}..."`);
          } else if (parsed.type === "pong") {
            // heartbeat acknowledged
          } else if (parsed.type === "error") {
            log("error", `server error: ${parsed.text || parsed.error || JSON.stringify(parsed)}`);
          }
        } catch (err) {
          log("warn", `failed to parse incoming message: ${err instanceof Error ? err.message : String(err)}`);
        }
      });

      socket.on("error", (err: Error) => {
        onError?.(err);
        log("error", `WebSocket error: ${err.message}`);
      });

      socket.on("close", (code: number, reason: Buffer) => {
        ws = null;
        cleanup();
        if (stopped || abortSignal?.aborted) return;
        log("warn", `WebSocket closed (code=${code}), reconnecting in ${backoffMs}ms`);
        reconnectTimer = setTimeout(() => {
          if (stopped || abortSignal?.aborted) return;
          void doConnect().catch((err) => {
            log("error", `reconnect failed: ${err instanceof Error ? err.message : String(err)}`);
          });
        }, backoffMs);
        backoffMs = Math.min(backoffMs * 2, MAX_BACKOFF_MS);
      });
    });
  }

  async function start(): Promise<void> {
    if (abortSignal) {
      if (abortSignal.aborted) {
        stop();
        return;
      }
      abortSignal.addEventListener("abort", stop, { once: true });
    }

    if (!platformUrl) {
      throw new Error("relayPlatformUrl is required for relay mode");
    }
    if (!channelId) {
      throw new Error("channelId is required for relay mode");
    }
    if (!secret) {
      throw new Error("secret is required for relay mode");
    }

    await doConnect();

    pingTimer = setInterval(() => {
      if (stopped || abortSignal?.aborted) {
        clearInterval(pingTimer);
        pingTimer = null;
        return;
      }
      if (!ws || ws.readyState !== ws.OPEN) {
        log("warn", "ping skipped — WebSocket not open");
        return;
      }
      try {
        ws.send(JSON.stringify({ type: "ping" }));
        sendStatusNow();
      } catch (err) {
        log("warn", `ping failed: ${err instanceof Error ? err.message : String(err)}`);
      }
    }, PING_INTERVAL_MS);
  }

  return { start, send, sendStatusNow, stop };
}
