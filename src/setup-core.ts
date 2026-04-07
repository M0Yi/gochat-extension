import type { ChannelSetupAdapter, ChannelSetupInput } from "openclaw/plugin-sdk/channel-setup";
import type { OpenClawConfig } from "openclaw/plugin-sdk/config-runtime";
import { DEFAULT_ACCOUNT_ID, normalizeAccountId } from "openclaw/plugin-sdk/routing";
import {
  createSetupInputPresenceValidator,
  mergeAllowFromEntries,
  createTopLevelChannelDmPolicy,
  promptParsedAllowFromForAccount,
  resolveSetupAccountId,
  setSetupChannelEnabled,
  type ChannelSetupDmPolicy,
  type ChannelSetupWizard,
  type WizardPrompter,
} from "openclaw/plugin-sdk/setup-runtime";
import { formatDocsLink } from "openclaw/plugin-sdk/setup-tools";
import { applyAccountNameToChannelSection, patchScopedAccountConfig } from "../runtime-api.js";
import {
  listGoChatAccountIds,
  resolveDefaultGoChatAccountId,
  resolveGoChatAccount,
} from "./accounts.js";
import type { CoreConfig } from "./types.js";
import { DEFAULT_RELAY_HTTP_URL, DEFAULT_RELAY_WS_URL } from "./types.js";

const channel = "gochat" as const;

type GoChatSetupInput = ChannelSetupInput & {
  secret?: string;
  secretFile?: string;
  relayPlatformUrl?: string;
  channelId?: string;
};
type GoChatSection = NonNullable<CoreConfig["channels"]>["gochat"];

export function setGoChatAccountConfig(
  cfg: CoreConfig,
  accountId: string,
  updates: Record<string, unknown>,
): CoreConfig {
  return patchScopedAccountConfig({
    cfg,
    channelKey: channel,
    accountId,
    patch: updates,
  }) as CoreConfig;
}

export function clearGoChatAccountFields(
  cfg: CoreConfig,
  accountId: string,
  fields: string[],
): CoreConfig {
  const section = cfg.channels?.gochat;
  if (!section) {
    return cfg;
  }

  if (accountId === DEFAULT_ACCOUNT_ID) {
    const nextSection = { ...section } as Record<string, unknown>;
    for (const field of fields) {
      delete nextSection[field];
    }
    return {
      ...cfg,
      channels: {
        ...(cfg.channels ?? {}),
        gochat: nextSection as GoChatSection,
      },
    } as CoreConfig;
  }

  const currentAccount = section.accounts?.[accountId];
  if (!currentAccount) {
    return cfg;
  }

  const nextAccount = { ...currentAccount } as Record<string, unknown>;
  for (const field of fields) {
    delete nextAccount[field];
  }
  return {
    ...cfg,
    channels: {
      ...(cfg.channels ?? {}),
      gochat: {
        ...section,
        accounts: {
          ...section.accounts,
          [accountId]: nextAccount as NonNullable<typeof section.accounts>[string],
        },
      },
    },
  } as CoreConfig;
}

async function promptGoChatAllowFrom(params: {
  cfg: CoreConfig;
  prompter: WizardPrompter;
  accountId: string;
}): Promise<CoreConfig> {
  return await promptParsedAllowFromForAccount({
    cfg: params.cfg,
    accountId: params.accountId,
    defaultAccountId: params.accountId,
    prompter: params.prompter,
    noteTitle: "GoChat user id",
    noteLines: [
      "1) Check your Go backend for user IDs",
      "2) User IDs are defined by your Go backend",
      `Docs: ${formatDocsLink("/channels/gochat", "gochat")}`,
    ],
    message: "GoChat allowFrom (user id)",
    placeholder: "user-id",
    parseEntries: (raw) => ({
      entries: String(raw)
        .split(/[\n,;]+/g)
        .map((value) => value.trim().toLowerCase())
        .filter(Boolean),
    }),
    getExistingAllowFrom: ({ cfg, accountId }) =>
      resolveGoChatAccount({ cfg, accountId }).config.allowFrom ?? [],
    mergeEntries: ({ existing, parsed }) =>
      mergeAllowFromEntries(
        existing.map((value) => String(value).trim().toLowerCase()),
        parsed,
      ),
    applyAllowFrom: ({ cfg, accountId, allowFrom }) =>
      setGoChatAccountConfig(cfg, accountId, {
        dmPolicy: "allowlist",
        allowFrom,
      }),
  });
}

