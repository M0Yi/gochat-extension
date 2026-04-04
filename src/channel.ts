import { describeWebhookAccountSnapshot } from "openclaw/plugin-sdk/account-helpers";
import { formatAllowFromLowercase } from "openclaw/plugin-sdk/allow-from";
import {
  adaptScopedAccountAccessor,
  createScopedChannelConfigAdapter,
  createScopedDmSecurityResolver,
} from "openclaw/plugin-sdk/channel-config-helpers";
import { createAccountStatusSink } from "openclaw/plugin-sdk/channel-lifecycle";
import {
  createLoggedPairingApprovalNotifier,
  createPairingPrefixStripper,
} from "openclaw/plugin-sdk/channel-pairing";
import { createAllowlistProviderRouteAllowlistWarningCollector } from "openclaw/plugin-sdk/channel-policy";
import { createChatChannelPlugin } from "openclaw/plugin-sdk/core";
import { runStoppablePassiveMonitor } from "openclaw/plugin-sdk/extension-shared";
import {
  buildWebhookChannelStatusSummary,
  createComputedAccountStatusAdapter,
  createDefaultChannelRuntimeState,
} from "openclaw/plugin-sdk/status-helpers";
import {
  buildChannelConfigSchema,
  clearAccountEntryFields,
  DEFAULT_ACCOUNT_ID,
  type ChannelPlugin,
  type OpenClawConfig,
} from "../runtime-api.js";
import {
  listGoChatAccountIds,
  resolveDefaultGoChatAccountId,
  resolveGoChatAccount,
  type ResolvedGoChatAccount,
} from "./accounts.js";

import { GoChatConfigSchema } from "./config-schema.js";
import { GoChatChannelConfigSchema } from "./config-surface.js";
import { monitorGoChatProvider } from "./gochat/monitor.js";
import { resolveGoChatGroupToolPolicy } from "./policy.js";
import { getGoChatRuntime } from "./runtime.js";
import { sendMessageGoChat, setDirectStorage } from "./send.js";
import {
  looksLikeGoChatTargetId,
  normalizeGoChatMessagingTarget,
} from "./normalize.js";
import { resolveGoChatOutboundSessionRoute } from "./session-route.js";
import { gochatSetupAdapter, setGoChatAccountConfig } from "./setup-core.js";
import { gochatSetupWizard } from "./setup-surface.js";
import type { CoreConfig, GroupPolicy } from "./types.js";
import { DEFAULT_RELAY_HTTP_URL, DEFAULT_RELAY_WS_URL } from "./types.js";
import { GoChatDirectStorage } from "./direct/storage.js";
import { createGoChatDirectServer } from "./direct/server.js";
import { handleGoChatInbound } from "./inbound.js";

const meta = {
  id: "gochat",
  label: "GoChat",
  selectionLabel: "GoChat (custom)",
  docsPath: "/channels/gochat",
  docsLabel: "gochat",
  blurb: "Custom chat backend. Local mode (built-in API) or relay mode (GoChat platform). Supports text, images, audio, and file attachments.",
  order: 90,
  quickstartAllowFrom: true,
};

const gochatConfigAdapter = createScopedChannelConfigAdapter<
  ResolvedGoChatAccount,
  ResolvedGoChatAccount,
  CoreConfig
>({
  sectionKey: "gochat",
  listAccountIds: listGoChatAccountIds,
  resolveAccount: adaptScopedAccountAccessor(resolveGoChatAccount),
  defaultAccountId: resolveDefaultGoChatAccountId,
  clearBaseFields: ["webhookSecret", "webhookSecretFile", "name"],
  resolveAllowFrom: (account) => account.config.allowFrom,
  formatAllowFrom: (allowFrom) =>
    formatAllowFromLowercase({
      allowFrom,
      stripPrefixRe: /^gochat:/i,
    }),
});

const resolveGoChatDmPolicy = createScopedDmSecurityResolver<ResolvedGoChatAccount>({
  channelKey: "gochat",
  resolvePolicy: (account) => account.config.dmPolicy,
  resolveAllowFrom: (account) => account.config.allowFrom,
  policyPathSuffix: "dmPolicy",
  normalizeEntry: (raw) =>
    raw
      .trim()
      .replace(/^gochat:/i, "")
      .trim()
      .toLowerCase(),
});

