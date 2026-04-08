import {
  resolveLoggerBackedRuntime,
  type RuntimeEnv,
} from "openclaw/plugin-sdk/extension-shared";
import { resolveGoChatAccount, listGoChatAccountIds } from "../accounts.js";
import { ensureGoChatGatewayAccess } from "../gateway-access.js";
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
const GATEWAY_ACCESS_POLL_MS = 1_500;
const GATEWAY_ACCESS_MIN_SPACING_MS = 750;
const GATEWAY_ACCESS_ACTIVE_WINDOW_MS = 15_000;
const GATEWAY_ACCESS_ERROR_WINDOW_MS = 20_000;

type RuntimeCommandSnapshot = {
  command: string;
  commandArgs: string;
};

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

function resolvePrimaryModelFromConfig(cfg: CoreConfig): string {
  const model = (cfg as { agents?: { defaults?: { model?: unknown } } }).agents?.defaults?.model;
  if (typeof model === "string") {
    return model.trim();
  }
  if (model && typeof model === "object") {
    const primary = (model as { primary?: unknown }).primary;
    if (typeof primary === "string") {
      return primary.trim();
    }
  }
  return "";
}

function normalizeProcessCommandArgs(argv: string[]): string[] {
  const args = [...argv];
  if (!args.length) {
    return [];
  }

  const first = args[0] ?? "";
  if (/[/\\]node(?:\.exe)?$/i.test(first)) {
    args.shift();
  }

  const next = args[0] ?? "";
  if (
    next.endsWith("/openclaw.mjs") ||
    next.endsWith("\\openclaw.mjs") ||
    /[/\\]openclaw(?:\.cmd|\.ps1|\.exe)?$/i.test(next)
  ) {
    args.shift();
  }

  return args.map((entry) => String(entry).trim()).filter(Boolean);
}

function findCliFlagValue(args: string[], flag: string): string {
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === flag) {
      return String(args[index + 1] ?? "").trim();
    }
    if (arg.startsWith(`${flag}=`)) {
      return arg.slice(flag.length + 1).trim();
    }
  }
  return "";
}

function resolveRuntimeCommandSnapshot(): RuntimeCommandSnapshot {
  const normalizedArgs = normalizeProcessCommandArgs(process.argv);
  if (!normalizedArgs.length) {
    return {
      command: "openclaw",
      commandArgs: "",
    };
  }

  const [command, ...rest] = normalizedArgs;
  return {
    command,
    commandArgs: rest.join(" "),
  };
}

