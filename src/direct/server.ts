import crypto from "node:crypto";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { pipeline } from "node:stream/promises";
import { WEBHOOK_RATE_LIMIT_DEFAULTS, createAuthRateLimiter } from "../../runtime-api.js";
import type { GoChatInboundMessage } from "../types.js";
import type { GoChatDirectStorage } from "./storage.js";
import {
  handleGetMessages,
  handleListConversations,
  handleSend,
  handleUpload,
} from "./routes.js";

const DEFAULT_DIRECT_PORT = 9750;
const DEFAULT_DIRECT_HOST = "0.0.0.0";
const DEFAULT_MAX_BODY_BYTES = 10 * 1024 * 1024;
const UPLOAD_MAX_BODY_BYTES = 50 * 1024 * 1024;
const HEALTH_PATH = "/healthz";
const RATE_LIMIT_SCOPE = "gochat-direct-auth";

function writeJson(res: ServerResponse, status: number, body?: unknown): void {
  const data = body !== undefined ? JSON.stringify(body) : undefined;
  res.writeHead(status, data ? { "Content-Type": "application/json" } : undefined);
  res.end(data);
}

function readBody(req: IncomingMessage, maxBytes: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let received = 0;
    req.on("data", (chunk: Buffer) => {
      received += chunk.length;
      if (received > maxBytes) {
        reject(new Error("PAYLOAD_TOO_LARGE"));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => {
      resolve(Buffer.concat(chunks).toString("utf-8"));
    });
    req.on("error", reject);
  });
}

function readBinaryBody(req: IncomingMessage, maxBytes: number): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let received = 0;
    req.on("data", (chunk: Buffer) => {
      received += chunk.length;
      if (received > maxBytes) {
        reject(new Error("PAYLOAD_TOO_LARGE"));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => {
      resolve(Buffer.concat(chunks));
    });
    req.on("error", reject);
  });
}

function verifySignature(
  signature: string,
  timestamp: string,
  body: string,
  secret: string,
): boolean {
  const ts = parseInt(timestamp, 10);
  if (!Number.isFinite(ts)) return false;
  const now = Math.floor(Date.now() / 1000);
  if (Math.abs(now - ts) > 300) return false;
  const payload = `${ts}.${body}`;
  const expected = crypto
    .createHmac("sha256", secret)
    .update(payload)
    .digest("hex");
  return crypto.timingSafeEqual(
    Buffer.from(signature, "hex"),
    Buffer.from(expected, "hex"),
  );
}

function parseUrlPath(url: string): string {
  const idx = url.indexOf("?");
  return idx >= 0 ? url.slice(0, idx) : url;
}

function parseQueryString(url: string): Record<string, string> {
  const idx = url.indexOf("?");
  if (idx < 0) return {};
  const qs = url.slice(idx + 1);
  const params: Record<string, string> = {};
  for (const part of qs.split("&")) {
    const eqIdx = part.indexOf("=");
    if (eqIdx >= 0) {
      params[decodeURIComponent(part.slice(0, eqIdx))] = decodeURIComponent(part.slice(eqIdx + 1));
    }
  }
  return params;
}

export type DirectServerOptions = {
  port?: number;
  host?: string;
  secret: string;
  storage: GoChatDirectStorage;
  onInbound: (message: GoChatInboundMessage) => Promise<void>;
  onError?: (error: Error) => void;
  abortSignal?: AbortSignal;
  allowPrivateNetwork?: boolean;
};