const collectGoChatSecurityWarnings =
  createAllowlistProviderRouteAllowlistWarningCollector<ResolvedGoChatAccount>({
    providerConfigPresent: (cfg) =>
      (cfg.channels as Record<string, unknown> | undefined)?.gochat !== undefined,
    resolveGroupPolicy: (account) => account.config.groupPolicy,
    resolveRouteAllowlistConfigured: (account) =>
      Boolean(account.config.conversations) &&
      Object.keys(account.config.conversations ?? {}).length > 0,
    restrictSenders: {
      surface: "GoChat conversations",
      openScope: "any member in allowed conversations",
      groupPolicyPath: "channels.gochat.groupPolicy",
      groupAllowFromPath: "channels.gochat.groupAllowFrom",
    },
    noRouteAllowlist: {
      surface: "GoChat conversations",
      routeAllowlistPath: "channels.gochat.conversations",
      routeScope: "conversation",
      groupPolicyPath: "channels.gochat.groupPolicy",
      groupAllowFromPath: "channels.gochat.groupAllowFrom",
    },
  });

function chunkTextForOutbound(text: string, limit: number): string[] {
  if (!text || text.length <= limit) {
    return [text];
  }
  const chunks: string[] = [];
  let remaining = text;
  while (remaining.length > 0) {
    if (remaining.length <= limit) {
      chunks.push(remaining);
      break;
    }
    let splitAt = remaining.lastIndexOf("\n", limit);
    if (splitAt <= 0) {
      splitAt = remaining.lastIndexOf(" ", limit);
    }
    if (splitAt <= 0) {
      splitAt = limit;
    }
    chunks.push(remaining.slice(0, splitAt));
    remaining = remaining.slice(splitAt).replace(/^\n+/, "");
  }
  return chunks;
}

