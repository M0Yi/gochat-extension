import { buildAgentMediaPayload } from "openclaw/plugin-sdk/media-runtime";
import {
  GROUP_POLICY_BLOCKED_LABEL,
  createChannelPairingController,
  deliverFormattedTextWithAttachments,
  dispatchInboundReplyWithBase,
  logInboundDrop,
  readStoreAllowFromForDmPolicy,
  resolveDmGroupAccessWithCommandGate,
  resolveAllowlistProviderRuntimeGroupPolicy,
  resolveDefaultGroupPolicy,
  warnMissingProviderGroupPolicyFallbackOnce,
  type OutboundReplyPayload,
  type OpenClawConfig,
  type RuntimeEnv,
} from "../runtime-api.js";
import type { ResolvedGoChatAccount } from "./accounts.js";
import { resolveGoChatAllowlistMatch, resolveGoChatConversationMatch } from "./policy.js";
import { getGoChatRuntime } from "./runtime.js";
import { sendMessageGoChat } from "./send.js";
import type { CoreConfig, GoChatAttachment, GoChatInboundMessage, GroupPolicy } from "./types.js";

const CHANNEL_ID = "gochat" as const;

type ResolvedMediaInfo = {
  path: string;
  contentType: string;
  kind: "image" | "audio" | "video" | "document" | "unknown";
};

