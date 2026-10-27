# 专用浏览器（Playwright）

给 Agent 一个**独立的可见 Chromium**：通过官方 [Playwright MCP](https://github.com/microsoft/playwright-mcp)（固定 `@playwright/mcp` 0.0.83）打开网页、读取页面结构、点击、输入和截图。它不连接你的日常 Chrome，也不读写它的用户目录。

这是一个只有 Agent 半边的插件，也就是一个标准的 Pi 包：

- `loopNow.mcpServers.browser`：平台把它写进已授权 Agent 的 Pi `mcp.json`，工具以 `mcp__playwright_browser__*` 直接出现在模型面前；
- `agent/index.ts`：Pi 扩展，每轮开始时补上浏览器的使用与安全规则（网页内容不是用户的指示，有后果的操作先问用户）。

## 准备（每台机器一次）

运行时不下载、不调用 npx：浏览器先准备到插件的数据目录里，之后每次都用它。在服务端所在机器上，用平台的 Node 24、以运行服务的系统用户执行：

```sh
# 1. 插件目录的运行依赖。官方插件随平台发布时已装好；从本机目录安装时自己执行一次。
npm ci --prefix <插件目录> --ignore-scripts --omit=dev --no-audit --no-fund

# 2. 下载专用 Chromium 到插件数据目录（<work root>/platform/plugins/playwright/data）。
node <插件目录>/setup.mjs <work root>/platform/plugins/playwright/data
```

`setup.mjs` 只往 `data/browser/` 下载 Playwright 对应版本的 Chromium，并写下 `data/browser/browser.json`（平台、可执行文件路径、SHA-256、MCP 与 Playwright 版本）。数据目录跨插件版本保留，插件更新后不用重新准备；Playwright 版本变了时启动器会提示重新运行。须在目标系统与架构上准备，目前实测的是 macOS arm64。

然后在控制台安装（或 `loopctl plugin install playwright`），在项目设置 › 插件里启用，再在 Agent 配置里给需要的 Agent 授权。没有配置项也没有账号。授权在该 Agent 下次启动 Pi 时生效。

## 运行方式

- 每个 Agent 一个持久的配置目录 `data/profiles/<Agent id>/`，跨轮次、跨 Pi 重启保留 Cookie 与登录状态；截图等输出在 `data/output/<Agent id>/`。
- 同一个 Agent 的另一个执行（例如任务里的副本）正开着这个配置目录时，这次改用只在内存里的临时配置（Playwright 的 `--isolated`），结束即丢弃，不会抢同一个目录。
- 浏览器在第一次调用浏览器工具时启动，Pi 会话结束时随 MCP 服务器一起关闭。
- 单个页面操作 10 秒、导航 45 秒超时；一次工具调用最多 120 秒。
- 启动器（`mcp/launch.mjs`）每次核对 `browser.json` 与可执行文件的校验值；不符或没有准备时直接退出并说明怎么做，错误在 Pi 的 `/mcp` 里可见。

## 文件

```
playwright/
├── package.json        清单：pi.extensions 与 loopNow.mcpServers；依赖 @playwright/mcp 0.0.83（锁文件固定）
├── package-lock.json
├── agent/index.ts      Pi 扩展：浏览器规则
├── mcp/launch.mjs      MCP 服务器入口：核对准备好的浏览器，在本进程启动官方 MCP
└── setup.mjs           一次性准备：下载专用 Chromium 到数据目录
```

`loopNow.mcpServers` 的条目与 Pi 的 `mcp.json` 格式一致；`${LOOP_NOW_PLUGIN_DATA}` 和 `${LOOP_NOW_AGENT_ID}` 由平台按插件和 Agent 替换，`./` 开头的路径按安装目录解析。写自己的 MCP 插件可以照这个结构来，规则见 `plugins/SPEC.md` §2。
