import type {
  BlockStreamingCoalesceConfig,
  DmPolicy,
  GroupPolicy,
  SecretInput,
} from "../runtime-api.js";

export type { DmPolicy, GroupPolicy };

export type GoChatConversationConfig = {
  requireMention?: boolean;
  tools?: { allow?: string[]; deny?: string[] };
  skills?: string[];
  enabled?: boolean;
  allowFrom?: string[];
  systemPrompt?: string;
};

export type GoChatMode = "local" | "relay";

export const DEFAULT_LOCAL_PORT = 9750;
export const DEFAULT_LOCAL_HOST = "0.0.0.0";
export const DEFAULT_RELAY_WS_URL = "wss://fund.moyi.vip/ws/plugin";
export const DEFAULT_RELAY_HTTP_URL = "https://fund.moyi.vip";

export type GoChatAccountConfig = {
  name?: string;
  enabled?: boolean;
  mode?: GoChatMode;
  webhookSecret?: SecretInput;
  webhookSecretFile?: string;
  dmPolicy?: DmPolicy;
  allowFrom?: string[];
  groupAllowFrom?: string[];
  groupPolicy?: GroupPolicy;
  conversations?: Record<string, GoChatConversationConfig>;
  historyLimit?: number;
  dmHistoryLimit?: number;
  textChunkLimit?: number;
  chunkMode?: "length" | "newline";
  blockStreaming?: boolean;
  blockStreamingCoalesce?: BlockStreamingCoalesceConfig;
  responsePrefix?: string;
  mediaMaxMb?: number;
  allowPrivateNetwork?: boolean;
  trustedAttachmentHosts?: string[];
  directPort?: number;
  directHost?: string;
  relayPlatformUrl?: string;
  channelId?: string;
};

export type GoChatConfig = {
  accounts?: Record<string, GoChatAccountConfig>;
  defaultAccount?: string;
} & GoChatAccountConfig;

export type CoreConfig = {
  channels?: {
    gochat?: GoChatConfig;
  };
  [key: string]: unknown;
};

export type GoChatAttachment = {
  url: string;
  type: "image" | "audio" | "video" | "file";
  name?: string;
  mimeType?: string;
  size?: number;
};

export type GoChatInboundMessage = {
  messageId: string;
  conversationId: string;
  conversationName: string;
  senderId: string;
  senderName: string;
  text: string;
  attachments: GoChatAttachment[];
  replyTo?: string;
  timestamp: number;
  isGroupChat: boolean;
};

export type GoChatSendResult = {
  messageId: string;
  conversationId: string;
  timestamp?: number;
};

export type GoChatSendOptions = {
  accountId?: string;
  replyTo?: string;
  verbose?: boolean;
  cfg?: CoreConfig;
};

export type GoChatWebhookPayload = {
  messageId: string;
  conversationId: string;
  conversationName: string;
  senderId: string;
  senderName: string;
  text: string;
  attachments?: GoChatAttachment[];
  replyTo?: string;
  timestamp: number;
  isGroupChat: boolean;
};

export type GoChatMonitorOptions = {
  accountId?: string;
  config?: CoreConfig;
  runtime?: import("../runtime-api.js").RuntimeEnv;
  abortSignal?: AbortSignal;
  onMessage?: (message: GoChatInboundMessage) => void | Promise<void>;
  statusSink?: (patch: { lastInboundAt?: number; lastOutboundAt?: number }) => void;
};

export type GoChatStoredConversation = {
  id: string;
  name: string;
  createdAt: string;
  lastActive: string;
  messageCount: number;
};

export type GoChatStoredMessage = {
  id: string;
  conversationId: string;
  direction: "inbound" | "outbound";
  senderId: string;
  senderName: string;
  text: string;
  attachments: GoChatAttachment[];
  replyTo?: string;
  timestamp: number;
};
