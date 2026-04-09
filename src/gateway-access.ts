import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { loadConfig, writeConfigFile } from "openclaw/plugin-sdk/config-runtime";

const execFileAsync = promisify(execFile);

const REQUIRED_OPERATOR_SCOPES = [
  "operator.admin",
  "operator.approvals",
  "operator.pairing",
  "operator.read",
  "operator.write",
] as const;

const LOOPBACK_ALIASES = new Set(["localhost", "openclaw.local"]);

type GatewayAccessConfig = {
  autoApproveLocalRepair?: boolean;
  normalizeLoopbackRemoteUrl?: boolean;
};

type GatewayAccessLogger = {
  info?: (message: string) => void;
  warn?: (message: string) => void;
  error?: (message: string) => void;
};

type PendingDeviceRequest = {
  requestId?: string;
  deviceId?: string;
  clientId?: string;
  clientMode?: string;
  role?: string;
  scopes?: string[];
  isRepair?: boolean;
};

type DeviceListResponse = {
  pending?: PendingDeviceRequest[];
};

type ConfigReader = () => unknown;
type ConfigWriter = (cfg: unknown) => Promise<void>;

export type GoChatGatewayAccessResult = {
  normalizedGatewayRemoteUrlFrom?: string;
  normalizedGatewayRemoteUrlTo?: string;
  approvedRequestId?: string;
  approvedDeviceId?: string;
  skippedReason?: string;
};

export type GoChatLocalRepairApprovalResult = Pick<
  GoChatGatewayAccessResult,
  "approvedRequestId" | "approvedDeviceId" | "skippedReason"
>;

let bootstrapPromise: Promise<GoChatGatewayAccessResult> | null = null;

function logInfo(logger: GatewayAccessLogger | undefined, message: string): void {
  logger?.info?.(`[gochat] ${message}`);
}

function logWarn(logger: GatewayAccessLogger | undefined, message: string): void {
  logger?.warn?.(`[gochat] ${message}`);
}

function extractJsonPayload(raw: string): unknown {
  const text = raw.trim();
  if (!text) {
    throw new Error("empty command output");
  }

  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (char !== "{" && char !== "[") {
      continue;
    }
    const candidate = text.slice(index).trim();
    try {
      return JSON.parse(candidate);
    } catch {
      // continue scanning
    }
  }

  throw new Error("json payload not found in command output");
}

function normalizeLoopbackGatewayRemoteUrl(rawUrl: string): string | null {
  const trimmed = rawUrl.trim();
  if (!trimmed) {
    return null;
  }

  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return null;
  }

  const host = parsed.hostname.trim().toLowerCase();
  if (!LOOPBACK_ALIASES.has(host)) {
    return null;
  }

  parsed.hostname = "127.0.0.1";
  return parsed.toString();
}

function readGoChatGatewayAccessConfig(cfg: unknown): GatewayAccessConfig {
  const record = (cfg ?? {}) as {
    channels?: { gochat?: { gatewayAccess?: GatewayAccessConfig } };
  };
  return record.channels?.gochat?.gatewayAccess ?? {};
}

function patchConfigValue(
  cfg: unknown,
  path: Array<string>,
  value: unknown,
): Record<string, unknown> {
  const root = cfg && typeof cfg === "object" && !Array.isArray(cfg)
    ? { ...(cfg as Record<string, unknown>) }
    : {};

  let cursor: Record<string, unknown> = root;
  for (let index = 0; index < path.length - 1; index += 1) {
    const key = path[index]!;
    const current = cursor[key];
    const next = current && typeof current === "object" && !Array.isArray(current)
      ? { ...(current as Record<string, unknown>) }
      : {};
    cursor[key] = next;
    cursor = next;
  }

  cursor[path[path.length - 1]!] = value;
  return root;
}

function normalizeScopes(scopes: unknown): string[] {
  if (!Array.isArray(scopes)) {
    return [];
  }
  return scopes
    .map((entry) => String(entry ?? "").trim())
    .filter(Boolean)
    .sort();
}

function isEligibleCliRepairRequest(req: PendingDeviceRequest): boolean {
  const role = String(req.role ?? "").trim().toLowerCase();
  if (role !== "operator" || req.isRepair !== true) {
    return false;
  }

  const clientId = String(req.clientId ?? "").trim().toLowerCase();
  const clientMode = String(req.clientMode ?? "").trim().toLowerCase();
  if (clientId !== "cli" && clientMode !== "cli") {
    return false;
  }

  const requested = normalizeScopes(req.scopes);
  const required = [...REQUIRED_OPERATOR_SCOPES].sort();
  if (requested.length !== required.length) {
    return false;
  }

  return required.every((scope, index) => scope === requested[index]);
}

