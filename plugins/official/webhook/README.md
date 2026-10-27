# Webhook 通知

收件箱收到新消息时，把事件 POST 到你的回调地址。默认只推送发给**外部身份**的消息，用来代替外部身份轮询自己的收件箱。
还可以**代管外部身份**：发给该身份的消息与提问推到你的地址，你把人的回复或选择 POST 回来，插件以该身份的名义送回平台，效果与该身份亲自回复相同（提问被回答、等待中的任务继续）。

这是随平台发布的官方插件，也是平台半边的完整示例：订阅一个观察类事件，用 SDK 的投递库可靠地外发 HTTP。

## 配置

| 字段 | 说明 |
|---|---|
| `url` | 回调地址（http 或 https）。为空时不推送。可在项目里覆盖。 |
| `secret` | 签名密钥，只写不回显。设置后每个请求带签名头。可在项目里覆盖。 |
| `recipients` | 只推给外部身份（`external`，默认）或全部收件人（`all`）。可在项目里覆盖。 |
| `retryLimit` | 失败重试次数，0 到 10，默认 4。 |
| `manageIdentities` | 打开后才推送代管身份的消息、接受回答。默认关闭。 |
| `publicBaseUrl` | 平台外部 TLS 口的地址（例如 `https://loop.example.com:8781`），对方据此回调回答入口。 |

只有在项目里启用了插件，该项目的消息才会推送。

## 请求

```http
POST <url>
content-type: application/json
user-agent: loop-now-webhook/1.0.0
x-loop-now-delivery: 01JD3Q4V1W0G8F7K2M9N5P6R7S
x-loop-now-timestamp: 1730000000
x-loop-now-signature: sha256=5d1c…

{"id":"01JD3Q…","type":"inbox.received","at":"2026-10-27T08:00:00Z","projectId":"…","agentId":null,"taskId":null,"runId":null,
 "messageId":"…","threadId":"…","from":{"kind":"agent","id":"…","address":"…"},"to":{"kind":"external","id":"…","address":"@bot"},
 "isQuestion":false,"excerpt":"正文前 80 个字符"}
```

正文就是平台的 `inbox.received` 事件（字段见 `api/openapi.yaml` 的 `PluginEvent`）。事件不带完整正文；需要全文时，用外部身份自己的凭证读 `GET /external-identities/{identityId}/inbox/{messageId}`。

- `x-loop-now-delivery`：这次投递的 id；重试时不变，可用来去重。
- `x-loop-now-timestamp`：发出这一次尝试的 Unix 秒。
- `x-loop-now-signature`：`sha256=` 加 `HMAC-SHA256(secret, "<timestamp>.<原始正文>")` 的十六进制。只在设置了 `secret` 时出现。

### 校验签名

Node 里可以直接用 SDK：

```ts
import { verifySignature } from '@loop-now/plugin-sdk'

const ok = verifySignature({
  secret: process.env.WEBHOOK_SECRET!,
  body: rawBody, // 收到的原始字节，不要先解析再序列化
  signature: req.headers['x-loop-now-signature'],
  timestamp: req.headers['x-loop-now-timestamp'],
  toleranceSeconds: 300, // 拒绝 5 分钟以前的请求
})
```

其他语言按上面的公式计算并做常量时间比较即可。

## 代管外部身份

1. 打开 `manageIdentities`，填好 `url`、`secret` 与 `publicBaseUrl`。
2. 在控制台的外部身份页，把身份的“代管”设为“Webhook 通知 › Webhook 回调”。
3. 之后 Agent 发给这个身份的消息与提问不再出现在本插件的 `inbox.received` 推送里，而是作为 `identity.inbound` 事件推送（同样签名、重试、记投递记录）：

```json
{"id":"01JD…","type":"identity.inbound","at":"…","projectId":"…","taskId":"…","agentId":"…","runId":null,
 "identityId":"…","channelId":"identity","messageId":"…","threadId":"…","from":{"kind":"agent","agentId":"…","name":"manager"},
 "nodeRunId":"…","isQuestion":true,"question":{"id":"…","options":[{"id":"o1","label":"发布"},{"id":"o2","label":"不发"}],"allowText":true,"deadline":null},
 "excerpt":"要不要发布？","text":"要不要发布？v1.2 的变更见 …","answerUrl":"https://loop.example.com:8781/hooks/webhook/answer"}
```

正文是 `identity.inbound` 事件本体，另附 `text`（消息全文，插件以代管权限读取）。`answerUrl` 只在设置了 `publicBaseUrl` 与 `secret` 时出现；普通消息也带它，可以经它回复。

### 回答与回复

对方把结果 POST 到 `answerUrl`，正文是 JSON，**用同一个密钥按推送的方式签名**（`x-loop-now-timestamp` 与 `x-loop-now-signature: sha256=HMAC-SHA256(secret, "<timestamp>.<原始正文>")`，5 分钟内有效）：

