# ESP32 Chat + Voice Client Protocol

> 文档目标: 提供一份可以直接交给 `ESP32 / ESP-IDF / Arduino ESP32` 开发的接入文档，描述当前 `gochat-server` 在 `2026-04-07` 的真实聊天与语音协议。
>
> 适用对象: `ESP32` 作为聊天客户端接入 `gochat-server`
>
> 不适用对象:
>
> - `OpenClaw` 插件端 `GET /ws/plugin`
> - 安装脚本内部调用的 `POST /api/plugin/pair/claim`

---

## 1. 一句话结论

当前 ESP32 的推荐接入方式和 `/app` 一样，走新版被动配对流程:

1. ESP32 调 `POST /api/chat/pair/start`
2. 服务端返回 `6位连接码 + sessionToken`
3. ESP32 显示连接码
4. 用户在 OpenClaw 所在设备上执行安装命令
5. ESP32 轮询 `GET /api/chat/pair/session?sessionToken=...`
6. 当返回 `status=connected` 且带 `token` 时
7. ESP32 连接 `GET /ws/chat?token=...`
8. WebSocket 打开后立即发 `hello`
9. 之后按需收发:
   - 文本: `message / reply / status / ping / pong / error`
   - 语音离线: `audio.start / binary audio / audio.stop / stt / reply`
   - 语音实时: `audio.start / binary audio / stt.partial / audio.stop / stt / reply`

一句话对比:

- 旧方案: `channelId + secret -> /api/chat/login -> /ws/chat`
- 新方案: `6位连接码 -> /api/chat/pair/start + /api/chat/pair/session -> token -> /ws/chat`

---

## 2. 当前能力矩阵

| 能力 | ESP32 发送格式 | STT 模式 | 服务端行为 | 设备侧收到 |
|---|---|---|---|---|
| 纯文本聊天 | JSON 文本帧 | 无 | 直接转发到 OpenClaw | `reply / status / pong / error` |
| 语音整段识别 | `Opus` 或 `pcm16le` | `offline` | `gochat-server` 收音频后统一转写 | `stt -> reply` |
| 语音实时字幕 | `pcm16le` | `2pass` | `gochat-server` 直接桥接 FunASR `online/2pass` | `stt.partial -> stt -> reply` |

关键约束:

- `sttMode=offline`:
  - 推荐 `format=opus`
  - 兼容 `format=pcm16le`
- `sttMode=2pass`:
  - 只能使用 `format=pcm16le`
  - 不能用 `opus`
- 当前语音识别全部由服务器完成:
  - ESP32 不直接调用 FunASR
  - ESP32 只负责采集、编码、分帧、发 WebSocket

推荐路线:

- 当前 ClawTile 这类已接 Opus 录音链路的设备，优先实现 `offline + opus`
- 如果要边说边出字，再额外实现 `2pass + pcm16le`

---

## 3. 推荐默认配置

```json
{
  "role": "esp32_chat_client",
  "mode": "passive_pairing_same_as_/app",
  "serverBaseUrl": "https://fund.moyi.vip",
  "pairStartPath": "/api/chat/pair/start",
  "pairSessionPath": "/api/chat/pair/session",
  "loginPath": "/api/chat/login",
  "sessionPath": "/api/chat/session",
  "wsPath": "/ws/chat",
  "conversationId": "default",
  "pairPollIntervalMs": 3000,
  "statusPollIntervalMs": 10000,
  "reconnectDelayMs": 3000,
  "heartbeatIntervalMs": 30000,
  "audioDefaults": {
    "sampleRate": 16000,
    "channels": 1,
    "offlineFormat": "opus",
    "offlineFrameDurationMs": 20,
    "realtimeFormat": "pcm16le",
    "realtimeFrameDurationMs": 60
  }
}
```

对应 ESP32 代码可以固化成:

