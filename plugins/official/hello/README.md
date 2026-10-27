# Hello 示例

最小的两半插件，兼作写插件的入门文档：

- **平台半边**（`platform/index.ts`）在插件宿主里运行，订阅 `run.ended`，每轮结束写一行插件日志。
- **Agent 半边**（`agent/index.ts`）是一个标准的 Pi 扩展，给已授权 Agent 的 Pi 加一个 `hello` 工具。

## 目录

```
hello/
├── package.json        唯一的清单：pi 段给 Pi，loopNow 段给平台
├── platform/index.ts   平台半边（可选）
└── agent/index.ts      Agent 半边（可选）
```

两半都可选：只有 `pi` 段的目录就是普通的 Pi 包，不改一字也能装进 Loop Now；只有 `loopNow.platform` 的插件只在平台上运行（例如 [webhook](../webhook/README.md)）。

## 清单

```json
{
  "name": "@loop-now/plugin-hello",
  "version": "1.0.0",
  "keywords": ["loop-now-plugin", "pi-package"],
  "peerDependencies": { "@loop-now/plugin-sdk": "*", "@earendil-works/pi-coding-agent": "*", "typebox": "*" },
  "pi": { "extensions": ["./agent/index.ts"] },
  "loopNow": {
    "id": "hello",
    "displayName": "Hello 示例",
    "platform": "./platform/index.ts",
    "events": ["run.ended"],
    "permissions": [],
    "configSchema": { "type": "object", "properties": { "greeting": { "type": "string", "default": "Hello", "x-scope": "project" } } }
  }
}
```

- `loopNow.id`：平台内唯一，小写字母、数字、连字符，安装后不可改。
- `loopNow.events`：平台半边订阅的事件。**清单说了算**：代码里订阅清单外的事件，加载时直接失败。
- `loopNow.permissions`：平台半边调用平台接口与读写状态需要的范围；hello 不需要任何权限。
- `loopNow.configSchema`：JSON Schema，平台据此生成配置表单。`x-secret: true` 的字段只写不回显，`x-scope: "project"` 的字段可在项目里覆盖。
- `peerDependencies`：`@loop-now/plugin-sdk` 由插件宿主提供，Pi 的包由 Pi 提供，都不要放进 `dependencies`。开发时把它们装在 `devDependencies` 里取类型。

完整规则见仓库里的 `plugins/SPEC.md` §2。

## 平台半边

```ts
import type { PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  api.on('run.ended', (event, ctx) => {
    api.log.info(`Agent ${ctx.agentId ?? '（平台）'} 的一轮结束：${event.status}`)
  })
}
```

- 入口默认导出 `register(api)`，可以是异步的；宿主等它返回才认为插件就绪。TypeScript 不需要先构建。
- `register` 里只做注册。定时器、连接之类放到 `api.lifecycle.on('start')`，在 `api.lifecycle.on('stop')` 里收尾。
- 所有注册都会在插件停用、卸载或更新时由宿主释放，不需要手动清理。
- 观察类事件（如 `run.ended`）至少送达一次：同一插件逐条处理，处理函数返回后才确认；抛错或超时会按 5 秒、30 秒、2 分钟、10 分钟重投。可能收到重复事件，需要时按 `event.id` 去重。
- `console.log` 和 `api.log.*` 都进插件日志（插件页“日志”）；不要往标准输出写东西，宿主会把它们改到标准错误。

## Agent 半边

```ts
import { defineTool, type ExtensionAPI } from '@earendil-works/pi-coding-agent'
import { Type } from 'typebox'

const helloTool = defineTool({
  name: 'hello',
  label: 'Hello',
  description: '向某人问好。',
  parameters: Type.Object({ name: Type.String({ description: '要问候的人' }) }),
  async execute(_id, params) {
    return { content: [{ type: 'text', text: `Hello, ${params.name}!` }], details: undefined }
  },
})

export default function (pi: ExtensionAPI) {
  pi.registerTool(helloTool)
}
```

这就是 Pi 自己的扩展 API，写法以 Pi 的 `docs/extensions.md` 为准。平台在启动 Agent 的 Pi 时加上 `-e <这个文件>`，并通过环境变量 `LOOP_NOW_PLUGIN_HELLO_CONFIG` 给出生效的配置（不含 secret 字段），所以 `greeting` 改了以后，Agent 下次启动 Pi 时生效。Agent 半边要调用平台时用 `loopctl`，权限就是这个 Agent 自己的权限。

## 试一试

```sh
# 校验目录（静态检查；加 --load 会加载平台半边，核对代码里的订阅）
node plugins/host/dist/main.js validate plugins/official/hello --load

# 用外部身份从本机目录安装（须是服务端所在机器上的绝对路径；非官方来源要 --force）
loopctl plugin install "$PWD/plugins/official/hello" --force
```

然后在控制台的项目设置 › 插件里启用它，再到 Agent 配置里给某个 Agent 授权。对话一轮之后，插件页的日志里会出现“一轮结束”的记录；让这个 Agent 调用 `hello` 工具即可看到 Agent 半边的效果。

官方插件随平台发布，也可以直接 `loopctl plugin install hello`。
