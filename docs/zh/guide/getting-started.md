# 快速上手

可以用**桌面版**（macOS Apple Silicon，什么都不用另装），也可以**从源码安装**；Docker Compose 一体化启动见 [README](https://github.com/AI-for-anyone/loop-now/blob/main/README.zh-CN.md#开始使用)。macOS Apple Silicon 是主要验证环境，部分组件支持 Linux，但尚未建立等效的整系统覆盖；飞书用户 CLI 运行时目前仅支持 macOS arm64。

::: warning 早期个人版
Loop Now 面向本地单实例。控制台没有登录功能，应通过本机回环地址访问。使用模型或工具供应商时，相关数据仍会发送给对应服务。
:::

## 桌面版（macOS Apple Silicon） {#desktop-app-macos-apple-silicon}

一个 DMG 里带着服务端、控制台、Node 和 Pi，不需要另装任何东西。

1. 从 [Releases](https://github.com/AI-for-anyone/loop-now/releases/latest) 下载 `loop-now-desktop-<版本>-mac-arm64.dmg`，打开后把 Loop Now 拖进"应用程序"。
2. 第一次打开：安装包目前没有 Apple 公证，macOS 会提示无法验证开发者。到"系统设置 › 隐私与安全性"，在"安全性"一栏点"仍要打开"，之后不再提示。
3. 启动后菜单栏出现 Loop Now 图标，窗口里就是控制台。关掉窗口，服务端和 Agent 仍在后台运行；要彻底停止，从菜单栏图标选"退出"。

数据在哪：数据库和每日备份在 `~/Library/Application Support/Loop Now/default/data`，Agent 工作区在 `/Users/Shared/loop-now-desktop/default`，日志在 `~/Library/Logs/Loop Now/default`。桌面版不使用源码安装的 `~/.loop_now` 和 `/Users/Shared/loop-now`，两种安装可以并存。要用一套独立的数据试用，带参数启动：`open -a "Loop Now" --args --profile=试用`。

更新：退出 Loop Now，用新的 DMG 替换"应用程序"里的 app，再打开，数据保留。菜单栏的"检查更新"会提示新版本并打开下载页；签名公证完成后才会改为自动安装。

桌面版里的 Agent 用随包的 Pi 1.4.0 和 Node 24.21；模型账号、CLI 账号、插件都在控制台里配置，和源码安装相同。外部身份和 `loopctl` 需要对外 TLS 端口，桌面版目前还不能开启。

接下来看[给第一个 Agent 派活](#give-your-first-agent-a-job)。

## 从源码安装 {#source-installation}

### 1. 准备依赖

源码安装运行**一个二进制**（`loop-server`），跑在 **Node.js 24** 上（Pi 和插件宿主也用它），使用**一个数据目录**（默认 `~/.loop_now`），不需要另装数据库或缓存。构建需要：

| 依赖 | 仓库基线版本 |
| --- | --- |
| Go | 1.27 或更高 |
| Node.js / npm | **24.21.0 / 11.19.0**；Web 脚本会校验这两个精确版本 |
| Pi 编码 Agent | **1.4.0** |
| 其他 | Git |

激活符合要求的 Node 与 npm 后：

```sh
npm install -g @earendil-works/pi-coding-agent@1.4.0
pi --version

git clone --depth 1 --branch main https://github.com/AI-for-anyone/loop-now.git
cd loop-now
```

### 2. 配置服务端

在仓库根目录执行以下命令：

```sh
export LOOP_NOW_NODE_BIN="$(command -v node)"
export LOOP_NOW_PI_BIN="$(command -v pi)"
```

::: danger 明文凭证
服务端把数据库和每日备份放在数据目录里，默认是服务端用户的 `~/.loop_now`（用 `LOOP_NOW_DATA_DIR` 修改），首次启动时自动建库。凭证以明文保存在这个数据库中，请保护好数据目录与备份。服务端读取进程环境变量，不会自动加载 `.env` 文件。
:::

选择一个**家目录之外**的持久工作目录，且其祖先目录中没有 `AGENTS.md` 或 `CLAUDE.md`，避免 Pi 继承无关的主机指令。全新安装的默认值是 macOS 的 `/Users/Shared/loop-now` 或 Linux 的 `/var/lib/loop-now`：

::: code-group
```sh [macOS]
export LOOP_NOW_WORK_ROOT='/Users/Shared/loop-now'
sudo mkdir -p "$LOOP_NOW_WORK_ROOT"
sudo chown "$(id -u):$(id -g)" "$LOOP_NOW_WORK_ROOT"
chmod 700 "$LOOP_NOW_WORK_ROOT"
```
```sh [Linux]
export LOOP_NOW_WORK_ROOT='/var/lib/loop-now'
sudo mkdir -p "$LOOP_NOW_WORK_ROOT"
sudo chown "$(id -u):$(id -g)" "$LOOP_NOW_WORK_ROOT"
chmod 700 "$LOOP_NOW_WORK_ROOT"
```
:::

### 3. 构建并启动后端

一起构建服务端和配套版本的 `loopctl`：

```sh
mkdir -p bin
go build -o bin/loop-server ./cmd/loop-server
loop_version="$(bin/loop-server version)"
go run ./cmd/loopctl-dist -version "$loop_version" -out "$PWD/.local-build/loopctl-dist"

export LOOP_NOW_LOOPCTL_DIST_DIR="$PWD/.local-build/loopctl-dist"
bin/loop-server
```

启动时会检查依赖、应用待执行的数据库迁移，并创建默认组织与项目。API 监听 `127.0.0.1:8780`。一个数据目录同时只能由一个服务端使用；升级时保留数据目录和工作目录。

插件需要 Node 插件宿主（在 `plugins/` 下构建，Node 24.21.0）。只有设置了 `LOOP_NOW_PLUGIN_HOST_ENTRY` 服务端才会运行宿主；设置 `LOOP_NOW_PLUGIN_OFFICIAL_DIR` 后官方插件会作为 `builtin` 安装来源提供：

```sh
(cd plugins && npm ci --ignore-scripts && npm run build)
export LOOP_NOW_PLUGIN_HOST_ENTRY="$PWD/plugins/host/dist/main.js"
export LOOP_NOW_PLUGIN_OFFICIAL_DIR="$PWD/plugins/official"
```

请在启动 `bin/loop-server` 之前设置好这些变量。`dist/main.js` 是自包含的单文件，可以复制到其他位置。可选的工具集成可能需要各自的依赖与凭证。

#### 从基于 PostgreSQL 的版本迁移 {#moving-from-a-postgresql-based-release}

SQLite 版之前的版本（到 v0.1.0 为止）把数据存在 PostgreSQL 和 Redis 里。把一套安装迁移过来：

1. 导入要求旧库处在 PostgreSQL 版的最后一个迁移，v0.1.0 还没有到。先检出 tag `pg-final`（PostgreSQL 版的最后一个提交），构建后用原来的配置启动一次，让它完成迁移，然后停掉。
2. 让 `LOOP_NOW_DATA_DIR` 指向一个还没有库的数据目录，把 `LOOP_NOW_DATABASE_URL` 设为旧库，运行一次 `loop-server import-postgres`。它在一个一致的快照里复制全部数据，核对行数与完整性，并打印每张表的行数；任何一步失败都不留下新库，也从不改动旧库。
3. 保持原来的 `LOOP_NOW_WORK_ROOT`，在这个数据目录上启动新的服务端。Redis 不再需要。

### 4. 启动控制台

在第二个终端中，仓库根目录，激活 Node 24.21.0 与 npm 11.19.0：

```sh
cd web
export LOOP_NOW_WEB_NODE_BIN="$(dirname "$(command -v node)")"
./bin/npm24 ci
./bin/npm24 run dev
```

打开 **[http://localhost:5173](http://localhost:5173)**。默认 API 地址已经配置好；如需修改，参见 [`web/.env.example`](https://github.com/AI-for-anyone/loop-now/blob/main/web/.env.example)。

## 给第一个 Agent 派活 {#give-your-first-agent-a-job}

1. 打开一个项目，用支持的登录方式或 API Key 添加一个模型账号。
2. 创建 Agent，选择它的模型与账号，并添加指令或 Skills。
3. 添加它需要的仓库或文件。启用插件并显式授予工具权限。
4. 开始对话。如需发送图片，选择支持图片输入的模型。
5. 想要协作时，添加专业 Agent 并指定项目的 Manager。

先用一个 Agent 和一个小任务起步，再扩展更长的流程。接下来可以阅读[核心概念](/zh/guide/concepts)，了解组织、项目、Agent、Inbox 和任务如何配合。