```c
#define GOCHAT_BASE_URL                   "https://fund.moyi.vip"
#define GOCHAT_HTTPS_PORT                 443
#define GOCHAT_WSS_PORT                   443
#define GOCHAT_PAIR_START_PATH            "/api/chat/pair/start"
#define GOCHAT_PAIR_SESSION_PATH          "/api/chat/pair/session"
#define GOCHAT_LOGIN_PATH                 "/api/chat/login"
#define GOCHAT_SESSION_PATH               "/api/chat/session"
#define GOCHAT_WS_PATH                    "/ws/chat"
#define GOCHAT_CONV_ID                    "default"
#define GOCHAT_PAIR_POLL_MS               3000
#define GOCHAT_STATUS_POLL_MS             10000
#define GOCHAT_RECONNECT_DELAY_MS         3000
#define GOCHAT_HEARTBEAT_MS               30000
#define GOCHAT_AUDIO_SAMPLE_RATE          16000
#define GOCHAT_AUDIO_CHANNELS             1
#define GOCHAT_AUDIO_OFFLINE_FRAME_MS     20
#define GOCHAT_AUDIO_REALTIME_FRAME_MS    60
```

说明:

- 当前默认服务器地址是 `https://fund.moyi.vip`
- 当前默认 HTTPS/WSS 端口是 `443`
- `conversationId` 推荐固定为 `"default"`
- ESP32 不需要预置 `channelId` 和 `secret`

---

## 4. 协议角色边界

ESP32 在这里扮演的是:

- `/app` 页面的同类聊天客户端
- 用户消息发送方
- AI 回复接收方
- 一个会显示配对码、文本消息、语音字幕的轻终端

ESP32 不应该直接使用:

- `/ws/plugin`
- 插件 HMAC 鉴权
- `/api/plugin/pair/claim`

一句话区分:

- `ESP32 聊天客户端` 用:
  - `POST /api/chat/pair/start`
  - `GET /api/chat/pair/session`
  - `GET /api/chat/session`
  - `GET /ws/chat?token=...`
- `OpenClaw 插件端` 才用:
  - 安装脚本
  - `/api/plugin/pair/claim`
  - `/ws/plugin`

---

## 5. 标准连接流程

### 5.1 步骤 1: 创建配对会话

请求:

```http
POST /api/chat/pair/start
Content-Type: application/json
```

```json
{
  "name": "ESP32 Desk Assistant"
}
```

成功响应示例:

```json
{
  "code": "482913",
  "sessionToken": "9d0d4d17b6c3d55e1b8b1d7af0a6d7d45f7c4e1d5f8c2a7b",
  "channelId": "f18b2fd5-7a1d-4a25-aad8-df46a71c82b0",
  "channelName": "ESP32 Desk Assistant",
  "expiresIn": 900,
  "expiresAt": 1775449500,
  "installCommand": "curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | bash -s -- 482913"
}
```

ESP32 建议行为:

1. 保存 `sessionToken`
2. 在屏幕显示 `code`
3. 如果显示空间够，也可以显示 `installCommand`
4. 立刻进入 `pair/session` 轮询阶段

### 5.2 步骤 2: 轮询配对状态

请求:

```http
GET /api/chat/pair/session?sessionToken=<SESSION_TOKEN>
```

状态枚举:

- `pending`: 连接码还没被认领
- `claimed`: 安装脚本已认领，但插件还没真正上线
- `connected`: 插件已连上，响应里会带 `token`

`connected` 响应示例:

```json
{
  "code": "482913",
  "status": "connected",
  "channelId": "f18b2fd5-7a1d-4a25-aad8-df46a71c82b0",
  "channelName": "ESP32 Desk Assistant",
  "online": true,
  "expiresAt": 1775449500,
  "claimedBy": "OpenClaw GoChat Plugin",
  "claimedAt": 1775449050,
  "token": "<JWT_TOKEN>",
  "expiresIn": 86400,
  "loginExpiresAt": 1775535450
}
```

建议处理:

- 一旦拿到 `token`，停止配对轮询
- 如果返回 `404` 或 `pair session not found`，重新发起 `pair/start`

### 5.3 步骤 3: 可选兜底登录

虽然现在推荐走配对流程，但服务端仍保留了旧登录方式:

```http
POST /api/chat/login
Content-Type: application/json
```

