# AGENTS

这份文档给 AI 助手、自动化代理和新接手的工程师看。目标不是介绍概念，而是让执行者在尽量少提问的前提下，能把 `go-claw-tile` 部署起来、验证起来，并在失败时知道先查哪里。

## 你在操作什么

这个仓库当前至少包含两条必须理解的链路：

- `gochat-server`：Web/App 入口、管理后台、上传、本地/S3 存储、消息桥接
- `extensions/gochat`：OpenClaw `gochat` 插件，负责把 OpenClaw 接到 GoChat

最常见的运行形态是：

```text
Web/App -> gochat-server -> OpenClaw + gochat plugin -> AI provider
```

## 默认假设

如果用户没有特别说明，按下面假设执行：

- 工作目录：仓库根目录
- OpenClaw 已经安装，可执行文件名为 `openclaw`
- 用户希望部署完整链路，而不是只看代码
- `gochat-server` 默认监听 `9750`
- OpenClaw 网关默认监听 `8790`
- 上传默认先用本地模式，S3 只在用户明确要求时配置

## 先做检查

开始前先确认这些命令可用：

```bash
openclaw --version
node --version
npm --version
go version
```

如果任何一个缺失，优先告诉用户缺少哪个依赖，再继续最小化地推进。

## 最短部署路径

### 1. 安装并启用 gochat 插件

优先使用仓库里的安装脚本：

```bash
cd /Users/moyi/Downloads/openclaw-main/extensions/gochat
./install.sh
```

安装后验证：

```bash
openclaw plugins list
```

预期结果：

- 能看到 `gochat`
- 没有 `plugins.allow is empty` 或 “untracked local code” 这类信任提醒

如果用户要求走远程发布版，改用：

```bash
curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | bash
```

### 2. 配置并启动 gochat-server

```bash
cd /Users/moyi/Downloads/openclaw-main/gochat-server
cp .env.example .env
```

至少填这些环境变量：

```dotenv
GOCHAT_WEBHOOK_SECRET=...
GOCHAT_CALLBACK_SECRET=...
GOCHAT_OPENCLAW_WEBHOOK_URL=http://localhost:8790/gochat-webhook
GOCHAT_ADMIN_USERNAME=admin
GOCHAT_ADMIN_PASSWORD=strong-password
GOCHAT_ADMIN_JWT_SECRET=replace-with-random-secret
```

启动命令：

```bash
cd /Users/moyi/Downloads/openclaw-main/gochat-server
go run ./cmd/server
```

### 3. 启动 OpenClaw

如果用户没有提供自己的启动方式，默认使用：

```bash
openclaw gateway run
```

## 推荐验证顺序

按这个顺序检查，定位问题最快：

1. `openclaw gateway run` 是否正常
2. `openclaw plugins list` 是否有 `gochat`
3. `gochat-server` 是否监听 `9750`
4. 能否登录 `/admin`
5. `/admin` 中上传测试是否通过
6. `/app` 中能否发送文本消息
7. 再验证附件上传、录音、会议处理

## 上传配置注意事项

当前实现有一个非常重要的行为：

- 后台保存上传配置只会持久化设置
- 新配置不会热更新到当前 uploader 实例
- 要真正影响 `/app` 上传或 `/api/upload/presign`，必须重启 `gochat-server`

如果用户说“后台测试通过，但网页上传不对”，先确认两件事：

1. `gochat-server` 是否在保存配置后重启过
2. 浏览器是否强刷过页面

## S3 问题排查顺序

如果用户反馈 S3 上传异常，按这个顺序查：

1. 后台 S3 测试是否通过
2. 保存配置后是否重启 `gochat-server`
3. 前端失败请求的响应 body 是什么
4. `bucket / region / endpoint / public URL / force path` 是否匹配

当前 S3 测试链路会：

1. PUT 测试对象
2. GET 公网地址验证可访问
3. DELETE 测试对象

所以如果后台测试通过而前端上传失败，优先怀疑：

- 服务没重启，仍在使用旧配置
- 浏览器缓存旧前端
- 具体 presign 请求响应有额外错误

## 插件流式执行问题

`gochat` 插件已经修过一个关键问题：

- WebSocket 收到消息时不再阻塞在 `await onMessage(parsed)`
- AI 长任务期间会周期性推送 active status

所以如果用户仍然反馈“流式输出时 ws 超时”，优先排查：

- 运行的是否真是最新插件版本
- OpenClaw 是否已经重启
- 中间代理是否对 WebSocket 有空闲超时

## 版本与发布

如果用户要求确认插件版本，优先检查：

- [extensions/gochat/package.json](/Users/moyi/Downloads/openclaw-main/extensions/gochat/package.json)
- [extensions/gochat/install.sh](/Users/moyi/Downloads/openclaw-main/extensions/gochat/install.sh)
- [extensions/gochat/CHANGELOG.md](/Users/moyi/Downloads/openclaw-main/extensions/gochat/CHANGELOG.md)

如果用户通过 `curl | bash` 安装到了旧版本，不要猜，直接核对远程脚本：

```bash
curl -sL https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh | rg '^VERSION='
```

## 面向 AI 的工作原则

- 先确认当前运行版本，再判断代码是否生效
- 遇到“配置保存了但效果没变”，优先怀疑重启或缓存
- 不要默认后台保存具备热更新能力
- 不要把 `uploads`、数据库、日志、二进制提交进 git
- 修改文档时保持“人类能照着做，AI 也能逐条执行”

## 关键路径

- 仓库根目录：`/Users/moyi/Downloads/openclaw-main`
- GoChat Server 文档：[gochat-server/README.md](/Users/moyi/Downloads/openclaw-main/gochat-server/README.md)
- GoChat 插件文档：[extensions/gochat/README.md](/Users/moyi/Downloads/openclaw-main/extensions/gochat/README.md)
- 根目录人类说明：[README.md](/Users/moyi/Downloads/openclaw-main/README.md)
