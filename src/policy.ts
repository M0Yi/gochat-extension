import type {
  AllowlistMatch,
  ChannelGroupContext,
  GroupPolicy,
} from "../runtime-api.js";
import {
  buildChannelKeyCandidates,
  evaluateMatchedGroupAccessForPolicy,
  normalizeChannelSlug,
  resolveChannelEntryMatchWithFallback,
  resolveNestedAllowlistDecision,
} from "../runtime-api.js";
import type { GoChatConversationConfig } from "./types.js";

function normalizeAllowEntry(raw: string): string {
  return raw.trim().toLowerCase().replace(/^gochat:/i, "");
}

export function resolveGoChatAllowlistMatch(params: {
  allowFrom: string[];
  senderId: string;
}): AllowlistMatch<"wildcard" | "id"> {
  if (params.allowFrom.length === 0) {
    return { allowed: false };
  }
  if (params.allowFrom.includes("*")) {
    return { allowed: true, matchKey: "*", matchSource: "wildcard" };
  }
  const senderId = normalizeAllowEntry(params.senderId);
  if (params.allowFrom.includes(senderId)) {
    return { allowed: true, matchKey: senderId, matchSource: "id" };
  }
  return { allowed: false };
}

export type GoChatConversationMatch = {
  conversationConfig?: GoChatConversationConfig;
  wildcardConfig?: GoChatConversationConfig;
  conversationKey?: string;
  matchSource?: "direct" | "parent" | "wildcard";
  allowed: boolean;
  allowlistConfigured: boolean;
};

export function resolveGoChatConversationMatch(params: {
  conversations?: Record<string, GoChatConversationConfig>;
  conversationId: string;
}): GoChatConversationMatch {
  const conversations = params.conversations ?? {};
  const allowlistConfigured = Object.keys(conversations).length > 0;
  const convCandidates = buildChannelKeyCandidates(params.conversationId);
  const match = resolveChannelEntryMatchWithFallback({
    entries: conversations,
    keys: convCandidates,
    wildcardKey: "*",
    normalizeKey: normalizeChannelSlug,
  });
  const conversationConfig = match.entry;
  const allowed = resolveNestedAllowlistDecision({
    outerConfigured: allowlistConfigured,
    outerMatched: Boolean(conversationConfig),
    innerConfigured: false,
    innerMatched: false,
  });

  return {
    conversationConfig,
    wildcardConfig: match.wildcardEntry,
    conversationKey: match.matchKey ?? match.key,
    matchSource: match.matchSource,
    allowed,
    allowlistConfigured,
  };
}

export function resolveGoChatGroupAllow(params: {
  groupPolicy: GroupPolicy;
  outerAllowFrom: string[];
  innerAllowFrom: string[];
  senderId: string;
}): { allowed: boolean; outerMatch: AllowlistMatch; innerMatch: AllowlistMatch } {
  const outerMatch = resolveGoChatAllowlistMatch({
    allowFrom: params.outerAllowFrom,
    senderId: params.senderId,
  });
  const innerMatch = resolveGoChatAllowlistMatch({
    allowFrom: params.innerAllowFrom,
    senderId: params.senderId,
  });
  const access = evaluateMatchedGroupAccessForPolicy({
    groupPolicy: params.groupPolicy,
    allowlistConfigured: params.outerAllowFrom.length > 0 || params.innerAllowFrom.length > 0,
    allowlistMatched: resolveNestedAllowlistDecision({
      outerConfigured: params.outerAllowFrom.length > 0 || params.innerAllowFrom.length > 0,
      outerMatched: params.outerAllowFrom.length > 0 ? outerMatch.allowed : true,
      innerConfigured: params.innerAllowFrom.length > 0,
      innerMatched: innerMatch.allowed,
    }),
  });

  return {
    allowed: access.allowed,
    outerMatch:
      params.groupPolicy === "open"
        ? { allowed: true }
        : params.groupPolicy === "disabled"
          ? { allowed: false }
          : outerMatch,
    innerMatch:
      params.groupPolicy === "open"
        ? { allowed: true }
        : params.groupPolicy === "disabled"
          ? { allowed: false }
          : innerMatch,
  };
}

export function resolveGoChatGroupToolPolicy(
  params: ChannelGroupContext,
): { allow?: string[]; deny?: string[] } | undefined {
  const cfg = params.cfg as {
    channels?: { gochat?: { conversations?: Record<string, GoChatConversationConfig> } };
  };
  const conversationId = params.groupId?.trim();
  if (!conversationId) {
    return undefined;
  }
  const match = resolveGoChatConversationMatch({
    conversations: cfg.channels?.gochat?.conversations,
    conversationId,
  });
  return match.conversationConfig?.tools ?? match.wildcardConfig?.tools;
}