```json
{
  "channelId": "<CHANNEL_ID>",
  "secret": "<SECRET>"
}
```

使用建议:

- 只作为调试或高级配置兜底
- 正常产品流程不要再把 `secret` 暴露给终端用户

### 5.4 步骤 4: 可选查询聊天 session

拿到 `token` 后，可调用:

```http
GET /api/chat/session
Authorization: Bearer <JWT_TOKEN>
```

典型响应:

```json
{
  "valid": true,
  "channelId": "f18b2fd5-7a1d-4a25-aad8-df46a71c82b0",
  "channelName": "ESP32 Desk Assistant",
  "userId": "ESP32 Desk Assistant",
  "enabled": true,
  "online": true,
  "version": "1.0.0",
  "agentCount": 1,
  "workStatus": "idle",
  "officeStatus": "idle",
  "connectedAt": "2026-04-07T02:09:21Z",
  "lastSeen": "2026-04-07T02:09:30Z",
  "expiresAt": 1775535450
}
```

ESP32 建议:

- 刚拿到 `token` 后调用一次
- 正常运行时每 `10s` 轮询一次
- 如果资源紧张，也可以只在 WebSocket 断开后调用

### 5.5 步骤 5: 建立 WebSocket

如果服务器地址是:

```text
https://fund.moyi.vip
```

则 WebSocket 地址是:

```text
wss://fund.moyi.vip/ws/chat?token=<JWT_TOKEN>
```

规则:

- 当前 `/app` 使用的是 query 参数传 token
- ESP32 为了完全对齐 `/app`，建议也用 query 参数
- 如果是 HTTPS 域名，则 WebSocket 用 `wss://`

### 5.6 步骤 6: WebSocket 打开后立即发送 `hello`

建议发送:

```json
{
  "type": "hello",
  "clientType": "esp32",
  "clientVersion": "1.1.0",
  "clientMetadata": {
    "platform": "esp32",
    "transport": "wifi",
    "firmware": "1.1.0",
    "audioModes": "offline-opus,2pass-pcm16le"
  }
}
```

规则:

- `type` 必须是 `"hello"`
- 服务端不会单独回复这条消息
- 这条消息会把当前连接登记为一个聊天客户端

---

## 6. WebSocket 消息规范

### 6.1 ESP32 -> 服务端: hello

```json
{
  "type": "hello",
  "clientType": "esp32",
  "clientVersion": "1.1.0",
  "clientMetadata": {
    "platform": "esp32",
    "transport": "wifi"
  }
}
```

### 6.2 ESP32 -> 服务端: 发送文本消息

最小格式:

```json
{
  "type": "message",
  "text": "你好",
  "conversationId": "default"
}
```

规则:

- `text` 和 `attachments` 至少要有一个
- 如果 ESP32 不做多会话，直接固定 `conversationId = "default"`

### 6.3 服务端 -> ESP32: AI 回复

```json
{
  "type": "reply",
  "text": "好的，我已经处理完成",
  "conversationId": "default",
  "replyTo": "",
  "mediaUrl": "",
  "timestamp": 1775401005000
}
```

ESP32 建议行为:

1. 显示 `text`
2. 如果 `mediaUrl` 非空，则作为可选媒体资源处理
3. 如果只做纯文本客户端，可以忽略 `mediaUrl`

### 6.4 服务端 -> ESP32: 状态更新

```json
{
  "type": "status",
  "channelId": "f18b2fd5-7a1d-4a25-aad8-df46a71c82b0",
  "online": true,
  "version": "1.0.0",
  "agentCount": 1,
  "workStatus": "executing",
  "officeStatus": "working",
  "uptime": 123,
  "timestamp": 1775401010000
}
```

当前状态归一化值:

| 值 | 含义 |
|---|---|
| idle | 空闲 |
| writing | 写作中 |
| researching | 调研中 |
| executing | 执行中 |
| syncing | 同步中 |
| error | 异常 |

### 6.5 双向心跳: ping / pong

ESP32 建议每 `30s` 主动发:

```json
{"type":"ping"}
```

服务端会返回:

```json
{"type":"pong"}
```

### 6.6 服务端 -> ESP32: 错误消息

常见格式:

```json
{
  "type": "error",
  "text": "channel offline"
}
```

处理建议:

1. 如果 `text=channel offline`，提示“插件离线”
2. 如果 WebSocket 握手 `401`，重新走登录或重新配对
3. 如果 `GET /api/chat/session` 返回 `401/403`，重新获取 token

---

## 7. 语音协议规范

### 7.1 总体说明

当前语音链路已经完成，识别发生在服务器端:

- ESP32 只负责发送音频流到 `/ws/chat`
- `gochat-server` 负责:
  - 接收二进制音频帧
  - 按 `sttMode` 选择离线或实时识别
  - 把最终文本当作普通 `message` 继续发给 OpenClaw
- 因此设备端只需要实现协议，不需要直接对接 FunASR

两条语音路线:

1. `offline`
   - 设备端适合 `Opus`
   - 录音结束后返回整段 `stt`
2. `2pass`
   - 设备端必须发 `pcm16le`
   - 录音中会收到 `stt.partial`
   - 录音结束后收到最终 `stt`

### 7.2 ESP32 -> 服务端: `audio.start`

通用格式:

```json
{
  "type": "audio.start",
  "format": "opus",
  "sttMode": "offline",
  "sampleRate": 16000,
  "channels": 1,
  "frameDuration": 20
}
```

字段说明:

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| type | string | 是 | 固定 `"audio.start"` |
| format | string | 是 | `"opus"` 或 `"pcm16le"` |
| sttMode | string | 否 | `"offline"` 或 `"2pass"`，默认 `"offline"` |
| sampleRate | int | 是 | 推荐固定 `16000` |
| channels | int | 是 | 推荐固定 `1` |
| frameDuration | int | 是 | 当前建议 `20` 或 `60` |

合法组合:

| format | sttMode | 是否支持 | 推荐场景 |
|---|---|---|---|
| opus | offline | 是 | 当前 ESP32 低带宽默认方案 |
| pcm16le | offline | 是 | 调试或极简链路 |
| pcm16le | 2pass | 是 | 实时字幕 |
| opus | 2pass | 否 | 不支持 |

推荐值:

- `offline + opus`:
  - `frameDuration = 20`
- `2pass + pcm16le`:
  - `frameDuration = 60`

### `offline + opus` 示例

```json
{
  "type": "audio.start",
  "format": "opus",
  "sttMode": "offline",
  "sampleRate": 16000,
  "channels": 1,
  "frameDuration": 20
}
```

### `2pass + pcm16le` 示例

```json
{
  "type": "audio.start",
  "format": "pcm16le",
  "sttMode": "2pass",
  "sampleRate": 16000,
  "channels": 1,
  "frameDuration": 60
}
```

### 7.3 ESP32 -> 服务端: 二进制音频帧

在 `audio.start` 和 `audio.stop` 之间，所有 WebSocket 二进制帧都按 `BinaryProtocol2` 发送。

### 头部格式

```
偏移    大小    字段            类型          说明
0       2       version         uint16 BE     固定 2
2       2       type            uint16 BE     固定 0，表示音频
4       4       reserved        uint32 BE     固定 0
8       4       timestamp       uint32 BE     采样起始时间戳（毫秒）
12      4       payload_size    uint32 BE     负载字节数
16      N       payload         bytes         音频负载
```

规则:

- 所有多字节字段均为 Big-Endian
- `type` 当前只能发 `0`
- `payload` 的实际编码由 `audio.start.format` 决定:
  - `format=opus` 时，负载是单帧 Opus
  - `format=pcm16le` 时，负载是小端 16-bit PCM
- `timestamp` 建议单调递增

伪代码:

```c
struct AudioFrameHeader {
  uint16_t version_be;      // 2
  uint16_t type_be;         // 0
  uint32_t reserved_be;     // 0
  uint32_t timestamp_be;    // ms
  uint32_t payload_size_be; // N
};
```

