# GoChat Server — 外部 APP 接入说明

## 概述

GoChat Server 是一个基于 Go + Gin 框架的 Web 聊天桥接服务，充当 **外部 APP 与 OpenClaw AI 网关之间的消息中继**。

核心能力：

- 接收外部 APP 发来的用户消息，转发至 OpenClaw 网关
- 接收 OpenClaw 网关的 AI 回复，存储并可被外部 APP 拉取
- 支持会话（Conversation）管理、文件上传、附件消息
- 通过 HMAC-SHA256 签名机制保障通信安全

### 架构流程

```
┌─────────────┐      POST /api/send            ┌──────────────────┐
│  外部 APP    │  ──────────────────────────▶    │                  │
│  (客户端)    │                                 │  GoChat Server   │
│             │  ◀──────────────────────────    │  (本服务)         │
└─────────────┘  GET /api/conversations/...      │                  │
                  GET /api/conversations/.../messages
                                                 │      │           │
                                                 │      ▼           │
                                                 │  OpenClaw 网关    │
                                                 │  (AI 后端)        │
                                                 │      │           │
                                                 │  回调 (reply)     │
                                                 └──────────────────┘
```

**消息流向：**

1. 外部 APP 调用 `POST /api/send` 发送用户消息
2. GoChat Server 将消息转发至 OpenClaw Webhook
3. OpenClaw 处理完毕后，回调 `POST /api/openclaw/reply` 返回 AI 回复
4. 外部 APP 通过轮询 `GET /api/conversations/:id/messages` 获取回复

---

## 快速开始

### 环境要求

- Go 1.25+
- 运行中的 OpenClaw 网关（默认 `http://localhost:8790`）

### 配置

复制 `.env.example` 为 `.env`，修改以下配置：

```bash
cp .env.example .env
```

| 环境变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `GOCHAT_WEBHOOK_SECRET` | **是** | — | 发往 OpenClaw 的请求签名密钥 |
| `GOCHAT_CALLBACK_SECRET` | **是** | — | 接收 OpenClaw 回调时验证签名的密钥 |
| `GOCHAT_OPENCLAW_WEBHOOK_URL` | 否 | `http://localhost:8790/gochat-webhook` | OpenClaw 网关 Webhook 地址 |
| `GOCHAT_SERVER_PORT` | 否 | `9750` | 服务监听端口 |
| `GOCHAT_UPLOAD_DIR` | 否 | `./uploads` | 文件上传目录 |
| `GOCHAT_DB_PATH` | 否 | `./gochat.db` | 数据库路径（预留） |
| `GOCHAT_AUDIO_STT_URL` | 否 | 空 | FunASR 离线 WebSocket 地址，例如 `ws://127.0.0.1:10095` |
| `GOCHAT_AUDIO_STT_ONLINE_URL` | 否 | 空 | FunASR `online/2pass` WebSocket 地址，例如 `ws://127.0.0.1:10096` |
| `GOCHAT_AUDIO_FFMPEG_BIN` | 否 | `ffmpeg` | `ffmpeg` 可执行文件路径，用于将 Opus 帧转成 WAV |
| `GOCHAT_AUDIO_STT_TIMEOUT_SEC` | 否 | `45` | 单次语音转写超时秒数 |

### 启动

```bash
cd gochat-server
go run ./cmd/server
```

启动成功后输出：

```
[gochat] server starting on :9750
[gochat] webhook: http://localhost:8790/gochat-webhook
[gochat] uploads: ./uploads
```

### FunASR 语音转写部署

当前仓库同时支持两条 FunASR 链路：

- `offline`：浏览器把 `Opus` 音频送到 GoChat，服务端在 `audio.stop` 后统一转成 WAV，再调用离线 FunASR 返回整段结果。
- `online/2pass`：浏览器直接送 `pcm16le` 音频块给 GoChat，GoChat 再桥接到 FunASR `2pass` WebSocket，并把实时字幕通过 `stt.partial` 推回 `/app`。

#### 1. 启动离线服务

```bash
mkdir -p gochat-server/.funasr-models
docker run -d --name openclaw-funasr-offline \
  -p 10095:10095 \
  -v "$PWD/gochat-server/.funasr-models:/workspace/models" \
  registry.cn-hangzhou.aliyuncs.com/funasr_repo/funasr:funasr-runtime-sdk-cpu-0.4.7 \
  /bin/bash -lc 'cd /workspace/FunASR/runtime && touch /workspace/models/hotwords.txt && bash run_server.sh --download-model-dir /workspace/models --hotword /workspace/models/hotwords.txt --certfile 0 > /workspace/funasr.log 2>&1; exec tail -f /workspace/funasr.log'
```

