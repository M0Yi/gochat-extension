import { resolveMarkdownTableMode } from "openclaw/plugin-sdk/config-runtime";
import { convertMarkdownTables } from "openclaw/plugin-sdk/text-runtime";
import { resolveGoChatAccount } from "./accounts.js";
import { stripGoChatTargetPrefix } from "./normalize.js";
import { getGoChatRuntime } from "./runtime.js";
import type { CoreConfig, GoChatSendResult } from "./types.js";

let directStorage: import("./direct/storage.js").GoChatDirectStorage | null = null;
let relayWsSender: ((data: any) => void) | null = null;

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

async function sendRelay(
  conversationId: string,
  text: string,
  opts: GoChatSendOpts,
  accountId: string,
): Promise<GoChatSendResult> {
  if (!relayWsSender) {
    throw new Error("GoChat relay not connected");
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
  if (opts.mediaUrl) {
    payload.mediaUrl = opts.mediaUrl;
  }
  relayWsSender(payload);

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

  return await sendRelay(conversationId, message, opts, account.accountId);
}
