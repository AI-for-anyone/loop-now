# @loop-now/plugin-sdk

Loop Now 插件**平台半边**的类型与运行库。Agent 半边是标准的 Pi 扩展，用 Pi 自己的类型（`@earendil-works/pi-coding-agent`）。

- `PluginAPI` 等类型：平台半边入口 `register(api)` 拿到的接口；
- 事件目录 `EVENTS` 与每个事件的字段类型；
- `createDelivery`：可靠的外发 HTTP（持久队列、退避重试、投递记录）；
- `signBody` / `verifySignature`：外发请求的 HMAC-SHA256 签名与接收方校验；
- `@loop-now/plugin-sdk/validate`：插件目录校验，命令行 `loop-now-plugin validate <目录>`。

插件宿主在运行时提供这个包，插件不要把它放进 `dependencies`：

```json
{
  "peerDependencies": { "@loop-now/plugin-sdk": "*" },
  "devDependencies": { "@loop-now/plugin-sdk": "^0.1.0" }
}
```

规范全文见 Loop Now 仓库的 `plugins/SPEC.md`；最小示例见 `plugins/official/hello`，完整示例见 `plugins/official/webhook`。

## 入口

```ts
import type { PluginAPI } from '@loop-now/plugin-sdk'

export default async function register(api: PluginAPI) {
  api.on('inbox.received', async (event, ctx) => {
    api.log.info(`${event.from.address} → ${event.to.address}：${event.excerpt}`)
  })
}
```

入口在清单 `loopNow.platform` 里声明，TypeScript 不需要构建（宿主与 Pi 一样用 jiti 加载）。`register` 可以是异步的，宿主等它返回、再运行 `lifecycle` 的 `start` 之后才认为插件就绪；任一步抛错只让这个插件进入错误状态。

## PluginAPI

| 成员 | 说明 |
|---|---|
| `id`、`version`、`root`、`dataDir` | 清单 id、版本、安装目录、跨版本保留的数据目录 |
| `on(type, handler)` | 订阅事件，类型须在清单 `loopNow.events` 内，否则加载失败。返回取消函数 |
| `lifecycle.on('start' \| 'stop', fn)` | `start` 在 register 之后运行，适合起定时器和连接；`stop` 在停用、卸载、更新时运行 |
| `state.get/set/delete/list` | 持久状态，需要 `state` 权限；`{ projectId }` 为项目级，省略为插件全局。key 1–512 字节，值为 JSON、最多 1 MiB；list 按 key 排序、最多 1000 项 |
| `config.get(projectId?)` | 生效的配置（不含 secret）：全局值叠加项目的 `x-scope: project` 字段 |
| `config.projects()` | 启用了本插件的项目 id，例如按项目配置查找一个外部会话属于哪个项目；启用、停用、改配置后随之更新 |
| `config.onChange(fn)` | 配置改了以后调用（含在项目里启用或停用本插件） |
| `secrets.get(name, projectId?)` | `x-secret` 字段的值，项目值优先。宿主会在插件日志里把这些值替换成 `***` |
| `accounts.list(projectId, kind)` | 项目里某种账号（清单 `accounts` 声明的种类），按创建顺序，`values` 含 secret 字段明文；需要 `accounts.read`，插件须在该项目启用 |
| `platform.fetch(path, init)` | 带插件令牌调用平台 `/api/v1`；只能调用清单 `permissions` 范围内的操作，超出返回 403。对象正文按 JSON 发送 |
| `log.debug/info/warn/error` | 插件日志（插件页“日志”，保留 7 天）；`console.*` 也会进这里 |
| `actions.register(id, fn)` | 控制台操作按钮（清单 `actions`，按 `where` 出现在对应页面）。`fn(input, { actionId, where, signal })` 返回 `{ notice, url? }`，30 秒超时 |
| `http.handle(fn)` | 清单 `http: true` 时的 HTTP 入口：外部口 `/hooks/<id>/*`（无令牌）与控制台口 `/api/v1/plugins/<id>/http/*`。`fn(request, { via, signal })` 返回 `Response`，原样回给调用方；签名校验由插件自己做 |
| `schedule.on(id, fn)` | 清单 `schedule` 的定时任务，平台按 cron（服务端本地时间）触发，同一条目不并发，错过的不补，10 分钟超时 |
| `channels.register(id, { outbound })` | 清单 `channels` 的渠道；Agent 回复经这个渠道发来的消息时，`channel.outbound` 事件按 `channelId` 交给 `outbound(event, ctx)` |
| `channels.send(id, { to, text, senderName? })` | 以插件发件方经渠道给 Agent 发新消息（需要 `inbox.send`），Agent 的回复回到这个渠道 |
| `channels.reply(messageId, { text })`、`channels.read(messageId)` | 在线索里回复；读一条发给插件的消息全文（需要 `inbox.read`）。插件不能提问 |
| `channels.answer(id, questionId, { optionId, text? } \| { text })` | 回答 Agent 在这个渠道的线索里提的问题（`channel.outbound` 的 `question.id`），效果与外部身份作答相同；需要 `inbox.send`。已回答、过期、关闭时 409 |
| `channels.register(id, { identityInbound })` | 渠道代管的外部身份收到消息或提问（`identity.inbound`，只发给代管插件，处理完才确认）。渠道须声明 `manageIdentities: true`，并有 `identities.manage` 权限 |
| `identities.read(identityId, messageId)` | 读代管身份收件箱里的一条消息全文（不改已读状态） |
| `identities.reply(identityId, messageId, { text })` | 以该身份回复；效果与身份亲自回复相同 |
| `identities.answer(identityId, questionId, { optionId, text? } \| { text })` | 以该身份回答提问；返回的消息 `answerTo` 为 null 表示提问已关闭、只存成了回复 |