export function createGoChatDirectServer(opts: DirectServerOptions): {
  server: Server;
  start: () => Promise<void>;
  stop: () => void;
  getBaseUrl: () => string;
} {
  const port = opts.port ?? DEFAULT_DIRECT_PORT;
  const host = opts.host ?? DEFAULT_DIRECT_HOST;
  const rateLimiter = createAuthRateLimiter({
    maxAttempts: WEBHOOK_RATE_LIMIT_DEFAULTS.maxRequests,
    windowMs: WEBHOOK_RATE_LIMIT_DEFAULTS.windowMs,
    lockoutMs: WEBHOOK_RATE_LIMIT_DEFAULTS.windowMs,
    exemptLoopback: false,
    pruneIntervalMs: WEBHOOK_RATE_LIMIT_DEFAULTS.windowMs,
  });

  const server = createServer(async (req: IncomingMessage, res: ServerResponse) => {
    const reqPath = parseUrlPath(req.url ?? "/");
    const method = req.method ?? "GET";

    try {
      if (reqPath === HEALTH_PATH) {
        writeJson(res, 200, { status: "ok", mode: "local" });
        return;
      }

      if (reqPath === "/api/gochat/send" && method === "POST") {
        const clientIp = req.socket.remoteAddress ?? "unknown";
        if (!rateLimiter.check(clientIp, RATE_LIMIT_SCOPE).allowed) {
          writeJson(res, 429, { error: "Too Many Requests" });
          return;
        }

        const signature = req.headers["x-gochat-signature"];
        const timestamp = req.headers["x-gochat-timestamp"];
        if (typeof signature !== "string" || !signature.trim()) {
          rateLimiter.recordFailure(clientIp, RATE_LIMIT_SCOPE);
          writeJson(res, 400, { error: "Missing X-GoChat-Signature header" });
          return;
        }
        if (typeof timestamp !== "string" || !timestamp.trim()) {
          writeJson(res, 400, { error: "Missing X-GoChat-Timestamp header" });
          return;
        }

        const body = await readBody(req, DEFAULT_MAX_BODY_BYTES);
        if (!verifySignature(signature, timestamp, body, opts.secret)) {
          rateLimiter.recordFailure(clientIp, RATE_LIMIT_SCOPE);
          writeJson(res, 401, { error: "Invalid signature" });
          return;
        }
        rateLimiter.reset(clientIp, RATE_LIMIT_SCOPE);

        const result = await handleSend(opts.storage, body, opts.onInbound);
        if ("error" in result) {
          writeJson(res, 400, result);
        } else {
          writeJson(res, 200, result);
        }
        return;
      }

      if (reqPath === "/api/gochat/conversations" && method === "GET") {
        const conversations = await handleListConversations(opts.storage);
        writeJson(res, 200, conversations);
        return;
      }

      const messagesMatch = reqPath.match(
        /^\/api\/gochat\/conversations\/([^/]+)\/messages$/,
      );
      if (messagesMatch && method === "GET") {
        const conversationId = decodeURIComponent(messagesMatch[1]);
        const qs = parseQueryString(req.url ?? "");
        const limit = qs["limit"] ? parseInt(qs["limit"], 10) : undefined;
        const result = await handleGetMessages(opts.storage, conversationId, limit);
        if ("error" in result) {
          writeJson(res, 400, result);
        } else {
          writeJson(res, 200, result);
        }
        return;
      }

      if (reqPath === "/api/gochat/upload" && method === "POST") {
        const contentType = req.headers["content-type"] ?? "";
        if (!contentType.includes("multipart/form-data")) {
          writeJson(res, 400, { error: "Content-Type must be multipart/form-data" });
          return;
        }

        const boundary = contentType.split("boundary=")[1]?.trim();
        if (!boundary) {
          writeJson(res, 400, { error: "Missing multipart boundary" });
          return;
        }

        const buffer = await readBinaryBody(req, UPLOAD_MAX_BODY_BYTES);
        const fileResult = parseMultipartFile(buffer, boundary);
        if (!fileResult) {
          writeJson(res, 400, { error: "No file found in upload" });
          return;
        }

        const baseUrl = getBaseUrl();
        const result = await handleUpload(
          opts.storage,
          fileResult.data,
          fileResult.filename ?? "upload",
          fileResult.contentType ?? "application/octet-stream",
          baseUrl,
        );
        writeJson(res, 200, result);
        return;
      }

      if (reqPath.startsWith("/files/")) {
        const filename = reqPath.slice("/files/".length);
        const filePath = opts.storage.getUploadPath(filename);
        try {
          const stat = await import("node:fs").then((fs) =>
            fs.promises.stat(filePath),
          );
          res.writeHead(200, {
            "Content-Length": stat.size,
            "Cache-Control": "public, max-age=86400",
          });
          const { createReadStream } = await import("node:fs");
          const stream = createReadStream(filePath);
          await pipeline(stream, res);
        } catch {
          writeJson(res, 404, { error: "File not found" });
        }
        return;
      }

      res.writeHead(404);
      res.end();
    } catch (err) {
      if (err instanceof Error && err.message === "PAYLOAD_TOO_LARGE") {
        writeJson(res, 413, { error: "Payload too large" });
        return;
      }
      const error = err instanceof Error ? err : new Error(String(err));
      opts.onError?.(error);
      writeJson(res, 500, { error: "Internal server error" });
    }
  });

  let stopped = false;
  const start = (): Promise<void> => {
    return new Promise((resolve, reject) => {
      server.once("error", (err: Error) => {
        if (!stopped) {
          reject(err);
        }
      });
      server.listen(port, host, () => {
        server.removeListener("error", reject);
        resolve();
      });
    });
  };

  const stop = () => {
    if (stopped) return;
    stopped = true;
    try {
      server.close();
    } catch {
      // ignore close races during shutdown
    }
  };

  if (opts.abortSignal) {
    if (opts.abortSignal.aborted) {
      stop();
    } else {
      opts.abortSignal.addEventListener("abort", stop, { once: true });
    }
  }

  const getBaseUrl = (): string => {
    const displayHost = host === "0.0.0.0" ? "localhost" : host;
    return `http://${displayHost}:${port}`;
  };

  return { server, start, stop, getBaseUrl };
}

type ParsedFile = {
  data: Buffer;
  filename?: string;
  contentType?: string;
};

function parseMultipartFile(buffer: Buffer, boundary: string): ParsedFile | null {
  const boundaryBytes = Buffer.from(`--${boundary}`);
  const parts: Buffer[] = [];
  let start = 0;

  while (start < buffer.length) {
    const idx = buffer.indexOf(boundaryBytes, start);
    if (idx < 0) break;
    if (start > 0) {
      parts.push(buffer.slice(start, idx));
    }
    start = idx + boundaryBytes.length;
  }

  for (const part of parts) {
    const headerEnd = part.indexOf("\r\n\r\n");
    if (headerEnd < 0) continue;

    const headerStr = part.slice(0, headerEnd).toString("utf-8");
    const dataStart = headerEnd + 4;
    let dataEnd = part.length;
    if (part.slice(dataEnd - 2).equals(Buffer.from("\r\n"))) {
      dataEnd -= 2;
    }

    const nameMatch = headerStr.match(/name="([^"]+)"/);
    const filenameMatch = headerStr.match(/filename="([^"]+)"/);
    const ctMatch = headerStr.match(/Content-Type:\s*([^\r\n]+)/i);

    if (!filenameMatch || !nameMatch) continue;

    return {
      data: part.slice(dataStart, dataEnd),
      filename: filenameMatch[1],
      contentType: ctMatch?.[1]?.trim(),
    };
  }

  return null;
}
