import {
  resolveLoggerBackedRuntime,
  type RuntimeEnv,
} from "openclaw/plugin-sdk/extension-shared";
import { resolveGoChatAccount } from "../accounts.js";
import { handleGoChatInbound } from "../inbound.js";
import { getGoChatRuntime } from "../runtime.js";
import { setRelayStatusReporter } from "../send.js";
import type { CoreConfig, GoChatInboundMessage } from "../types.js";
import {
  agentFetchJson,
  agentRestBase,
  getAgentTranscript,
  markAgentRecordingState,
  type AgentRecording,
  type AgentEvent,
} from "./agent-client.js";

const INITIAL_BACKOFF_MS = 1_000;
const MAX_BACKOFF_MS = 30_000;

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new Error("aborted"));
      return;
    }
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        reject(new Error("aborted"));
      },
      { once: true },
    );
  });
}

function parseSseChunk(buffer: string): { events: AgentEvent[]; rest: string } {
  const events: AgentEvent[] = [];
  const parts = buffer.split(/\r?\n\r?\n/);
  const rest = parts.pop() ?? "";
  for (const part of parts) {
    const lines = part.split(/\r?\n/);
    let event = "message";
    let id = "";
    const dataLines: string[] = [];
    for (const line of lines) {
      if (!line || line.startsWith(":")) {
        continue;
      }
      if (line.startsWith("event:")) {
        event = line.slice("event:".length).trim();
      } else if (line.startsWith("id:")) {
        id = line.slice("id:".length).trim();
      } else if (line.startsWith("data:")) {
        dataLines.push(line.slice("data:".length).trimStart());
      }
    }
    const rawData = dataLines.join("\n").trim();
    let data: Record<string, unknown> | undefined;
    if (rawData) {
      try {
        data = JSON.parse(rawData) as Record<string, unknown>;
      } catch {
        data = { raw: rawData };
      }
    }
    events.push({ event, id: id || undefined, data });
  }
  return { events, rest };
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    const text = String(value ?? "").trim();
    if (text) {
      return text;
    }
  }
  return "";
}

function buildRecordingPrompt(params: {
  recordingId: string;
  title: string;
  transcriptText: string;
}): string {
  return [
    `请为 ClawTile 录音生成一份中文纪要。`,
    ``,
    `录音 ID: ${params.recordingId}`,
    `标题: ${params.title || "未命名录音"}`,
    ``,
    `要求：`,
    `- 输出可以直接作为会议/录音纪要保存，不要解释你的工作过程。`,
    `- 优先包含：核心结论、关键讨论、待办事项。`,
    `- 如果内容不是会议，也按内容整理成清晰摘要。`,
    ``,
    `转写正文：`,
    params.transcriptText || "(无转写正文)",
  ].join("\n");
}

