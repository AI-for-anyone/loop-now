# 更新日志

各版本面向使用者的变化。发布流水线取对应版本的一节作为 GitHub Release 的说明（见 `scripts/release/notes.sh`）。

## 未发布

### 本机模式

- 新增第四个开关"用本机的登录状态"，默认关闭（升级后也是关闭），打开时要确认一次。开启后，Agent 直接用你在这台电脑上各个工具的登录，等同以你本人的身份操作，所有 Agent 共用（任务里的 Agent 也一样）；Loop Now 里已为 Agent 配了账号的 CLI 仍用配置的账号。它与"用本机的 CLI"互相独立：只开这一项时，只有 Agent 原本就能调用的命令用得上这些登录。

### 控制台

- 项目里左侧栏的"任务"旁显示正在运行的任务数（准备中、等待和阻塞的不算），为 0 时不显示。任务开始或结束后几秒内自动更新，打开任务页时即时更新，不用刷新页面。

### 服务端

- 新增 `GET /projects/{projectId}/tasks/counts`（仅控制台）：按状态返回项目里未结束的任务数。

## v0.2.2

新增"本机模式"：让 Agent 直接用这台电脑上已有的目录、git 凭证和命令。三项各有开关，在"全局设置 › 本机模式"里，默认全部开启，升级后也是。

### 本机模式

- 绑定本机目录：在项目设置和 Agent 配置里登记本机目录（绝对路径加备注），项目登记的对项目里所有 Agent 有效。Agent 自己的文件夹不变，这些目录是另外告诉它可以去干活的地方。Agent 在里面的改动直接落在真实文件上，任务里也一样，不走任务副本；这份清单只是告知，不是访问隔离。目录不存在时会标出来，暂不告诉 Agent。
- 用本机的 git 身份："从 Git 拉取"时，HTTPS 地址可以用这台电脑上已保存的 git 凭证（如钥匙串），SSH 地址可以用 ssh-agent 里的密钥，GitHub 以外的私有仓库因此可以拉取。SSH 地址用 `~/.ssh` 下的密钥一直可用，不受这个开关影响。Agent 配了 GitHub 账号时，github.com 仍只用该账号。
- 用本机的 CLI：Agent 能调用这台电脑上安装的命令（你的 PATH 接在 Agent 的 PATH 之后）；这些命令的登录状态仍用 Loop Now 里配置的账号。PATH 在服务端启动时读取一次，新装的工具要重启 Loop Now 才看得到。
- 拉取失败时区分"认证失败"和"SSH 主机密钥未确认"，并给出下一步怎么做。

### 服务端

- `loop-server import-postgres` 改为先建到基线结构、导入并核对后再应用后续迁移。

## v0.2.1

修复桌面版里外部身份和 `loopctl` 用不了的问题。

### 桌面版

- 外部身份和 `loopctl` 可以用了：桌面版自动在本机（127.0.0.1）开对外 TLS 端口，端口第一次由系统选、之后沿用，证书是服务端自己维护的自签证书（`~/Library/Application Support/Loop Now/<配置>/shell/tls/cert.pem`）。菜单栏图标多了"外部身份连接方式…"，显示地址、证书位置和把令牌配进 `loopctl` 的命令，可以一键复制。之前创建的 `lne_` 令牌不用重发。目前只接受本机的连接。

### 服务端

- 新增 `LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED=<目录>`：对外端口改用服务端在该目录里维护的自签证书（`cert.pem`、`key.pem`，没有或临近过期时生成，覆盖回环地址与对外监听的主机），与 `LOOP_NOW_EXTERNAL_TLS_CERT`／`LOOP_NOW_EXTERNAL_TLS_KEY` 互斥。开了对外端口时就绪文件多一项 `externalUrl`。

## v0.2.0

这一版把存储换成了 SQLite，不再需要 PostgreSQL 和 Redis，并推出 macOS 桌面版。从 v0.1.0 升级要做一次数据导入，见下文"从 v0.1.0 升级"。

### 桌面版（macOS Apple Silicon）

- 一个 DMG 里带着服务端、控制台、Node 24.21 和 Pi 1.4.0，不需要另装任何东西。下载 `loop-now-desktop-<版本>-mac-arm64.dmg`，拖进"应用程序"即可。
- 菜单栏图标显示运行状态，可以打开窗口、在浏览器中打开、设置登录时启动、查看日志、重启和退出。关掉窗口时服务端和 Agent 继续在后台运行。
- 页面的提醒以系统通知和 Dock 未读数显示；外部链接在系统浏览器中打开。
- 数据与源码安装分开：数据库和每日备份在 `~/Library/Application Support/Loop Now/default/data`，Agent 工作区在 `/Users/Shared/loop-now-desktop/default`，日志在 `~/Library/Logs/Loop Now/default`。用 `open -a "Loop Now" --args --profile=<名字>` 可以另用一套独立的数据。
- 安装包尚未经过 Apple 公证：第一次打开时，到"系统设置 › 隐私与安全性"点"仍要打开"。菜单栏的"检查更新"只提示新版本并打开下载页，不会自动安装。
- 目前只有 Apple Silicon 版。外部身份和 `loopctl` 需要的对外 TLS 端口，桌面版暂时还不能开启。

