import {
  resolveLoggerBackedRuntime,
  type RuntimeEnv,
} from "openclaw/plugin-sdk/extension-shared";
import { resolveGoChatAccount } from "../accounts.js";
import { handleGoChatInbound } from "../inbound.js";
import { getGoChatRuntime } from "../runtime.js";
import { createRelayWSConnection } from "./relay-ws.js";
import { setRelayWsSender } from "../send.js";
import type {
  CoreConfig,
  GoChatInboundMessage,
} from "../types.js";

export async function monitorGoChatProvider(
  opts: {
    accountId?: string;
    config?: CoreConfig;
    runtime?: RuntimeEnv;
    abortSignal?: AbortSignal;
    onMessage?: (message: GoChatInboundMessage) => void | Promise<void>;
    statusSink?: (patch: { lastInboundAt?: number; lastOutboundAt?: number }) => void;
  } = {},
): Promise<{ stop: () => void }> {
  const core = getGoChatRuntime();
  const cfg = opts.config ?? (core.config.loadConfig() as CoreConfig);
  const account = resolveGoChatAccount({
    cfg,
    accountId: opts.accountId,
  });
  const runtime: RuntimeEnv = resolveLoggerBackedRuntime(
    opts.runtime,
    core.logging.getChildLogger(),
  );

  if (!account.secret) {
    throw new Error(`GoChat secret not configured for relay account "${account.accountId}"`);
  }

  const logger = core.logging.getChildLogger({
    channel: "gochat",
    accountId: account.accountId,
  });

  const { start: startRelay, stop: stopRelay, send: sendRelay } = createRelayWSConnection({
    platformUrl: account.relayPlatformUrl,
    channelId: account.channelId,
    secret: account.secret,
    onMessage: async (message) => {
      core.channel.activity.record({
        channel: "gochat",
        accountId: account.accountId,
        direction: "inbound",
        at: message.timestamp ?? Date.now(),
      });
      if (opts.onMessage) {
        await opts.onMessage(message);
        return;
      }
      await handleGoChatInbound({
        message,
        account,
        config: cfg,
        runtime,
        statusSink: opts.statusSink,
      });
    },
    onError: (error) => {
      logger.error(`[gochat:${account.accountId}] relay error: ${error.message}`);
    },
    abortSignal: opts.abortSignal,
  });

  if (opts.abortSignal?.aborted) {
    return { stop: stopRelay };
  }

  setRelayWsSender(sendRelay);
  await startRelay();
  logger.info(
    `[gochat:${account.accountId}] relay connected to ${account.relayPlatformUrl}`,
  );

  return {
    stop: () => {
      setRelayWsSender(null);
      stopRelay();
    },
  };
}