#### 2. 启动在线 `2pass` 服务

```bash
mkdir -p gochat-server/.funasr-models-online
docker run -d --name openclaw-funasr-online \
  -p 10096:10095 \
  -v "$PWD/gochat-server/.funasr-models-online:/workspace/models" \
  registry.cn-hangzhou.aliyuncs.com/funasr_repo/funasr:funasr-runtime-sdk-online-cpu-0.1.12 \
  /bin/bash -lc 'cd /workspace/FunASR/runtime && bash run_server_2pass.sh --download-model-dir /workspace/models --certfile 0 > /workspace/funasr-2pass.log 2>&1; exec tail -f /workspace/funasr-2pass.log'
```

然后在 `gochat-server/.env` 里配置：

```bash
GOCHAT_AUDIO_STT_URL=ws://127.0.0.1:10095
GOCHAT_AUDIO_STT_ONLINE_URL=ws://127.0.0.1:10096
GOCHAT_AUDIO_FFMPEG_BIN=ffmpeg
GOCHAT_AUDIO_STT_TIMEOUT_SEC=45
```

说明：

- 首次启动 FunASR 会自动下载模型，耗时取决于网络。
- `/app` 录音按钮上方可以切换 `离线整段` 和 `实时字幕` 两种演示模式。
- `离线整段` 会保留旧的 `Opus -> offline` 方案，适合兼容设备端当前协议。
- `实时字幕` 会走 `pcm16le -> online/2pass`，录音时收到 `stt.partial`，松开后再收到最终 `stt`。

---

## API 接口文档

基础路径：`http://<host>:9750/api`

所有请求和响应均为 JSON 格式。

---

### 1. 发送消息

将用户消息发送至指定会话，GoChat Server 会自动转发给 OpenClaw AI 网关。

```
POST /api/send
Content-Type: application/json
```

**请求体：**

