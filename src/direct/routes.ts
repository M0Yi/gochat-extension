import { safeParseJsonWithSchema } from "openclaw/plugin-sdk/extension-shared";
import { basename, extname } from "node:path";
import { GoChatWebhookPayloadSchema } from "../config-schema.js";
import type { GoChatInboundMessage, GoChatStoredMessage } from "../types.js";
import type { GoChatDirectStorage } from "./storage.js";

type classifyMediaType = "image" | "audio" | "video" | "file";

function classifyAttachment(mimeType?: string, filename?: string): classifyMediaType {
  const mime = (mimeType ?? "").toLowerCase();
  if (mime.startsWith("image/")) return "image";
  if (mime.startsWith("audio/")) return "audio";
  if (mime.startsWith("video/")) return "video";

  const ext = (filename ?? "").toLowerCase();
  if (/\.(jpe?g|png|gif|webp)$/.test(ext)) return "image";
  if (/\.(mp3|wav|ogg|m4a)$/.test(ext)) return "audio";
  if (/\.(mp4|webm|mov)$/.test(ext)) return "video";

  return "file";
}

type SendRequestBody = {
  conversationId: string;
  conversationName?: string;
  senderId?: string;
  senderName?: string;
  text?: string;
  attachments?: Array<{
    url: string;
    type: "image" | "audio" | "video" | "file";
    name?: string;
    mimeType?: string;
    size?: number;
  }>;
  replyTo?: string;
  isGroupChat?: boolean;
};

export async function handleSend(
  storage: GoChatDirectStorage,
  body: string,
  onInbound: (message: GoChatInboundMessage) => Promise<void>,
): Promise<{ messageId: string; timestamp: number; ok: boolean } | { error: string }> {
  let parsed: SendRequestBody;
  try {
    parsed = JSON.parse(body) as SendRequestBody;
  } catch {
    return { error: "invalid request: malformed JSON" };
  }

  if (!parsed.conversationId?.trim()) {
    return { error: "invalid request: conversationId required" };
  }

  if (!parsed.text?.trim() && (!parsed.attachments || parsed.attachments.length === 0)) {
    return { error: "invalid request: message must have text or attachments" };
  }

  const conversationId = parsed.conversationId.trim();
  const now = Date.now();

  await storage.upsertConversation(conversationId, parsed.conversationName);

  const stored = await storage.appendMessage(conversationId, {
    direction: "inbound",
    senderId: parsed.senderId ?? "web-user",
    senderName: parsed.senderName ?? "",
    text: parsed.text ?? "",
    attachments: (parsed.attachments ?? []).map((a) => ({
      url: a.url,
      type: a.type,
      name: a.name,
      mimeType: a.mimeType,
      size: a.size,
    })),
    replyTo: parsed.replyTo,
  });

  const inboundMessage: GoChatInboundMessage = {
    messageId: stored.id,
    conversationId,
    conversationName: parsed.conversationName ?? "",
    senderId: parsed.senderId ?? "web-user",
    senderName: parsed.senderName ?? "",
    text: parsed.text ?? "",
    attachments: (parsed.attachments ?? []).map((a) => ({
      url: a.url,
      type: a.type,
      name: a.name,
      mimeType: a.mimeType,
      size: a.size,
    })),
    replyTo: parsed.replyTo,
    timestamp: now,
    isGroupChat: parsed.isGroupChat ?? false,
  };

  try {
    await onInbound(inboundMessage);
  } catch {
    // inbound dispatch errors are logged by the caller
  }

  return { messageId: stored.id, timestamp: now, ok: true };
}

export async function handleListConversations(
  storage: GoChatDirectStorage,
): Promise<Array<{
  id: string;
  name: string;
  createdAt: string;
  lastActive: string;
  messageCount: number;
}>> {
  return await storage.listConversations();
}

export async function handleGetMessages(
  storage: GoChatDirectStorage,
  conversationId: string,
  limit?: number,
): Promise<GoChatStoredMessage[] | { error: string }> {
  if (!conversationId?.trim()) {
    return { error: "conversationId required" };
  }
  return await storage.getMessages(conversationId.trim(), limit ?? 100);
}

export async function handleUpload(
  storage: GoChatDirectStorage,
  fileBuffer: Buffer,
  originalFilename: string,
  mimeType: string,
  baseUrl: string,
): Promise<{
  url: string;
  type: classifyMediaType;
  name: string;
  mimeType: string;
  size: number;
}> {
  const savedName = await storage.saveUpload(originalFilename, fileBuffer);
  const type = classifyAttachment(mimeType, originalFilename);
  return {
    url: `${baseUrl}/files/${savedName}`,
    type,
    name: basename(originalFilename),
    mimeType: mimeType || "application/octet-stream",
    size: fileBuffer.length,
  };
}