export async function monitorGoChatAgentProvider(
  opts: {
    accountId?: string;
    config?: CoreConfig;
    runtime?: RuntimeEnv;
    abortSignal?: AbortSignal;
    statusSink?: (patch: { lastInboundAt?: number; lastOutboundAt?: number }) => void;
  } = {},
): Promise<{ stop: () => void }> {
  const core = getGoChatRuntime();
  const cfg = opts.config ?? (core.config.loadConfig() as CoreConfig);
  const account = resolveGoChatAccount({ cfg, accountId: opts.accountId });
  const runtime = resolveLoggerBackedRuntime(
    opts.runtime,
    core.logging.getChildLogger(),
  );
  const logger = core.logging.getChildLogger({
    channel: "gochat",
    accountId: account.accountId,
    mode: "agent",
  });
  const controller = new AbortController();
  const externalSignal = opts.abortSignal;

  const stop = (): void => {
    controller.abort();
    setRelayStatusReporter(null);
  };
  externalSignal?.addEventListener("abort", stop, { once: true });

  if (!account.secret) {
    throw new Error(`GoChat agent token not configured for account "${account.accountId}"`);
  }

  setRelayStatusReporter((status) => {
    if (status === "error") {
      logger.warn(`[gochat:${account.accountId}] agent status=error`);
    }
  });

  const handleEvent = async (ev: AgentEvent): Promise<void> => {
    if (ev.event === "connection.ack" || ev.event === "message") {
      return;
    }
    if (ev.event !== "recording.transcribed" && ev.event !== "recording.summary_requested") {
      return;
    }

    const recordingId = firstString(ev.data?.recording_id, ev.id);
    if (!recordingId) {
      return;
    }
    opts.statusSink?.({ lastInboundAt: Date.now() });
    logger.info(`[gochat:${account.accountId}] agent event ${ev.event}: recording=${recordingId}`);

    try {
      await markAgentRecordingState(account, recordingId, "dispatched").catch(() => undefined);
      const transcript = await getAgentTranscript(account, recordingId);
      const title = firstString(ev.data?.title, recordingId);
      const prompt = buildRecordingPrompt({
        recordingId,
        title,
        transcriptText: firstString(transcript.text),
      });
      const message: GoChatInboundMessage = {
        messageId: `${ev.event}:${recordingId}:${Date.now()}`,
        conversationId: `agent-recording:${recordingId}`,
        conversationName: title || recordingId,
        senderId: "clawtile-agent",
        senderName: "ClawTile",
        text: prompt,
        attachments: [],
        timestamp: Date.now(),
        isGroupChat: false,
      };
      await handleGoChatInbound({
        message,
        account,
        config: cfg,
        runtime,
        statusSink: opts.statusSink,
      });
    } catch (error) {
      await markAgentRecordingState(account, recordingId, "failed").catch(() => undefined);
      logger.warn(
        `[gochat:${account.accountId}] agent event failed recording=${recordingId}: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
  };

  const reconcilePendingRecordings = async (): Promise<void> => {
    const data = await agentFetchJson<{ recordings?: AgentRecording[] }>(
      account,
      "/recordings?summary_state=none&status=completed&limit=20",
    );
    for (const rec of data.recordings ?? []) {
      if (!rec.id) {
        continue;
      }
      void handleEvent({
        event: "recording.transcribed",
        id: rec.id,
        data: {
          recording_id: rec.id,
          title: rec.title,
        },
      });
    }
  };

  void (async () => {
    let backoff = INITIAL_BACKOFF_MS;
    while (!controller.signal.aborted && !externalSignal?.aborted) {
      try {
        await agentFetchJson(account, "/me");
        await reconcilePendingRecordings().catch((error) => {
          logger.warn(
            `[gochat:${account.accountId}] agent pending recording reconciliation failed: ${error instanceof Error ? error.message : String(error)}`,
          );
        });
        const resp = await fetch(`${agentRestBase(account.agentServerUrl)}/events`, {
          headers: { Authorization: `Bearer ${account.secret}` },
          signal: controller.signal,
        });
        if (!resp.ok || !resp.body) {
          throw new Error(`SSE connect failed: HTTP ${resp.status}`);
        }
        logger.info(`[gochat:${account.accountId}] agent SSE connected to ${account.agentServerUrl}`);
        backoff = INITIAL_BACKOFF_MS;
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done || controller.signal.aborted || externalSignal?.aborted) {
            break;
          }
          buffer += decoder.decode(value, { stream: true });
          const parsed = parseSseChunk(buffer);
          buffer = parsed.rest;
          for (const ev of parsed.events) {
            void handleEvent(ev);
          }
        }
        if (!controller.signal.aborted && !externalSignal?.aborted) {
          throw new Error("SSE connection closed");
        }
      } catch (error) {
        if (controller.signal.aborted || externalSignal?.aborted) {
          return;
        }
        logger.warn(
          `[gochat:${account.accountId}] agent SSE error: ${error instanceof Error ? error.message : String(error)}; retrying in ${backoff}ms`,
        );
        await sleep(backoff, controller.signal).catch(() => undefined);
        backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
      }
    }
  })();

  return { stop };
}
