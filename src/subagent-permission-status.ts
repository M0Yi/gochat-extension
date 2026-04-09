import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const CACHE_TTL_MS = 3_000;

type PendingDeviceRequest = {
  requestId?: string;
  deviceId?: string;
  clientId?: string;
  clientMode?: string;
  role?: string;
  scopes?: string[];
  isRepair?: boolean;
  createdAtMs?: number;
};

type PairedDevice = {
  deviceId?: string;
  clientId?: string;
  clientMode?: string;
  role?: string;
  scopes?: string[];
};

type DeviceListResponse = {
  pending?: PendingDeviceRequest[];
  paired?: PairedDevice[];
};

export type SubagentPermissionStatus =
  | {
      state: "ready";
      summary: string;
      detailSignature: string;
      scopes: string[];
      deviceId?: string;
    }
  | {
      state: "pending_approval";
      summary: string;
      detailSignature: string;
      approvalCommand: string;
      requestId?: string;
      deviceId?: string;
    }
  | {
      state: "degraded";
      summary: string;
      detailSignature: string;
      scopes: string[];
      deviceId?: string;
    }
  | {
      state: "unknown";
      summary: string;
      detailSignature: string;
    };

let cachedStatus:
  | {
      at: number;
      value: SubagentPermissionStatus;
    }
  | null = null;

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
      // keep scanning
    }
  }

  throw new Error("json payload not found in command output");
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

function looksLikeCliDevice(entry: {
  clientId?: string;
  clientMode?: string;
}): boolean {
  const clientId = String(entry.clientId ?? "").trim().toLowerCase();
  const clientMode = String(entry.clientMode ?? "").trim().toLowerCase();
  if (!clientId && !clientMode) {
    return true;
  }
  return clientId === "cli" || clientMode === "cli";
}

function selectRelevantPendingRequest(pending: PendingDeviceRequest[]): PendingDeviceRequest | null {
  const filtered = pending.filter((entry) => {
    const role = String(entry.role ?? "").trim().toLowerCase();
    if (role && role !== "operator") {
      return false;
    }
    if (!looksLikeCliDevice(entry)) {
      return false;
    }
    const scopes = normalizeScopes(entry.scopes);
    return scopes.length === 0 || scopes.includes("operator.admin");
  });

  const ordered = [...filtered].sort((a, b) => (b.createdAtMs ?? 0) - (a.createdAtMs ?? 0));
  return ordered[0] ?? null;
}

function selectRelevantPairedDevice(paired: PairedDevice[]): PairedDevice | null {
  return (
    paired.find((entry) => {
      const role = String(entry.role ?? "").trim().toLowerCase();
      if (role && role !== "operator") {
        return false;
      }
      return looksLikeCliDevice(entry);
    }) ?? null
  );
}

async function loadDeviceList(): Promise<DeviceListResponse> {
  const openclawBin = process.env.GOCHAT_OPENCLAW_BIN?.trim() || "openclaw";
  const { stdout, stderr } = await execFileAsync(openclawBin, ["devices", "list", "--json", "--timeout", "5000"], {
    timeout: 10_000,
    maxBuffer: 2 * 1024 * 1024,
  });
  return extractJsonPayload([stdout, stderr].filter(Boolean).join("\n")) as DeviceListResponse;
}

function buildApprovalCommand(request: PendingDeviceRequest | null): string {
  return "openclaw gochat approve-local-repair";
}

function buildDirectApprovalFallbackCommand(request: PendingDeviceRequest | null): string {
  if (request?.requestId?.trim()) {
    return `openclaw devices approve ${request.requestId.trim()}`;
  }
  return "openclaw devices approve --latest";
}

function inspectFromDeviceList(deviceList: DeviceListResponse): SubagentPermissionStatus {
  const pending = selectRelevantPendingRequest(deviceList.pending ?? []);
  if (pending) {
    const approvalCommand = buildApprovalCommand(pending);
    return {
      state: "pending_approval",
      summary: "Subagent permission needs approval.",
      detailSignature: `pending:${pending.requestId ?? "latest"}:${pending.deviceId ?? ""}`,
      approvalCommand,
      requestId: pending.requestId?.trim() || undefined,
      deviceId: pending.deviceId?.trim() || undefined,
    };
  }

  const paired = selectRelevantPairedDevice(deviceList.paired ?? []);
  if (!paired) {
    return {
      state: "unknown",
      summary: "Subagent permission status is unavailable.",
      detailSignature: "unknown:no-cli-device",
    };
  }

  const scopes = normalizeScopes(paired.scopes);
  if (scopes.includes("operator.admin")) {
    return {
      state: "ready",
      summary: "Subagent permission is ready.",
      detailSignature: `ready:${paired.deviceId ?? ""}:${scopes.join(",")}`,
      scopes,
      deviceId: paired.deviceId?.trim() || undefined,
    };
  }

  return {
    state: "degraded",
    summary: "Subagent permission is limited.",
    detailSignature: `degraded:${paired.deviceId ?? ""}:${scopes.join(",")}`,
    scopes,
    deviceId: paired.deviceId?.trim() || undefined,
  };
}

export async function inspectSubagentPermissionStatus(params?: {
  forceRefresh?: boolean;
}): Promise<SubagentPermissionStatus> {
  const now = Date.now();
  if (!params?.forceRefresh && cachedStatus && now - cachedStatus.at < CACHE_TTL_MS) {
    return cachedStatus.value;
  }

  let value: SubagentPermissionStatus;
  try {
    value = inspectFromDeviceList(await loadDeviceList());
  } catch (error) {
    value = {
      state: "unknown",
      summary: "Subagent permission status is unavailable.",
      detailSignature: `unknown:${error instanceof Error ? error.message : String(error)}`,
    };
  }

  cachedStatus = {
    at: now,
    value,
  };
  return value;
}

export function buildSubagentPermissionStatusMessage(
  status: SubagentPermissionStatus,
): string {
  if (status.state === "ready") {
    return [
      "Subagent permission: ready",
      "",
      "Current local gateway device already includes `operator.admin`.",
    ].join("\n");
  }

  if (status.state === "pending_approval") {
    const fallbackCommand = buildDirectApprovalFallbackCommand({
      requestId: status.requestId,
      deviceId: status.deviceId,
    });
    return [
      "Subagent permission: action required",
      "",
      "A local gateway repair request is waiting for `operator.admin` approval.",
      "",
      "```bash",
      status.approvalCommand,
      "```",
      "",
      "Fallback direct command:",
      "",
      "```bash",
      fallbackCommand,
      "```",
      "",
      "Run the command, then resend your last message.",
    ].join("\n");
  }

  if (status.state === "degraded") {
    return [
      "Subagent permission: limited",
      "",
      `Current local gateway device scopes: ${status.scopes.join(", ") || "(none)"}`,
      "",
      "Trigger the subagent action again if needed, then recover with:",
      "",
      "```bash",
      "openclaw gochat approve-local-repair",
      "```",
    ].join("\n");
  }

  return [
    "Subagent permission: unknown",
    "",
    "The plugin could not inspect local gateway pairing status right now.",
  ].join("\n");
}
