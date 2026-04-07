import {
  resolveLoggerBackedRuntime,
  type RuntimeEnv,
} from "openclaw/plugin-sdk/extension-shared";
import { resolveGoChatAccount, listGoChatAccountIds } from "../accounts.js";
import { handleGoChatInbound } from "../inbound.js";
import { getGoChatRuntime } from "../runtime.js";
import { createRelayWSConnection } from "./relay-ws.js";
import { setRelayStatusReporter, setRelayWsSender } from "../send.js";
import type {
  CoreConfig,
  GoChatInboundMessage,
} from "../types.js";

import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

let _pluginVersion: string | null = null;
const ACTIVE_STATUS_REFRESH_MS = 10_000;

function getPluginVersion(): string {
  if (_pluginVersion) return _pluginVersion;
  try {
    const manifestPath = resolve(dirname(fileURLToPath(import.meta.url)), "../../package.json");
    const raw = readFileSync(manifestPath, "utf-8");
    _pluginVersion = JSON.parse(raw).version ?? "unknown";
  } catch {
    _pluginVersion = "unknown";
  }
  return _pluginVersion;
}

function getRuntimePlatform(): string {
  try {
    const unameOut: string = process.platform;
    switch (unameOut) {
      case "darwin": return "macos";
      case "linux": {
        try {
          const versionFile = readFileSync("/proc/version", "utf-8");
          if (versionFile.toLowerCase().includes("microsoft")) return "linux-wsl";
        } catch { /* not WSL */ }
        return "linux";
      }
      case "win32": return "windows";
      default: return unameOut;
    }
  } catch {
    return "unknown";
  }
}

function getRuntimeArch(): string {
  return process.arch;
}

function getNodeVersion(): string {
  return process.version;
}

export async function monitorGoChatProvider(
  opts: {
    accountId?: string;
    config?: CoreConfig;
    runtime?: RuntimeEnv;
    abortSignal?: AbortSignal;
    onMessage?: (message: GoChatInboundMessage) => void | Promise<void>;
    statusSink?: (patch: { lastInboundAt?: number; lastOutboundAt?: number }) => void;
  } = {},
): Promise<{ stop: () => void }> {
  const core = getGoChatRuntime();
  const cfg = opts.config ?? (core.config.loadConfig() as CoreConfig);
  const account = resolveGoChatAccount({
    cfg,
    accountId: opts.accountId,
  });
  const runtime: RuntimeEnv = resolveLoggerBackedRuntime(
    opts.runtime,
    core.logging.getChildLogger(),
  );

  if (!account.secret) {
    throw new Error(`GoChat secret not configured for relay account "${account.accountId}"`);
  }

  const logger = core.logging.getChildLogger({
    channel: "gochat",
    accountId: account.accountId,
  });

  const startedAt = Date.now();
  let activeJobs = 0;
  let transientStatus: "syncing" | "error" | null = null;
  let transientUntil = 0;
  let transientTimer: ReturnType<typeof setTimeout> | null = null;
  let activeStatusTimer: ReturnType<typeof setInterval> | null = null;

  const clearTransientTimer = (): void => {
    if (transientTimer) {
      clearTimeout(transientTimer);
      transientTimer = null;
    }
  };

  const stopActiveStatusPulse = (): void => {
    if (activeStatusTimer) {
      clearInterval(activeStatusTimer);
      activeStatusTimer = null;
    }
  };

  const resolveStatus = (): "idle" | "executing" | "syncing" | "error" => {
    const now = Date.now();
    if (transientStatus && transientUntil > now) {
      return transientStatus;
    }
    if (activeJobs > 0) {
      return "executing";
    }
    return "idle";
  };

  let lastReportedStatus = resolveStatus();

  let pushRelayStatusNow = (): void => {};

  const ensureActiveStatusPulse = (): void => {
    if (activeJobs <= 0 || activeStatusTimer) {
      return;
    }
    activeStatusTimer = setInterval(() => {
      if (activeJobs <= 0) {
        stopActiveStatusPulse();
        return;
      }
      pushRelayStatusNow();
    }, ACTIVE_STATUS_REFRESH_MS);
  };

  const scheduleStatusReset = (): void => {
    clearTransientTimer();
    if (!transientStatus) {
      return;
    }
    const delay = Math.max(0, transientUntil - Date.now());
    transientTimer = setTimeout(() => {
      transientStatus = null;
      transientUntil = 0;
      publishStatus();
    }, delay);
  };

  const publishStatus = (): void => {
    const nextStatus = resolveStatus();
    if (nextStatus === lastReportedStatus) {
      return;
    }
    lastReportedStatus = nextStatus;
    pushRelayStatusNow();
  };

  const setTransientStatus = (
    status: "syncing" | "error",
    ttlMs: number,
  ): void => {
    transientStatus = status;
    transientUntil = Date.now() + ttlMs;
    scheduleStatusReset();
    publishStatus();
  };

  const beginJob = (): void => {
    activeJobs += 1;
    ensureActiveStatusPulse();
    publishStatus();
  };

  const finishJob = (): void => {
    activeJobs = Math.max(0, activeJobs - 1);
    if (activeJobs === 0) {
      stopActiveStatusPulse();
    }
    publishStatus();
  };

  const { start: startRelay, stop: stopRelay, send: sendRelay, sendStatusNow } = createRelayWSConnection({
    platformUrl: account.relayPlatformUrl,
    channelId: account.channelId,
    secret: account.secret,
    onMessage: async (message) => {
      core.channel.activity.record({
        channel: "gochat",
        accountId: account.accountId,
        direction: "inbound",
        at: message.timestamp ?? Date.now(),
      });
      if (opts.onMessage) {
        await opts.onMessage(message);
        return;
      }
      beginJob();
      try {
        await handleGoChatInbound({
          message,
          account,
          config: cfg,
          runtime,
          statusSink: opts.statusSink,
        });
      } finally {
        finishJob();
      }
    },
    onError: (error) => {
      setTransientStatus("error", 8_000);
      logger.error(`[gochat:${account.accountId}] relay error: ${error.message}`);
    },
    abortSignal: opts.abortSignal,
    statusProvider: () => {
      const accountIds = listGoChatAccountIds(cfg);
      return {
        type: "plugin",
        version: getPluginVersion(),
        agentCount: accountIds.length,
        status: resolveStatus(),
        uptime: Math.floor((Date.now() - startedAt) / 1000),
        metadata: {
          openclawVersion: core.version,
          pluginVersion: getPluginVersion(),
          accountId: account?.accountId || "default",
          platform: `${getRuntimePlatform()} (${getRuntimeArch()})`,
          nodeVersion: getNodeVersion(),
        },
      };
    },
  });

  if (opts.abortSignal?.aborted) {
    return { stop: stopRelay };
  }

  pushRelayStatusNow = sendStatusNow;
  setRelayStatusReporter((status) => {
    if (status === "error") {
      setTransientStatus("error", 8_000);
      return;
    }
    if (status === "syncing") {
      setTransientStatus("syncing", 3_000);
      return;
    }
    transientStatus = null;
    transientUntil = 0;
    clearTransientTimer();
    publishStatus();
  });
  setRelayWsSender(sendRelay);
  await startRelay();
  logger.info(
    `[gochat:${account.accountId}] relay connected to ${account.relayPlatformUrl}`,
  );

  return {
    stop: () => {
      clearTransientTimer();
      stopActiveStatusPulse();
      setRelayStatusReporter(null);
      setRelayWsSender(null);
      stopRelay();
    },
  };
}
