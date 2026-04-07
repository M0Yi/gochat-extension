import { resolveMarkdownTableMode } from "openclaw/plugin-sdk/config-runtime";
import { convertMarkdownTables } from "openclaw/plugin-sdk/text-runtime";
import { resolveGoChatAccount } from "./accounts.js";
import { stripGoChatTargetPrefix } from "./normalize.js";
import { getGoChatRuntime } from "./runtime.js";
import type { CoreConfig, GoChatSendResult } from "./types.js";

let directStorage: import("./direct/storage.js").GoChatDirectStorage | null = null;
let relayWsSender: ((data: any) => void) | null = null;
let relayStatusReporter: ((status: "idle" | "writing" | "researching" | "executing" | "syncing" | "error") => void) | null = null;

export function setDirectStorage(
  storage: import("./direct/storage.js").GoChatDirectStorage | null,
): void {
  directStorage = storage;
}

export function setRelayWsSender(
  sender: ((data: any) => void) | null,
): void {
  relayWsSender = sender;
}

export function setRelayStatusReporter(
  reporter: ((status: "idle" | "writing" | "researching" | "executing" | "syncing" | "error") => void) | null,
): void {
  relayStatusReporter = reporter;
}

type GoChatSendOpts = {
  accountId?: string;
  replyTo?: string;
  mediaUrl?: string;
  verbose?: boolean;
  cfg?: CoreConfig;
};

function normalizeConversationId(to: string): string {
  const normalized = stripGoChatTargetPrefix(to);
  if (!normalized) {
    throw new Error("Conversation ID is required for GoChat sends");
  }
  return normalized;
}

function recordGoChatOutboundActivity(accountId: string): void {
  try {
    getGoChatRuntime().channel.activity.record({
      channel: "gochat",
      accountId,
      direction: "outbound",
    });
  } catch (error) {
    if (!(error instanceof Error) || error.message !== "GoChat runtime not initialized") {
      throw error;
    }
  }
}

async function sendDirect(
  conversationId: string,
  text: string,
  opts: GoChatSendOpts,
  accountId: string,
): Promise<GoChatSendResult> {
  if (!directStorage) {
    throw new Error("GoChat local storage not initialized");
  }

  const stored = await directStorage.appendMessage(conversationId, {
    direction: "outbound",
    text,
    attachments: opts.mediaUrl
      ? [{ url: opts.mediaUrl, type: "file" as const }]
      : [],
    replyTo: opts.replyTo,
  });

  recordGoChatOutboundActivity(accountId);

  console.log(`[gochat:local] Sent message ${stored.id} to conversation ${conversationId}`);

  return {
    messageId: stored.id,
    conversationId,
    timestamp: stored.timestamp,
  };
}

async function uploadToGoChatServer(
  mediaUrl: string,
  relayPlatformUrl: string,
): Promise<string> {
  try {
    const response = await fetch(mediaUrl);
    if (!response.ok) {
      console.warn(`[gochat:relay] failed to fetch media from ${mediaUrl}: ${response.status}`);
      return mediaUrl;
    }
    const blob = await response.blob();
    const contentType = response.headers.get("Content-Type") || "application/octet-stream";
    const filename = mediaUrl.split("/").pop() || "attachment";

    const baseUrl = relayPlatformUrl.replace("wss://", "https://").replace("ws://", "http://").replace("/ws/plugin", "");

    const presignRes = await fetch(`${baseUrl}/api/upload/presign`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ filename, contentType }),
    });
    if (!presignRes.ok) {
      console.warn(`[gochat:relay] presign failed: ${presignRes.status}`);
      return mediaUrl;
    }
    const presign = await presignRes.json() as { uploadUrl: string; fileKey: string };

    const uploadRes = await fetch(presign.uploadUrl, {
      method: "PUT",
      headers: { "Content-Type": contentType },
      body: blob,
    });
    if (!uploadRes.ok) {
      console.warn(`[gochat:relay] upload failed: ${uploadRes.status}`);
      return mediaUrl;
    }

    const confirmRes = await fetch(`${baseUrl}/api/upload/confirm`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ fileKey: presign.fileKey }),
    });
    if (!confirmRes.ok) {
      console.warn(`[gochat:relay] confirm failed: ${confirmRes.status}`);
      return mediaUrl;
    }
    const confirm = await confirmRes.json() as { url: string };
    console.log(`[gochat:relay] reuploaded media: ${mediaUrl} -> ${confirm.url}`);
    return confirm.url;
  } catch (err) {
    console.warn(`[gochat:relay] upload to gochat-server failed: ${err instanceof Error ? err.message : String(err)}`);
    return mediaUrl;
  }
}

async function sendRelay(
  conversationId: string,
  text: string,
  opts: GoChatSendOpts,
  accountId: string,
  relayPlatformUrl: string,
): Promise<GoChatSendResult> {
  relayStatusReporter?.("syncing");
  if (!relayWsSender) {
    console.error(`[gochat:relay] Cannot send reply — relayWsSender is null (relay not connected)`);
    relayStatusReporter?.("error");
    throw new Error("GoChat relay not connected");
  }

  let finalMediaUrl = opts.mediaUrl;
  if (opts.mediaUrl) {
    finalMediaUrl = await uploadToGoChatServer(opts.mediaUrl, relayPlatformUrl);
  }

  const payload: Record<string, unknown> = {
    type: "reply",
    conversationId,
    text,
    timestamp: Date.now(),
  };
  if (opts.replyTo) {
    payload.replyTo = opts.replyTo;
  }
  if (finalMediaUrl) {
    payload.mediaUrl = finalMediaUrl;
  }
  const mediaLabel = finalMediaUrl ? ` mediaUrl="${finalMediaUrl.substring(0, 120)}..."` : '';
  console.log(`[gochat:relay] Sending reply to conv=${conversationId} text="${text.substring(0, 80)}..."${mediaLabel}`);
  try {
    relayWsSender(payload);
  } catch (error) {
    relayStatusReporter?.("error");
    throw error;
  }

  const messageId = `ws-${Date.now()}`;
  recordGoChatOutboundActivity(accountId);
  console.log(`[gochat:relay] Sent message ${messageId} to conversation ${conversationId}`);

  return { messageId, conversationId };
}

export async function sendMessageGoChat(
  to: string,
  text: string,
  opts: GoChatSendOpts = {},
): Promise<GoChatSendResult> {
  const cfg = (opts.cfg ?? getGoChatRuntime().config.loadConfig()) as CoreConfig;
  const account = resolveGoChatAccount({
    cfg,
    accountId: opts.accountId,
  });
  const conversationId = normalizeConversationId(to);

  if (!text?.trim() && !opts.mediaUrl) {
    throw new Error("Message must be non-empty for GoChat sends");
  }

  const tableMode = resolveMarkdownTableMode({
    cfg,
    channel: "gochat",
    accountId: account.accountId,
  });
  const message = convertMarkdownTables(text?.trim() ?? "", tableMode);

  if (account.mode === "local") {
    return await sendDirect(conversationId, message, opts, account.accountId);
  }

  return await sendRelay(conversationId, message, opts, account.accountId, account.relayPlatformUrl);
}
