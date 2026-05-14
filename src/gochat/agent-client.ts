import type { ResolvedGoChatAccount } from "../accounts.js";

export type AgentPairExchangeResult = {
  token: string;
  token_type?: string;
  scopes?: string[];
  endpoints?: {
    rest_base?: string;
    sse?: string;
    mcp?: string;
  };
  agent?: {
    id?: string;
    displayName?: string;
    agentHint?: string;
    tokenPrefix?: string;
    createdAt?: number;
  };
  user?: {
    id?: string;
  };
  server_time?: string;
};

export type AgentRecording = {
  id: string;
  title?: string;
  created_at?: string;
  completed_at?: string;
  duration_seconds?: number;
  status?: string;
  summary_state?: string;
};

export type AgentTranscript = {
  recording_id: string;
  language?: string;
  text?: string;
  segments?: Array<{
    start_ms?: number;
    end_ms?: number;
    speaker?: string;
    text?: string;
  }>;
  speaker_mapping?: Record<string, string>;
};

export type AgentEvent = {
  event: string;
  id?: string;
  data?: Record<string, unknown>;
};

function trimBaseUrl(serverUrl: string): string {
  return serverUrl.trim().replace(/\/+$/, "");
}

export function agentRestBase(serverUrl: string): string {
  return `${trimBaseUrl(serverUrl)}/api/agent`;
}

async function readErrorText(resp: Response): Promise<string> {
  const text = await resp.text().catch(() => "");
  return text.trim() || `HTTP ${resp.status}`;
}

export async function exchangeAgentPairCode(params: {
  serverUrl: string;
  code: string;
  displayName?: string;
  version?: string;
}): Promise<AgentPairExchangeResult> {
  const base = agentRestBase(params.serverUrl);
  const resp = await fetch(`${base}/pair/exchange`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      code: params.code.trim(),
      display_name: params.displayName?.trim() || "OpenClaw",
      agent_hint: "openclaw",
      client_info: {
        platform: "openclaw-gochat",
        version: params.version || "unknown",
      },
    }),
    signal: AbortSignal.timeout(15_000),
  });
  if (!resp.ok) {
    throw new Error(`agent pairing failed: ${await readErrorText(resp)}`);
  }
  const data = (await resp.json()) as AgentPairExchangeResult;
  if (!data.token) {
    throw new Error("agent pairing response missing token");
  }
  return data;
}

export async function agentFetchJson<T>(
  account: ResolvedGoChatAccount,
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const resp = await fetch(`${agentRestBase(account.agentServerUrl)}${path}`, {
    ...init,
    headers: {
      ...(init.headers ?? {}),
      Authorization: `Bearer ${account.secret}`,
    },
  });
  if (!resp.ok) {
    throw new Error(`agent request failed ${resp.status}: ${await readErrorText(resp)}`);
  }
  return (await resp.json()) as T;
}

export async function getAgentTranscript(
  account: ResolvedGoChatAccount,
  recordingId: string,
): Promise<AgentTranscript> {
  return await agentFetchJson<AgentTranscript>(
    account,
    `/recordings/${encodeURIComponent(recordingId)}/transcript`,
  );
}

export async function markAgentRecordingState(
  account: ResolvedGoChatAccount,
  recordingId: string,
  state: "dispatched" | "failed",
): Promise<void> {
  await agentFetchJson(account, `/recordings/${encodeURIComponent(recordingId)}/state`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ state }),
  });
}

export async function writeAgentSummary(params: {
  account: ResolvedGoChatAccount;
  recordingId: string;
  summary: string;
  sourceLabel?: string;
  model?: string;
}): Promise<void> {
  await agentFetchJson(params.account, `/recordings/${encodeURIComponent(params.recordingId)}/summary`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      summary: params.summary,
      source_label: params.sourceLabel || "openclaw",
      model: params.model || undefined,
      metadata: {
        plugin: "gochat",
        mode: "agent",
      },
    }),
  });
}

