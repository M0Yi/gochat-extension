import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const CACHE_TTL_MS = 10_000;
const DEFAULT_ACTIVE_MINUTES = 120;
const DEFAULT_LIMIT = 120;

type OpenClawGatewaySessionsListResponse = {
  ts?: number;
  path?: string | null;
  count?: number;
  defaults?: {
    modelProvider?: string;
    model?: string;
    contextTokens?: number;
  };
  sessions?: Array<{
    key?: string;
    kind?: string;
    displayName?: string;
    chatType?: string;
    updatedAt?: number;
    sessionId?: string;
    systemSent?: boolean;
    abortedLastRun?: boolean;
    inputTokens?: number;
    outputTokens?: number;
    totalTokens?: number;
    status?: string;
    startedAt?: number;
    endedAt?: number;
    runtimeMs?: number;
    modelProvider?: string;
    model?: string;
  }>;
};

type OpenClawGatewayHealthResponse = {
  defaultAgentId?: string;
};

type OpenClawModelsListResponse = {
  models?: Array<{
    key?: string;
    name?: string;
    input?: string;
    contextWindow?: number;
    available?: boolean;
    tags?: string[];
    missing?: boolean;
  }>;
};

type OpenClawModelsStatusResponse = {
  defaultModel?: string;
  resolvedDefault?: string;
};

type OpenClawSessionSnapshot = {
  sourceMethod: string;
  stateDir: string;
  path: string;
  defaultAgentId?: string;
  defaultModelProvider?: string;
  defaultModel?: string;
  defaultContextTokens?: number;
  count: number;
  sessions: Array<Record<string, string | number | boolean>>;
};

type OpenClawModelsSnapshot = {
  currentModel?: string;
  modelSource: string;
  models: Array<{
    key: string;
    name?: string;
    input?: string;
    contextWindow?: number;
    available: boolean;
    tags?: string[];
    current?: boolean;
  }>;
};

type CachedSnapshot = {
  at: number;
  metadata: Record<string, string>;
};

let cachedSnapshot: CachedSnapshot | null = null;

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

function deriveAgentIdFromSessionKey(key: unknown): string {
  const text = String(key ?? "").trim();
  if (!text) {
    return "";
  }
  const parts = text.split(":");
  if (parts.length >= 2 && parts[0] === "agent") {
    return parts[1] ?? "";
  }
  return "";
}

function resolveOpenClawBin(): string {
  return process.env.GOCHAT_OPENCLAW_BIN?.trim() || "openclaw";
}

function resolveStateDir(): string {
  const explicit = process.env.OPENCLAW_STATE_DIR?.trim();
  if (explicit) {
    return explicit;
  }
  return join(homedir(), ".openclaw");
}

async function runOpenClawJson(args: string[]): Promise<unknown> {
  const { stdout, stderr } = await execFileAsync(resolveOpenClawBin(), args, {
    timeout: 12_000,
    maxBuffer: 4 * 1024 * 1024,
  });
  return extractJsonPayload([stdout, stderr].filter(Boolean).join("\n"));
}

function normalizeSessionSnapshot(
  list: OpenClawGatewaySessionsListResponse,
  defaultAgentId: string,
): OpenClawSessionSnapshot {
  const now = typeof list.ts === "number" ? list.ts : Date.now();
  const sessions = Array.isArray(list.sessions) ? list.sessions : [];
  return {
    sourceMethod: "plugin.gateway.call sessions.list",
    stateDir: resolveStateDir(),
    path: String(list.path ?? "(multiple)").trim() || "(multiple)",
    defaultAgentId: defaultAgentId || undefined,
    defaultModelProvider: String(list.defaults?.modelProvider ?? "").trim() || undefined,
    defaultModel: String(list.defaults?.model ?? "").trim() || undefined,
    defaultContextTokens: Number.isFinite(list.defaults?.contextTokens)
      ? Number(list.defaults?.contextTokens)
      : undefined,
    count: Number.isFinite(list.count) ? Number(list.count) : sessions.length,
    sessions: sessions.map((session) => {
      const updatedAt = Number.isFinite(session.updatedAt) ? Number(session.updatedAt) : 0;
      const ageMs = updatedAt > 0 ? Math.max(0, now - updatedAt) : 0;
      return {
        key: String(session.key ?? "").trim(),
        agentId: deriveAgentIdFromSessionKey(session.key),
        sessionId: String(session.sessionId ?? "").trim(),
        kind: String(session.kind ?? "").trim(),
        displayName: String(session.displayName ?? "").trim(),
        status: String(session.status ?? "").trim(),
        model: String(session.model ?? "").trim(),
        modelProvider: String(session.modelProvider ?? "").trim(),
        chatType: String(session.chatType ?? "").trim(),
        updatedAt,
        startedAt: Number.isFinite(session.startedAt) ? Number(session.startedAt) : 0,
        endedAt: Number.isFinite(session.endedAt) ? Number(session.endedAt) : 0,
        runtimeMs: Number.isFinite(session.runtimeMs) ? Number(session.runtimeMs) : 0,
        ageMs,
        systemSent: session.systemSent === true,
        abortedLastRun: session.abortedLastRun === true,
        inputTokens: Number.isFinite(session.inputTokens) ? Number(session.inputTokens) : 0,
        outputTokens: Number.isFinite(session.outputTokens) ? Number(session.outputTokens) : 0,
        totalTokens: Number.isFinite(session.totalTokens) ? Number(session.totalTokens) : 0,
      };
    }),
  };
}

