# 配置项

服务端的全部选项与默认值定义在 [`internal/server/config/config.go`](https://github.com/AI-for-anyone/loop-now/blob/main/internal/server/config/config.go)。服务端读取进程环境变量，不会自动加载 `.env` 文件。

## 常用配置

| 变量 | 用途 / 默认值 |
| --- | --- |
| `LOOP_NOW_LISTEN_ADDR` | 控制台 API；`127.0.0.1:8780` |
| `LOOP_NOW_WORK_ROOT` | 持久的 Agent 工作区与运行文件 |
| `LOOP_NOW_CONTEXT_DIR` | 共享的项目（`projects/<id>/`）与全局（`global/`）上下文文件夹；默认为服务端用户的 `~/.loop_now` |
| `LOOP_NOW_PLUGIN_HOST_ENTRY` | 插件宿主入口的绝对路径，由 Node 运行；不设置则插件平台半边不运行 |
| `LOOP_NOW_PLUGIN_OFFICIAL_DIR` | 随平台发布的官方插件目录（每个插件一个目录）；不设置则没有 `builtin` 安装来源 |
| `LOOP_NOW_PLUGIN_MARKET_URL` | 官方插件市场 `loop-now-market.json` 的 `https` 地址；默认为官方市场索引，可指向其他地址 |
| `LOOP_NOW_PI_MAX_PROCESSES` | `0` 表示不限；正数限制本地 Pi 进程数量 |
| `LOOP_NOW_PI_IDLE_TIMEOUT` | Pi 进程空闲回收超时；`10m` |
| `LOOP_NOW_CONSOLE_ORIGINS` | 允许的浏览器来源；默认本地 5173 端口 |
| `LOOP_NOW_LARK_USER_BUNDLE_DIR` | 可选的已验证飞书用户 CLI 包 |

## 源码安装必需

| 变量 | 用途 |
| --- | --- |
| `LOOP_NOW_DATABASE_URL` | PostgreSQL 连接串；凭证以明文保存在该数据库中 |
| `LOOP_NOW_REDIS_URL` | Redis 连接串；请使用专用数据库并设置 `LOOP_NOW_REDIS_KEY_PREFIX` |
| `LOOP_NOW_NODE_BIN` | 用于运行插件宿主的 Node 二进制路径 |
| `LOOP_NOW_PI_BIN` | Pi 编码 Agent 二进制路径 |
| `LOOP_NOW_LOOPCTL_DIST_DIR` | 存放配套版本 `loopctl` 发行包的目录 |

## 安全边界

当前版本面向**可信的个人使用**：

- 控制台没有身份验证。请保持本机回环访问，不要暴露到公共网络。
- Agent 以服务端的操作系统用户身份执行本地命令。任务目录相互独立，但不是操作系统级的安全沙箱。
- 插件会执行代码，只安装你信任的来源。模型与工具访问同样取决于你提供的凭证与权限。
- 对话历史是持久的，但服务端重启不保证执行不间断、也不自动重放在途工作。建议在 Agent 空闲时升级。