async function runOpenClawJson(args: string[]): Promise<unknown> {
  const openclawBin = process.env.GOCHAT_OPENCLAW_BIN?.trim() || "openclaw";
  const { stdout, stderr } = await execFileAsync(openclawBin, args, {
    timeout: 10_000,
    maxBuffer: 2 * 1024 * 1024,
  });
  return extractJsonPayload([stdout, stderr].filter(Boolean).join("\n"));
}

function stringifyError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function maybePersistNormalizedGatewayUrl(params: {
  readConfig: ConfigReader;
  writeConfig: ConfigWriter;
  logger?: GatewayAccessLogger;
}): Promise<Pick<GoChatGatewayAccessResult, "normalizedGatewayRemoteUrlFrom" | "normalizedGatewayRemoteUrlTo">> {
  const cfg = params.readConfig();
  const gateway = (cfg ?? {}) as { gateway?: { remote?: { url?: string } } };
  const currentUrl = String(gateway.gateway?.remote?.url ?? "").trim();
  const normalizedUrl = normalizeLoopbackGatewayRemoteUrl(currentUrl);
  if (!normalizedUrl || normalizedUrl === currentUrl) {
    return {};
  }

  const nextCfg = patchConfigValue(cfg, ["gateway", "remote", "url"], normalizedUrl);
  await params.writeConfig(nextCfg);
  logInfo(params.logger, `normalized gateway.remote.url to ${normalizedUrl}`);
  return {
    normalizedGatewayRemoteUrlFrom: currentUrl,
    normalizedGatewayRemoteUrlTo: normalizedUrl,
  };
}

async function maybeApproveLocalRepair(params: {
  logger?: GatewayAccessLogger;
}): Promise<GoChatLocalRepairApprovalResult> {
  let deviceList: DeviceListResponse;
  try {
    deviceList = (await runOpenClawJson(["devices", "list", "--json", "--timeout", "5000"])) as DeviceListResponse;
  } catch (error) {
    return {
      skippedReason: `devices list unavailable: ${stringifyError(error)}`,
    };
  }

  const matching = (deviceList.pending ?? []).filter(isEligibleCliRepairRequest);
  if (matching.length === 0) {
    return {
      skippedReason: "no eligible local CLI repair request",
    };
  }

  if (matching.length > 1) {
    return {
      skippedReason: "multiple eligible local CLI repair requests are pending",
    };
  }

  const request = matching[0]!;
  if (!request.requestId?.trim()) {
    return {
      skippedReason: "eligible repair request is missing requestId",
    };
  }

  try {
    await runOpenClawJson([
      "devices",
      "approve",
      request.requestId,
      "--json",
      "--timeout",
      "5000",
    ]);
    logInfo(params.logger, `approved local CLI repair request ${request.requestId}`);
    return {
      approvedRequestId: request.requestId,
      approvedDeviceId: request.deviceId?.trim() || undefined,
    };
  } catch (error) {
    return {
      skippedReason: `approve failed: ${stringifyError(error)}`,
    };
  }
}

export async function approveGoChatLocalRepair(params?: {
  logger?: GatewayAccessLogger;
}): Promise<GoChatLocalRepairApprovalResult> {
  return await maybeApproveLocalRepair({
    logger: params?.logger,
  });
}

export async function ensureGoChatGatewayAccess(params?: {
  logger?: GatewayAccessLogger;
  readConfig?: ConfigReader;
  writeConfig?: ConfigWriter;
}): Promise<GoChatGatewayAccessResult> {
  const readConfig = params?.readConfig ?? (() => loadConfig());
  const writeConfig = params?.writeConfig ?? (async (cfg: unknown) => {
    await writeConfigFile(cfg as Parameters<typeof writeConfigFile>[0]);
  });
  const logger = params?.logger;
  const access = readGoChatGatewayAccessConfig(readConfig());

  const normalizeLoopbackRemoteUrl = access.normalizeLoopbackRemoteUrl !== false;
  const autoApproveLocalRepair = access.autoApproveLocalRepair !== false;

  const result: GoChatGatewayAccessResult = {};

  if (normalizeLoopbackRemoteUrl) {
    try {
      Object.assign(result, await maybePersistNormalizedGatewayUrl({
        readConfig,
        writeConfig,
        logger,
      }));
    } catch (error) {
      logWarn(logger, `failed to normalize gateway.remote.url: ${stringifyError(error)}`);
    }
  }

  if (autoApproveLocalRepair) {
    Object.assign(result, await maybeApproveLocalRepair({ logger }));
  }

  return result;
}

export async function ensureGoChatGatewayAccessOnce(params?: {
  logger?: GatewayAccessLogger;
  readConfig?: ConfigReader;
  writeConfig?: ConfigWriter;
}): Promise<GoChatGatewayAccessResult> {
  if (!bootstrapPromise) {
    bootstrapPromise = ensureGoChatGatewayAccess(params).finally(() => {
      bootstrapPromise = null;
    });
  }
  return await bootstrapPromise;
}
