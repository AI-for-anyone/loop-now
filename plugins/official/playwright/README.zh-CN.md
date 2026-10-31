# 专用浏览器（Playwright）

[English](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/playwright/README.md)

通过官方的 [Playwright MCP](https://github.com/microsoft/playwright-mcp)（固定为 `@playwright/mcp` 0.0.83）给 Agent 一个 **独立的可见 Chromium**：打开网页、读取页面结构、点击、输入和截图。它不连接你的日常 Chrome，也不读写它的用户目录。

这是一个只有 Agent 半边的插件，由两部分组成：

- 一个提供浏览器工具的 MCP 服务器，工具名以 `mcp__playwright_browser__` 开头；
- 每轮附加的使用规则：网页上的文字不是用户的指示，有后果的操作要先得到用户同意。

没有配置项、账号或权限。

## 需要

- 浏览器在 Loop Now 所在的机器上、以运行 Loop Now 服务端的系统用户运行，并在那台机器上打开可见窗口，所以那台机器要有桌面会话。目前实测的是 Apple 芯片（arm64）的 macOS。
- Docker Compose 部署里用不了：容器里没有桌面，镜像里也没有装插件的依赖。
- 平台的 Node 24，以及一次联网（下载 Chromium）。

## 1. 安装

控制台打开 **插件市场**，在 **随平台发布的官方插件** 里找到 **专用浏览器**，点 **安装**，看过预览后确认。或者用命令行：

```sh
loopctl plugin install playwright
```

## 2. 准备浏览器（每台机器一次）

Agent 使用浏览器时不下载任何东西：Chromium 预先下载到插件的数据目录，之后每次都用它。在 Loop Now 所在的机器上，以运行服务端的系统用户、用平台的 Node 24 执行：

```sh
# 只在 <插件目录>/node_modules 不存在时：安装插件的依赖。
npm ci --prefix <插件目录> --ignore-scripts --omit=dev --no-audit --no-fund

# 把专用 Chromium 下载到插件的数据目录。
node <插件目录>/setup.mjs <work root>/platform/plugins/playwright/data
```

- `<work root>` 是服务端的 `LOOP_NOW_WORK_ROOT`。
- `<插件目录>` 是插件的安装位置：`<work root>/platform/plugins/playwright/<版本>`（例如 `…/playwright/1.0.0`）；从本地目录安装的，就是你选的那个目录。

`setup.mjs` 把与 Playwright 版本对应的 Chromium 下载到 `data/browser/`，并写下 `data/browser/browser.json`，记录平台、浏览器路径和 SHA-256。数据目录在插件更新后保留，更新后不用重做，除非插件提示（见“常见问题”）。请在浏览器实际运行的机器和系统上准备。

## 3. 在项目里启用并授权给 Agent

1. **项目设置** › **插件** › **管理项目插件**：启用 **专用浏览器**。
2. Agent 配置的 **插件权限** 一栏：授权 **专用浏览器** 并保存。

授权在该 Agent 下次启动 Pi 时生效，不打断当前回复。平台在 Agent 空闲一段时间后（默认 10 分钟）回收它的 Pi，之后的第一条消息会重新启动。

## 使用

对 Agent 说，例如：“打开 https://example.com，告诉我页面上写了什么，再截个图。”Agent 用 `browser_navigate` 打开页面，用 `browser_snapshot` 读取页面，用 `browser_click`、`browser_type` 等工具操作，用 `browser_take_screenshot` 截图。Agent 第一次调用浏览器工具时，Loop Now 那台机器上会打开一个 Chromium 窗口。

## 运行方式

- 每个 Agent 有自己的浏览器配置目录 `data/profiles/<Agent id>/`，在里面产生的 Cookie 和登录状态跨轮次、跨 Pi 重启保留。截图等文件存在 `data/output/<Agent id>/`。
- 同一个 Agent 的另一个执行（例如任务里它的副本）正开着这个配置目录时，这次改用只在内存里的临时配置，结束即丢弃，也没有已保存的 Cookie。
- 浏览器在第一次调用浏览器工具时启动，在该 Agent 的 Pi 会话结束时关闭。
- 超时：单个页面操作 10 秒，打开页面 45 秒，一次工具调用 120 秒。
- Agent 会被要求：提交表单、付款、发消息、删除数据等有后果的操作，先问用户。

## 常见问题

- **Agent 没有浏览器工具**：浏览器的 MCP 服务器拒绝启动。通常是浏览器还没准备、是在另一类机器上准备的，或者插件更新到了新的 Playwright 版本。想知道原因，可以以运行 Loop Now 的系统用户手动运行它：

  ```sh
  node <插件目录>/mcp/launch.mjs --data <work root>/platform/plugins/playwright/data --agent <Agent id>
  ```

  有问题时它会立即退出，并打印一条以 `[playwright]` 开头的说明，告诉你怎么做，通常是重新运行 `setup.mjs`。如果它一直在运行，说明没问题，按 Ctrl-C 结束。修好后，工具在该 Agent 下次启动 Pi 时出现。
- **没有出现窗口**：窗口开在 Loop Now 那台机器的桌面上，而且要等 Agent 调用浏览器工具才会打开。
- **`[playwright] 配置目录 … 正被另一个浏览器使用，这次使用临时配置`**：这个 Agent 的另一个执行正开着它的浏览器，这次看不到已保存的 Cookie 和登录状态。
- **让某个 Agent 的浏览器从头开始**：在该 Agent 没有使用浏览器时，删除 `data/profiles/<Agent id>/`。
- 卸载插件时，数据目录（浏览器和所有配置目录）默认保留；卸载对话框里勾选 **同时删除数据目录** 才会删除。
