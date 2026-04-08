import type { ChannelSetupInput } from "openclaw/plugin-sdk/channel-setup";
import type { OpenClawConfig } from "openclaw/plugin-sdk/config-runtime";
import { DEFAULT_ACCOUNT_ID } from "openclaw/plugin-sdk/routing";
import {
  createStandardChannelSetupStatus,
  formatDocsLink,
  setSetupChannelEnabled,
  type ChannelSetupWizard,
} from "openclaw/plugin-sdk/setup";
import { listGoChatAccountIds, resolveGoChatAccount } from "./accounts.js";
import {
  clearGoChatAccountFields,
  gochatDmPolicy,
  gochatSetupAdapter,
  setGoChatAccountConfig,
} from "./setup-core.js";
import {
  consumeGoChatModeSwitchAuthorization,
  getGoChatModeSwitchAuthorizationStatus,
} from "./mode-switch-authorization.js";
import type { CoreConfig } from "./types.js";
import { DEFAULT_RELAY_HTTP_URL, DEFAULT_RELAY_WS_URL } from "./types.js";

const channel = "gochat" as const;

export const gochatSetupWizard: ChannelSetupWizard = {
  channel,
  stepOrder: "text-first",
  status: createStandardChannelSetupStatus({
    channelLabel: "GoChat",
    configuredLabel: "configured",
    unconfiguredLabel: "needs setup",
    configuredHint: "configured",
    unconfiguredHint: "custom chat backend",
    configuredScore: 1,
    unconfiguredScore: 5,
    resolveConfigured: ({ cfg }) =>
      listGoChatAccountIds(cfg as CoreConfig).some((accountId) => {
        const account = resolveGoChatAccount({ cfg: cfg as CoreConfig, accountId });
        return account.enabled;
      }),
  }),
  introNote: {
    title: "GoChat setup",
    lines: [
      "Choose mode: local (built-in server) or relay (connect to GoChat platform).",
      "Fresh setup is automatic. Switching an existing account between local and relay requires a one-time CLI authorization.",
      `Docs: ${formatDocsLink("/channels/gochat", "channels/gochat")}`,
    ],
    shouldShow: ({ cfg, accountId }) => {
      const account = resolveGoChatAccount({ cfg: cfg as CoreConfig, accountId });
      return !account.enabled;
    },
  },
  credentials: [],
  textInputs: [
    {
      inputKey: "mode",
      message: "Choose mode [local/relay] (default: relay)",
      currentValue: ({ cfg, accountId }) =>
        resolveGoChatAccount({ cfg: cfg as CoreConfig, accountId }).mode ?? "relay",
      shouldPrompt: () => true,
      validate: ({ value }) =>
        value === "local" || value === "relay"
          ? undefined
          : "Mode must be 'local' or 'relay'",
      normalizeValue: ({ value }) => (value?.trim().toLowerCase() || "relay") as string,
      applySet: async (params) => {
        const mode = (params.value?.trim().toLowerCase() || "relay") as "local" | "relay";
        const currentAccount = resolveGoChatAccount({
          cfg: params.cfg as CoreConfig,
          accountId: params.accountId,
        });
        const authStatus = getGoChatModeSwitchAuthorizationStatus({
          cfg: params.cfg as CoreConfig,
          accountId: params.accountId,
          currentMode: currentAccount.enabled ? currentAccount.mode : "",
          nextMode: mode,
        });
        if (!authStatus.allowed) {
          throw new Error(
            `${authStatus.reason} Run: openclaw gochat authorize-mode-switch --mode ${mode}`,
          );
        }

        let nextCfg = setGoChatAccountConfig(params.cfg as CoreConfig, params.accountId, {
          mode,
          dmPolicy: "open",
          enabled: true,
        });

        console.log(`[gochat:setup] mode=${mode} accountId=${params.accountId}`);

        if (mode === "relay") {
          try {
            const registerUrl = DEFAULT_RELAY_HTTP_URL + "/api/plugin/register";
            const deviceName = resolveGoChatAccount({
              cfg: params.cfg as CoreConfig,
              accountId: params.accountId,
            }).name || `OpenClaw`;
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
            if (data.channelId && data.secret) {
              nextCfg = setGoChatAccountConfig(nextCfg, params.accountId, {
                relayPlatformUrl: DEFAULT_RELAY_WS_URL,
                channelId: data.channelId,
                webhookSecret: data.secret,
              });
              console.log(`[gochat:setup] relay registered channelId=${data.channelId} relayUrl=${DEFAULT_RELAY_WS_URL}`);
            }
          } catch (err) {
            console.warn(
              `[gochat:setup] relay auto-registration failed (will retry on startup): ${err instanceof Error ? err.message : String(err)}`,
            );
            nextCfg = setGoChatAccountConfig(nextCfg, params.accountId, {
              relayPlatformUrl: DEFAULT_RELAY_WS_URL,
            });
          }
        } else {
          console.log(`[gochat:setup] local mode configured - built-in server will start on port 9750`);
        }

        nextCfg = consumeGoChatModeSwitchAuthorization({
          cfg: nextCfg as CoreConfig,
          accountId: params.accountId,
          currentMode: currentAccount.enabled ? currentAccount.mode : "",
          nextMode: mode,
        });

        const finalAccount = resolveGoChatAccount({ cfg: nextCfg as CoreConfig, accountId: params.accountId });
        console.log(`[gochat:setup] ──── Final Config ────`);
        console.log(`[gochat:setup]   mode:         ${finalAccount.mode}`);
        console.log(`[gochat:setup]   enabled:      ${finalAccount.enabled}`);
        console.log(`[gochat:setup]   secretSource: ${finalAccount.secretSource}`);
        if (finalAccount.mode === "relay") {
          console.log(`[gochat:setup]   relayUrl:     ${finalAccount.relayPlatformUrl}`);
          console.log(`[gochat:setup]   channelId:    ${finalAccount.channelId || "(pending)"}`);
        } else {
          console.log(`[gochat:setup]   port:         ${finalAccount.directPort}`);
        }
        console.log(`[gochat:setup]   dmPolicy:     ${finalAccount.config.dmPolicy ?? "open"}`);
        console.log(`[gochat:setup] ───────────────────`);

        return nextCfg;
      },
    },
  ],
  dmPolicy: gochatDmPolicy,
  disable: (cfg) => setSetupChannelEnabled(cfg, channel, false),
};

export { gochatSetupAdapter };
