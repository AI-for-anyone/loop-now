<p align="center">
  <img src="web/public/favicon.svg" width="72" alt="Loop Now 图标" />
</p>

<h1 align="center">Loop Now</h1>

<p align="center">
  <strong>让多个 AI Agent 在本地协作，用插件扩展它们的能力。</strong><br />
  接入 CLI 与 MCP，订阅事件、编写 Hook，把项目、对话和任务放在一个工作台中。
</p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a><br />
  <a href="#开始使用">开始使用</a> ·
  <a href="#你可以用它做什么">功能</a> ·
  <a href="#核心能力可扩展的插件体系">插件</a> ·
  <a href="https://github.com/AI-for-anyone/loop-now/issues">反馈问题</a>
</p>

![Loop Now 控制台：Agent 对话、图表渲染与紧凑输入框](docs/assets/console.png)

<p align="center"><sub>真实控制台，使用演示数据。界面支持中文和英文，按浏览器语言或在设置里切换。</sub></p>

Loop Now 把多个 Agent 的工作放进一个浏览器工作台。每个 Agent 保留自己的工作目录和会话，Manager 可以通过任务流程协调不同角色。你可以随时查看对话、检查工具调用、发消息介入，并了解谁在执行、谁在等待。

**插件是平台的核心扩展入口：**工具、事件 Hook、账号、权限和资源可以一起打包、安装与管理版本。

