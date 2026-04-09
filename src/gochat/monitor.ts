import {
  resolveLoggerBackedRuntime,
  type RuntimeEnv,
} from "openclaw/plugin-sdk/extension-shared";
import { resolveGoChatAccount } from "../accounts.js";
import { handleGoChatInbound } from "../inbound.js";
import { getGoChatRuntime } from "../runtime.js";
import {
  buildSubagentPermissionMetadata,
  inspectSubagentPermissionStatus,
  type SubagentPermissionStatus,
} from "../subagent-permission-status.js";
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

type RuntimeCommandSnapshot = {
  command: string;
  commandArgs: string;
};

type RuntimeWorkUnitSnapshot = {
  id: string;
  label: string;
  status: string;
  source: string;
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

function buildRuntimeWorkUnitMetadata(activeJobs: number, status: string): Record<string, string> {
  const count = Math.max(0, Math.floor(activeJobs));
  const normalizedStatus = String(status || "idle").trim() || "idle";
  const items: RuntimeWorkUnitSnapshot[] = Array.from({ length: count }, (_, index) => ({
    id: `job-${index + 1}`,
    label: `运行任务 ${index + 1}`,
    status: normalizedStatus,
    source: "gochat-active-jobs",
  }));
  return {
    runtimeWorkUnitLabel: "运行任务",
    runtimeWorkUnitSource: "gochat-active-jobs",
    runtimeWorkUnitCount: String(count),
    runtimeWorkUnitsJson: JSON.stringify(items),
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
  let permissionPollTimer: ReturnType<typeof setInterval> | null = null;
  let subagentPermissionStatus: SubagentPermissionStatus = {
    state: "unknown",
    summary: "Subagent permission status is unavailable.",
    detailSignature: "unknown:initial",
    approvalState: "unknown",
    approvalLabel: "unknown",
  };

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

  const refreshSubagentPermissionStatus = async (forceRefresh = false): Promise<void> => {
    try {
      const nextStatus = await inspectSubagentPermissionStatus({ forceRefresh });
      if (nextStatus.detailSignature === subagentPermissionStatus.detailSignature) {
        return;
      }
      subagentPermissionStatus = nextStatus;
      pushRelayStatusNow();
    } catch {
      // keep last known metadata
    }
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
      const liveConfig = resolveLiveConfig();
      const liveAccount = resolveGoChatAccount({
        cfg: liveConfig,
        accountId: opts.accountId,
      });
      const runtimeModel = resolveRuntimeModel(liveConfig);
      const resolvedStatus = resolveStatus();
      return {
        type: "plugin",
        version: getPluginVersion(),
        agentCount: activeJobs,
        status: resolvedStatus,
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
          ...buildRuntimeWorkUnitMetadata(activeJobs, resolvedStatus),
          ...buildSubagentPermissionMetadata(subagentPermissionStatus),
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
  void refreshSubagentPermissionStatus(true);
  permissionPollTimer = setInterval(() => {
    void refreshSubagentPermissionStatus(true);
  }, 10_000);
  logger.info(
    `[gochat:${account.accountId}] relay connected to ${account.relayPlatformUrl}`,
  );

  return {
    stop: () => {
      if (permissionPollTimer) {
        clearInterval(permissionPollTimer);
        permissionPollTimer = null;
      }
      clearTransientTimer();
      stopActiveStatusPulse();
      setRelayStatusReporter(null);
      setRelayWsSender(null);
      stopRelay();
    },
  };
}
