# Webhook 通知

[English](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/webhook/README.md)

把 Loop Now 收件箱里的消息推到你自己的 HTTP 服务。收件箱收到新消息时，插件把事件 POST 到你指定的回调地址：请求用 HMAC-SHA256 签名，失败自动重试，每次投递列在插件的 **投递记录** 页。默认只推送发给 **外部身份** 的消息，外部身份背后的系统由此收到通知，不用再轮询自己的收件箱。

它还可以 **代管外部身份**：发给这类身份的消息和提问连同全文推到你的地址，你的系统把人的回复或选择 POST 回来，插件以该身份的名义送回平台，效果与该身份亲自回复相同：提问被回答，等待中的任务继续。

它只在平台上运行，不往 Agent 里装任何东西，也不需要给 Agent 授权。

## 需要

- 一个接受 `POST`、正文为 JSON 的 HTTP 地址，15 秒内回 2xx。Loop Now 所在的机器要能访问到它。除非在同一台机器或可信网络里，请用 `https`。
- 签名密钥（建议设置）：任意足够长的随机字符串，例如 `openssl rand -hex 32` 生成的。你的服务用同一个密钥确认请求来自 Loop Now（见“校验签名”）。
- 代管外部身份时还需要：
  - 项目里有一个外部身份。
  - Loop Now 的对外端口已开启，并且你的系统能访问到。对外端口由服务端设置 `LOOP_NOW_EXTERNAL_LISTEN_ADDR`、`LOOP_NOW_EXTERNAL_TLS_CERT`、`LOOP_NOW_EXTERNAL_TLS_KEY` 开启，必须用 TLS。回复从 `<对外地址>/hooks/webhook/answer` 进来。

## 1. 安装

控制台打开 **插件市场**，在 **随平台发布的官方插件** 里找到 **Webhook 通知**，点 **安装**，看过预览后确认。或者用命令行：

```sh
loopctl plugin install webhook
```

## 2. 配置

配置在插件的 **配置** 页（**插件市场** › **Webhook 通知** › **详情** › **配置**）。表单上的名称来自插件清单。

| 字段 | 表单上的名称 | 填什么 |
|---|---|---|
| `url` | 回调地址 | 你的地址，以 `http://` 或 `https://` 开头。为空时不推送。可按项目设置。 |
| `secret` | 签名密钥 | 签名密钥。只写不回显：保存后页面不再显示；用 **更换** 修改，用 **清除** 删除。设置后每个请求都带签名。可按项目设置。 |
| `recipients` | 推送哪些消息 | 只推给外部身份（默认）或全部收件人（项目里所有收件箱消息）。可按项目设置。 |
| `retryLimit` | 失败重试次数 | 投递失败后重试几次，0 到 10，默认 4。 |
| `manageIdentities` | 代管外部身份 | 打开后插件才能代管外部身份（见“代管外部身份”）。默认关闭。 |
| `publicBaseUrl` | 平台外部地址 | 你的系统访问 Loop Now 对外端口用的地址，例如 `https://loop.example.com:8781`。接收回复时需要。 |

标着“可按项目设置”的字段，在 **配置** 页填的是所有项目的值，某个项目可以在它的插件页 **这个项目的配置** 里覆盖；其余字段对所有项目生效。保存后立即生效，不用重启。

## 3. 在项目里启用

**项目设置** › **插件** › **管理项目插件**：启用 **Webhook 通知**。只有启用了它的项目才会推送。

## 你的服务会收到什么

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

