export function stripGoChatTargetPrefix(raw: string): string | undefined {
  const trimmed = raw.trim();
  if (!trimmed) {
    return undefined;
  }

  let normalized = trimmed;

  if (normalized.startsWith("gochat:")) {
    normalized = normalized.slice("gochat:".length).trim();
  }

  if (normalized.startsWith("conv:")) {
    normalized = normalized.slice("conv:".length).trim();
  }

  if (!normalized) {
    return undefined;
  }

  return normalized;
}

export function normalizeGoChatMessagingTarget(raw: string): string | undefined {
  const normalized = stripGoChatTargetPrefix(raw);
  return normalized ? `gochat:${normalized}`.toLowerCase() : undefined;
}

export function looksLikeGoChatTargetId(raw: string): boolean {
  const trimmed = raw.trim();
  if (!trimmed) {
    return false;
  }
  if (/^gochat:/i.test(trimmed)) {
    return true;
  }
  return /^[a-z0-9_-]{4,}$/i.test(trimmed);
}