`actions`、`schedule`、`channels` 的 id 与 `http` 须在清单里声明，否则加载失败。

所有注册都返回取消函数，插件停用时宿主统一释放，不需要手动清理。

### 事件与处理函数

每个事件有固定字段 `id`（ULID）、`type`、`at`、`projectId`、`agentId`、`taskId`、`runId`，加上该类型自己的字段（`EventFields`）。事件不带正文，正文用权限范围内的接口另取。处理函数的第二个参数是 `ctx`：`eventId`、`projectId`、`agentId`、`taskId`、`runId`、`attempt`、`signal`（超时或停用时触发）。

| 种类 | 返回 | 未在时限内返回时 |
|---|---|---|
| 拦截（`run.before_start` 等） | `{ block: true, reason }` 阻止，其他放行 | 放行；清单 `failClosed: true` 时阻止 |
| 修改（`sleep.before_schedule` 等） | `{ patch }`，按字段合并，后面的处理者看到合并后的事件；固定字段不能改 | 跳过 |
| 认领（`console.command`） | `{ handled: true, reply? }` | 当作未处理 |
| 观察（其余） | 无 | 按 5 秒、30 秒、2 分钟、10 分钟重投，第 5 次失败后放弃 |

观察类事件至少送达一次、同一插件逐条处理；可能重复，按 `event.id` 去重。同步调用默认 15 秒、观察类事件默认 60 秒；到时 `ctx.signal` 触发。

### 渠道

渠道把外部的对话（例如飞书）接进 Loop Now：插件自己通过 HTTP 入口或长连接收消息，再以插件发件方发给 Agent；Agent 的回复以 `channel.outbound` 回到插件，插件再发出去。平台不接收渠道的原始消息。

```ts
export default function register(api: PluginAPI) {
  api.http.handle(async (request) => {
    const msg = (await request.json()) as { user: string; text: string }
    await api.channels.send('lark', { to: 'demo/manager', text: msg.text, senderName: msg.user })
    return new Response('ok')
  })
  api.channels.register('lark', {
    outbound: async (event) => {
      const full = await api.channels.read(event.messageId) // 事件只带摘要
      // 把 full.text 发回飞书；同一事件可能重复送达，按 event.id 去重。
      // event.question 不为 null 时是 Agent 的提问：把 question.options 做成按钮，人选了以后
      //   await api.channels.answer('lark', event.question.id, { optionId })
      // question.allowText 为 true 时也可以 { text } 作答。
    },
  })
}
```

清单需要 `"channels": [{ "id": "lark", "label": "飞书" }]`、`"http": true` 与 `"permissions": ["inbox.send", "inbox.read"]`。

