# 配置项

服务端的全部选项与默认值定义在 [`internal/server/config/config.go`](https://github.com/AI-for-anyone/loop-now/blob/main/internal/server/config/config.go)。服务端读取进程环境变量，不会自动加载 `.env` 文件。

## 常用配置

| 变量 | 用途 / 默认值 |
| --- | --- |
| `LOOP_NOW_LISTEN_ADDR` | 控制台 API；`127.0.0.1:8780` |
| `LOOP_NOW_DATA_DIR` | 数据库（`db/`）与每日备份（`backups/`）；默认为服务端用户的 `~/.loop_now` |
| `LOOP_NOW_WORK_ROOT` | 持久的 Agent 工作区与运行文件 |
| `LOOP_NOW_CONTEXT_DIR` | 共享的项目（`projects/<id>/`）与全局（`global/`）上下文文件夹；默认为服务端用户的 `~/.loop_now` |
| `LOOP_NOW_PLUGIN_HOST_ENTRY` | 插件宿主入口的绝对路径，由 Node 运行；不设置则插件平台半边不运行 |
| `LOOP_NOW_PLUGIN_OFFICIAL_DIR` | 随平台发布的官方插件目录（每个插件一个目录）；不设置则没有 `builtin` 安装来源 |
| `LOOP_NOW_PLUGIN_MARKET_URL` | 官方插件市场 `loop-now-market.json` 的 `https` 地址；默认为官方市场索引，可指向其他地址 |
| `LOOP_NOW_PI_MAX_PROCESSES` | `0` 表示不限；正数限制本地 Pi 进程数量 |
| `LOOP_NOW_PI_IDLE_TIMEOUT` | Pi 进程空闲回收超时；`10m` |
| `LOOP_NOW_CONSOLE_ORIGINS` | 允许跨源调用 API 的浏览器来源（Vite 开发服务器）；默认本地 5173 端口，`none` 表示一个都不允许。服务端自己提供的控制台是同源，不需要列出 |
| `LOOP_NOW_LARK_USER_BUNDLE_DIR` | 可选的已验证飞书用户 CLI 包 |

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

`loop-server` 的退出码：`0` 正常退出，`1` 其他启动失败或退出过程出错，`2` 配置有误，`3` 数据目录正被另一个实例使用，`4` 监听地址已被占用。

## 安全边界

当前版本面向**可信的个人使用**：

- 控制台没有身份验证。请保持本机回环访问，不要暴露到公共网络。
- Agent 以服务端的操作系统用户身份执行本地命令。任务目录相互独立，但不是操作系统级的安全沙箱。
- 插件会执行代码，只安装你信任的来源。模型与工具访问同样取决于你提供的凭证与权限。
- 对话历史是持久的，但服务端重启不保证执行不间断、也不自动重放在途工作。建议在 Agent 空闲时升级。
