# 快速上手

当前的安装方式是**源码安装**。macOS Apple Silicon 是主要验证环境，部分组件支持 Linux，但尚未建立等效的整系统覆盖；飞书用户 CLI 运行时目前仅支持 macOS arm64。

::: warning 早期个人版
Loop Now 面向本地单实例。控制台没有登录功能，应通过本机回环地址访问。使用模型或工具供应商时，相关数据仍会发送给对应服务。
:::

## 1. 准备依赖

| 依赖 | 仓库基线版本 |
| --- | --- |
| Go | 1.27 或更高 |
| Node.js / npm | **24.21.0 / 11.19.0**；Web 脚本会校验这两个精确版本 |
| PostgreSQL | 18 |
| Redis | 8 |
| Pi 编码 Agent | **1.4.0** |
| 其他 | Git |

激活符合要求的 Node 与 npm 后：

```sh
npm install -g @earendil-works/pi-coding-agent@1.4.0
pi --version

git clone --depth 1 --branch main https://github.com/AI-for-anyone/loop-now.git
cd loop-now
```

用你偏好的本地安装方式启动 PostgreSQL 和 Redis，并创建专用的 PostgreSQL 角色与数据库（以本地 PostgreSQL 管理员身份执行）：

```sh
createuser --login --pwprompt loopnow
createdb --owner=loopnow loopnow
```

## 2. 配置服务端

在仓库根目录执行以下命令。把数据库密码换成你设置的值，必要时做 URL 编码。使用专用的 Redis 数据库与键前缀；如果服务端要求认证，在 URL 中加入。

```sh
export LOOP_NOW_DATABASE_URL='postgres://loopnow:YOUR_URL_ENCODED_PASSWORD@127.0.0.1:5432/loopnow?sslmode=disable'
export LOOP_NOW_REDIS_URL='redis://127.0.0.1:6379/1'
export LOOP_NOW_REDIS_KEY_PREFIX='loopnow:'
export LOOP_NOW_NODE_BIN="$(command -v node)"
export LOOP_NOW_PI_BIN="$(command -v pi)"
```

::: danger 明文凭证
凭证以明文保存在 PostgreSQL 中，请保护好数据库访问与备份文件。服务端读取进程环境变量，不会自动加载 `.env` 文件。
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

## 3. 构建并启动后端

一起构建服务端和配套版本的 `loopctl`：

```sh
mkdir -p bin
go build -o bin/loop-server ./cmd/loop-server
loop_version="$(bin/loop-server version)"
go run ./cmd/loopctl-dist -version "$loop_version" -out "$PWD/.local-build/loopctl-dist"

export LOOP_NOW_LOOPCTL_DIST_DIR="$PWD/.local-build/loopctl-dist"
bin/loop-server
```

启动时会检查依赖、应用待执行的数据库迁移，并创建默认组织与项目。API 监听 `127.0.0.1:8780`。每个安装只运行一个服务端实例；升级时保留数据库、Redis 配置和工作目录。

插件需要 Node 插件宿主（在 `plugins/` 下构建，Node 24.21.0）。只有设置了 `LOOP_NOW_PLUGIN_HOST_ENTRY` 服务端才会运行宿主；设置 `LOOP_NOW_PLUGIN_OFFICIAL_DIR` 后官方插件会作为 `builtin` 安装来源提供：

```sh
(cd plugins && npm ci --ignore-scripts && npm run build)
export LOOP_NOW_PLUGIN_HOST_ENTRY="$PWD/plugins/host/dist/main.js"
export LOOP_NOW_PLUGIN_OFFICIAL_DIR="$PWD/plugins/official"
```

请在启动 `bin/loop-server` 之前设置好这些变量。`dist/main.js` 是自包含的单文件，可以复制到其他位置。可选的工具集成可能需要各自的依赖与凭证。

## 4. 启动控制台

在第二个终端中，仓库根目录，激活 Node 24.21.0 与 npm 11.19.0：

```sh
cd web
export LOOP_NOW_WEB_NODE_BIN="$(dirname "$(command -v node)")"
./bin/npm24 ci
./bin/npm24 run dev
```

打开 **[http://localhost:5173](http://localhost:5173)**。默认 API 地址已经配置好；如需修改，参见 [`web/.env.example`](https://github.com/AI-for-anyone/loop-now/blob/main/web/.env.example)。

## 5. 给第一个 Agent 派活

1. 打开一个项目，用支持的登录方式或 API Key 添加一个模型账号。
2. 创建 Agent，选择它的模型与账号，并添加指令或 Skills。
3. 添加它需要的仓库或文件。启用插件并显式授予工具权限。
4. 开始对话。如需发送图片，选择支持图片输入的模型。
5. 想要协作时，添加专业 Agent 并指定项目的 Manager。

先用一个 Agent 和一个小任务起步，再扩展更长的流程。接下来可以阅读[核心概念](/zh/guide/concepts)，了解组织、项目、Agent、Inbox 和任务如何配合。