export const gochatPlugin: ChannelPlugin<ResolvedGoChatAccount> = createChatChannelPlugin({
  base: {
    id: "gochat",
    meta,
    setupWizard: gochatSetupWizard,
    capabilities: {
      chatTypes: ["direct", "channel", "group"],
      reactions: false,
      threads: false,
      media: true,
      nativeCommands: false,
    },
    reload: { configPrefixes: ["channels.gochat"] },
    configSchema: buildChannelConfigSchema(GoChatConfigSchema),
    config: {
      ...gochatConfigAdapter,
      isConfigured: (account) => account.secretSource !== "none",
      describeAccount: (account) =>
        describeWebhookAccountSnapshot({
          account,
          configured: account.secretSource !== "none",
          extra: {
            secretSource: account.secretSource,
            mode: account.mode,
          },
        }),
    },
    groups: {
      resolveRequireMention: ({ cfg, accountId, groupId }) => {
        const account = resolveGoChatAccount({ cfg: cfg as CoreConfig, accountId });
        const conversations = account.config.conversations;
        if (!conversations || !groupId) {
          return true;
        }

        const convConfig = conversations[groupId];
        if (convConfig?.requireMention !== undefined) {
          return convConfig.requireMention;
        }

        const wildcardConfig = conversations["*"];
        if (wildcardConfig?.requireMention !== undefined) {
          return wildcardConfig.requireMention;
        }

        return true;
      },
      resolveToolPolicy: resolveGoChatGroupToolPolicy,
    },
    messaging: {
      normalizeTarget: normalizeGoChatMessagingTarget,
      resolveOutboundSessionRoute: (params) => resolveGoChatOutboundSessionRoute(params),
      targetResolver: {
        looksLikeId: looksLikeGoChatTargetId,
        hint: "<conversationId>",
      },
    },
    setup: gochatSetupAdapter,
    status: createComputedAccountStatusAdapter<ResolvedGoChatAccount>({
      defaultRuntime: createDefaultChannelRuntimeState(DEFAULT_ACCOUNT_ID),
      buildChannelSummary: ({ snapshot }) =>
        buildWebhookChannelStatusSummary(snapshot, {
          secretSource: snapshot.secretSource ?? "none",
        }),
      resolveAccountSnapshot: ({ account }) => ({
        accountId: account.accountId,
        name: account.name,
        enabled: account.enabled,
        configured: account.secretSource !== "none",
        extra: {
          secretSource: account.secretSource,
          mode: account.mode,
        },
      }),
    }),
    gateway: {
      startAccount: async (ctx) => {
        const account = ctx.account;

        console.log(`[gochat] ──── GoChat Account Configuration ────`);
        console.log(`[gochat]   accountId:     ${account.accountId}`);
        console.log(`[gochat]   mode:          ${account.mode}`);
        console.log(`[gochat]   enabled:       ${account.enabled}`);
        console.log(`[gochat]   secretSource:  ${account.secretSource}`);
        if (account.name) {
          console.log(`[gochat]   name:          ${account.name}`);
        }
        if (account.mode === "local") {
          console.log(`[gochat]   host:          ${account.directHost}`);
          console.log(`[gochat]   port:          ${account.directPort}`);
        }
        if (account.mode === "relay") {
          console.log(`[gochat]   relayUrl:      ${account.relayPlatformUrl}`);
          console.log(`[gochat]   channelId:     ${account.channelId || "(pending auto-register)"}`);
        }
        console.log(`[gochat]   dmPolicy:      ${account.config.dmPolicy ?? "open"}`);
        console.log(`[gochat]   groupPolicy:   ${account.config.groupPolicy ?? "allowlist"}`);
        if (account.config.allowFrom?.length) {
          console.log(`[gochat]   allowFrom:     ${account.config.allowFrom.join(", ")}`);
        }
        console.log(`[gochat] ─────────────────────────────────────`);

        const statusSink = createAccountStatusSink({
          accountId: ctx.accountId,
          setStatus: ctx.setStatus,
        });

        if (account.mode === "local") {
          ctx.log?.info(`[${account.accountId}] starting GoChat local server on :${account.directPort}`);

          const core = getGoChatRuntime();
          const stateDir = process.env.OPENCLAW_STATE_DIR || "";
          const storage = new GoChatDirectStorage(stateDir || "~/.openclaw");
          await storage.init();
          setDirectStorage(storage);

          const { start, stop, getBaseUrl } = createGoChatDirectServer({
            port: account.directPort,
            host: account.directHost,
            secret: account.secret,
            storage,
            onInbound: async (message) => {
              core.channel.activity.record({
                channel: "gochat",
                accountId: account.accountId,
                direction: "inbound",
                at: message.timestamp,
              });
              await handleGoChatInbound({
                message,
                account,
                config: ctx.cfg as CoreConfig,
                runtime: ctx.runtime,
                statusSink,
              });
            },
            onError: (error) => {
              ctx.log?.error(`[gochat:${account.accountId}] local server error: ${error.message}`);
            },
            abortSignal: ctx.abortSignal,
            allowPrivateNetwork: account.config.allowPrivateNetwork,
          });

          if (ctx.abortSignal?.aborted) {
            return;
          }
          await start();
          if (ctx.abortSignal?.aborted) {
            stop();
            return;
          }

          const baseUrl = getBaseUrl();
          ctx.log?.info(`[gochat:${account.accountId}] local server listening on ${baseUrl}`);
          return;
        }

        if (!account.channelId) {
          console.log(`[gochat] channelId missing — auto-registering with ${DEFAULT_RELAY_HTTP_URL}/api/plugin/register`);
          try {
            const registerUrl = DEFAULT_RELAY_HTTP_URL + "/api/plugin/register";
            const deviceName = account.name || "OpenClaw Plugin";
            const resp = await fetch(registerUrl, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name: deviceName }),
              signal: AbortSignal.timeout(10000),
            });
            if (!resp.ok) {
              const errText = await resp.text().catch(() => "");
              throw new Error(`Registration failed (${resp.status}): ${errText}`);
            }
            const data = (await resp.json()) as { channelId?: string; secret?: string };
            if (!data.channelId || !data.secret) {
              throw new Error("Registration response missing channelId or secret");
            }
            const core = getGoChatRuntime();
            const cfg = core.config.loadConfig() as CoreConfig;
            const updatedCfg = setGoChatAccountConfig(cfg, account.accountId, {
              channelId: data.channelId,
              webhookSecret: data.secret,
              relayPlatformUrl: DEFAULT_RELAY_WS_URL,
            });
            await core.config.writeConfigFile(updatedCfg);
            console.log(`[gochat] auto-registered OK — channelId=${data.channelId} saved to config`);
            account.channelId = data.channelId;
            account.secret = data.secret;
          } catch (err) {
            console.warn(`[gochat] auto-registration failed: ${err instanceof Error ? err.message : String(err)}`);
            console.warn(`[gochat] relay will fail without channelId. Run: openclaw gochat setup`);
          }
        }

        ctx.log?.info(`[${account.accountId}] starting GoChat relay connection to ${account.relayPlatformUrl}`);

        await runStoppablePassiveMonitor({
          abortSignal: ctx.abortSignal,
          start: async () =>
            await monitorGoChatProvider({
              accountId: account.accountId,
              config: ctx.cfg as CoreConfig,
              runtime: ctx.runtime,
              abortSignal: ctx.abortSignal,
              statusSink,
            }),
        });
      },
      logoutAccount: async ({ accountId, cfg }) => {
        const nextCfg = { ...cfg } as OpenClawConfig;
        const nextSection = cfg.channels?.gochat
          ? { ...cfg.channels.gochat }
          : undefined;
        let cleared = false;
        let changed = false;

        if (nextSection) {
          if (accountId === DEFAULT_ACCOUNT_ID && nextSection.webhookSecret) {
            delete nextSection.webhookSecret;
            cleared = true;
            changed = true;
          }
          const accountCleanup = clearAccountEntryFields({
            accounts: nextSection.accounts,
            accountId,
            fields: ["webhookSecret"],
          });
          if (accountCleanup.changed) {
            changed = true;
            if (accountCleanup.cleared) {
              cleared = true;
            }
            if (accountCleanup.nextAccounts) {
              nextSection.accounts = accountCleanup.nextAccounts;
            } else {
              delete nextSection.accounts;
            }
          }
        }

        if (changed) {
          if (nextSection && Object.keys(nextSection).length > 0) {
            nextCfg.channels = { ...nextCfg.channels, gochat: nextSection };
          } else {
            const nextChannels = { ...nextCfg.channels } as Record<string, unknown>;
            delete nextChannels.gochat;
            if (Object.keys(nextChannels).length > 0) {
              nextCfg.channels = nextChannels as OpenClawConfig["channels"];
            } else {
              delete nextCfg.channels;
            }
          }
        }

        const resolved = resolveGoChatAccount({
          cfg: changed ? (nextCfg as CoreConfig) : (cfg as CoreConfig),
          accountId,
        });
        const loggedOut = resolved.secretSource === "none";

        if (changed) {
          await getGoChatRuntime().config.writeConfigFile(nextCfg);
        }

        return {
          cleared,
          envSecret: Boolean(process.env.GOCHAT_WEBHOOK_SECRET?.trim()),
          loggedOut,
        };
      },
    },
  },
  pairing: {
    text: {
      idLabel: "gochatUserId",
      message: "OpenClaw: your access has been approved.",
      normalizeAllowEntry: createPairingPrefixStripper(
        /^gochat:/i,
        (entry) => entry.toLowerCase(),
      ),
      notify: createLoggedPairingApprovalNotifier(
        ({ id }) => `[gochat] User ${id} approved for pairing`,
      ),
    },
  },
  security: {
    resolveDmPolicy: resolveGoChatDmPolicy,
    collectWarnings: collectGoChatSecurityWarnings,
  },
  outbound: {
    base: {
      deliveryMode: "direct",
      chunker: chunkTextForOutbound,
      chunkerMode: "markdown",
      textChunkLimit: 4000,
    },
    attachedResults: {
      channel: "gochat",
      sendText: async ({ cfg, to, text, accountId, replyToId }) =>
        await sendMessageGoChat(to, text, {
          accountId: accountId ?? undefined,
          replyTo: replyToId ?? undefined,
          cfg: cfg as CoreConfig,
        }),
      sendMedia: async ({ cfg, to, text, mediaUrl, accountId, replyToId }) =>
        await sendMessageGoChat(to, text ?? "", {
          accountId: accountId ?? undefined,
          replyTo: replyToId ?? undefined,
          mediaUrl,
          cfg: cfg as CoreConfig,
        }),
    },
  },
});