async function promptGoChatAllowFromForAccount(params: {
  cfg: OpenClawConfig;
  prompter: WizardPrompter;
  accountId?: string;
}): Promise<OpenClawConfig> {
  const accountId = resolveSetupAccountId({
    accountId: params.accountId,
    defaultAccountId: resolveDefaultGoChatAccountId(params.cfg as CoreConfig),
  });
  return await promptGoChatAllowFrom({
    cfg: params.cfg as CoreConfig,
    prompter: params.prompter,
    accountId,
  });
}

export const gochatDmPolicy: ChannelSetupDmPolicy = createTopLevelChannelDmPolicy({
  label: "GoChat",
  channel,
  policyKey: "channels.gochat.dmPolicy",
  allowFromKey: "channels.gochat.allowFrom",
  getCurrent: (cfg) => cfg.channels?.gochat?.dmPolicy ?? "open",
  promptAllowFrom: promptGoChatAllowFromForAccount,
});

async function autoRegisterRelay(
  cfg: CoreConfig,
  accountId: string,
): Promise<CoreConfig> {
  console.log(`[gochat:setup] auto-registering relay for accountId=${accountId}...`);
  try {
    const registerUrl = DEFAULT_RELAY_HTTP_URL + "/api/plugin/register";
    const deviceName = resolveGoChatAccount({ cfg, accountId }).name || `OpenClaw`;
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
      let nextCfg = setGoChatAccountConfig(cfg, accountId, {
        relayPlatformUrl: DEFAULT_RELAY_WS_URL,
        channelId: data.channelId,
        webhookSecret: data.secret,
        enabled: true,
        blockStreaming: true,
      });
      console.log(`[gochat:setup] relay registered OK: channelId=${data.channelId} relayUrl=${DEFAULT_RELAY_WS_URL}`);
      return nextCfg;
    }
  } catch (err) {
    console.warn(
      `[gochat:setup] relay auto-registration failed: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  return setGoChatAccountConfig(cfg, accountId, {
    relayPlatformUrl: DEFAULT_RELAY_WS_URL,
    enabled: true,
    blockStreaming: true,
  });
}

export const gochatSetupAdapter: ChannelSetupAdapter = {
  resolveAccountId: ({ accountId }) => normalizeAccountId(accountId),
  applyAccountName: ({ cfg, accountId, name }) =>
    applyAccountNameToChannelSection({
      cfg,
      channelKey: channel,
      accountId,
      name,
    }),
  validateInput: createSetupInputPresenceValidator({
    defaultAccountOnlyEnvError:
      "GOCHAT_WEBHOOK_SECRET can only be used for the default account.",
    validate: () => null,
  }),
  applyAccountConfig: async ({ cfg, accountId, input }) => {
    const setupInput = input as GoChatSetupInput;
    const namedConfig = applyAccountNameToChannelSection({
      cfg,
      channelKey: channel,
      accountId,
      name: setupInput.name,
    });
    let nextCfg = namedConfig as CoreConfig;
    const patch: Record<string, unknown> = {
      enabled: true,
      dmPolicy: "open",
      blockStreaming: true,
    };

    if (setupInput.relayPlatformUrl) {
      patch.relayPlatformUrl = setupInput.relayPlatformUrl.trim().replace(/\/+$/, "");
      patch.mode = "relay";
      nextCfg = setGoChatAccountConfig(nextCfg, accountId, patch);
      nextCfg = await autoRegisterRelay(nextCfg, accountId);
      return nextCfg;
    }

    if (!setupInput.useEnv) {
      if (setupInput.secretFile) {
        patch.webhookSecretFile = setupInput.secretFile;
      } else if (setupInput.secret) {
        patch.webhookSecret = setupInput.secret;
      }
    }

    return setGoChatAccountConfig(nextCfg, accountId, patch);
  },
};
