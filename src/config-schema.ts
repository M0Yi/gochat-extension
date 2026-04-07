import {
  BlockStreamingCoalesceSchema,
  DmPolicySchema,
  GroupPolicySchema,
  MarkdownConfigSchema,
  ReplyRuntimeConfigSchemaShape,
  ToolPolicySchema,
  requireOpenAllowFrom,
} from "openclaw/plugin-sdk/channel-config-schema";
import { requireChannelOpenAllowFrom } from "openclaw/plugin-sdk/extension-shared";
import { z } from "openclaw/plugin-sdk/zod";
import { buildSecretInputSchema } from "./secret-input.js";

export const GoChatConversationSchema = z
  .object({
    requireMention: z.boolean().optional(),
    tools: ToolPolicySchema,
    skills: z.array(z.string()).optional(),
    enabled: z.boolean().optional(),
    allowFrom: z.array(z.string()).optional(),
    systemPrompt: z.string().optional(),
  })
  .strict();

const GoChatAttachmentSchema = z.object({
  url: z.string().url(),
  type: z.enum(["image", "audio", "video", "file"]),
  name: z.string().optional(),
  mimeType: z.string().optional(),
  size: z.number().int().positive().optional(),
});

export const GoChatWebhookPayloadSchema = z.object({
  messageId: z.string().min(1),
  conversationId: z.string().min(1),
  conversationName: z.string(),
  senderId: z.string().min(1),
  senderName: z.string(),
  text: z.string(),
  attachments: z.array(GoChatAttachmentSchema).optional().default([]),
  replyTo: z.string().optional(),
  timestamp: z.number(),
  isGroupChat: z.boolean(),
});

export const GoChatAccountSchemaBase = z
  .object({
    name: z.string().optional(),
    enabled: z.boolean().optional(),
    mode: z.enum(["local", "relay"]).optional().default("relay"),
    markdown: MarkdownConfigSchema,
    webhookSecret: buildSecretInputSchema().optional(),
    webhookSecretFile: z.string().optional(),
    dmPolicy: DmPolicySchema.optional().default("open"),
    allowFrom: z.array(z.string()).optional(),
    groupAllowFrom: z.array(z.string()).optional(),
    groupPolicy: GroupPolicySchema.optional().default("allowlist"),
    conversations: z.record(z.string(), GoChatConversationSchema.optional()).optional(),
    allowPrivateNetwork: z.boolean().optional(),
    trustedAttachmentHosts: z.array(z.string()).optional(),
    directPort: z.number().int().positive().optional(),
    directHost: z.string().optional(),
    relayPlatformUrl: z.string().optional(),
    channelId: z.string().optional(),
    ...ReplyRuntimeConfigSchemaShape,
  })
  .strict();

export const GoChatAccountSchema = GoChatAccountSchemaBase.superRefine((value, ctx) => {
  requireChannelOpenAllowFrom({
    channel: "gochat",
    policy: value.dmPolicy,
    allowFrom: value.allowFrom,
    ctx,
    requireOpenAllowFrom,
  });
});

export const GoChatConfigSchema = GoChatAccountSchemaBase.extend({
  accounts: z.record(z.string(), GoChatAccountSchema.optional()).optional(),
  defaultAccount: z.string().optional(),
}).superRefine((value, ctx) => {
  requireChannelOpenAllowFrom({
    channel: "gochat",
    policy: value.dmPolicy,
    allowFrom: value.allowFrom,
    ctx,
    requireOpenAllowFrom,
  });
});
