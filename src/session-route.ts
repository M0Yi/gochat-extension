import {
  buildChannelOutboundSessionRoute,
  type ChannelOutboundSessionRouteParams,
} from "openclaw/plugin-sdk/core";
import { stripGoChatTargetPrefix } from "./normalize.js";

export function resolveGoChatOutboundSessionRoute(
  params: ChannelOutboundSessionRouteParams,
) {
  const conversationId = stripGoChatTargetPrefix(params.target);
  if (!conversationId) {
    return null;
  }
  return buildChannelOutboundSessionRoute({
    cfg: params.cfg,
    agentId: params.agentId,
    channel: "gochat",
    accountId: params.accountId,
    peer: {
      kind: "direct",
      id: conversationId,
    },
    chatType: "direct",
    from: `gochat:conv:${conversationId}`,
    to: `gochat:${conversationId}`,
  });
}