### 存储只用 SQLite

- 运行只需要一个 `loop-server` 和一个数据目录（`LOOP_NOW_DATA_DIR`，默认 `~/.loop_now`）。数据库在其中的 `db/` 下，首次启动时自动创建。
- 每天自动备份到 `<数据目录>/backups`，保留最近 7 份（`LOOP_NOW_BACKUP_DIR`、`LOOP_NOW_BACKUP_KEEP` 可改）；`loop-server backup <目录>` 可以随时手动备份，服务运行中也可以。
- 凭证仍以明文保存在数据目录的数据库文件里，同一用户的其他进程可以直接读取。请保护好数据目录和备份。

### 发行包

- 每个版本在 GitHub Releases 提供服务端发行包，平台有 macOS（Apple Silicon、Intel）和 Linux（x86_64、ARM64）。包里有内嵌控制台的 `loop-server`、`loopctl`、插件宿主、官方插件，以及 Node 24.21.0 和 Pi 1.4.0，解开后不用另装 Node 和 Pi。控制台就在服务端口的根路径（默认 `http://127.0.0.1:8780/`）。
- 全部文件的校验和在 `SHA256SUMS` 里。

### 控制台

- 项目设置 › 模型账号：提供方改为可搜索的选择面板。按用途分组，显示名称、别名和地区版本；已有账号的提供方排在前面。只需 API Key 的提供方直接打开填写表单，Codex 直接进入登录。账号行显示提供方名称、id 和认证方式，更新时间按控制台的语言格式显示。
- 插件详情页和安装、更新前的预览会显示插件的 README（按控制台语言选择）；来自 git 的插件显示仓库链接。官方插件都有中英文 README。
- 控制台支持英文：默认跟随浏览器语言，可以在"全局设置"里切换，也可以用 `?lang=` 指定。
- 全局设置 › 模型配置可以编辑固定版本 Pi 的原生 `models.json`，添加自定义 Provider 或覆盖内置 Provider。`apiKey` 和 `headers` 只接受 `$NAME` 形式的环境变量引用，真实凭证仍在项目的模型账号里配置。

### 插件与工具

- 插件清单的 `mcpServers` 支持 streamable HTTP 条目（`http://` 或 `https://` 地址）。
- 插件的 `displayName`、`description` 可以按语言分别写，控制台按当前语言显示。
- Agent 在插件的线索里回复时可以带选项提问，插件用 SDK 的 `api.channels.answer` 作答；插件拿到的 `config.projects` 只包含启用了它的项目。
- mysql、gcloud 两个 CLI 工具支持 Linux/arm64。
- 新增 Docker Compose 一体化运行，端口只绑定本机回环地址。

### 从 v0.1.0 升级

v0.1.0 的数据在 PostgreSQL 里，v0.2.0 要一次性导入到 SQLite。导入只读旧库、不改动它，需要回退时旧版本仍可以用旧库运行（切换后产生的数据不会回到旧库）。

1. 导入要求旧库已经迁移到 PostgreSQL 版的最后一个迁移，而 v0.1.0 还差两个迁移。先检出 tag `pg-final`（PostgreSQL 版的最后一个提交），从源码构建，用原来的配置启动一次，迁移完成后停掉服务。
2. 用 v0.2.0 的 `loop-server`：让 `LOOP_NOW_DATA_DIR` 指向一个还没有数据库的数据目录（默认的 `~/.loop_now` 里只要还没有 `db/` 就可以），把 `LOOP_NOW_DATABASE_URL` 设为旧库，运行一次 `loop-server import-postgres`。它在一个一致的快照里复制全部数据，核对行数、外键与完整性，并打印每张表的行数；任何一步失败都不会留下新库。
3. 保持原来的 `LOOP_NOW_WORK_ROOT`，在这个数据目录上启动 v0.2.0。之后不再需要 Redis。

### 不兼容的变化

- 不再使用 PostgreSQL 和 Redis。`LOOP_NOW_REDIS_URL`、`LOOP_NOW_REDIS_KEY_PREFIX`、`LOOP_NOW_LOCK_TTL`、`LOOP_NOW_LOCK_RENEW_INTERVAL`、`LOOP_NOW_RUN_TEXT_TTL` 已删除，设置了也不再读取；`LOOP_NOW_DATABASE_URL` 只在 `loop-server import-postgres` 导入时读取。
- 健康接口 `GET /api/v1/health` 改为 `{ok, database}`：去掉了 `postgres`、`redis` 字段，`database` 里带数据库状态、WAL 大小和告警。`loopctl health` 也改为显示数据库状态。
- 一个数据目录、一个工作根同时只能由一个服务端使用。启动失败的退出码固定为：2 配置错误，3 数据目录或工作根被另一个服务端占用，4 监听地址被占用，其他错误为 1。
- 进行中的回复只保存在内存里：正常停止服务时，回复以已经生成的内容结束并标为中断；服务端崩溃时这部分内容会丢失（以前保存在 Redis 里）。
- 生命周期事件、工具调用事件和审计日志只保留最近 90 天，每天清理一次。
- 新安装创建的默认组织和默认项目都叫 `default`（原来是"默认组织""默认项目"），已有数据不受影响。
