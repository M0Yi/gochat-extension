# go-claw-tile

`go-claw-tile` 是一个围绕 OpenClaw 搭建的整合仓库，当前把下面几部分放在一起维护：

- `gochat-server`：Go 写的消息桥接与管理后台，负责 Web/App 侧消息、上传、S3、本地存储、管理页等能力。
- `extensions/gochat`：OpenClaw 的 `gochat` 插件，负责把 OpenClaw 接入 GoChat 本地直连模式或 Relay 模式。
- 根目录 OpenClaw 配置样例：用于本地开发和部署参考。

这份文档给人类开发者和运维同学看，目标是让你拿到仓库后可以自己部署起来。

## 适合什么场景

- 想把 OpenClaw 接到一个自定义聊天前端
- 想同时拥有 Web 管理后台、文件上传、本地或 S3 存储
- 想让 OpenClaw 通过 `gochat` 插件跑本地模式或 Relay 模式
- 想把录音转写、会议处理等能力一起带上

## 仓库结构

```text
openclaw-main/
├── gochat-server/         # GoChat 服务端
├── extensions/gochat/     # OpenClaw gochat 插件
├── .env.example           # OpenClaw 环境变量样例
└── README.md              # 当前文档
```

## 部署模式

最常见的是下面这两种：

### 模式 1：完整部署

适合你要跑完整链路：

- OpenClaw 网关
- `gochat` 插件
- `gochat-server`
- Web 管理后台 / Web App

### 模式 2：只部署插件

适合你已经有 OpenClaw，只需要把 `gochat` 插件装进去。

## 环境要求

建议至少准备这些环境：

- Node.js 18+
- npm 9+
- Go 1.25+
- 一个可运行的 OpenClaw 实例

如果你要启用语音转写，还需要：

- `ffmpeg`
- 可访问的 FunASR 服务，或本地语音转写依赖

如果你要启用对象存储上传，还需要：

- 兼容 S3 的对象存储
- 可写 bucket
- 正确的公网访问地址

## 快速部署

### 1. 启动 OpenClaw

先准备 OpenClaw 的环境变量：

```bash
cp /Users/moyi/Downloads/openclaw-main/.env.example /Users/moyi/Downloads/openclaw-main/.env
```

至少填好你实际要用的模型 API Key 和网关鉴权信息。

如果你是用已经安装好的 OpenClaw，通常直接启动即可：

```bash
openclaw gateway run
```

### 2. 安装 gochat 插件

如果你要从当前仓库源码安装：

```bash
cd /Users/moyi/Downloads/openclaw-main/extensions/gochat
./install.sh
```

如果你已经把插件单独发布到远端，也可以直接走远程安装：

```bash
curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | bash
```

安装完成后建议确认：

```bash
openclaw plugins list
```

### 3. 启动 gochat-server

先准备服务端配置：

```bash
cd /Users/moyi/Downloads/openclaw-main/gochat-server
cp .env.example .env
```

至少要填这些值：

- `GOCHAT_WEBHOOK_SECRET`
- `GOCHAT_CALLBACK_SECRET`
- `GOCHAT_OPENCLAW_WEBHOOK_URL`
- `GOCHAT_ADMIN_USERNAME`
- `GOCHAT_ADMIN_PASSWORD`
- `GOCHAT_ADMIN_JWT_SECRET`

然后启动：

```bash
cd /Users/moyi/Downloads/openclaw-main/gochat-server
go run ./cmd/server
```

默认端口是 `9750`。

## 上传配置

当前上传支持两种模式：

- 本地目录
- S3 / S3 兼容对象存储

后台已经支持编辑上传配置和测试 S3 配置，但这里有一个重要行为：

- 保存上传配置会持久化到后台设置
- `gochat-server` 需要重启后，新上传配置才会真正生效

也就是说，后台保存成功不等于热更新已经生效。

### S3 配置建议

如果你启用 S3，请确认这些项一致：

- `bucket`
- `region`
- `endpoint`
- `access key`
- `secret key`
- `public URL`
- 是否启用 path-style

后台的“S3 测试”现在会执行：

1. 上传测试对象
2. 通过公网地址访问测试对象
3. 删除测试对象

所以它能覆盖写入和访问这两条链路。

## 常见访问地址

部署完成后，通常会用到这些地址：

- GoChat Server API：`http://<host>:9750/api`
- GoChat Web App：`http://<host>:9750/app`
- GoChat 管理后台：`http://<host>:9750/admin`
- OpenClaw Webhook：`http://<host>:8790/gochat-webhook`

## 建议验证顺序

部署后按这个顺序检查最省事：

1. `openclaw gateway run` 是否正常启动
2. `openclaw plugins list` 里是否有 `gochat`
3. `gochat-server` 是否成功监听 `9750`
4. 打开 `/admin` 能否登录
5. 后台“上传配置测试”是否通过
6. 打开 `/app` 发送一条文本消息
7. 再测试文件上传和语音流程

## 开发和维护建议

- 不要把 `gochat-server/uploads/`、数据库、二进制和日志提交进 git
- 改完上传配置后记得重启 `gochat-server`
- 改完插件代码后记得重启或重新加载 OpenClaw
- Web 静态文件改完后，如果浏览器表现不对，先强刷页面

## 进一步说明

- GoChat Server 详细说明见 [gochat-server/README.md](/Users/moyi/Downloads/openclaw-main/gochat-server/README.md)
- GoChat 插件说明见 [extensions/gochat/README.md](/Users/moyi/Downloads/openclaw-main/extensions/gochat/README.md)
- 给 AI/自动化代理的部署说明见 [AGENTS.md](/Users/moyi/Downloads/openclaw-main/AGENTS.md)
