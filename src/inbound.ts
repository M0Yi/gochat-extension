import { buildAgentMediaPayload } from "openclaw/plugin-sdk/media-runtime";
import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
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
import {
  buildSubagentPermissionStatusMessage,
  inspectSubagentPermissionStatus,
  type SubagentPermissionStatus,
} from "./subagent-permission-status.js";
import type { CoreConfig, GoChatAttachment, GoChatInboundMessage, GroupPolicy } from "./types.js";

const CHANNEL_ID = "gochat" as const;
const execFileAsync = promisify(execFile);
const LOCAL_AUDIO_SKILL_NAME = "gochat-local-audio-notes";
const DEFAULT_MAX_TRANSCRIPT_CHARS = 12000;

type ResolvedMediaInfo = {
  path: string;
  contentType: string;
  kind: "image" | "audio" | "video" | "document" | "unknown";
  name?: string;
};

type RemoteFetchResult = {
  buffer: Buffer;
  contentType?: string;
};

type LocalAudioTranscriptionResult = {
  attachmentName?: string;
  path: string;
  engine: string;
  model: string;
  language?: string;
  text: string;
};

const conversationPermissionAnnouncements = new Map<string, string>();

function normalizeHost(value: string | undefined): string | null {
  const host = String(value ?? "").trim().toLowerCase();
  return host ? host : null;
}

function tryParseUrl(value: string): URL | null {
  try {
    return new URL(value);
  } catch {
    return null;
  }
}

function resolveTrustedAttachmentHosts(account: ResolvedGoChatAccount): Set<string> {
  const hosts = new Set<string>();
  const relayUrl = tryParseUrl(account.relayPlatformUrl);
  const relayHost = normalizeHost(relayUrl?.hostname);
  if (relayHost) {
    hosts.add(relayHost);
  }
  for (const host of account.config.trustedAttachmentHosts ?? []) {
    const normalized = normalizeHost(host);
    if (normalized) {
      hosts.add(normalized);
    }
  }
  return hosts;
}

function resolveLocalAudioScriptCandidates(): string[] {
  const currentDir = dirname(fileURLToPath(import.meta.url));
  const repoBundled = resolve(
    currentDir,
    "../skills/gochat-local-audio-notes/scripts/transcribe_audio.py",
  );
  const homeBundled = resolve(
    process.env.OPENCLAW_STATE_DIR || resolve(process.env.HOME || "~", ".openclaw"),
    "skills/gochat-local-audio-notes/scripts/transcribe_audio.py",
  );
  return [homeBundled, repoBundled];
}

function clipTranscript(text: string, maxChars: number): string {
  if (text.length <= maxChars) {
    return text;
  }
  return `${text.slice(0, Math.max(0, maxChars)).trim()}\n...[transcript truncated]`;
}

function isGatewayPairingRequiredError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error ?? "");
  return /pairing required/i.test(message) || /GatewayClientRequestError/i.test(message);
}

async function maybePushSubagentPermissionStatus(params: {
  conversationId: string;
  accountId: string;
  statusSink?: (patch: { lastOutboundAt?: number }) => void;
  runtime: RuntimeEnv;
}): Promise<SubagentPermissionStatus> {
  const status = await inspectSubagentPermissionStatus();
  const conversationKey = `${params.accountId}:${params.conversationId}`;
  const previousSignature = conversationPermissionAnnouncements.get(conversationKey);
  if (previousSignature === status.detailSignature) {
    return status;
  }

  conversationPermissionAnnouncements.set(conversationKey, status.detailSignature);
  if (status.state === "unknown") {
    return status;
  }

  try {
    await sendMessageGoChat(params.conversationId, buildSubagentPermissionStatusMessage(status), {
      accountId: params.accountId,
    });
    params.statusSink?.({ lastOutboundAt: Date.now() });
  } catch {
    params.runtime.error?.("gochat: failed to push subagent permission status");
  }
  return status;
}

