# 配置项

服务端的全部选项与默认值定义在 [`internal/server/config/config.go`](https://github.com/AI-for-anyone/loop-now/blob/main/internal/server/config/config.go)。服务端读取进程环境变量，不会自动加载 `.env` 文件。

## 常用配置

| 变量 | 用途 / 默认值 |
| --- | --- |
| `LOOP_NOW_LISTEN_ADDR` | 控制台 API；`127.0.0.1:8780` |
| `LOOP_NOW_DATA_DIR` | 数据库（`db/`）与每日备份（`backups/`）；默认为服务端用户的 `~/.loop_now` |
| `LOOP_NOW_WORK_ROOT` | 持久的 Agent 工作区与运行文件；和数据目录一样，同时只给一个服务端实例（由 `.loop-now-server.lock` 锁定） |
| `LOOP_NOW_CONTEXT_DIR` | 共享的项目（`projects/<id>/`）与全局（`global/`）上下文文件夹；默认为服务端用户的 `~/.loop_now` |
| `LOOP_NOW_PLUGIN_HOST_ENTRY` | 插件宿主入口的绝对路径，由 Node 运行；不设置则插件的平台侧不运行 |
| `LOOP_NOW_PLUGIN_OFFICIAL_DIR` | 随平台发布的官方插件目录（每个插件一个目录）；不设置则没有 `builtin` 安装来源 |
| `LOOP_NOW_PLUGIN_MARKET_URL` | 官方插件市场 `loop-now-market.json` 的 `https` 地址；默认为官方市场索引，可指向其他地址 |
| `LOOP_NOW_PI_MAX_PROCESSES` | `0` 表示不限；正数限制本地 Pi 进程数量 |
| `LOOP_NOW_PI_IDLE_TIMEOUT` | Pi 进程空闲回收超时；`10m` |
| `LOOP_NOW_CONSOLE_ORIGINS` | 允许跨源调用 API 的浏览器来源（Vite 开发服务器）；默认本地 5173 端口，`none` 表示一个都不允许。服务端自己提供的控制台是同源，不需要列出 |
| `LOOP_NOW_TEXT_EDIT_MAX` | 控制台在 Agent 文件夹和上下文文件夹里能预览（包括图片）或在线编辑的最大文件；默认 `20MiB`。不能大于 `LOOP_NOW_MAX_FOLDER_UPLOAD`（默认 `1GiB`，写入这些文件夹的单个文件上限） |
| `LOOP_NOW_LARK_USER_BUNDLE_DIR` | 可选的已验证飞书用户 CLI 包 |
| `LOOP_NOW_RELAY_URL` | 账号和通道用的 Loop Now 中继（见[手机和其它设备](#phones-and-other-devices)）；默认 `https://relay.loop-now.app`。只有本机回环地址可以用 `http`，用于本地开发的中继 |
| `LOOP_NOW_CLOUDFLARED_PATH` | 通道要运行的 `cloudflared` 的绝对路径；设置后只用它。不设置时先在 `PATH` 上找，再下载固定版本 |

## 数据目录与备份

数据库是 SQLite：`data.db` 保存平台状态与凭证（明文），`events.db` 保存运行与审计日志。一个数据目录同时只能由一个服务端使用。服务端每天做一次检查点、清理 90 天前的运行与审计日志，并写一份备份。

| 变量 | 用途 / 默认值 |
| --- | --- |
| `LOOP_NOW_DATA_DIR` | 数据目录；默认为服务端用户的 `~/.loop_now` |
| `LOOP_NOW_BACKUP_DIR` | 每日备份的目录，每份一个以时刻命名的子目录；默认 `<数据目录>/backups` |
| `LOOP_NOW_BACKUP_KEEP` | 保留几份每日备份；默认 `7` |

`loop-server backup <目录>` 随时写一份备份，服务运行中也可以。`loop-server import-postgres` 用于一次性从基于 PostgreSQL 的版本迁移（见[快速上手](/zh/guide/getting-started#moving-from-a-postgresql-based-release)）。

## 源码安装必需

| 变量 | 用途 |
| --- | --- |
| `LOOP_NOW_NODE_BIN` | 用于运行插件宿主的 Node 二进制路径 |
| `LOOP_NOW_PI_BIN` | Pi 编码 Agent 二进制路径 |
| `LOOP_NOW_LOOPCTL_DIST_DIR` | 存放配套版本 `loopctl` 发行包的目录（发行包里默认取 `loop-server` 旁边的） |

## 发行包与桌面壳

发行包（`scripts/release/build.sh` 或 `release` 工作流产出）解开是一个目录：

| 路径 | 内容 |
| --- | --- |
| `loop-server` | 内嵌控制台的服务端，在自己的端口上于 `/` 提供控制台 |
| `loopctl-dist/` | 各平台的 `loopctl` |
| `plugins/host/main.js`、`plugins/official/` | 插件宿主与官方插件 |
| `runtime/node/` | Node 24.21.0（带 npm） |
| `runtime/pi/` | 用这个 Node 安装的 Pi 编码 Agent 1.4.0 |

`LOOP_NOW_NODE_BIN`、`LOOP_NOW_PI_BIN`、`LOOP_NOW_LOOPCTL_DIST_DIR`、`LOOP_NOW_PLUGIN_HOST_ENTRY`、`LOOP_NOW_PLUGIN_OFFICIAL_DIR` 没有设置、而 `loop-server` 旁边有对应文件时就用它们，机器上不必另装 Node 和 Pi。配置的 Node 所在目录排在 Agent 的 `PATH` 最前。

| 变量 | 用途 |
| --- | --- |
| `LOOP_NOW_WEB_DIR` | 从这个目录提供控制台，代替内嵌的 |
| `LOOP_NOW_READY_FILE` | 开始提供服务后把 `{"url","pid","version"}` 写到这个文件（退出时删除）；配合 `LOOP_NOW_LISTEN_ADDR=127.0.0.1:0` 得知端口。就绪检查：`GET <url>/healthz` |

`loop-server` 的退出码：`0` 正常退出，`1` 其他启动失败或退出过程出错，`2` 配置有误，`3` 数据目录或工作根正被另一个实例使用，`4` 监听地址已被占用。

## 手机和其它设备 {#phones-and-other-devices}

**全局设置 › 手机和其它设备**让手机或另一台电脑在任何地方使用同一个控制台：这台电脑登录 Loop Now 账号，拿到一个地址 `https://<编号>.loop-now.app`，并保持一条 Cloudflare 通道连到第二个监听，即“远程入口”。一个请求能做什么，由它从哪个监听进来决定，从不看请求头。

- **Loop Now 账号。** “登录”会在系统浏览器里打开 Loop Now 中继（`relay.loop-now.app`）的登录页；你在那里确认后，浏览器回到这台电脑一次，落在服务端为这次登录临时打开的本机端口上（PKCE、一次性代码）。中继发的凭证保存在 `<数据目录>/relay/host.json`，只有服务端的用户能读，不写进日志，也不显示。“退出登录”会让中继作废它并删除，同时关掉“允许从手机访问”；地址仍为这台电脑保留。
- **允许从手机访问（默认关闭）。** 打开（要先登录）会打开远程入口，在中继登记这台电脑一次（之后地址不再变），并运行 `cloudflared`：通道由这台电脑向外连出，不用设置路由器。服务端依次使用 `LOOP_NOW_CLOUDFLARED_PATH`、`PATH` 上 2026.10.3 及以上的 `cloudflared`，都没有时从 Cloudflare 的 GitHub 发布页下载固定版本到 `<数据目录>/tools/cloudflared` 并核对 SHA-256。`cloudflared` 退出或与 Cloudflare 断开两分钟时，服务端会重新开通道并重启它，接连很快失败时等得越来越久；开关开着时服务端启动也会这样做。关闭会停掉 `cloudflared`。“释放地址”会在中继删除通道和地址，之后再打开会拿到新地址。这台电脑要一直开着（关掉桌面应用的窗口它仍在运行，退出应用就停了）。
- **远程入口。** 在 `127.0.0.1` 上监听，端口由服务端选定并在重启后保持不变，也可以自己指定（`GET` / `PUT /api/v1/remote-access`）；通道转发到这里。关闭时会断开上面的所有连接，并关掉“允许从手机访问”。
- **配对。** “添加设备”生成一个只能用一次、10 分钟内有效的配对码，显示成二维码和链接 `<地址>/pair#<配对码>`（通道在线时是通道地址，否则是远程入口自己的地址）；配对码放在链接的 `#` 之后，不会作为路径或查询参数发给服务端，也不会写进日志。只有这台电脑能生成配对码。一分钟内错 10 次后，配对要等一分钟。
- **登录状态。** 配对过的设备可以使用控制台的全部功能。登录状态保存在 Cookie 里（HttpOnly、SameSite=Strict，HTTPS 下加 Secure），服务端只保存它的哈希。30 天没用过的设备要重新配对；移除设备会立刻结束它的登录，并断开它正在接收的实时更新。
- **远程入口拒绝的请求。** 没有设备登录的 API 请求，配对除外（控制台本身的页面文件谁都能取，配对页才能打开）；生成配对码、设备列表和远程入口设置，即使已经登录；`Origin` 不是远程入口自己的请求（它的本机地址，或中继给的 `https://<编号>.loop-now.app`；不看 `Host` 请求头；包括 `Origin: null`），以及没有 `Origin` 的修改；`Authorization` 请求头里的令牌；Agent 接口和插件 hooks。这里也不提供选择路径的对话框，因为它会弹在这台电脑的屏幕上。
- 本机控制台监听，以及外部身份和 `loopctl` 用的外部 TLS 端口，都不变。

## 安全边界

当前版本面向**可信的个人使用**：

- 控制台没有身份验证。请保持本机回环访问，不要暴露到公共网络。手机和其它设备通过上面的远程入口访问，需要先配对。
- Agent 以服务端的操作系统用户身份执行本地命令。任务目录相互独立，但不是操作系统级的安全沙箱。
- 插件会执行代码，只安装你信任的来源。模型与工具访问同样取决于你提供的凭证与权限。
- 对话历史是持久的，但服务端重启不保证执行不间断、也不自动重放在途工作。建议在 Agent 空闲时升级。