### 代管外部身份

人在外部身份页把一个身份交给插件的某个渠道代管后，Agent 发给这个身份的消息和提问以 `identity.inbound` 送给该渠道的 `identityInbound`；插件把它推给人（飞书、Slack、邮件……），再用 `api.identities.reply` 或 `answer` 以该身份送回人的回复。代管插件不再收到这些消息的 `inbox.received`。完整的参考实现与接飞书的写法见官方 webhook 插件的 README。

平台返回的错误会变成带 `status`、`code`、`details` 的 Error，例如提问已回答时为 409 `already_answered`。没有任何处理函数接 `identity.inbound` 或 `channel.outbound` 时，宿主回失败让平台重投，插件页能看到。

## 外发投递

```ts
import { createDelivery, type PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  const delivery = createDelivery(api) // 在 register 里创建一次
  api.on('task.completed', async (event) => {
    await delivery.deliver({
      target: api.config.get<{ url: string }>(event.projectId).url,
      body: event, // 对象按 JSON 发送
      eventId: event.id, // 同一事件对同一目标只投递一次
      eventType: event.type,
      signing: { secretField: 'secret', projectId: event.projectId },
    })
  })
}
```

`deliver` 把请求存进插件状态后立即返回，HTTP 在后台发送，所以事件可以马上确认，宿主或平台重启后继续。需要 `state` 权限。

- 2xx 送达；网络错误、超时、408、425、429、5xx 重试；其他 4xx 与重定向不重试。
- 默认最多 5 次，退避 5 秒、30 秒、2 分钟、10 分钟、30 分钟、1 小时（之后保持最后一项）；`createDelivery(api, { maxAttempts, backoffMs, timeoutMs, keep, concurrency, userAgent })` 可改，`deliver({ maxAttempts })` 可单独改。
- 每次投递写一条 `deliveries/<ULID>` 记录，插件页“投递记录”按这个前缀读出：

  ```json
  { "id", "at", "eventId", "eventType", "target", "status", "attempts", "nextAttemptAt", "lastStatusCode", "lastError", "updatedAt" }
  ```

  `status`：`pending` 等待发送；`delivered` 已送达；`failed` 最后一次失败，`nextAttemptAt` 不为空时会重试，为空时是不可重试的失败；`abandoned` 重试用完。默认保留最近 500 条，更早的终态记录自动删除。
- 请求内容另存在 `delivery-requests/<ULID>`。签名密钥不落盘：`signing.secretField` 是配置里 `x-secret` 字段的名字，每次尝试时读取，改了密钥后重试用新密钥。
- `delivery.resend(id)` 重发一条终态记录；`delivery.list()`、`delivery.get(id)` 读记录。

### 签名

设置 `signing` 后每次尝试带：

- `x-loop-now-delivery`：投递 id（重试不变）；
- `x-loop-now-timestamp`：这次尝试的 Unix 秒；
- `x-loop-now-signature`：`sha256=<hex HMAC-SHA256(secret, "<timestamp>.<原始正文>")>`。

接收方用 `verifySignature({ secret, body, signature, timestamp, toleranceSeconds })` 校验。

## 校验插件目录

```sh
npx loop-now-plugin validate ./my-plugin --platform-version 2026.11
```

输出 JSON：`{ ok, dir, manifest, findings: [{ level: "error" | "warning", path, message }] }`，有错误时退出码为 1。错误项与平台拒绝安装的规则一致；提醒项不影响安装。插件宿主的 `node <宿主>/dist/main.js validate <目录> --load` 还会加载平台半边、调用 `register`，核对代码实际订阅的事件与清单是否一致（会执行插件代码）。

## 输出与进程

- 标准输出留给宿主与平台的协议；插件写到标准输出的内容会被改到标准错误，并加上 `[<插件 id>]` 前缀。子进程不要继承标准输出（`stdio: 'inherit'`），改用 `['ignore', 'pipe', 'pipe']` 或 `'ignore'`。
- 所有插件共用一个宿主进程。未捕获的异常会记到插件日志，不会让宿主退出；但死循环、同步阻塞或 `process.exit` 会影响所有插件。