### 7.4 `offline` 模式

### 推荐参数

| 参数 | 值 |
|---|---|
| format | `opus` |
| sttMode | `offline` |
| sampleRate | `16000` |
| channels | `1` |
| frameDuration | `20` |

### 时序

```text
ESP32                                gochat-server
  | audio.start(opus,offline)  -----> |
  | binary opus frame #1       -----> |
  | binary opus frame #2       -----> |
  | ...                               |
  | binary opus frame #N       -----> |
  | audio.stop                 -----> |
  |                                   | 服务端统一转写
  | <----- stt                         |
  | <----- reply                       |
```

### 设备端收到的 `stt`

```json
{
  "type": "stt",
  "text": "你好，今天天气怎么样"
}
```

说明:

- `stt` 是最终文本
- 收到 `stt` 后，服务端才会把文本继续转成内部 `message`
- 后续还会收到 AI 的 `reply`

### 7.5 `2pass` 实时字幕模式

### 推荐参数

| 参数 | 值 |
|---|---|
| format | `pcm16le` |
| sttMode | `2pass` |
| sampleRate | `16000` |
| channels | `1` |
| frameDuration | `60` |

### PCM 负载计算

16kHz、单声道、16-bit 时:

- `20ms` PCM = `16000 * 1 * 2 * 20 / 1000 = 640 bytes`
- `60ms` PCM = `16000 * 1 * 2 * 60 / 1000 = 1920 bytes`

推荐实时字幕用 `60ms`，服务端已按这个节奏做过联调。

### 时序

```text
ESP32                                  gochat-server
  | audio.start(pcm16le,2pass)   -----> |
  | binary pcm chunk #1          -----> |
  | binary pcm chunk #2          -----> |
  | <----- stt.partial(2pass-online)    |
  | binary pcm chunk #3          -----> |
  | <----- stt.partial(2pass-online)    |
  | ...                                 |
  | audio.stop                   -----> |
  | <----- stt.partial(2pass-offline)   |
  | <----- stt                          |
  | <----- reply                        |
```

### 实时字幕消息: `stt.partial`

```json
{
  "type": "stt.partial",
  "text": "欢迎大家来体验达摩院推出的语音识别模型",
  "phase": "2pass-online"
}
```

字段说明:

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| type | string | 是 | 固定 `"stt.partial"` |
| text | string | 是 | 当前可展示字幕 |
| phase | string | 否 | `"2pass-online"` 或 `"2pass-offline"` |

建议处理:

- `phase=2pass-online`:
  - 当作实时字幕预览
  - 可以覆盖更新同一行 UI
- `phase=2pass-offline`:
  - 表示服务端已经拿到最终修正版片段
  - 仍然不要当作最终指令提交
- 只有收到正式 `stt`，才把用户语音消息真正落屏为最终文本

### 最终消息: `stt`

```json
{
  "type": "stt",
  "text": "欢迎大家来体验达摩院推出的语音识别模型。"
}
```

说明:

- `stt` 是本次语音的最终文本
- 只有在收到 `stt` 后，服务端才会继续触发 OpenClaw 生成 `reply`
- 因此设备端 UI 最稳妥的做法是:
  - 录音中显示 `stt.partial`
  - `audio.stop` 后等待正式 `stt`
  - 用 `stt` 覆盖最终“我说的话”
  - 再等待 AI `reply`

### 7.6 ESP32 -> 服务端: `audio.stop`

```json
{
  "type": "audio.stop"
}
```

规则:

- 每个活跃音频会话必须以 `audio.stop` 结束
- `offline` 模式下，`audio.stop` 后才开始最终转写
- `2pass` 模式下，`audio.stop` 会触发最终收尾和修正

### 7.7 推荐设备侧 UI 行为

如果 ESP32 有屏幕，建议把语音流程分成 4 个显示阶段:

1. `录音中`
2. `识别中`
3. `我: <stt 或 stt.partial>`
4. `AI: <reply>`

推荐细节:

- `offline`:
  - 录音期间不需要字幕
  - `audio.stop` 后显示“识别中...”