```json
{
  "conversationId": "conv-001",
  "conversationName": "我的会话",
  "senderId": "user-123",
  "senderName": "张三",
  "text": "你好，请帮我写一段代码",
  "attachments": [],
  "replyTo": "",
  "isGroupChat": false
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `conversationId` | string | **是** | 会话唯一标识，用于关联消息 |
| `conversationName` | string | 否 | 会话显示名称 |
| `senderId` | string | 否 | 发送者 ID，默认 `"web-user"` |
| `senderName` | string | 否 | 发送者昵称 |
| `text` | string | 否 | 消息文本内容 |
| `attachments` | Attachment[] | 否 | 附件列表（见附件结构） |
| `replyTo` | string | 否 | 引用回复的消息 ID |
| `isGroupChat` | bool | 否 | 是否群聊消息，默认 `false` |

**附件（Attachment）结构：**

```json
{
  "url": "http://localhost:9750/files/abc123.jpg",
  "type": "image",
  "name": "photo.jpg",
  "mimeType": "image/jpeg",
  "size": 102400
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `url` | string | 文件访问地址 |
| `type` | string | 附件类型：`image` / `audio` / `video` / `file` |
| `name` | string | 原始文件名 |
| `mimeType` | string | MIME 类型 |
| `size` | int64 | 文件大小（字节） |

**成功响应（200）：**

```json
{
  "messageId": "20260331-143052.001",
  "timestamp": 1743402652001,
  "ok": true
}
```

**失败响应（400）：**

```json
{
  "error": "invalid request: ..."
}
```

**失败响应（502）：**

```json
{
  "error": "send to openclaw failed: ..."
}
```

---

### 2. 获取会话列表

获取所有已创建的会话，按最后活跃时间倒序排列。

```
GET /api/conversations
```

**成功响应（200）：**

```json
[
  {
    "id": "conv-001",
    "name": "我的会话",
    "createdAt": "2026-03-31T14:30:00Z",
    "lastActive": "2026-03-31T14:35:00Z",
    "messageCount": 5
  },
  {
    "id": "conv-002",
    "name": "技术讨论",
    "createdAt": "2026-03-31T10:00:00Z",
    "lastActive": "2026-03-31T12:00:00Z",
    "messageCount": 12
  }
]
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 会话唯一标识 |
| `name` | string | 会话名称 |
| `createdAt` | string (RFC3339) | 创建时间 |
| `lastActive` | string (RFC3339) | 最后活跃时间 |
| `messageCount` | int | 消息数量 |

---

### 3. 获取会话消息

获取指定会话的最近消息（最多 100 条）。

```
GET /api/conversations/:conversationId/messages
```

**路径参数：**

| 参数 | 说明 |
|---|---|
| `conversationId` | 会话 ID |

**成功响应（200）：**

```json
[
  {
    "id": "a1b2c3d4-...",
    "conversationId": "conv-001",
    "direction": "inbound",
    "senderId": "user-123",
    "senderName": "张三",
    "text": "你好，请帮我写一段代码",
    "attachments": [],
    "replyTo": "",
    "timestamp": "2026-03-31T14:30:52Z"
  },
  {
    "id": "e5f6g7h8-...",
    "conversationId": "conv-001",
    "direction": "outbound",
    "senderId": "",
    "senderName": "",
    "text": "好的，请问您需要什么语言的代码？",
    "attachments": [],
    "replyTo": "",
    "timestamp": "2026-03-31T14:31:05Z"
  }
]
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 消息唯一 ID（UUID） |
| `conversationId` | string | 所属会话 ID |
| `direction` | string | 消息方向：`inbound`（用户发送）/ `outbound`（AI 回复） |
| `senderId` | string | 发送者 ID（AI 回复为空） |
| `senderName` | string | 发送者昵称（AI 回复为空） |
| `text` | string | 消息文本 |
| `attachments` | Attachment[] | 附件列表 |
| `replyTo` | string | 引用回复的消息 ID |
| `timestamp` | string (RFC3339) | 消息时间 |

**失败响应（400）：**

```json
{
  "error": "conversationId required"
}
```

---

### 4. 上传文件

上传文件并获取附件对象，附件可随消息发送。

```
POST /api/upload
Content-Type: multipart/form-data
```

**表单字段：**

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `file` | file | **是** | 要上传的文件 |

**成功响应（200）：**

```json
{
  "url": "http://localhost:9750/files/a1b2c3d4.jpg",
  "type": "image",
  "name": "photo.jpg",
  "mimeType": "image/jpeg",
  "size": 102400
}
```

附件类型自动识别规则：

| 类型 | MIME 前缀 / 扩展名 |
|---|---|
| `image` | `image/*`, `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp` |
| `audio` | `audio/*`, `.mp3`, `.wav`, `.ogg`, `.m4a` |
| `video` | `video/*`, `.mp4`, `.webm`, `.mov` |
| `file` | 其他所有类型 |

---

### 5. 访问上传文件

获取已上传的文件。

```
GET /files/:filename
```

直接返回文件内容，浏览器可直接渲染图片/音视频。

---

### 6. OpenClaw 回调接口（内部）

> 此接口由 OpenClaw 网关调用，外部 APP 通常不需要直接使用。

```
POST /api/openclaw/reply
```

OpenClaw 处理完消息后回调此接口返回 AI 回复。请求需携带 HMAC 签名头：

| Header | 说明 |
|---|---|
| `X-GoChat-Signature` | HMAC-SHA256 签名 |
| `X-GoChat-Timestamp` | Unix 时间戳（秒） |

**请求体（OutboundReply）：**

```json
{
  "text": "AI 回复内容",
  "conversationId": "conv-001",
  "replyTo": "20260331-143052.001",
  "mediaUrl": "",
  "timestamp": 1743402665000
}
```

---

## 安全机制

### HMAC-SHA256 签名

GoChat Server 与 OpenClaw 网关之间的通信使用 HMAC-SHA256 签名验证：

**签名生成流程：**

```
payload = timestamp + "." + body
signature = HMAC-SHA256(secret, payload)
```

**签名验证规则：**

1. 从请求头获取 `X-GoChat-Signature` 和 `X-GoChat-Timestamp`
2. 校验时间戳与当前时间的偏差不超过 **300 秒**
3. 使用相同算法计算期望签名，与请求签名比对

**Go 代码示例：**

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "strconv"
    "time"
)

func SignPayload(secret, timestamp, body string) string {
    mac := hmac.New(sha256.New, []byte(secret))
    payload := timestamp + "." + body
    mac.Write([]byte(payload))
    return hex.EncodeToString(mac.Sum(nil))
}