function buildAudioTranscriptContext(transcripts: LocalAudioTranscriptionResult[]): string {
  if (!transcripts.length) {
    return "";
  }
  return transcripts
    .map((entry, index) => {
      const label = entry.attachmentName || `audio-${index + 1}`;
      const meta = [
        `engine=${entry.engine}`,
        `model=${entry.model}`,
        entry.language ? `language=${entry.language}` : "",
      ]
        .filter(Boolean)
        .join(" ");
      return [
        `Local audio transcript for ${label}${meta ? ` (${meta})` : ""}:`,
        entry.text,
      ]
        .filter(Boolean)
        .join("\n");
    })
    .join("\n\n");
}

async function resolveLocalAudioTranscripts(params: {
  mediaList: ResolvedMediaInfo[];
  account: ResolvedGoChatAccount;
  logger: { warn: (message: string) => void; info?: (message: string) => void };
}): Promise<LocalAudioTranscriptionResult[]> {
  const { mediaList, account, logger } = params;
  const settings = account.config.localAudioTranscription;
  if (settings?.enabled === false) {
    return [];
  }

  const audioMedia = mediaList.filter((item) => item.kind === "audio");
  if (!audioMedia.length) {
    return [];
  }

  const finalScriptPath =
    resolveLocalAudioScriptCandidates().find((candidate) => existsSync(candidate)) ?? null;
  if (!finalScriptPath) {
    logger.warn("[gochat] local audio transcription script not found");
    return [];
  }

  const maxChars = settings?.maxTranscriptChars ?? DEFAULT_MAX_TRANSCRIPT_CHARS;
  const results: LocalAudioTranscriptionResult[] = [];

  for (const media of audioMedia) {
    const args = [
      finalScriptPath,
      media.path,
      "--engine",
      settings?.engine ?? "auto",
      "--model",
      settings?.model ?? process.env.GOCHAT_AUDIO_MODEL ?? "base",
      "--task",
      settings?.task ?? "transcribe",
      "--device",
      settings?.device ?? process.env.GOCHAT_AUDIO_DEVICE ?? "auto",
      "--compute-type",
      settings?.computeType ?? process.env.GOCHAT_AUDIO_COMPUTE_TYPE ?? "auto",
      "--beam-size",
      String(settings?.beamSize ?? Number(process.env.GOCHAT_AUDIO_BEAM_SIZE || 5)),
      "--output-format",
      "json",
    ];
    if (settings?.language) {
      args.push("--language", settings.language);
    }
    if (settings?.wordTimestamps) {
      args.push("--word-timestamps");
    }

    try {
      const { stdout } = await execFileAsync("python3", args, {
        timeout: 15 * 60 * 1000,
        maxBuffer: 20 * 1024 * 1024,
      });
      const parsed = JSON.parse(stdout) as {
        ok?: boolean;
        engine?: string;
        model?: string;
        language?: string;
        text?: string;
      };
      const transcriptText = clipTranscript(String(parsed.text ?? "").trim(), maxChars);
      if (!transcriptText) {
        continue;
      }
      results.push({
        attachmentName: media.name,
        path: media.path,
        engine: String(parsed.engine ?? settings?.engine ?? "auto"),
        model: String(parsed.model ?? settings?.model ?? "base"),
        language: parsed.language ? String(parsed.language) : undefined,
        text: transcriptText,
      });
    } catch (err) {
      logger.warn(
        `[gochat] local audio transcription failed for ${media.path}: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }

  return results;
}

function shouldBypassRemoteMediaSsrf(params: {
  url: string;
  trustedHosts: Set<string>;
}): boolean {
  const parsed = tryParseUrl(params.url);
  if (!parsed) {
    return false;
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    return false;
  }
  return params.trustedHosts.has(parsed.hostname.trim().toLowerCase());
}

async function fetchTrustedRemoteMedia(params: {
  url: string;
  trustedHosts: Set<string>;
  logger: { warn: (message: string) => void; info?: (message: string) => void };
  redirectCount?: number;
}): Promise<RemoteFetchResult> {
  const redirectCount = params.redirectCount ?? 0;
  if (redirectCount > 5) {
    throw new Error("too many redirects");
  }

  const parsed = tryParseUrl(params.url);
  if (!parsed) {
    throw new Error("invalid attachment URL");
  }
  if (!params.trustedHosts.has(parsed.hostname.trim().toLowerCase())) {
    throw new Error(`attachment host is not trusted: ${parsed.hostname}`);
  }

  const response = await fetch(parsed, {
    redirect: "manual",
    signal: AbortSignal.timeout(15000),
  });

  if (response.status >= 300 && response.status < 400) {
    const location = response.headers.get("location");
    if (!location) {
      throw new Error(`redirect (${response.status}) without location`);
    }
    const nextUrl = new URL(location, parsed).toString();
    return await fetchTrustedRemoteMedia({
      ...params,
      url: nextUrl,
      redirectCount: redirectCount + 1,
    });
  }

  if (!response.ok) {
    throw new Error(`download failed (${response.status})`);
  }

  const arrayBuffer = await response.arrayBuffer();
  return {
    buffer: Buffer.from(arrayBuffer),
    contentType: response.headers.get("content-type") ?? undefined,
  };
}

async function resolveGoChatMedia(
  attachments: GoChatAttachment[],
  account: ResolvedGoChatAccount,
): Promise<ResolvedMediaInfo[]> {
  const core = getGoChatRuntime();
  const results: ResolvedMediaInfo[] = [];
  const logger = core.logging.getChildLogger({ channel: "gochat" });
  const trustedHosts = resolveTrustedAttachmentHosts(account);

  for (const attachment of attachments) {
    if (!attachment.url) {
      continue;
    }

    try {
      const fetchResult = shouldBypassRemoteMediaSsrf({
        url: attachment.url,
        trustedHosts,
      })
        ? await fetchTrustedRemoteMedia({
            url: attachment.url,
            trustedHosts,
            logger,
          })
        : await core.channel.media.fetchRemoteMedia({
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
          name: attachment.name,
        });
      }
    } catch (err) {
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
  const logger = core.logging.getChildLogger({ channel: "gochat", accountId: account.accountId });
  const audioTranscripts = mediaList.length
    ? await resolveLocalAudioTranscripts({
        mediaList,
        account,
        logger,
      })
    : [];
  const mediaPlaceholder = buildMediaPlaceholder(mediaList);
  const audioTranscriptContext = buildAudioTranscriptContext(audioTranscripts);
  const bodyText = [rawBody, mediaPlaceholder, audioTranscriptContext].filter(Boolean).join("\n\n").trim();

  const currentPermissionStatus = await maybePushSubagentPermissionStatus({
    conversationId,
    accountId: account.accountId,
    statusSink,
    runtime,
  });

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

  try {
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
        skillFilter: mediaList.some((item) => item.kind === "audio")
          ? Array.from(new Set([...(convConfig?.skills ?? []), LOCAL_AUDIO_SKILL_NAME]))
          : convConfig?.skills,
        disableBlockStreaming:
          typeof account.config.blockStreaming === "boolean"
            ? !account.config.blockStreaming
            : undefined,
      },
    });
  } catch (error) {
    if (!isGatewayPairingRequiredError(error)) {
      throw error;
    }

    runtime.error?.(`gochat: gateway pairing required: ${error instanceof Error ? error.message : String(error)}`);

    try {
      const latestPermissionStatus = currentPermissionStatus.state === "pending_approval"
        ? currentPermissionStatus
        : await inspectSubagentPermissionStatus({ forceRefresh: true });

      if (
        currentPermissionStatus.state === "pending_approval" &&
        latestPermissionStatus.state === "pending_approval" &&
        currentPermissionStatus.detailSignature === latestPermissionStatus.detailSignature
      ) {
        return;
      }

      await sendMessageGoChat(conversationId, buildSubagentPermissionStatusMessage(latestPermissionStatus), {
        accountId: account.accountId,
      });
      statusSink?.({ lastOutboundAt: Date.now() });
      return;
    } catch (replyError) {
      runtime.error?.(`gochat: failed to send pairing-required reply: ${String(replyError)}`);
      throw error;
    }
  }
}
