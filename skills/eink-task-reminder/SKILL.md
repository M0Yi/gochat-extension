---
name: eink-task-reminder
description: "E-ink task reminder for GoChat. Manage persistent task lists per conversation: create, list, toggle done/undone, delete, clear completed, and show summaries."
---

# E-Ink Task Reminder Skill

This skill manages per-conversation task lists via the GoChat REST API. All endpoints are scoped under `/api/conversations/:conversationId/tasks`.

## API Endpoints

### List Tasks
```
GET /api/conversations/:conversationId/tasks
```
Response: `{ "tasks": [ { "id", "conversationId", "title", "description", "done", "createdAt", "doneAt" } ] }`

### Create Task
```
POST /api/conversations/:conversationId/tasks
Body: { "title": "task title", "description": "optional details" }
```
Response: the created task object with `id`, `done: false`, `createdAt`.

### Toggle Task (done ↔ undone)
```
POST /api/conversations/:conversationId/tasks/:taskId/toggle
```
Response: the updated task. `done` flips between true/false. `doneAt` is set when marking done, null when marking undone.

### Delete Task
```
DELETE /api/conversations/:conversationId/tasks/:taskId
```
Response: `{ "ok": true }`

### Clear All Completed Tasks
```
POST /api/conversations/:conversationId/tasks/clear-completed
```
Response: `{ "ok": true, "cleared": <count> }`

### Task Summary
```
GET /api/conversations/:conversationId/tasks/summary
```
Response: `{ "total": N, "pending": N, "completed": N }`

## Trigger Phrases (Chinese)

- "添加任务 / 新建任务 / 提醒我..." → create task
- "显示任务 / 我的任务 / 任务列表 / 有什么待办" → list tasks
- "完成任务 / 标记完成 / 打勾" → toggle task done
- "撤销完成 / 取消完成" → toggle task undone
- "删除任务" → delete task
- "清除已完成 / 清空完成的任务" → clear completed
- "任务概况 / 多少任务 / 任务统计" → task summary

## Trigger Phrases (English)

- "add task / remind me to / new task" → create task
- "show tasks / my tasks / task list / what's pending" → list tasks
- "mark done / complete task" → toggle task done
- "undo / uncheck" → toggle task undone
- "delete task / remove task" → delete task
- "clear completed / clean up done tasks" → clear completed
- "task summary / how many tasks" → task summary

## Workflow

### 1. Creating a task
- Extract the task title from the user's message.
- Optionally extract a description for more detail.
- Call `POST .../tasks` with `{ "title": "...", "description": "..." }`.
- Reply with confirmation: "✅ 已添加任务：{title}"
- Then show the current task list.

### 2. Listing tasks
- Call `GET .../tasks`.
- Format as a numbered list with status indicators:
  - `⬜` for pending, `✅` for completed.
- Example:
  ```
  📋 任务列表：
  1. ⬜ 买牛奶
  2. ✅ 买面包
  3. ⬜ 寄快递
  ```
- If no tasks exist: "📋 当前没有任务。说"添加任务"来创建一个吧！"

### 3. Toggling a task
- Identify which task the user wants to toggle (by title match or position number).
- Look up the task ID from the task list.
- Call `POST .../tasks/:taskId/toggle`.
- Confirm: "✅ 已完成：{title}" or "⬜ 已恢复待办：{title}"
- Show updated task list.

### 4. Deleting a task
- Identify the task by title or position.
- Confirm with the user before deleting: "确定要删除「{title}」吗？"
- Call `DELETE .../tasks/:taskId`.
- Confirm: "🗑️ 已删除：{title}"
- Show updated task list.

### 5. Clearing completed tasks
- Call `POST .../tasks/clear-completed`.
- Reply: "🧹 已清除 {cleared} 个已完成的任务"
- Show remaining tasks.

### 6. Task summary
- Call `GET .../tasks/summary`.
- Reply: "📊 任务统计：共 {total} 项，待办 {pending} 项，已完成 {completed} 项"
- If all completed: "🎉 所有任务都已完成！太棒了！"
- If no tasks: "📋 暂无任务"

## Important Notes

- Always use the current conversation's `conversationId` for all API calls.
- After any mutation (create/toggle/delete/clear), show the updated task list so the user sees current state.
- When matching task titles, use fuzzy matching. If ambiguous, list the matching tasks and ask the user to clarify.
- Default language is Chinese (zh). Use English only if the user writes in English.