func main() {
    secret := "your-callback-secret"
    body := `{"text":"hello","conversationId":"conv-001"}`
    timestamp := strconv.FormatInt(time.Now().Unix(), 10)
    signature := SignPayload(secret, timestamp, body)
    fmt.Println("Signature:", signature)
    fmt.Println("Timestamp:", timestamp)
}
```

---

## 外部 APP 接入指南

### 接入步骤

1. **部署 GoChat Server** — 配置 `.env` 并启动服务
2. **创建会话** — 调用 `POST /api/send` 发送第一条消息，自动创建会话
3. **发送消息** — 调用 `POST /api/send` 传入 `conversationId` 和 `text`
4. **获取回复** — 轮询 `GET /api/conversations/:id/messages` 获取 AI 回复
5. **上传文件**（可选） — 先调用 `POST /api/upload` 上传文件，获取附件 URL，再将附件随消息发送

### 典型交互流程

```
APP                          GoChat Server              OpenClaw
 │                                │                        │
 │  POST /api/send                │                        │
 │  {text:"你好"}                 │                        │
 │ ────────────────────────────▶  │                        │
 │                                │  转发消息               │
 │  返回 {messageId, ok:true}     │ ─────────────────────▶ │
 │ ◀────────────────────────────  │                        │
 │                                │                        │ AI 处理
 │                                │                        │
 │                                │  POST /api/openclaw/   │
 │                                │  reply {AI回复}        │
 │                                │ ◀───────────────────── │
 │                                │                        │
 │  GET /api/conversations/       │                        │
 │  conv-001/messages             │                        │
 │ ────────────────────────────▶  │                        │
 │                                │                        │
 │  返回 [用户消息, AI回复]        │                        │
 │ ◀────────────────────────────  │                        │
```

### 推荐实践

1. **消息轮询间隔** — 建议 3 秒，避免过于频繁
2. **会话 ID 设计** — 使用有意义的不重复标识，如 `{appId}-{userId}-{sessionId}`
3. **发送者标识** — 始终提供 `senderId`，便于区分多用户场景
4. **文件上传** — 先上传获取 URL，再在消息中引用，不要跳过上传步骤直接传 URL
5. **错误处理** — 对 502 错误实现重试机制，表明 OpenClaw 网关暂时不可用

### cURL 示例

**发送消息：**

```bash
curl -X POST http://localhost:9750/api/send \
  -H "Content-Type: application/json" \
  -d '{
    "conversationId": "app-user-001-session-1",
    "conversationName": "用户会话",
    "senderId": "user-001",
    "senderName": "李四",
    "text": "帮我总结一下今天的新闻"
  }'
```

**获取会话列表：**

```bash
curl http://localhost:9750/api/conversations
```

**获取会话消息：**

```bash
curl http://localhost:9750/api/conversations/app-user-001-session-1/messages
```

**上传文件：**

```bash
curl -X POST http://localhost:9750/api/upload \
  -F "file=@/path/to/photo.jpg"
```

**发送带附件的消息：**

```bash
curl -X POST http://localhost:9750/api/send \
  -H "Content-Type: application/json" \
  -d '{
    "conversationId": "app-user-001-session-1",
    "senderId": "user-001",
    "text": "请看这张图片",
    "attachments": [
      {
        "url": "http://localhost:9750/files/a1b2c3d4.jpg",
        "type": "image",
        "name": "photo.jpg",
        "mimeType": "image/jpeg",
        "size": 102400
      }
    ]
  }'
```

---

## 项目结构

```
gochat-server/
├── cmd/server/main.go          # 入口，路由注册，服务启动
├── internal/
│   ├── config/config.go        # 环境变量配置加载
│   ├── crypto/crypto.go        # HMAC-SHA256 签名与验签
│   ├── handler/
│   │   ├── api.go              # REST API 处理器（发送、列表、上传）
│   │   └── callback.go         # OpenClaw 回调处理器
│   ├── client/client.go        # OpenClaw Webhook HTTP 客户端
│   ├── store/store.go          # 内存会话与消息存储
│   └── types/types.go          # 公共类型定义
├── web/static/index.html       # 内置 Web 聊天界面
├── uploads/                    # 上传文件目录
├── .env.example                # 环境变量模板
├── go.mod / go.sum             # Go 模块依赖
└── README.md                   # 本文档
```

---

## 注意事项

- **存储** — 当前使用内存存储，服务重启后会话和消息会丢失
- **CORS** — 已开启全量跨域（`AllowOrigins: *`），生产环境建议限制来源
- **文件安全** — `ServeFile` 已做路径遍历防护（`filepath.Base`），但未做鉴权
- **回调签名** — OpenClaw 回调必须携带有效签名，否则返回 401
- **时间戳校验** — 签名中的时间戳与服务器时间偏差超过 300 秒将被拒绝
