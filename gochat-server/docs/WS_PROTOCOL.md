# GoChat Server WebSocket API 文档

> 版本: v2026.4.5
> 服务器地址: `http://localhost:8080` (默认)

---

## 目录

1. [认证流程](#1-认证流程)
2. [Plugin WebSocket（OpenClaw 插件连接）](#2-plugin-websocketopenclaw-插件连接)
3. [Chat WebSocket（用户/AI 客户端连接）](#3-chat-websocket用户ai-客户端连接)
4. [REST API](#4-rest-api)
5. [消息类型参考](#5-消息类型参考)
6. [错误处理](#6-错误处理)
7. [集成示例](#7-集成示例)

---

## 1. 认证流程

### 1.1 Plugin 认证（OpenClaw 插件连接）

Plugin 通过 **HMAC-SHA256 签名** 认证，无需 JWT。

**连接 URL:**
```
ws://localhost:8080/ws/plugin?channelId={channelId}&ts={timestamp}&sig={signature}
```

**签名算法:**
```python
sig = HMAC-SHA256(secret, channelId + ":" + ts)
# secret = 渠道配置中的 secret
# ts = 当前 Unix 时间戳（秒），需与服务器时间误差在 30 秒内
```

**示例（Python）:**
```python
import hmac
import hashlib
import time
import requests

channel_id = "my-channel"
secret = "your-channel-secret"
ts = str(int(time.time()))
sig = hmac.new(secret.encode(), f"{channel_id}:{ts}".encode(), hashlib.sha256).hexdigest()

ws_url = f"ws://localhost:8080/ws/plugin?channelId={channel_id}&ts={ts}&sig={sig}"
# 使用 WebSocket 库连接
```

### 1.2 Chat 客户端认证（AI 客户端 / 用户 App）

客户端通过 **JWT Token** 认证。

**Step 1: 获取 Token**
```
POST /api/chat/login
Content-Type: application/json

{
  "channelId": "my-channel",
  "secret": "your-channel-secret"
}
```

**响应:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "channelId": "my-channel",
  "channelName": "My Channel",
  "expiresIn": 86400
}
```

**Step 2: 使用 Token 连接 WebSocket**
```
ws://localhost:8080/ws/chat?token={jwt_token}
```

---

## 2. Plugin WebSocket（OpenClaw 插件连接）

Plugin WebSocket 是 **服务器主动推送** 通道。服务器接收用户消息后，通过此连接将消息推送给 OpenClaw 插件。

### 2.1 连接参数

| 参数 | 必填 | 说明 |
|------|------|------|
| channelId | 是 | 渠道 ID |
| ts | 是 | Unix 时间戳（秒） |
| sig | 是 | HMAC-SHA256 签名 |

### 2.2 服务器 → Plugin：推送用户消息

当有用户消息时，服务器通过 WebSocket 发送 `InboundMessage`：

```json
{
  "type": "message",
  "messageId": "uuid-xxxx-xxxx",
  "conversationId": "default",
  "channelId": "my-channel",
  "senderId": "user-123",
  "senderName": "User",
  "text": "你好，帮我写一段代码",
  "attachments": [
    {
      "url": "https://external-server.com/file.png",
      "type": "image",
      "name": "screenshot.png",
      "mimeType": "image/png",
      "size": 12345
    }
  ],
  "replyTo": "",
  "timestamp": 1712234567890,
  "isGroupChat": false
}
```

### 2.3 Plugin → 服务器：发送回复

Plugin 处理完消息后，通过同一 WebSocket 连接发送回复：

```json
{
  "type": "reply",
  "text": "好的，这是代码...",
  "conversationId": "default",
  "replyTo": "uuid-xxxx-xxxx",
  "mediaUrl": "https://gochat-server.com/uploads/result.png",
  "timestamp": 1712234600000
}
```

**字段说明:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| type | string | 是 | 固定为 `"reply"` |
| text | string | 是 | 回复文本内容 |
| conversationId | string | 是 | 对话 ID，需与收到的消息一致 |
| replyTo | string | 否 | 被回复的消息 ID（messageId） |
| mediaUrl | string | 否 | 附件 URL（图片、音频等） |
| timestamp | int64 | 否 | Unix 毫秒时间戳，默认当前时间 |

### 2.4 Plugin → 服务器：定期状态上报

```json
{
  "type": "status",
  "version": "v2026.4.5-plugin.7",
  "agentCount": 3,
  "status": "working",
  "uptime": 3600
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| version | string | 是 | OpenClaw 版本号 |
| agentCount | int | 是 | 当前活跃的 Agent 数量 |
| status | string | 是 | `"idle"` 或 `"working"` |
| uptime | int64 | 是 | 运行时长（秒） |

### 2.5 心跳

Plugin 应定期发送 ping（建议每 30 秒）：

**发送:**
```json
{"type": "ping"}
```

**服务器响应:**
```json
{"type": "pong"}
```

---

## 3. Chat WebSocket（用户/AI 客户端连接）

Chat WebSocket 是 **客户端推送** 通道。AI 客户端通过此连接接收用户消息、发送 AI 回复。

### 3.1 连接参数

| 参数 | 必填 | 说明 |
|------|------|------|
| token | 是 | JWT Token（通过 `/api/chat/login` 获取） |

### 3.2 客户端 → 服务器：发送消息

```json
{
  "type": "message",
  "text": "帮我解释一下什么是 WebSocket",
  "conversationId": "default",
  "attachments": [
    {
      "url": "https://gochat-server.com/uploads/image.png",
      "type": "image",
      "name": "diagram.png",
      "mimeType": "image/png"
    }
  ],
  "timestamp": 1712234567890
}
```

**字段说明:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| type | string | 是 | 固定为 `"message"` |
| text | string | 是 | 消息文本 |
| conversationId | string | 否 | 对话 ID，默认 `"default"` |
| attachments | array | 否 | 附件列表 |
| timestamp | int64 | 否 | Unix 毫秒时间戳，默认当前时间 |

### 3.3 服务器 → 客户端：AI 回复

当 OpenClaw 插件处理完消息后，服务器将回复推送给客户端：

```json
{
  "type": "reply",
  "text": "WebSocket 是一种双向通信协议...",
  "conversationId": "default",
  "replyTo": "uuid-xxxx-xxxx",
  "mediaUrl": "https://gochat-server.com/uploads/result.png",
  "timestamp": 1712234600000
}
```

### 3.4 服务器 → 客户端：错误消息

```json
{
  "type": "error",
  "code": "auth",
  "text": "认证失败，请重新登录"
}
```

**错误码:**

| code | 说明 |
|------|------|
| `auth` / `unauthorized` | Token 无效或过期，需重新登录 |
| `channel_offline` | 插件不在线，消息无法送达 |

---

## 4. REST API

### 4.1 渠道管理

#### 创建渠道
```
POST /api/admin/channels
Authorization: Bearer {admin_token}
Content-Type: application/json

{
  "name": "My Channel",
  "secret": "optional-secret",
  "webhookUrl": "https://example.com/webhook"
}
```

#### 获取渠道列表
```
GET /api/admin/channels
Authorization: Bearer {admin_token}
```

**响应:**
```json
[
  {
    "id": "my-channel",
    "name": "My Channel",
    "webhookUrl": "",
    "enabled": true,
    "createdAt": "2026-04-01T00:00:00Z",
    "online": true,
    "workStatus": "idle",
    "agentCount": 3,
    "version": "v2026.4.5-plugin.7",
    "lastSeen": "2026-04-05T12:00:00Z"
  }
]
```

#### 获取渠道状态
```
GET /api/admin/channels/{channelId}/status
Authorization: Bearer {admin_token}
```

### 4.2 文件上传

#### 获取预签名上传 URL
```
POST /api/upload/presign
Authorization: Bearer {chat_token}
Content-Type: application/json

{
  "filename": "image.png",
  "contentType": "image/png"
}
```

**响应:**
```json
{
  "uploadUrl": "https://storage.example.com/upload?signature=...",
  "fileKey": "uploads/uuid-image.png",
  "method": "PUT",
  "headers": {}
}
```

#### 确认上传
```
POST /api/upload/confirm
Authorization: Bearer {chat_token}
Content-Type: application/json

{
  "fileKey": "uploads/uuid-image.png"
}
```

**响应:**
```json
{
  "url": "https://gochat-server.com/static/uploads/uuid-image.png",
  "name": "image.png",
  "type": "image/png",
  "size": 12345
}
```

---

## 5. 消息类型参考

### 5.1 InboundMessage（服务器 → Plugin）

```typescript
interface InboundMessage {
  type: "message";
  messageId: string;           // 消息唯一 ID
  conversationId: string;       // 对话 ID
  conversationName?: string;   // 对话名称
  channelId: string;            // 渠道 ID
  senderId: string;             // 发送者 ID
  senderName: string;          // 发送者名称
  text: string;                // 消息文本
  attachments?: Attachment[];   // 附件列表
  replyTo?: string;            // 被回复的消息 ID
  timestamp: number;           // Unix 毫秒时间戳
  isGroupChat: boolean;        // 是否群聊
}

interface Attachment {
  url: string;
  type: "image" | "audio" | "video" | "file";
  name?: string;
  mimeType?: string;
  size?: number;
}
```

### 5.2 OutboundReply（Plugin → 服务器）

```typescript
interface OutboundReply {
  type: "reply";
  text: string;                // 回复文本
  conversationId: string;      // 对话 ID（需与 InboundMessage 一致）
  replyTo?: string;            // 被回复的消息 ID
  mediaUrl?: string;           // 附件 URL
  timestamp?: number;          // Unix 毫秒时间戳
}
```

### 5.3 PluginStatus（Plugin → 服务器）

```typescript
interface PluginStatus {
  type: "status";
  version: string;            // OpenClaw 版本
  agentCount: number;          // Agent 数量
  status: "idle" | "working"; // 工作状态
  uptime: number;              // 运行时长（秒）
  currentModel?: string;       // 当前生效模型
  command?: string;            // 当前主命令
  commandArgs?: string;        // 当前命令参数
  metadata?: Record<string, string>; // 额外运行时元数据
}
```

### 5.4 ChatMessage（客户端 → 服务器）

```typescript
interface ChatMessage {
  type: "message";
  text: string;
  conversationId?: string;     // 默认 "default"
  attachments?: Attachment[];
  timestamp?: number;
}
```

### 5.5 ChatReply（服务器 → 客户端）

```typescript
interface ChatReply {
  type: "reply";
  text: string;
  conversationId: string;
  replyTo?: string;
  mediaUrl?: string;
  timestamp: number;
}
```

---

## 6. 错误处理

### 6.1 WebSocket 错误

连接断开时，WebSocket 会自动重连。客户端应处理以下情况：

1. **收到 `error` 类型消息（code: auth/unauthorized）**: 清除本地 Token，提示用户重新登录
2. **收到 `error` 类型消息（code: channel_offline）**: 提示用户 AI 不在线
3. **WebSocket 连接断开**: 等待 3 秒后自动重连

### 6.2 HTTP 错误响应

```json
{
  "error": "错误描述"
}
```

| HTTP 状态码 | 说明 |
|-------------|------|
| 400 | 请求参数错误 |
| 401 | 认证失败（Plugin 签名错误 / Chat Token 无效） |
| 403 | 渠道已禁用 |
| 404 | 渠道不存在 |
| 503 | Plugin 不在线 |

---

## 7. 集成示例

### 7.1 Python AI 客户端示例

```python
import json
import time
import hmac
import hashlib
import websocket
import threading
import requests

class GoChatClient:
    def __init__(self, server_url, channel_id, secret):
        self.server_url = server_url.rstrip("/")
        self.channel_id = channel_id
        self.secret = secret
        self.token = None
        self.ws = None
        
    def login(self):
        """获取 JWT Token"""
        resp = requests.post(
            f"{self.server_url}/api/chat/login",
            json={"channelId": self.channel_id, "secret": self.secret}
        )
        if resp.status_code != 200:
            raise Exception(f"Login failed: {resp.text}")
        data = resp.json()
        self.token = data["token"]
        print(f"Logged in as {data['channelName']}, expires in {data['expiresIn']}s")
        
    def connect(self):
        """连接 WebSocket"""
        if not self.token:
            raise Exception("Not logged in")
        ws_url = f"ws://{self.server_url.replace('http://', '')}/ws/chat?token={self.token}"
        self.ws = websocket.WebSocketApp(
            ws_url,
            on_message=self.on_message,
            on_error=self.on_error,
            on_close=self.on_close,
            on_open=self.on_open
        )
        # 启动心跳线程
        threading.Thread(target=self.heartbeat, daemon=True).start()
        self.ws.run_forever()
        
    def on_open(self, ws):
        print("WebSocket connected")
        
    def on_message(self, ws, message):
        data = json.loads(message)
        print(f"Received: {data}")
        
        if data.get("type") == "reply":
            # 收到 AI 回复，打印结果
            print(f"AI 回复: {data.get('text')}")
            if data.get("mediaUrl"):
                print(f"附件: {data.get('mediaUrl')}")
        elif data.get("type") == "error":
            print(f"错误: {data.get('text')}")
            if data.get("code") in ("auth", "unauthorized"):
                print("Token 失效，需重新登录")
                
    def on_error(self, ws, error):
        print(f"WebSocket error: {error}")
        
    def on_close(self, ws, close_status_code, close_msg):
        print(f"WebSocket closed: {close_status_code} - {close_msg}")
        # 自动重连
        time.sleep(3)
        self.connect()
        
    def send_message(self, text, conversation_id="default"):
        """发送消息"""
        payload = {
            "type": "message",
            "text": text,
            "conversationId": conversation_id,
            "timestamp": int(time.time() * 1000)
        }
        self.ws.send(json.dumps(payload))
        print(f"Sent: {text}")
        
    def heartbeat(self):
        """心跳保活（可选，每 30 秒）"""
        while True:
            time.sleep(30)
            if self.ws and self.ws.sock and self.ws.sock.connected:
                self.ws.send(json.dumps({"type": "ping"}))

# 使用
client = GoChatClient("http://localhost:8080", "my-channel", "secret")
client.login()
client.connect()
```

### 7.2 Go Plugin 服务端示例

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

type InboundMessage struct {
    Type           string   `json:"type"`
    MessageID      string   `json:"messageId"`
    ConversationID string   `json:"conversationId"`
    ChannelID      string   `json:"channelId"`
    SenderID       string   `json:"senderId"`
    SenderName     string   `json:"senderName"`
    Text           string   `json:"text"`
    Attachments    []Attachment `json:"attachments,omitempty"`
    Timestamp      int64    `json:"timestamp"`
}

type Attachment struct {
    URL      string `json:"url"`
    Type     string `json:"type"`
    Name     string `json:"name,omitempty"`
    MimeType string `json:"mimeType,omitempty"`
}

type OutboundReply struct {
    Type           string `json:"type"`
    Text           string `json:"text"`
    ConversationID string `json:"conversationId"`
    ReplyTo        string `json:"replyTo,omitempty"`
    MediaURL       string `json:"mediaUrl,omitempty"`
    Timestamp      int64  `json:"timestamp"`
}

func sign(secret, channelID, ts string) string {
    h := hmac.New(sha256.New, []byte(secret))
    h.Write([]byte(channelID + ":" + ts))
    return hex.EncodeToString(h.Sum(nil))
}

func main() {
    channelID := "my-channel"
    secret := "my-secret"
    serverURL := "ws://localhost:8080/ws/plugin"

    ts := fmt.Sprintf("%d", time.Now().Unix())
    sig := sign(secret, channelID, ts)

    u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/ws/plugin"}
    q := u.Query()
    q.Set("channelId", channelID)
    q.Set("ts", ts)
    q.Set("sig", sig)
    u.RawQuery = q.Encode()

    conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("dial:", err)
    }
    defer conn.Close()

    log.Println("Connected to GoChat server")

    // 发送状态
    sendStatus(conn)

    // 启动心跳
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        for range ticker.C {
            conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`))
        }
    }()

    // 读取消息
    for {
        _, message, err := conn.ReadMessage()
        if err != nil {
            log.Println("read:", err)
            return
        }

        var msg InboundMessage
        if err := json.Unmarshal(message, &msg); err != nil {
            continue
        }

        if msg.Type == "message" {
            log.Printf("Received: [%s] %s", msg.SenderName, msg.Text)
            // TODO: 调用你的 AI 处理
            reply := OutboundReply{
                Type:           "reply",
                Text:           "收到消息: " + msg.Text,
                ConversationID: msg.ConversationID,
                ReplyTo:        msg.MessageID,
                Timestamp:      time.Now().UnixMilli(),
            }
            conn.WriteJSON(reply)
        }
    }
}

func sendStatus(conn *websocket.Conn) {
    status := map[string]interface{}{
        "type":       "status",
        "version":    "v2026.4.5-plugin.1",
        "agentCount": 2,
        "status":     "idle",
        "uptime":     0,
    }
    conn.WriteJSON(status)
}
```

---

## 附录 A：WebSocket 端点汇总

| 端点 | 用途 | 认证方式 |
|------|------|----------|
| `ws://host/ws/plugin` | OpenClaw 插件连接 | HMAC 签名 |
| `ws://host/ws/chat` | AI 客户端连接 | JWT Token |
| `GET /ws/devices` | 设备列表（调试） | 无 |

## 附录 B：静态资源

- 客户端 JS: `/static/app.html`（Web Chat UI）
- 管理员后台: `/static/admin.html`
- 附件资源: `/static/uploads/{fileKey}`