- `2pass`:
  - 录音期间直接用 `stt.partial` 刷新字幕
  - 收到 `stt` 后把字幕定稿
- 无论哪种模式，只要收到 `reply`，都把它作为 AI 最终输出

---

## 8. HTTP 接口摘要

### 8.1 配对开始

```yaml
name: chat_pair_start
method: POST
path: /api/chat/pair/start
content_type: application/json
request:
  name: string
response_ok:
  code: string
  sessionToken: string
  channelId: string
  channelName: string
  expiresIn: int
  expiresAt: int
  installCommand: string
```

### 8.2 配对状态查询

```yaml
name: chat_pair_session
method: GET
path: /api/chat/pair/session?sessionToken=<token>
response_ok:
  code: string
  status: pending|claimed|connected
  channelId: string
  channelName: string
  online: bool
  expiresAt: int
  claimedBy: string
  claimedAt: int
  token: string
  expiresIn: int
  loginExpiresAt: int
```

### 8.3 旧版登录接口

```yaml
name: chat_login
method: POST
path: /api/chat/login
content_type: application/json
request:
  channelId: string
  secret: string
response_ok:
  token: string
  channelId: string
  channelName: string
  expiresIn: int
  expiresAt: int
```

### 8.4 Session 查询接口

```yaml
name: chat_session
method: GET
path: /api/chat/session
headers:
  Authorization: "Bearer <token>"
```

### 8.5 附件上传接口

如果 ESP32 需要上传图片/音频/文件，可复用现有 `/app` 的 3 步上传流程:

1. `POST /api/upload/presign`
2. `PUT <uploadUrl>`
3. `POST /api/upload/confirm`

---

## 9. ESP32 实现规则

```yaml
esp32_client_rules:
  pairing:
    - do_not_embed_channel_secret_by_default
    - use_pair_start_and_pair_session
    - save_session_token
    - recreate_pair_session_when_expired
  websocket:
    - connect_with_query_token
    - send_hello_immediately_after_open
    - send_ping_every_30s
    - reconnect_after_3s_when_socket_closed
  text_chat:
    - keep_conversationId_as_default
    - treat_reply_as_ai_output
    - treat_status_as_runtime_presence
  audio_offline:
    - prefer_opus_20ms
    - send_audio_start_before_binary_frames
    - send_audio_stop_after_button_release
    - wait_for_final_stt_then_wait_for_reply
  audio_realtime:
    - only_use_pcm16le
    - prefer_60ms_chunks
    - render_stt_partial_as_preview
    - render_stt_as_final_user_text
    - never_treat_partial_as_final_command
  errors:
    - channel_offline_means_plugin_offline
    - audio_STT_mode_not_configured_means_backend_not_deployed_for_that_mode
    - 401_or_403_means_repair_or_relogin
```

额外建议:

- 当前 ESP32 若已经有 Opus 编码器，优先先做 `offline`
- `2pass` 的带宽明显更高，适合 Wi-Fi 稳定、需要实时字幕的场景
- 一条 WebSocket 连接同一时刻只维护一个活跃音频会话

---

## 10. 常见错误与排查

### 10.1 `audio STT mode is not configured`

含义:

- 当前服务器没有部署该模式对应的 STT 后端

常见原因:

- 发了 `sttMode=2pass`，但服务端没配置 `GOCHAT_AUDIO_STT_ONLINE_URL`
- 前端或设备已经升级，但线上还是旧版 `gochat-server`

### 10.2 `audio session not started`

含义:

- 先发了二进制音频帧，但没先发 `audio.start`

### 10.3 `audio session is empty`

含义:

- 发了 `audio.start` 和 `audio.stop`，但中间没有有效音频帧

### 10.4 `audio transcription returned empty text`

含义:

- 服务端收到了音频，但 STT 没产出文本

建议:

- 检查采样率是否真是 `16000`
- 检查 `channels` 是否真是 `1`
- 检查 PCM 是否是 `16-bit little-endian`
- 检查 Opus 分帧时长是否稳定在 `20ms`

---

## 11. 最小可行实现