平台运行在你的电脑上，以 [Pi](https://github.com/earendil-works/pi) 为执行引擎。使用哪些模型、账号、代码仓库和工具，由你为各个 Agent 配置。

> **早期个人版。** 当前仓库面向本地单实例。控制台没有登录功能，应通过本机回环地址访问。使用模型或工具供应商时，相关数据仍会发送给对应服务。

## 你可以用它做什么

| 能力 | 使用方式 |
| --- | --- |
| **组建 Agent 团队** | 按项目组织 Agent，分别配置模型、思考等级、指令、Skills、仓库与工具权限，并指定 Manager 协调工作。 |
| **保留工作上下文** | 持续使用 Agent 的常态会话与工作目录。任务使用独立副本，同一 Agent 在同一任务中连续复用自己的目录和会话。 |
| **通过 Inbox 协作** | Agent 与人之间可以发消息、提问、回复；忙碌时选择排队或打断，sleep 等待中的 Agent 可被新消息唤醒。 |
| **运行可观察的任务流程** | 查看节点、条件分支、有次数限制的审查回路、重试与成果。任务开始后固定流程和已捕获的配置。 |
| **用插件扩展能力** | 在同一插件中组合 CLI、MCP、事件 Hook、账号、权限和 Skills，接入团队需要的工具与服务。 |
| **看清执行过程** | 流式阅读回复、发送图片、渲染 Markdown 和 Mermaid，在每条回复的运行时间线中查看模型用量、工具命令、耗时和失败原因。 |

### 一个例子：从开发到审查

创建 Manager、开发和审查三个 Agent，配置它们使用的仓库、模型和工具，然后对 Manager 说：

> 实现这个功能，让审查 Agent 检查后给我结果。需要我决定的事情通过 Inbox 提问。

任务流程可以从开发进入审查，也可以在限定次数内返回修改。各成员在自己的任务上下文中工作，你能沿途查看消息和成果。具体开发、QA 或部署流程由项目指令与 Skills 定义，平台不预设某一种业务流程。

## 核心能力：可扩展的插件体系

**新工具与项目特有的行为都放进插件。**一个插件就是一个目录，`package.json` 是它唯一的清单。插件最多分两半，两半都可以没有：

- **Agent 半边**是标准的 [Pi 包](https://pi.dev/packages)：Pi 扩展、Skills、MCP 服务器和 CLI。每个获得授权的 Agent 在自己的 Pi 进程里加载它。现成的 Pi 包不用改就能装。
- **平台半边**运行在服务端旁边一个常驻的 Node 插件宿主里，订阅平台事件，并用按权限签发的插件令牌调用平台接口。

| 宿主提供 | 对插件意味着什么 |
| --- | --- |
| **事件** | 事件目录里的全部观察类事件：执行轮次、任务、节点、收件箱、提问、sleep、Agent、Pi 进程、上下文文件夹、插件自身。事件先持久化，处理函数返回后才确认，失败重投，重启后续投。拦截、修改、认领类 Hook 各有超时和超时策略。 |
| **状态与密钥** | 每个插件有自己的持久状态，分全局与项目级。`x-secret` 配置字段在控制台只写不回显，并在插件日志里遮蔽。 |
| **配置表单** | `loopNow.configSchema` 就是 JSON Schema，控制台据此生成表单；标了 `x-scope: project` 的字段可以按项目覆盖。 |
| **可靠的外发 HTTP** | SDK 的投递库带持久队列和退避重试，用 HMAC-SHA256 签名，并写下投递记录，插件页直接展示。 |
| **日志与隔离** | 每个插件的输出进入它自己的日志。一个插件抛错、超时或加载失败，只影响它自己。 |

清单说了算：订阅清单没有声明的事件、调用清单之外的接口范围都会失败。安装前，平台会把两半各有什么、订阅哪些事件、要哪些权限展示给人看。

观察类事件、状态、密钥、配置和投递库已经可用。拦截与修改 Hook、控制台操作按钮、HTTP 入口、定时、渠道和插件账号已在 [`plugins/SPEC.md`](plugins/SPEC.md) 定稿，正在陆续上线。

### 平台安装，项目启用，Agent 授权

可以从官方插件、Git 仓库、npm 包或本机目录安装，在控制台里操作，或用 `loopctl plugin install`。装好后，先在项目里启用插件并填写配置，再授权给需要加载 Agent 半边的 Agent。安装或启用本身不会给任何 Agent 授权。版本是固定的：更新是显式操作，进行中的任务继续用启动时的文件。

随平台发布三个官方插件：

- [**webhook**](plugins/official/webhook/README.md)：收件箱收到消息时，签名后 POST 到你的地址，失败重试，并记下投递记录。外部身份用它代替轮询收件箱。
- [**playwright**](plugins/official/playwright/README.md)：给 Agent 一个专用的可见 Chromium，通过官方 Playwright MCP 打开网页、读取页面、点击、输入和截图，不连接你的日常 Chrome。
- [**hello**](plugins/official/hello/README.md)：两半齐全的最小插件，可以从它开始写自己的插件。

### 几行代码写一个平台半边

```ts
import { createDelivery, type PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  const delivery = createDelivery(api)
  api.on('task.completed', async (event) => {
    const { url } = api.config.get<{ url?: string }>(event.projectId)
    if (url) await delivery.deliver({ target: url, body: event, eventId: event.id, eventType: event.type })
  })
}
```

```json
{
  "name": "task-notifier",
  "version": "1.0.0",
  "keywords": ["loop-now-plugin"],
  "peerDependencies": { "@loop-now/plugin-sdk": "*" },
  "loopNow": {
    "id": "task-notifier",
    "platform": "./platform.ts",
    "events": ["task.completed"],
    "permissions": ["state"],
    "configSchema": { "type": "object", "properties": { "url": { "type": "string", "x-scope": "project" } } }
  }
}
```

宿主和 Pi 一样直接加载 TypeScript，并自己提供 `@loop-now/plugin-sdk`。用 `node plugins/host/dist/main.js validate <目录> --load` 检查目录：它报出清单错误，以及代码里超出 `loopNow.events` 的订阅。

入门先看 [SDK 说明](plugins/sdk/README.md) 和官方插件。[`plugins/SPEC.md`](plugins/SPEC.md) 是完整合同，包括包格式、事件目录、Hook 种类与超时、权限和安装流程；[`api/plugin-host-protocol.md`](api/plugin-host-protocol.md) 是服务端与宿主之间的通信协议。平台不做沙箱：插件会执行代码，只安装可信来源。

飞书用户身份执行另需[构建说明](scripts/lark-user-variant/README.md)中固定版本的平台发行包；普通对话不需要它。

## 开始使用

可以用 **Docker Compose** 一体化启动，也可以从源码安装。主要验证环境是 macOS Apple Silicon；部分组件支持 Linux，但尚未完成同等范围的整套验证。飞书用户 CLI 运行组件目前仅支持 macOS arm64。

### Docker Compose（本地一体化）

首次启动会在本机构建服务端和控制台镜像，并启动 PostgreSQL 18、Redis 8：

```sh
docker compose up --build -d
```

打开 **[http://localhost:5173](http://localhost:5173)**。控制台没有登录功能，因此 Compose 只把 Web 的 `5173` 和 API 的 `8780` 发布到 `127.0.0.1`；PostgreSQL 与 Redis 不发布到宿主机。数据库默认用户、密码和库名都是 `loopnow`，仅用于 Compose 内部网络，不要把这套默认配置暴露到外部。

`pgdata` 命名卷保存数据库，`loopdata` 保存 Agent 工作区、会话和工具。`docker compose down` 会保留它们；`docker compose down -v` 会删除这些数据，执行前请先备份。

容器中的 Agent 有一些边界：

- 飞书用户 CLI 只支持 macOS arm64，不能在这套 Linux 容器里使用。
- 容器不会自动看到宿主机目录。已有仓库或资料需要另外挂载到 `server` 容器，并在控制台配置挂载后的容器内路径；`loopdata` 只保存平台自己的工作区。
- 容器不会共享宿主机上已经登录的 Pi 或 CLI 会话。请预先在项目中配置模型 API Key 和所需工具账号。

### 原生 models.json

在“全局设置 → 模型配置”中可以编辑固定版本 Pi 的原生 `models.json`，添加自定义 Provider 或覆盖内置 Provider。例如：

```json
{
  "providers": {
    "example": {
      "baseUrl": "https://models.example.com/v1",
      "api": "openai-completions",
      "apiKey": "$EXAMPLE_KEY",
      "headers": { "X-Client": "$EXAMPLE_CLIENT_HEADER" },
      "models": [
        { "id": "my-model", "input": ["text", "image"], "contextWindow": 128000, "maxTokens": 8192 }
      ]
    }
  }
}
```

`apiKey` 以及 Provider、模型和模型覆盖中的 `headers` 值只接受 `$NAME` 形式的环境变量引用；变量名由英文字母或下划线开头，后接字母、数字或下划线。不要填写明文密钥或 `!命令`。真实密钥仍在项目的模型账号中配置，不会写进 `models.json` 或回显到编辑器。

保存后，常态 Agent 从下一条消息使用新目录，已启动任务继续使用创建时冻结的 Provider 配置。如果重启后已保存配置不再通过当前 Pi 校验，服务会暂时退回内置模型目录，并在设置页显示警告，让你仍能修正并重新保存。

### 源码安装：1. 准备依赖

| 依赖 | 仓库当前版本基线 |
| --- | --- |
| Go | 1.27 或以上 |
| Node.js / npm | **24.21.0 / 11.19.0**；Web 脚本会检查精确版本 |
| PostgreSQL | 18 |
| Redis | 8 |
| Pi coding agent | **1.4.0** |
| 其他工具 | Git |

切换到要求的 Node 和 npm 后：

```sh
npm install -g @earendil-works/pi-coding-agent@1.4.0
pi --version

git clone --depth 1 --branch main https://github.com/AI-for-anyone/loop-now.git
cd loop-now
```

通过你使用的本地安装方式启动 PostgreSQL 和 Redis。使用本机 PostgreSQL 管理员创建专用角色和数据库：

```sh
createuser --login --pwprompt loopnow
createdb --owner=loopnow loopnow
```

### 2. 配置服务端

以下命令在仓库根目录运行。把数据库密码替换为刚设置的值，必要时做 URL 编码。Redis 使用专用 DB 和 key 前缀；如果启用了认证，请在连接地址中加入认证信息。

```sh
export LOOP_NOW_DATABASE_URL='postgres://loopnow:YOUR_URL_ENCODED_PASSWORD@127.0.0.1:5432/loopnow?sslmode=disable'
export LOOP_NOW_REDIS_URL='redis://127.0.0.1:6379/1'
export LOOP_NOW_REDIS_KEY_PREFIX='loopnow:'
export LOOP_NOW_NODE_BIN="$(command -v node)"
export LOOP_NOW_PI_BIN="$(command -v pi)"
```

凭证以明文存在 PostgreSQL，请保护好数据库访问权限和备份文件。服务端从进程环境变量读取配置，不会自动加载 `.env` 文件。

执行工作根目录必须持久保存，并位于**用户主目录之外**，它和上级目录不能包含 `AGENTS.md` 或 `CLAUDE.md`，以免 Pi 继承宿主上无关的指令。新安装默认使用 macOS 的 `/Users/Shared/loop-now`，或 Linux 的 `/var/lib/loop-now`：

```sh
# macOS；Linux 改用 /var/lib/loop-now。
export LOOP_NOW_WORK_ROOT='/Users/Shared/loop-now'
sudo mkdir -p "$LOOP_NOW_WORK_ROOT"
sudo chown "$(id -u):$(id -g)" "$LOOP_NOW_WORK_ROOT"
chmod 700 "$LOOP_NOW_WORK_ROOT"
```

### 3. 构建并启动后端

服务端和配套 `loopctl` 必须使用同一版本构建：

```sh
mkdir -p bin
go build -o bin/loop-server ./cmd/loop-server
loop_version="$(bin/loop-server version)"
go run ./cmd/loopctl-dist -version "$loop_version" -out "$PWD/.local-build/loopctl-dist"

export LOOP_NOW_LOOPCTL_DIST_DIR="$PWD/.local-build/loopctl-dist"
bin/loop-server
```

启动时会检查依赖、执行待应用的数据库迁移，并创建默认组织和项目。API 监听 `127.0.0.1:8780`。同一套安装只运行一个服务端；更新时保留原数据库、Redis 配置和工作目录。

插件需要 Node 插件宿主，从 `plugins/` 构建（Node 24.21.0）。设置了 `LOOP_NOW_PLUGIN_HOST_ENTRY` 时服务端才运行它；设置了 `LOOP_NOW_PLUGIN_OFFICIAL_DIR` 时，官方插件作为 `builtin` 安装来源提供：

```sh
(cd plugins && npm ci --ignore-scripts && npm run build)
export LOOP_NOW_PLUGIN_HOST_ENTRY="$PWD/plugins/host/dist/main.js"
export LOOP_NOW_PLUGIN_OFFICIAL_DIR="$PWD/plugins/official"
```

在启动 `bin/loop-server` 之前设置好。`dist/main.js` 是一个自带全部依赖的文件，可以复制到别处。可选工具集成可能还需要各自的依赖和凭证。

### 4. 启动控制台

打开第二个终端，从仓库根目录运行，确保当前 Node 为 24.21.0、npm 为 11.19.0：

```sh
cd web
export LOOP_NOW_WEB_NODE_BIN="$(dirname "$(command -v node)")"
./bin/npm24 ci
./bin/npm24 run dev
```

访问 **[http://localhost:5173](http://localhost:5173)**。默认 API 地址已配置好；需要修改时参见 [`web/.env.example`](web/.env.example)。

### 5. 给第一个 Agent 安排工作

1. 进入项目，通过支持的登录方式或 API Key 添加模型账号。
2. 创建 Agent，选择模型与账号，填写指令或添加 Skills。
3. 添加需要的仓库和资料，启用插件，并明确授予工具权限。
4. 开始对话。发送图片时，选择支持图片输入的模型。
5. 需要协作时，再添加不同角色的 Agent，并指定项目 Manager。

建议先让一个 Agent 完成小任务，再组合较长的工作流。

## 系统如何组成

```mermaid
flowchart LR
    UI[Web 控制台] <-->|HTTP 与 SSE| Server[Go 服务端]
    Server <--> PG[(PostgreSQL)]
    Server <--> Redis[(Redis)]
    Server -->|RPC| Pi[Pi 进程]
    Server <-->|stdio| Host[Node 插件宿主]
    Host -->|HTTP，插件令牌| Server
    Pi --> CLI[按作用域配置的 CLI]
    Pi --> Ext[插件扩展与 MCP 服务]
```

PostgreSQL 保存平台状态、凭证、消息与任务记录，Redis 负责协调、队列和实时流，本地文件系统保存 Agent 工作目录、会话与已安装工具。这些组件组成一套本地安装，图中拆分不代表已经支持独立扩缩容。

| 目录 | 用途 |
| --- | --- |
| [`web/`](web/) | React、TypeScript、Vite 控制台 |
| [`internal/server/`](internal/server/) | Go API、调度、存储和本机执行器 |
| [`cmd/loopctl/`](cmd/loopctl/) | 平台 Agent 与外部身份使用的 JSON CLI |
| [`plugins/`](plugins/) | 插件规范、Node 插件宿主、SDK 与官方插件 |
| [`api/`](api/) | 控制台与 Agent 的 OpenAPI 契约 |
| [`MAIN.md`](MAIN.md) | 维护者项目概览与当前实现状态 |

## 配置与当前边界

全部服务端配置和默认值见 [`internal/server/config/config.go`](internal/server/config/config.go)，常用项包括：

| 环境变量 | 用途／默认值 |
| --- | --- |
| `LOOP_NOW_LISTEN_ADDR` | 控制台 API；默认 `127.0.0.1:8780` |
| `LOOP_NOW_WORK_ROOT` | 持久化 Agent 工作目录与运行文件 |
| `LOOP_NOW_CONTEXT_DIR` | 项目（`projects/<id>/`）与全局（`global/`）上下文文件夹；默认服务运行用户的 `~/.loop_now` |
| `LOOP_NOW_PLUGIN_HOST_ENTRY` | 插件宿主入口的绝对路径（用 Node 运行）；不设时插件的平台半边不运行 |
| `LOOP_NOW_PLUGIN_OFFICIAL_DIR` | 随平台发布的官方插件目录的绝对路径（每个插件一个子目录）；不设时没有 `builtin` 安装来源 |
| `LOOP_NOW_PLUGIN_MARKET_URL` | 官方插件市场 `loop-now-market.json` 的 `https` 地址；默认是官方市场的索引地址，可改成别的地址 |
| `LOOP_NOW_PI_MAX_PROCESSES` | `0` 表示不限；正数限制本机 Pi 进程数 |
| `LOOP_NOW_PI_IDLE_TIMEOUT` | Pi 空闲退出时间；默认 `10m` |
| `LOOP_NOW_CONSOLE_ORIGINS` | 浏览器来源白名单；默认本机 5173 端口 |
| `LOOP_NOW_LARK_USER_BUNDLE_DIR` | 可选的已验证飞书用户 CLI 发行包 |

当前版本面向可信的个人使用环境：

- 控制台没有身份认证，请保留回环监听，不要直接暴露到公网。
- Agent 使用服务端的操作系统用户执行本地命令，独立任务目录不等于操作系统安全沙箱。
- 插件会执行代码，应只安装可信来源。模型和工具能访问什么，也取决于你提供的凭证与权限。
- 工作流支持线性节点、条件分支和有次数限制的回路；并行汇合、多副本部署和弹性伸缩不属于当前个人版范围。
- 会话记录会持久保存，但服务端重启不代表执行能无缝继续，也不保证自动重放进行中的工作；宜在 Agent 空闲时更新。

## 参与贡献

欢迎问题反馈、小范围修复、插件集成和文档改进。较大的行为改动请先开 [Issue](https://github.com/AI-for-anyone/loop-now/issues) 对齐范围。

先阅读 [`AGENTS.md`](AGENTS.md) 和 [`MAIN.md`](MAIN.md)，保持改动聚焦，并运行与改动相关的检查：

```sh
# 后端，在仓库根目录运行
go test ./cmd/... ./internal/... ./api/...

# Web，在 web/ 中运行，沿用上文的 Node 路径设置
./bin/npm24 run typecheck
./bin/npm24 run api:check
./bin/npm24 run lint
./bin/npm24 test
./bin/npm24 run build
```

数据库集成测试需通过 `LOOP_NOW_TEST_DATABASE_URL` 指向隔离测试库；涉及 Redis 的测试还需 `LOOP_NOW_TEST_REDIS_URL`。原生和浏览器测试有各自的夹具要求，跳过不等于集成验证通过。请勿在贡献中提交凭证、本地运行数据或构建产物。

## 许可证

Copyright 2026 Loop Now contributors.

Loop Now 采用 [Apache License 2.0](LICENSE)。仓库内第三方代码与字体保留各自的许可证和声明。
