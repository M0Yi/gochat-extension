import { DEFAULT_ACCOUNT_ID } from "openclaw/plugin-sdk/routing";
import { patchScopedAccountConfig } from "../runtime-api.js";
import type {
  CoreConfig,
  GoChatMode,
  GoChatModeSwitchAuthorizationConfig,
} from "./types.js";

export const DEFAULT_MODE_SWITCH_AUTH_TTL_MINUTES = 10;

function readAuthorization(
  cfg: CoreConfig,
  accountId: string,
): GoChatModeSwitchAuthorizationConfig | undefined {
  const section = cfg.channels?.gochat;
  if (!section) {
    return undefined;
  }

  if (accountId === DEFAULT_ACCOUNT_ID) {
    return section.modeSwitchAuthorization;
  }

  return section.accounts?.[accountId]?.modeSwitchAuthorization;
}

function clearAuthorizationFromConfig(
  cfg: CoreConfig,
  accountId: string,
): CoreConfig {
  const section = cfg.channels?.gochat;
  if (!section) {
    return cfg;
  }

  if (accountId === DEFAULT_ACCOUNT_ID) {
    if (!section.modeSwitchAuthorization) {
      return cfg;
    }
    const nextSection = { ...section };
    delete nextSection.modeSwitchAuthorization;
    return {
      ...cfg,
      channels: {
        ...(cfg.channels ?? {}),
        gochat: nextSection,
      },
    };
  }

  const currentAccount = section.accounts?.[accountId];
  if (!currentAccount?.modeSwitchAuthorization) {
    return cfg;
  }

  const nextAccount = { ...currentAccount };
  delete nextAccount.modeSwitchAuthorization;
  return {
    ...cfg,
    channels: {
      ...(cfg.channels ?? {}),
      gochat: {
        ...section,
        accounts: {
          ...(section.accounts ?? {}),
          [accountId]: nextAccount,
        },
      },
    },
  };
}

export function modeSwitchRequiresAuthorization(
  currentMode: GoChatMode | "" | undefined,
  nextMode: GoChatMode,
): boolean {
  return Boolean(currentMode) && currentMode !== nextMode;
}

export function grantGoChatModeSwitchAuthorization(params: {
  cfg: CoreConfig;
  accountId: string;
  targetMode: GoChatMode;
  ttlMinutes?: number;
  now?: Date;
}): CoreConfig {
  const ttlMinutes = Math.max(1, Math.floor(params.ttlMinutes ?? DEFAULT_MODE_SWITCH_AUTH_TTL_MINUTES));
  const issuedAt = params.now ?? new Date();
  const expiresAt = new Date(issuedAt.getTime() + ttlMinutes * 60_000);

  return patchScopedAccountConfig({
    cfg: params.cfg,
    channelKey: "gochat",
    accountId: params.accountId,
    patch: {
      modeSwitchAuthorization: {
        targetMode: params.targetMode,
        issuedAt: issuedAt.toISOString(),
        expiresAt: expiresAt.toISOString(),
      } satisfies GoChatModeSwitchAuthorizationConfig,
    },
  }) as CoreConfig;
}

export function getGoChatModeSwitchAuthorizationStatus(params: {
  cfg: CoreConfig;
  accountId: string;
  currentMode: GoChatMode | "" | undefined;
  nextMode: GoChatMode;
  now?: Date;
}): {
  allowed: boolean;
  requiresAuthorization: boolean;
  reason?: string;
  expiresAt?: string;
} {
  if (!modeSwitchRequiresAuthorization(params.currentMode, params.nextMode)) {
    return {
      allowed: true,
      requiresAuthorization: false,
    };
  }

  const auth = readAuthorization(params.cfg, params.accountId);
  if (!auth?.targetMode) {
    return {
      allowed: false,
      requiresAuthorization: true,
      reason: `Mode switch from ${params.currentMode} to ${params.nextMode} requires authorization.`,
    };
  }

  if (auth.targetMode !== params.nextMode) {
    return {
      allowed: false,
      requiresAuthorization: true,
      reason: `Mode switch authorization currently targets ${auth.targetMode}, not ${params.nextMode}.`,
      expiresAt: auth.expiresAt,
    };
  }

  if (auth.expiresAt) {
    const expiresAt = new Date(auth.expiresAt);
    if (!Number.isNaN(expiresAt.getTime()) && expiresAt.getTime() <= (params.now ?? new Date()).getTime()) {
      return {
        allowed: false,
        requiresAuthorization: true,
        reason: `Mode switch authorization for ${params.nextMode} expired at ${auth.expiresAt}.`,
        expiresAt: auth.expiresAt,
      };
    }
  }

  return {
    allowed: true,
    requiresAuthorization: true,
    expiresAt: auth.expiresAt,
  };
}

export function consumeGoChatModeSwitchAuthorization(params: {
  cfg: CoreConfig;
  accountId: string;
  currentMode: GoChatMode | "" | undefined;
  nextMode: GoChatMode;
}): CoreConfig {
  if (!modeSwitchRequiresAuthorization(params.currentMode, params.nextMode)) {
    return params.cfg;
  }
  return clearAuthorizationFromConfig(params.cfg, params.accountId);
}