### 11.1 文本版最小闭环

如果现在只想让 ESP32 先跑通文本聊天，最小闭环只要实现 7 件事:

1. `POST /api/chat/pair/start`
2. 屏幕显示 `6位连接码`
3. `GET /api/chat/pair/session` 轮询直到拿到 `token`
4. 连接 `wss://<host>/ws/chat?token=<token>`
5. `onOpen` 发 `hello`
6. 发文本 `message`
7. 收 `reply / status / pong`

### 11.2 语音整段版最小闭环

在文本版基础上再补 5 件事:

1. 按下按键时发 `audio.start(opus, offline)`
2. 持续发 Opus 二进制帧
3. 松开按键时发 `audio.stop`
4. 等待 `stt`
5. 再等待 `reply`

### 11.3 实时字幕版最小闭环

在文本版基础上再补 6 件事:

1. 按下按键时发 `audio.start(pcm16le, 2pass)`
2. 持续发 `60ms pcm16le` 二进制帧
3. 录音中处理 `stt.partial`
4. 松开按键时发 `audio.stop`
5. 等待最终 `stt`
6. 再等待 `reply`

---

## 12. 可直接喂给 AI 的最终协议摘要

```json
{
  "protocolName": "gochat-esp32-chat-and-voice-client",
  "sameAsPage": "/app",
  "mode": "passive_pairing",
  "serverBaseUrl": "https://fund.moyi.vip",
  "pairing": {
    "start": {
      "method": "POST",
      "path": "/api/chat/pair/start",
      "body": {
        "name": "ESP32 Desk Assistant"
      }
    },
    "session": {
      "method": "GET",
      "path": "/api/chat/pair/session?sessionToken=<SESSION_TOKEN>",
      "statusValues": ["pending", "claimed", "connected"],
      "tokenAppearsWhen": "status=connected"
    }
  },
  "httpSession": {
    "method": "GET",
    "path": "/api/chat/session",
    "auth": "Bearer token"
  },
  "websocket": {
    "path": "/ws/chat?token=<JWT_TOKEN>",
    "onOpenSend": {
      "type": "hello",
      "clientType": "esp32",
      "clientVersion": "1.1.0"
    },
    "heartbeat": {
      "intervalMs": 30000,
      "send": {"type": "ping"},
      "expect": {"type": "pong"}
    }
  },
  "textMessages": {
    "send": {
      "type": "message",
      "conversationId": "default"
    },
    "receive": ["reply", "status", "error", "pong"]
  },
  "audioModes": {
    "offline": {
      "audioStart": {
        "type": "audio.start",
        "format": "opus",
        "sttMode": "offline",
        "sampleRate": 16000,
        "channels": 1,
        "frameDuration": 20
      },
      "binaryProtocol": "BinaryProtocol2",
      "receive": ["stt", "reply", "error"]
    },
    "realtime2pass": {
      "audioStart": {
        "type": "audio.start",
        "format": "pcm16le",
        "sttMode": "2pass",
        "sampleRate": 16000,
        "channels": 1,
        "frameDuration": 60
      },
      "binaryProtocol": "BinaryProtocol2",
      "receive": ["stt.partial", "stt", "reply", "error"]
    }
  },
  "binaryProtocol2": {
    "version": 2,
    "frameType": 0,
    "headerSize": 16,
    "timestampUnit": "ms",
    "payloadDependsOnAudioStartFormat": true
  },
  "doNotUse": ["/ws/plugin", "/api/plugin/pair/claim"]
}
```

---

## 13. 备注

- 这份文档已经按 `2026-04-07` 当前代码更新为最新版
- 相比上一版，最大的新增是:
  - 正式纳入语音协议
  - 同时支持 `offline` 与 `online/2pass`
  - 明确了 `stt.partial / stt / reply` 的时序关系
- 如果需要，我下一步可以继续补:
  - `ESP-IDF` 版录音状态机伪代码
  - `Arduino ESP32` 版 WebSocket 发包示例
  - I2S 麦克风采样到 `pcm16le` 的数据通路说明