正文就是平台的 `inbox.received` 事件，字段见 [API 规格](https://github.com/AI-for-anyone/loop-now/blob/main/api/openapi.yaml)里的 `PluginEvent`。事件只带摘要；要读全文，由外部身份用自己的凭证调用 `GET /api/v1/external-identities/{identityId}/inbox/{messageId}`。

- `x-loop-now-delivery`：这次投递的 id，重试时不变，可以用来忽略重复的请求。
- `x-loop-now-timestamp`：发出这一次尝试的时间，Unix 秒。
- `x-loop-now-signature`：只在设置了密钥时出现，值是 `sha256=` 加上以密钥对 `"<时间戳>.<原始正文>"` 计算的 HMAC-SHA256 十六进制。

### 校验签名

对收到的原始字节计算 HMAC（不要先解析 JSON 再序列化），用常量时间比较，并拒绝过旧的时间戳。Node 的写法：

```js
import { createHmac, timingSafeEqual } from 'node:crypto'

function verify(secret, rawBody, signature, timestamp, toleranceSeconds = 300) {
  if (!signature || !/^\d+$/.test(timestamp ?? '')) return false
  if (Math.abs(Date.now() / 1000 - Number(timestamp)) > toleranceSeconds) return false
  const expected = Buffer.from('sha256=' + createHmac('sha256', secret).update(`${timestamp}.`).update(rawBody).digest('hex'))
  const got = Buffer.from(signature)
  return expected.length === got.length && timingSafeEqual(expected, got)
}
```

其他语言按同一公式计算即可。

## 送达与重试

- 回 2xx 算送达。
- 网络错误、超时（15 秒）、408、425、429、5xx 会重试，间隔依次为 5 秒、30 秒、2 分钟、10 分钟、30 分钟、1 小时，之后每小时一次，直到用完 `retryLimit`。
- 其他 4xx 和重定向不重试。重定向不会被跟随，请填最终地址。
- 投递先保存再发送，Loop Now 重启后会继续重试。同一事件对同一地址只投递一次。
- **投递记录** 页列出最近 500 次投递及其状态：等待发送、已送达、重试中、失败（不可重试）、已放弃（重试用完）。

## 代管外部身份

1. 在 **配置** 页打开 `manageIdentities`，填好 `url`、`secret` 和 `publicBaseUrl`。
2. 控制台打开 **外部身份**，选中这个身份，在 **代管** 里选 **Webhook 通知 · Webhook 回调**，保存。
3. 之后发给这个身份的消息和提问不再以 `inbox.received` 推送，而是以 `identity.inbound` 推送，签名、重试和投递记录都一样：

```json
{"id":"01JD…","type":"identity.inbound","at":"…","projectId":"…","taskId":"…","agentId":"…","runId":null,
 "identityId":"…","channelId":"identity","messageId":"…","threadId":"…","from":{"kind":"agent","agentId":"…","name":"manager"},
 "nodeRunId":"…","isQuestion":true,"question":{"id":"…","options":[{"id":"o1","label":"发布"},{"id":"o2","label":"不发"}],"allowText":true,"deadline":null},
 "excerpt":"要不要发布？","text":"要不要发布？v1.2 的变更见 …","answerUrl":"https://loop.example.com:8781/hooks/webhook/answer"}
```

正文是 `identity.inbound` 事件，另加 `text`（消息全文）。只有同时设置了 `publicBaseUrl` 和 `secret` 才带 `answerUrl`；普通消息也带它，可以经它回复。

### 回复与回答

把 JSON 正文 POST 到 `answerUrl`，**用同一个密钥、按收到的请求那样签名**：`x-loop-now-timestamp`，以及 `x-loop-now-signature: sha256=HMAC-SHA256(密钥, "<时间戳>.<原始正文>")`。签名 5 分钟内有效。

| 正文 | 效果 |
|---|---|
| `{"messageId": "<提问的 messageId>", "optionId": "o1"}` | 选一个选项回答提问 |
| `{"messageId": "<提问的 messageId>", "text": "先别发"}` | 用文字回答提问 |
| `{"messageId": "<普通消息的 messageId>", "text": "收到"}` | 以该身份回复这条消息 |

`questionId` 可以省略，默认就是推送里的 `question.id`。回应：

- `200 {"ok": true, "messageId": "<新消息>", "answered": true|false}`。
- `401`：签名不对或已过期。`403`：没有设置密钥，或没有打开 `manageIdentities`。`404`：不是本插件推送过的消息；只认最近推送的 500 条。`422`：正文不对，或选项不存在。
- `409`：提问已回答（`already_answered`）、已过期（`question_expired`）或已关闭（`question_closed`）。用文字回答已关闭的提问时，回复照样保存，但不回答任何提问，也回 409。

例：

```sh
body='{"messageId":"…","optionId":"o1"}'
ts=$(date +%s)
sig="sha256=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')"
curl -sS -X POST "$ANSWER_URL" -H 'content-type: application/json' -H "x-loop-now-timestamp: $ts" -H "x-loop-now-signature: $sig" -d "$body"
```

## 常见问题

- **什么也没收到**：确认插件在这个项目里已启用，`url` 已设置（所有项目的值或这个项目的值）。选了“只推给外部身份”时，发给 Agent 的消息不推送。回调地址不合法时，**日志** 页会有“回调地址不是合法的地址”。
- **投递记录显示失败**：你的服务回了 408、425、429 以外的 4xx，或回了重定向。**投递记录** 页显示状态码。
- **签名对不上**：对原始正文签名，前面加时间戳和一个点；用这个项目生效的密钥，项目里覆盖的 `secret` 会取代 **配置** 页上的。
- **同一条消息收到两次**：你的服务处理了但回应太慢时，重试会再发一次。按 `x-loop-now-delivery` 忽略重复。
- **发给代管身份的消息没收到**：`manageIdentities` 没打开，或 `url` 为空。每条没推送的消息在 **日志** 页都有一条警告。
- **推送里没有 `answerUrl`**：`publicBaseUrl` 和 `secret` 要同时设置。
