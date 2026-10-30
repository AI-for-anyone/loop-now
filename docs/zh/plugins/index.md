# 插件体系概览

**新工具与项目特有的行为都放进插件。** 一个插件就是一个目录，`package.json` 是它唯一的清单。插件最多分两半，两半都可以没有：

- **Agent 半边**是标准的 [Pi 包](https://pi.dev/packages)：Pi 扩展、Skills、MCP 服务器和 CLI。每个获得授权的 Agent 在自己的 Pi 进程里加载它。现成的 Pi 包不用改就能装。
- **平台半边**运行在服务端旁边一个常驻的 Node 插件宿主里，订阅平台事件，并用按权限签发的插件令牌调用平台接口。

## 宿主提供什么

| 宿主提供 | 对插件意味着什么 |
| --- | --- |
| **事件** | 事件目录里的全部观察类事件：执行轮次、任务、节点、收件箱、提问、sleep、Agent、Pi 进程、上下文文件夹、插件自身。事件先持久化，处理函数返回后才确认，失败重投，重启后续投。拦截、修改、认领类 Hook 各有超时和超时策略。 |
| **状态 + 密钥** | 按插件持久化状态，可分全局或按项目。标记 `x-secret` 的配置字段在控制台只写不回显，插件日志中打码。 |
| **配置表单** | `loopNow.configSchema` 是普通的 JSON Schema，控制台据此渲染表单。标记 `x-scope: project` 的字段可按项目覆盖。 |
| **可靠的对外 HTTP** | SDK 的投递库带持久化队列、退避重试、HMAC-SHA256 签名，并写投递记录，插件页面可查看。 |
| **日志与隔离** | 每个插件的输出落在自己的日志里。抛错、超时或加载失败的插件只影响它自己。 |

清单说了算：订阅未声明的事件、调用未声明的接口都会失败；安装前会向用户展示插件的两半内容、事件和权限。

## 安装一次，按项目启用，按 Agent 授权

可以从官方插件、Git 仓库、npm 包或本地目录安装，在控制台或用 `loopctl plugin install` 都行。然后在项目中启用插件并填写配置，再把它授权给需要加载 Agent 半边的 Agent。

安装或启用插件本身不会给 Agent 授权。版本是钉死的：更新是显式动作，进行中的任务保留启动时的文件。

平台附带三个官方插件：

- [**webhook**](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/webhook/README.md)：把 `inbox.received` 事件带签名 POST 到你的地址，带重试和投递日志。外部身份用它代替轮询收件箱。
- [**playwright**](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/playwright/README.md)：给 Agent 一个专用的可见 Chromium，通过官方 Playwright MCP 打开网页、读取页面、点击、输入和截图，不连接你的日常 Chrome。
- [**hello**](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/hello/README.md)：同时具备两半的最小插件，是编写自己插件的起点。

## 几行代码写一个平台半边

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

宿主像 Pi 一样直接加载 TypeScript，并自己提供 `@loop-now/plugin-sdk`。用 `node plugins/host/dist/main.js validate <dir> --load` 检查一个目录，它会报出清单错误，以及代码在 `loopNow.events` 之外声明的订阅。

## 信任模型

没有沙箱：插件会执行代码，所以只安装你信任的来源。信任来自来源分级（官方 → 市场审核 → 任意 git/npm/本地）、安装前的两半与权限预览，以及全局一键停用。

## 深入阅读

- [规范](/plugins/spec)（英文）——完整契约：包格式、事件目录、Hook 种类与超时、权限和安装。
- [发布到插件市场](/plugins/publish)（英文）——把插件提交进官方市场索引。
- [SDK 参考](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/sdk/README.md)与仓库中的官方插件。
- [插件宿主协议](https://github.com/AI-for-anyone/loop-now/blob/main/api/plugin-host-protocol.md)——服务端与宿主之间的线路协议。
