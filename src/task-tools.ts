import crypto from "node:crypto";
import { resolveGoChatAccount } from "./accounts.js";
import { getGoChatRuntime } from "./runtime.js";
import type { CoreConfig } from "./types.js";
import { DEFAULT_RELAY_HTTP_URL } from "./types.js";

type TaskResult = {
  content: Array<{ type: "text"; text: string }>;
  details: unknown;
};

type GoChatTask = {
  id: string;
  conversationId: string;
  title: string;
  done: boolean;
  createdAt: string;
  doneAt?: string;
};

function resolveGoBackendBaseUrl(cfg: CoreConfig): string {
  const account = resolveGoChatAccount({ cfg });
  if (account.mode === "relay") {
    return account.relayPlatformUrl.replace(/^wss?/, "https").replace(/\/ws\/plugin$/, "");
  }
  return `http://localhost:${account.directPort}`;
}

function signGoChatRequest(
  secret: string,
  body: string,
): { signature: string; timestamp: string } {
  const ts = Math.floor(Date.now() / 1000);
  const payload = `${ts}.${body}`;
  const signature = crypto.createHmac("sha256", secret).update(payload).digest("hex");
  return { signature, timestamp: String(ts) };
}

async function goBackendFetch(
  baseUrl: string,
  secret: string,
  path: string,
  init?: RequestInit,
): Promise<Response> {
  const body = init?.body as string | undefined;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string> | undefined),
  };

  if (body) {
    const { signature, timestamp } = signGoChatRequest(secret, body);
    headers["X-GoChat-Signature"] = signature;
    headers["X-GoChat-Timestamp"] = timestamp;
  }

  const url = `${baseUrl}${path}`;
  const response = await fetch(url, { ...init, headers, body });

  if (!response.ok) {
    const errorBody = await response.text().catch(() => "");
    throw new Error(`GoChat task API error (${response.status}): ${errorBody}`);
  }

  return response;
}

function textSuccess(text: string): TaskResult {
  return { content: [{ type: "text", text }], details: {} };
}

function textError(text: string): TaskResult {
  return { content: [{ type: "text", text }], details: { error: true } };
}

export function createGoChatTaskTool() {
  return {
    name: "gochat_tasks",
    label: "GoChat Tasks",
    description:
      "Manage task lists in GoChat conversations. " +
      "Use this to create, list, toggle (complete/uncomplete), and delete tasks. " +
      "Tasks are scoped to a specific conversation. " +
      "Examples: create a task 'Buy groceries' in conversation 'default', list all tasks, mark a task as done.",
    parameters: {
      type: "object" as const,
      properties: {
        action: {
          type: "string" as const,
          description:
            "The task action to perform: 'create', 'list', 'toggle', or 'delete'.",
        },
        conversationId: {
          type: "string" as const,
          description: "The conversation ID to scope the task operation to.",
        },
        title: {
          type: "string" as const,
          description: "Task title (required for 'create' action).",
        },
        taskId: {
          type: "string" as const,
          description: "Task ID (required for 'toggle' and 'delete' actions).",
        },
      },
      required: ["action", "conversationId"] as string[],
    },
    async execute(
      _id: string,
      params: {
        action: string;
        conversationId: string;
        title?: string;
        taskId?: string;
      },
    ): Promise<TaskResult> {
      try {
        const cfg = getGoChatRuntime().config.loadConfig() as CoreConfig;
        const account = resolveGoChatAccount({ cfg });
        const baseUrl = resolveGoBackendBaseUrl(cfg);
        const secret = account.secret?.trim() || "";

        switch (params.action) {
          case "create": {
            if (!params.title?.trim()) {
              return textError("'title' is required for the 'create' action.");
            }
            const body = JSON.stringify({ title: params.title });
            const res = await goBackendFetch(
              baseUrl,
              secret,
              `/api/conversations/${encodeURIComponent(params.conversationId)}/tasks`,
              { method: "POST", body },
            );
            const task = (await res.json()) as GoChatTask;
            return textSuccess(
              `Task created: "${task.title}" (id: ${task.id}) in conversation "${params.conversationId}"`,
            );
          }

          case "list": {
            const res = await goBackendFetch(
              baseUrl,
              secret,
              `/api/conversations/${encodeURIComponent(params.conversationId)}/tasks`,
            );
            const data = (await res.json()) as { tasks: GoChatTask[] };
            if (data.tasks.length === 0) {
              return textSuccess(`No tasks in conversation "${params.conversationId}".`);
            }
            const lines = data.tasks.map(
              (t) => `${t.done ? "✅" : "⬜"} ${t.title} (id: ${t.id})`,
            );
            return textSuccess(
              `Tasks in "${params.conversationId}":\n${lines.join("\n")}`,
            );
          }

          case "toggle": {
            if (!params.taskId) {
              return textError("'taskId' is required for the 'toggle' action.");
            }
            const res = await goBackendFetch(
              baseUrl,
              secret,
              `/api/conversations/${encodeURIComponent(params.conversationId)}/tasks/${encodeURIComponent(params.taskId)}/toggle`,
              { method: "POST" },
            );
            const task = (await res.json()) as GoChatTask;
            return textSuccess(
              `Task "${task.title}" ${task.done ? "completed ✅" : "reopened ⬜"} (id: ${task.id})`,
            );
          }

          case "delete": {
            if (!params.taskId) {
              return textError("'taskId' is required for the 'delete' action.");
            }
            await goBackendFetch(
              baseUrl,
              secret,
              `/api/conversations/${encodeURIComponent(params.conversationId)}/tasks/${encodeURIComponent(params.taskId)}`,
              { method: "DELETE" },
            );
            return textSuccess(`Task deleted (id: ${params.taskId})`);
          }

          default:
            return textError(
              `Unknown action '${params.action}'. Use: create, list, toggle, delete.`,
            );
        }
      } catch (error: unknown) {
        const msg = error instanceof Error ? error.message : String(error);
        return textError(`GoChat task error: ${msg}`);
      }
    },
  };
}