function resolveRuntimeModel(cfg: CoreConfig): { currentModel: string; modelSource: string } {
  const normalizedArgs = normalizeProcessCommandArgs(process.argv);
  const cliModel = findCliFlagValue(normalizedArgs, "--model");
  if (cliModel) {
    return {
      currentModel: cliModel,
      modelSource: "cli",
    };
  }

  const configuredModel = resolvePrimaryModelFromConfig(cfg);
  if (configuredModel) {
    return {
      currentModel: configuredModel,
      modelSource: "config",
    };
  }

  return {
    currentModel: "unknown",
    modelSource: "unknown",
  };
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
  const runtimeCommand = resolveRuntimeCommandSnapshot();

  const resolveLiveConfig = (): CoreConfig => {
    try {
      return (core.config.loadConfig() as CoreConfig) ?? cfg;
    } catch {
      return cfg;
    }
  };

  const startedAt = Date.now();
  let activeJobs = 0;
  let transientStatus: "syncing" | "error" | null = null;
  let transientUntil = 0;
  let transientTimer: ReturnType<typeof setTimeout> | null = null;
  let activeStatusTimer: ReturnType<typeof setInterval> | null = null;
  let gatewayAccessTimer: ReturnType<typeof setInterval> | null = null;
  let gatewayAccessWindowUntil = 0;
  let gatewayAccessInFlight: Promise<void> | null = null;
  let gatewayAccessLastAttemptAt = 0;

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

  const stopGatewayAccessWatch = (): void => {
    if (gatewayAccessTimer) {
      clearInterval(gatewayAccessTimer);
      gatewayAccessTimer = null;
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

  const runGatewayAccessCheck = (reason: string): void => {
    const now = Date.now();
    if (gatewayAccessInFlight || now - gatewayAccessLastAttemptAt < GATEWAY_ACCESS_MIN_SPACING_MS) {
      return;
    }
    gatewayAccessLastAttemptAt = now;
    gatewayAccessInFlight = (async () => {
      try {
        const result = await ensureGoChatGatewayAccess({
          logger: {
            info: (message) => logger.info(message),
            warn: (message) => logger.warn(message),
            error: (message) => logger.error(message),
          },
          readConfig: () => core.config.loadConfig(),
          writeConfig: async (nextCfg) => {
            await core.config.writeConfigFile(nextCfg as CoreConfig);
          },
        });

        if (result.approvedRequestId) {
          logger.info(
            `[gochat:${account.accountId}] approved local gateway repair request ${result.approvedRequestId} during ${reason}`,
          );
          return;
        }

        if (result.normalizedGatewayRemoteUrlTo) {
          logger.info(
            `[gochat:${account.accountId}] normalized gateway.remote.url to ${result.normalizedGatewayRemoteUrlTo} during ${reason}`,
          );
        }
      } catch (error) {
        logger.warn(
          `[gochat:${account.accountId}] gateway access check failed during ${reason}: ${error instanceof Error ? error.message : String(error)}`,
        );
      } finally {
        gatewayAccessInFlight = null;
        if (activeJobs <= 0 && Date.now() >= gatewayAccessWindowUntil) {
          stopGatewayAccessWatch();
        }
      }
    })();
  };

  const extendGatewayAccessWindow = (ttlMs: number, reason: string): void => {
    gatewayAccessWindowUntil = Math.max(gatewayAccessWindowUntil, Date.now() + ttlMs);
    if (!gatewayAccessTimer) {
      gatewayAccessTimer = setInterval(() => {
        if (activeJobs <= 0 && Date.now() >= gatewayAccessWindowUntil) {
          stopGatewayAccessWatch();
          return;
        }
        runGatewayAccessCheck("runtime poll");
      }, GATEWAY_ACCESS_POLL_MS);
    }
    runGatewayAccessCheck(reason);
  };

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
    extendGatewayAccessWindow(GATEWAY_ACCESS_ACTIVE_WINDOW_MS, "job start");
    publishStatus();
  };

  const finishJob = (): void => {
    activeJobs = Math.max(0, activeJobs - 1);
    if (activeJobs === 0) {
      stopActiveStatusPulse();
      extendGatewayAccessWindow(GATEWAY_ACCESS_ACTIVE_WINDOW_MS, "job finish");
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
      extendGatewayAccessWindow(GATEWAY_ACCESS_ERROR_WINDOW_MS, "relay error");
      logger.error(`[gochat:${account.accountId}] relay error: ${error.message}`);
    },
    abortSignal: opts.abortSignal,
    statusProvider: () => {
      const liveConfig = resolveLiveConfig();
      const liveAccount = resolveGoChatAccount({
        cfg: liveConfig,
        accountId: opts.accountId,
      });
      const accountIds = listGoChatAccountIds(liveConfig);
      const runtimeModel = resolveRuntimeModel(liveConfig);
      return {
        type: "plugin",
        version: getPluginVersion(),
        agentCount: accountIds.length,
        status: resolveStatus(),
        uptime: Math.floor((Date.now() - startedAt) / 1000),
        metadata: {
          runtimeSchemaVersion: "1",
          openclawVersion: core.version,
          pluginVersion: getPluginVersion(),
          accountId: liveAccount?.accountId || account?.accountId || "default",
          platform: `${getRuntimePlatform()} (${getRuntimeArch()})`,
          nodeVersion: getNodeVersion(),
          command: runtimeCommand.command,
          commandArgs: runtimeCommand.commandArgs,
          currentModel: runtimeModel.currentModel,
          modelSource: runtimeModel.modelSource,
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
      extendGatewayAccessWindow(GATEWAY_ACCESS_ERROR_WINDOW_MS, "relay status error");
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
      stopGatewayAccessWatch();
      setRelayStatusReporter(null);
      setRelayWsSender(null);
      stopRelay();
    },
  };
}
