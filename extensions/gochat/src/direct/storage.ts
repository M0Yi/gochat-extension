import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import type {
  GoChatAttachment,
  GoChatStoredConversation,
  GoChatStoredMessage,
} from "../types.js";

const DEFAULT_MESSAGES_PER_CONV = 1000;
const FILE_ENCODING = "utf-8";

type LockEntry = {
  promise: Promise<void>;
  resolve: () => void;
};

export class GoChatDirectStorage {
  private readonly basePath: string;
  private readonly conversationsPath: string;
  private readonly messagesDir: string;
  private readonly uploadsDir: string;
  private readonly lockMap = new Map<string, LockEntry>();
  private conversationsCache: Map<string, GoChatStoredConversation> | null = null;

  constructor(stateDir: string) {
    this.basePath = path.join(stateDir, "gochat");
    this.conversationsPath = path.join(this.basePath, "conversations.json");
    this.messagesDir = path.join(this.basePath, "messages");
    this.uploadsDir = path.join(this.basePath, "uploads");
  }

  async init(): Promise<void> {
    await fs.mkdir(this.messagesDir, { recursive: true });
    await fs.mkdir(this.uploadsDir, { recursive: true });
    try {
      await fs.access(this.conversationsPath);
    } catch {
      await fs.writeFile(this.conversationsPath, "{}", FILE_ENCODING);
    }
  }

  private async withLock<T>(key: string, fn: () => Promise<T>): Promise<T> {
    const existing = this.lockMap.get(key);
    if (existing) {
      await existing.promise;
    }

    let resolve: () => void;
    const promise = new Promise<void>((r) => {
      resolve = r;
    });
    this.lockMap.set(key, { promise, resolve: resolve! });

    try {
      return await fn();
    } finally {
      this.lockMap.delete(key);
      resolve!();
    }
  }

  private async readConversations(): Promise<Map<string, GoChatStoredConversation>> {
    if (this.conversationsCache) {
      return this.conversationsCache;
    }
    const raw = await fs.readFile(this.conversationsPath, FILE_ENCODING);
    const parsed = JSON.parse(raw) as Record<string, GoChatStoredConversation>;
    this.conversationsCache = new Map(Object.entries(parsed));
    return this.conversationsCache;
  }

  private async writeConversations(convs: Map<string, GoChatStoredConversation>): Promise<void> {
    this.conversationsCache = convs;
    const obj: Record<string, GoChatStoredConversation> = {};
    for (const [k, v] of convs) {
      obj[k] = v;
    }
    await fs.writeFile(this.conversationsPath, JSON.stringify(obj, null, 2), FILE_ENCODING);
  }

  async upsertConversation(id: string, name?: string): Promise<GoChatStoredConversation> {
    return this.withLock("conversations", async () => {
      const convs = await this.readConversations();
      const existing = convs.get(id);
      const now = new Date().toISOString();
      if (existing) {
        existing.lastActive = now;
        if (name) {
          existing.name = name;
        }
        await this.writeConversations(convs);
        return existing;
      }
      const conv: GoChatStoredConversation = {
        id,
        name: name ?? id,
        createdAt: now,
        lastActive: now,
        messageCount: 0,
      };
      convs.set(id, conv);
      await this.writeConversations(convs);
      return conv;
    });
  }

  async listConversations(): Promise<GoChatStoredConversation[]> {
    const convs = await this.readConversations();
    return [...convs.values()].sort(
      (a, b) => new Date(b.lastActive).getTime() - new Date(a.lastActive).getTime(),
    );
  }

  async getConversation(id: string): Promise<GoChatStoredConversation | undefined> {
    const convs = await this.readConversations();
    return convs.get(id);
  }

  private messageFilePath(conversationId: string): string {
    const safeId = conversationId.replace(/[^a-zA-Z0-9._-]/g, "_");
    return path.join(this.messagesDir, `${safeId}.json`);
  }

  async appendMessage(
    conversationId: string,
    msg: {
      direction: "inbound" | "outbound";
      senderId?: string;
      senderName?: string;
      text: string;
      attachments?: GoChatAttachment[];
      replyTo?: string;
    },
  ): Promise<GoChatStoredMessage> {
    return this.withLock(`msg:${conversationId}`, async () => {
      const filePath = this.messageFilePath(conversationId);
      let messages: GoChatStoredMessage[] = [];
      try {
        const raw = await fs.readFile(filePath, FILE_ENCODING);
        messages = JSON.parse(raw) as GoChatStoredMessage[];
      } catch {
        // file does not exist yet
      }

      const stored: GoChatStoredMessage = {
        id: crypto.randomUUID(),
        conversationId,
        direction: msg.direction,
        senderId: msg.senderId ?? "",
        senderName: msg.senderName ?? "",
        text: msg.text,
        attachments: msg.attachments ?? [],
        replyTo: msg.replyTo,
        timestamp: Date.now(),
      };

      messages.push(stored);
      if (messages.length > DEFAULT_MESSAGES_PER_CONV) {
        messages = messages.slice(-DEFAULT_MESSAGES_PER_CONV);
      }

      await fs.writeFile(filePath, JSON.stringify(messages, null, 2), FILE_ENCODING);

      await this.withLock("conversations", async () => {
        const convs = await this.readConversations();
        const conv = convs.get(conversationId);
        if (conv) {
          conv.messageCount = messages.length;
          conv.lastActive = new Date().toISOString();
          await this.writeConversations(convs);
        }
      });

      return stored;
    });
  }

  async getMessages(
    conversationId: string,
    limit?: number,
  ): Promise<GoChatStoredMessage[]> {
    const filePath = this.messageFilePath(conversationId);
    try {
      const raw = await fs.readFile(filePath, FILE_ENCODING);
      const messages = JSON.parse(raw) as GoChatStoredMessage[];
      if (limit && limit > 0) {
        return messages.slice(-limit);
      }
      return messages;
    } catch {
      return [];
    }
  }

  async saveUpload(filename: string, buffer: Buffer): Promise<string> {
    const safeName = path.basename(filename).replace(/[^a-zA-Z0-9._-]/g, "_");
    const uniqueName = `${Date.now()}-${safeName}`;
    const filePath = path.join(this.uploadsDir, uniqueName);
    await fs.writeFile(filePath, buffer);
    return uniqueName;
  }

  getUploadPath(filename: string): string {
    return path.join(this.uploadsDir, path.basename(filename));
  }

  invalidateCache(): void {
    this.conversationsCache = null;
  }
}