async function resolveGoChatMedia(
  attachments: GoChatAttachment[],
  account: ResolvedGoChatAccount,
): Promise<ResolvedMediaInfo[]> {
  const core = getGoChatRuntime();
  const results: ResolvedMediaInfo[] = [];

  for (const attachment of attachments) {
    if (!attachment.url) {
      continue;
    }

    try {
      const fetchResult = await core.channel.media.fetchRemoteMedia({
        url: attachment.url,
        ssrfPolicy: account.config.allowPrivateNetwork
          ? { allowPrivateNetwork: true }
          : undefined,
      });

      if (!fetchResult || !fetchResult.buffer) {
        continue;
      }

      const saved = await core.channel.media.saveMediaBuffer(
        fetchResult.buffer,
        fetchResult.contentType ?? attachment.mimeType ?? "application/octet-stream",
        "inbound",
      );

      if (saved) {
        const contentType = fetchResult.contentType ?? attachment.mimeType ?? "application/octet-stream";
        const kind = classifyMediaKind(contentType);
        results.push({
          path: saved.path,
          contentType,
          kind,
        });
      }
    } catch (err) {
      const core2 = getGoChatRuntime();
      const logger = core2.logging.getChildLogger({ channel: "gochat" });
      logger.warn(
        `[gochat] failed to fetch media ${attachment.url}: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  return results;
}

function classifyMediaKind(
  mimeType: string,
): "image" | "audio" | "video" | "document" | "unknown" {
  const lower = mimeType.toLowerCase();
  if (lower.startsWith("image/")) {
    return "image";
  }
  if (lower.startsWith("audio/")) {
    return "audio";
  }
  if (lower.startsWith("video/")) {
    return "video";
  }
  return "document";
}

function buildMediaPlaceholder(mediaList: ResolvedMediaInfo[]): string {
  if (mediaList.length === 0) {
    return "";
  }

  const counts: Record<string, number> = {};
  for (const m of mediaList) {
    const label = m.kind === "unknown" ? "file" : m.kind;
    counts[label] = (counts[label] ?? 0) + 1;
  }

  const parts = Object.entries(counts).map(([kind, count]) =>
    count === 1 ? `<media:${kind}>` : `<media:${kind}> (${count} files)`,
  );

  return parts.join(" ");
}

async function deliverGoChatReply(params: {
  payload: OutboundReplyPayload;
  conversationId: string;
  accountId: string;
  statusSink?: (patch: { lastOutboundAt?: number }) => void;
}): Promise<void> {
  const { payload, conversationId, accountId, statusSink } = params;
  await deliverFormattedTextWithAttachments({
    payload,
    send: async ({ text, replyToId }) => {
      await sendMessageGoChat(conversationId, text, {
        accountId,
        replyTo: replyToId,
      });
      statusSink?.({ lastOutboundAt: Date.now() });
    },
  });
}

export async function handleGoChatInbound(params: {
  message: GoChatInboundMessage;
  account: ResolvedGoChatAccount;
  config: CoreConfig;
  runtime: RuntimeEnv;
  statusSink?: (patch: { lastInboundAt?: number; lastOutboundAt?: number }) => void;
}): Promise<void> {
  const { message, account, config, runtime, statusSink } = params;
  const core = getGoChatRuntime();
  const pairing = createChannelPairingController({
    core,
    channel: CHANNEL_ID,
    accountId: account.accountId,
  });

  const rawBody = message.text?.trim() ?? "";
  const attachments = message.attachments ?? [];
  const hasAttachments = attachments.length > 0;
  if (!rawBody && !hasAttachments) {
    return;
  }

  const senderId = message.senderId;
  const senderName = message.senderName;
  const conversationId = message.conversationId;
  const conversationName = message.conversationName;
  const isGroup = message.isGroupChat;

  statusSink?.({ lastInboundAt: message.timestamp });

  const dmPolicy = account.config.dmPolicy ?? "pairing";
  const defaultGroupPolicy = resolveDefaultGroupPolicy(config as OpenClawConfig);
  const { groupPolicy, providerMissingFallbackApplied } =
    resolveAllowlistProviderRuntimeGroupPolicy({
      providerConfigPresent:
        ((config.channels as Record<string, unknown> | undefined)?.gochat ?? undefined) !==
        undefined,
      groupPolicy: account.config.groupPolicy as GroupPolicy | undefined,
      defaultGroupPolicy,
    });
  warnMissingProviderGroupPolicyFallbackOnce({
    providerMissingFallbackApplied,
    providerKey: "gochat",
    accountId: account.accountId,
    blockedLabel: GROUP_POLICY_BLOCKED_LABEL.room,
    log: (msg) => runtime.log?.(msg),
  });

  const configAllowFrom = (account.config.allowFrom ?? []).map((v) =>
    String(v).trim().toLowerCase(),
  );
  const configGroupAllowFrom = (account.config.groupAllowFrom ?? []).map((v) =>
    String(v).trim().toLowerCase(),
  );
  const storeAllowFrom = await readStoreAllowFromForDmPolicy({
    provider: CHANNEL_ID,
    accountId: account.accountId,
    dmPolicy,
    readStore: pairing.readStoreForDmPolicy,
  });
  const storeAllowList = (storeAllowFrom ?? []).map((v) => String(v).trim().toLowerCase());

  const convMatch = resolveGoChatConversationMatch({
    conversations: account.config.conversations,
    conversationId,
  });
  const convConfig = convMatch.conversationConfig;

  if (isGroup && !convMatch.allowed) {
    runtime.log?.(`gochat: drop conversation ${conversationId} (not allowlisted)`);
    return;
  }
  if (convConfig?.enabled === false) {
    runtime.log?.(`gochat: drop conversation ${conversationId} (disabled)`);
    return;
  }

  const convAllowFrom = (convConfig?.allowFrom ?? []).map((v) =>
    String(v).trim().toLowerCase(),
  );

  const allowTextCommands = core.channel.commands.shouldHandleTextCommands({
    cfg: config as OpenClawConfig,
    surface: CHANNEL_ID,
  });
  const useAccessGroups =
    (config.commands as Record<string, unknown> | undefined)?.useAccessGroups !== false;
  const hasControlCommand = core.channel.text.hasControlCommand(rawBody, config as OpenClawConfig);

  const normalizeSenderId = (id: string) => id.trim().toLowerCase().replace(/^gochat:/i, "");

  const access = resolveDmGroupAccessWithCommandGate({
    isGroup,
    dmPolicy,
    groupPolicy,
    allowFrom: configAllowFrom,
    groupAllowFrom: configGroupAllowFrom,
    storeAllowFrom: storeAllowList,
    isSenderAllowed: (allowFrom) =>
      resolveGoChatAllowlistMatch({ allowFrom, senderId }).allowed,
    command: {
      useAccessGroups,
      allowTextCommands,
      hasControlCommand,
    },
  });
  const commandAuthorized = access.commandAuthorized;
  const effectiveGroupAllowFrom = access.effectiveGroupAllowFrom;

  if (isGroup) {
    if (access.decision !== "allow") {
      runtime.log?.(`gochat: drop group sender ${senderId} (reason=${access.reason})`);
      return;
    }
  } else {
    if (access.decision !== "allow") {
      if (access.decision === "pairing") {
        await pairing.issueChallenge({
          senderId,
          senderIdLine: `Your user id: ${senderId}`,
          meta: { name: senderName || undefined },
          sendPairingReply: async (text) => {
            await sendMessageGoChat(conversationId, text, { accountId: account.accountId });
            statusSink?.({ lastOutboundAt: Date.now() });
          },
          onReplyError: (err) => {
            runtime.error?.(`gochat: pairing reply failed for ${senderId}: ${String(err)}`);
          },
        });
      }
      runtime.log?.(`gochat: drop DM sender ${senderId} (reason=${access.reason})`);
      return;
    }
  }

  if (access.shouldBlockControlCommand) {
    logInboundDrop({
      log: (msg) => runtime.log?.(msg),
      channel: CHANNEL_ID,
      reason: "control command (unauthorized)",
      target: senderId,
    });
    return;
  }

  const mentionRegexes = core.channel.mentions.buildMentionRegexes(config as OpenClawConfig);
  const wasMentioned = mentionRegexes.length
    ? core.channel.mentions.matchesMentionPatterns(rawBody, mentionRegexes)
    : false;
  const shouldRequireMention = isGroup
    ? convConfig?.requireMention ?? true
    : false;

  if (isGroup && shouldRequireMention && !wasMentioned && !hasControlCommand) {
    runtime.log?.(`gochat: drop conversation ${conversationId} (no mention)`);
    return;
  }

  const route = core.channel.routing.resolveAgentRoute({
    cfg: config as OpenClawConfig,
    channel: CHANNEL_ID,
    accountId: account.accountId,
    peer: {
      kind: isGroup ? "group" : "direct",
      id: isGroup ? conversationId : senderId,
    },
  });

  const fromLabel = isGroup
    ? `conv:${conversationName || conversationId}`
    : senderName || `user:${senderId}`;
  const storePath = core.channel.session.resolveStorePath(
    (config.session as Record<string, unknown> | undefined)?.store as string | undefined,
    {
      agentId: route.agentId,
    },
  );
  const envelopeOptions = core.channel.reply.resolveEnvelopeFormatOptions(config as OpenClawConfig);
  const previousTimestamp = core.channel.session.readSessionUpdatedAt({
    storePath,
    sessionKey: route.sessionKey,
  });

  const mediaList = hasAttachments
    ? await resolveGoChatMedia(message.attachments, account)
    : [];
  const mediaPlaceholder = buildMediaPlaceholder(mediaList);
  const bodyText = [rawBody, mediaPlaceholder].filter(Boolean).join("\n").trim();

  const body = core.channel.reply.formatAgentEnvelope({
    channel: "GoChat",
    from: fromLabel,
    timestamp: message.timestamp,
    previousTimestamp,
    envelope: envelopeOptions,
    body: bodyText,
  });

  const convSystemPrompt = convConfig?.systemPrompt?.trim() || undefined;
  const mediaPayload = buildAgentMediaPayload(mediaList);

  const ctxPayload = core.channel.reply.finalizeInboundContext({
    Body: body,
    BodyForAgent: bodyText,
    RawBody: rawBody,
    CommandBody: rawBody,
    From: isGroup ? `gochat:conv:${conversationId}` : `gochat:${senderId}`,
    To: `gochat:${conversationId}`,
    SessionKey: route.sessionKey,
    AccountId: route.accountId,
    ChatType: isGroup ? "group" : "direct",
    ConversationLabel: fromLabel,
    SenderName: senderName || undefined,
    SenderId: senderId,
    GroupSubject: isGroup ? conversationName || conversationId : undefined,
    GroupSystemPrompt: isGroup ? convSystemPrompt : undefined,
    Provider: CHANNEL_ID,
    Surface: CHANNEL_ID,
    WasMentioned: isGroup ? wasMentioned : undefined,
    MessageSid: message.messageId,
    Timestamp: message.timestamp,
    OriginatingChannel: CHANNEL_ID,
    OriginatingTo: `gochat:${conversationId}`,
    CommandAuthorized: commandAuthorized,
    ...mediaPayload,
  });

  console.log(`[gochat:inbound] Received message ${message.messageId} from ${senderId} in conversation ${conversationId}`);

  await dispatchInboundReplyWithBase({
    cfg: config as OpenClawConfig,
    channel: CHANNEL_ID,
    accountId: account.accountId,
    route,
    storePath,
    ctxPayload,
    core,
    deliver: async (payload) => {
      await deliverGoChatReply({
        payload,
        conversationId,
        accountId: account.accountId,
        statusSink,
      });
    },
    onRecordError: (err) => {
      runtime.error?.(`gochat: failed updating session meta: ${String(err)}`);
    },
    onDispatchError: (err, info) => {
      runtime.error?.(`gochat ${info.kind} reply failed: ${String(err)}`);
    },
    replyOptions: {
      skillFilter: convConfig?.skills,
      disableBlockStreaming:
        typeof account.config.blockStreaming === "boolean"
          ? !account.config.blockStreaming
          : undefined,
    },
  });
}