async function loadDefaultAgentId(): Promise<string> {
  try {
    const payload = await runOpenClawJson(["gateway", "call", "health", "--json"]);
    const health = payload as OpenClawGatewayHealthResponse;
    return String(health.defaultAgentId ?? "").trim();
  } catch {
    return "";
  }
}

async function buildOpenClawRuntimeMetadata(): Promise<Record<string, string>> {
  const payload = await runOpenClawJson([
    "gateway",
    "call",
    "sessions.list",
    "--json",
    "--params",
    JSON.stringify({
      activeMinutes: DEFAULT_ACTIVE_MINUTES,
      limit: DEFAULT_LIMIT,
      includeGlobal: true,
      includeUnknown: true,
    }),
  ]);
  const list = payload as OpenClawGatewaySessionsListResponse;
  const defaultAgentId = await loadDefaultAgentId();
  const snapshot = normalizeSessionSnapshot(list, defaultAgentId);
  const modelsSnapshot = await loadOpenClawModelsSnapshot();
  return {
    openclawSessionsSource: snapshot.sourceMethod,
    openclawSessionsCount: String(snapshot.count),
    openclawSessionsJson: JSON.stringify(snapshot),
    openclawModelsJson: JSON.stringify(modelsSnapshot),
  };
}

async function loadOpenClawModelsSnapshot(): Promise<OpenClawModelsSnapshot> {
  const [statusPayload, listPayload] = await Promise.all([
    runOpenClawJson(["models", "status", "--json"]),
    runOpenClawJson(["models", "list", "--all", "--json"]),
  ]);

  const status = statusPayload as OpenClawModelsStatusResponse;
  const list = listPayload as OpenClawModelsListResponse;
  const currentModel = String(status.resolvedDefault ?? status.defaultModel ?? "").trim();
  const models = (Array.isArray(list.models) ? list.models : [])
    .filter((model) => model?.available !== false && model?.missing !== true)
    .map((model) => {
      const key = String(model?.key ?? "").trim();
      return {
        key,
        name: String(model?.name ?? "").trim() || undefined,
        input: String(model?.input ?? "").trim() || undefined,
        contextWindow: Number.isFinite(model?.contextWindow) ? Number(model?.contextWindow) : undefined,
        available: true,
        tags: Array.isArray(model?.tags)
          ? model.tags.map((tag) => String(tag ?? "").trim()).filter(Boolean)
          : undefined,
        current: !!key && key === currentModel,
      };
    })
    .filter((model) => !!model.key)
    .sort((a, b) => {
      if (!!a.current !== !!b.current) {
        return a.current ? -1 : 1;
      }
      return a.key.localeCompare(b.key);
    });

  if (currentModel && !models.some((model) => model.key === currentModel)) {
    models.unshift({
      key: currentModel,
      name: currentModel,
      available: true,
      tags: ["current"],
      current: true,
    });
  }

  return {
    currentModel: currentModel || undefined,
    modelSource: "plugin",
    models,
  };
}

export async function setOpenClawCurrentModel(model: string): Promise<void> {
  const nextModel = String(model ?? "").trim();
  if (!nextModel) {
    throw new Error("model is required");
  }
  await execFileAsync(resolveOpenClawBin(), ["models", "set", nextModel], {
    timeout: 15_000,
    maxBuffer: 2 * 1024 * 1024,
  });
  cachedSnapshot = null;
}

export async function loadOpenClawRuntimeSnapshotMetadata(params?: {
  forceRefresh?: boolean;
}): Promise<Record<string, string>> {
  const now = Date.now();
  if (!params?.forceRefresh && cachedSnapshot && now - cachedSnapshot.at < CACHE_TTL_MS) {
    return cachedSnapshot.metadata;
  }

  let metadata: Record<string, string>;
  try {
    metadata = await buildOpenClawRuntimeMetadata();
  } catch (error) {
    metadata = {
      openclawSessionsSource: "plugin.gateway.call sessions.list",
      openclawSessionsError: error instanceof Error ? error.message : String(error),
    };
  }

  cachedSnapshot = {
    at: now,
    metadata,
  };
  return metadata;
}
