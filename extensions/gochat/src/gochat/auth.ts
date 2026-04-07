import type { ResolvedGoChatAccount } from "../accounts.js";

export function isGoChatSenderAllowed(params: {
  allowFrom: Array<string | number> | undefined;
  senderId: string;
}): boolean {
  const allowFrom = (params.allowFrom ?? [])
    .map((v) => String(v).trim().toLowerCase().replace(/^gochat:/i, ""))
    .filter(Boolean);

  if (allowFrom.length === 0) {
    return false;
  }
  if (allowFrom.includes("*")) {
    return true;
  }
  const senderId = params.senderId.trim().toLowerCase().replace(/^gochat:/i, "");
  return allowFrom.includes(senderId);
}

export function resolveGoChatSenderId(senderId: string): string {
  return senderId.trim().toLowerCase().replace(/^gochat:/i, "");
}