| 正文 | 效果 |
|---|---|
| `{"messageId": "<提问的 messageId>", "optionId": "o1"}` | 选一个选项回答提问 |
| `{"messageId": "<提问的 messageId>", "text": "先别发"}` | 用文字回答提问 |
| `{"messageId": "<普通消息的 messageId>", "text": "收到"}` | 以该身份回复这条消息 |

`questionId` 可省略，默认就是推送里的 `question.id`。回应：

- `200 {"ok": true, "messageId": "<新消息>", "answered": true|false}`；
- `401` 签名不对或过期；`403` 没有设置密钥或没有打开代管；`404` 不是本插件推送过的消息（只认最近 500 条）；`422` 正文不对或选项不存在；
- `409`：提问已回答（`already_answered`）、已过期（`question_expired`）或已关闭（`question_closed`）。用文字回答已关闭的提问时，回复照样保存，但不回答任何提问，也回 409。

例：

```sh
body='{"messageId":"…","optionId":"o1"}'
ts=$(date +%s)
sig="sha256=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')"
curl -sS -X POST "$ANSWER_URL" -H 'content-type: application/json' -H "x-loop-now-timestamp: $ts" -H "x-loop-now-signature: $sig" -d "$body"
```

## 照这个插件接飞书、Slack 等渠道

webhook 把“推出去”和“收回来”都交给你自己的服务。直接接一个聊天工具时，结构相同，只是两头换成该工具的接口：

```ts
export default function register(api: PluginAPI) {
  // 1. 推出去：代管身份收到消息或提问时，发一条飞书消息（提问做成带按钮的卡片）。
  api.channels.register('lark', {
    identityInbound: async (event) => {
      if (await api.state.get(`sent/${event.id}`)) return // 事件可能重复送达，按 event.id 去重
      const full = await api.identities.read(event.identityId, event.messageId) // 事件只带摘要
      const chat = await chatFor(event.identityId) // 身份对应的飞书会话，存在插件配置或状态里
      const sent = await lark.send(chat, full.text, event.question?.options) // 飞书消息 id
      await api.state.set(`sent/${event.id}`, true)
      await api.state.set(`lark/${sent}`, { identityId: event.identityId, messageId: event.messageId, questionId: event.question?.id ?? null })
    },
  })
  // 2. 收回来：飞书的事件回调（按钮点击、回复消息）走插件的 HTTP 入口 /hooks/<id>/*。
  api.http.handle(async (request) => {
    if (!lark.verify(request)) return new Response('bad signature', { status: 401 }) // 用飞书自己的签名校验
    const { replyTo, optionId, text } = await lark.parse(request)
    const m = await api.state.get(`lark/${replyTo}`)
    if (!m) return new Response('unknown', { status: 404 })
    if (m.questionId) await api.identities.answer(m.identityId, m.questionId, optionId ? { optionId } : { text })
    else await api.identities.reply(m.identityId, m.messageId, { text })
    return new Response('ok')
  })
}
```

清单需要：

```json
"loopNow": {
  "platform": "./platform/index.ts",
  "permissions": ["state", "identities.manage"],
  "channels": [{ "id": "lark", "label": "飞书", "manageIdentities": true }],
  "http": true,
  "accounts": [{ "kind": "lark_bot", "label": "飞书机器人", "fields": { "type": "object", "properties": { "appId": { "type": "string" }, "appSecret": { "type": "string", "x-secret": true } } } }]
}
```

要点：

- 渠道声明 `manageIdentities: true` 并有 `identities.manage` 权限，人才能在外部身份页把身份交给它；一个身份只有一个代管者。
- `identity.inbound` 只发给代管插件，处理完才确认，失败按 5 秒、30 秒、2 分钟、10 分钟重投；处理函数要能承受重复送达。
- `api.identities.*` 只能操作本插件代管的身份；回复与回答对 Agent 和任务的效果与该身份亲自回复相同。
- 外部回调走平台的外部 TLS 口 `/hooks/<插件 id>/*`，不需要令牌，签名校验由插件自己做；控制台口另有 `/api/v1/plugins/<id>/http/*`。
- 凭证放在账号（`accounts`）或 `x-secret` 配置里，不要写进状态或日志。

## 送达与重试

- 回 2xx 算送达。
- 网络错误、超时（15 秒）、408、425、429、5xx 会重试，间隔依次为 5 秒、30 秒、2 分钟、10 分钟、30 分钟、1 小时（之后保持 1 小时），直到用完 `retryLimit`。
- 其他 4xx 与重定向不重试。
- 投递先存进插件状态再确认事件，宿主或平台重启后继续重试；同一事件对同一地址只投递一次。
- 插件页的“投递记录”列出最近 500 次投递：等待发送、已送达、重试中、失败（不可重试）、已放弃（重试用完）。

## 文件

```
webhook/
├── package.json        清单：loopNow 段声明事件、权限、代管身份的渠道、HTTP 入口与配置表单
└── platform/index.ts   平台半边：inbox.received 与 identity.inbound 的推送（投递库），回答入口 /answer
```

写自己的插件可以从这里改起；更小的两半示例见 [hello](../hello/README.md)，接口说明见 [SDK](../../sdk/README.md)。
