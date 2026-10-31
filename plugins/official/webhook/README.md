# Webhook notifications

[中文](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/webhook/README.zh-CN.md)

Sends Loop Now Inbox messages to your own HTTP endpoint. When a new message arrives in an Inbox, the plugin POSTs the event to a callback URL you choose. Requests are signed with HMAC-SHA256, retried when they fail, and listed on the plugin's **Deliveries** tab. By default only messages addressed to **external identities** are sent, so the system behind an external identity is notified instead of polling its Inbox.

It can also **manage external identities**: messages and questions sent to such an identity are pushed to your URL with their full text, and your system POSTs people's replies and choices back. The plugin delivers them as that identity, with the same effect as the identity replying itself: questions get answered and waiting tasks continue.

It runs only on the platform. Nothing is installed into Agents, and there is nothing to grant to them.

## Requirements

- An HTTP endpoint that accepts `POST` with a JSON body and answers with a 2xx status within 15 seconds. It must be reachable from the machine running Loop Now. Use `https` unless the endpoint is on the same machine or a trusted network.
- A signing secret, recommended: any long random string, for example from `openssl rand -hex 32`. Your endpoint uses the same secret to check that requests come from Loop Now (see **Checking the signature**).
- To manage external identities, also:
  - An external identity in the project.
  - Loop Now's external port, switched on and reachable from your system. It is enabled by the server settings `LOOP_NOW_EXTERNAL_LISTEN_ADDR`, `LOOP_NOW_EXTERNAL_TLS_CERT` and `LOOP_NOW_EXTERNAL_TLS_KEY`, and always uses TLS. Replies come in at `<external address>/hooks/webhook/answer`.

## 1. Install

In the console, open **Plugin market**. Under **Official plugins that ship with the platform**, click **Install** on **Webhook notifications**, check the preview, and confirm. Or with the CLI:

```sh
loopctl plugin install webhook
```

## 2. Configure

Settings are on the plugin's **Config** tab (**Plugin market** › **Webhook notifications** › **Details** › **Config**). The form labels come from the plugin's manifest and are in Chinese.

| Field | Label in the form | What to enter |
|---|---|---|
| `url` | 回调地址 | Your endpoint, starting with `http://` or `https://`. When empty, nothing is sent. Per project. |
| `secret` | 签名密钥 | The signing secret. It is write-only: the page never shows it again. Use **Replace** to change it and **Clear** to remove it. When set, every request is signed. Per project. |
| `recipients` | 推送哪些消息 | 只推给外部身份 (only messages to external identities, the default) or 全部收件人 (every Inbox message in the project). Per project. |
| `retryLimit` | 失败重试次数 | How many times a failed delivery is retried, 0 to 10. Default 4. |
| `manageIdentities` | 代管外部身份 | Turn on to let the plugin manage external identities (see **Managing an external identity**). Off by default. |
| `publicBaseUrl` | 平台外部地址 | Loop Now's external address as your system reaches it, for example `https://loop.example.com:8781`. Needed for replies. |

Fields marked "per project" are set on the **Config** tab for all projects and can be overridden for one project on its plugin page, under **This project's config**. The others apply to all projects. Saved changes take effect at once; nothing needs restarting.

## 3. Enable it in a project

**Project settings** › **Plugins** › **Manage project plugins**: enable **Webhook notifications**. Only projects where it is enabled send anything.

## What your endpoint receives

```http
POST <url>
content-type: application/json
user-agent: loop-now-webhook/1.0.0
x-loop-now-delivery: 01JD3Q4V1W0G8F7K2M9N5P6R7S
x-loop-now-timestamp: 1730000000
x-loop-now-signature: sha256=5d1c…

{"id":"01JD3Q…","type":"inbox.received","at":"2026-10-27T08:00:00Z","projectId":"…","agentId":null,"taskId":null,"runId":null,
 "messageId":"…","threadId":"…","from":{"kind":"agent","id":"…","address":"…"},"to":{"kind":"external","id":"…","address":"@bot"},
 "isQuestion":false,"excerpt":"first 80 characters of the message"}
```

The body is the platform's `inbox.received` event; its fields are described under `PluginEvent` in the [API specification](https://github.com/AI-for-anyone/loop-now/blob/main/api/openapi.yaml). It carries only an excerpt. To read the whole message, the external identity calls `GET /api/v1/external-identities/{identityId}/inbox/{messageId}` with its own credentials.

- `x-loop-now-delivery`: the ID of this delivery. It stays the same across retries, so use it to ignore duplicates.
- `x-loop-now-timestamp`: when this attempt was sent, in Unix seconds.
- `x-loop-now-signature`: present only when a secret is set. It is `sha256=` followed by the hex HMAC-SHA256 of `"<timestamp>.<raw body>"`, keyed with the secret.

### Checking the signature

Compute the HMAC over the raw bytes you received, not over JSON you parsed and serialized again, compare it in constant time, and reject old timestamps. In Node:

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

Other languages follow the same formula.

## Delivery and retries

- A 2xx response counts as delivered.
- Network errors, timeouts (15 seconds), 408, 425, 429 and 5xx are retried after 5 seconds, 30 seconds, 2 minutes, 10 minutes, 30 minutes and 1 hour, then every hour, until `retryLimit` is used up.
- Other 4xx responses and redirects are not retried. Redirects are not followed, so give the final URL.
- A delivery is saved before it is sent, so retries continue after Loop Now restarts. Each event is sent to each URL only once.
- The **Deliveries** tab lists the latest 500 deliveries with their state: Pending, Delivered, Retrying, Failed (not retryable) or Abandoned (retries used up).

## Managing an external identity

1. On the **Config** tab, turn on `manageIdentities` and set `url`, `secret` and `publicBaseUrl`.
2. In the console, open **External identities**, pick the identity, and under **Managed by** choose **Webhook notifications · Webhook 回调**. Save.
3. From then on, messages and questions sent to that identity no longer come as `inbox.received`. They come as `identity.inbound`, signed, retried and listed the same way:

```json
{"id":"01JD…","type":"identity.inbound","at":"…","projectId":"…","taskId":"…","agentId":"…","runId":null,
 "identityId":"…","channelId":"identity","messageId":"…","threadId":"…","from":{"kind":"agent","agentId":"…","name":"manager"},
 "nodeRunId":"…","isQuestion":true,"question":{"id":"…","options":[{"id":"o1","label":"Release"},{"id":"o2","label":"Hold"}],"allowText":true,"deadline":null},
 "excerpt":"Release it?","text":"Release it? The changes in v1.2 are …","answerUrl":"https://loop.example.com:8781/hooks/webhook/answer"}
```

The body is the `identity.inbound` event plus `text`, the full message. `answerUrl` is included only when both `publicBaseUrl` and `secret` are set. Ordinary messages carry it too, so they can be replied to.

### Sending replies and answers

POST a JSON body to `answerUrl`, **signed with the same secret in the same way** as the requests you receive: `x-loop-now-timestamp`, and `x-loop-now-signature: sha256=HMAC-SHA256(secret, "<timestamp>.<raw body>")`. Signatures are valid for 5 minutes.

| Body | Effect |
|---|---|
| `{"messageId": "<the question's messageId>", "optionId": "o1"}` | Answers the question by choosing an option |
| `{"messageId": "<the question's messageId>", "text": "Not yet"}` | Answers the question in words |
| `{"messageId": "<an ordinary message's messageId>", "text": "Got it"}` | Replies to the message as the identity |

`questionId` can be left out; it defaults to the `question.id` that was pushed. Responses:

- `200 {"ok": true, "messageId": "<the new message>", "answered": true|false}`.
- `401`: the signature is wrong or expired. `403`: no secret is set, or `manageIdentities` is off. `404`: the message was not pushed by this plugin; only the latest 500 pushed messages are accepted. `422`: the body is malformed or the option does not exist.
- `409`: the question was already answered (`already_answered`), has expired (`question_expired`) or is closed (`question_closed`). A text answer to a closed question is still saved as a reply, but it answers nothing and also gets 409.

For example:

```sh
body='{"messageId":"…","optionId":"o1"}'
ts=$(date +%s)
sig="sha256=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')"
curl -sS -X POST "$ANSWER_URL" -H 'content-type: application/json' -H "x-loop-now-timestamp: $ts" -H "x-loop-now-signature: $sig" -d "$body"
```

## Troubleshooting

- **Nothing arrives:** check that the plugin is enabled in the project and that `url` is set, for all projects or for this one. With 只推给外部身份 (only external identities), messages to Agents are not sent. The **Logs** tab says so when the URL is not valid (`回调地址不是合法的地址`).
- **Deliveries show Failed:** your endpoint answered with a 4xx status other than 408, 425 or 429, or with a redirect. The **Deliveries** tab shows the status code.
- **The signature does not match:** sign the raw body, with the timestamp and a dot in front, and use the secret that applies to this project. A project override of `secret` replaces the one on the **Config** tab.
- **The same message arrives twice:** a retry can follow a delivery your endpoint processed but answered too late. Ignore repeats of the same `x-loop-now-delivery`.
- **Messages to a managed identity do not arrive:** `manageIdentities` is off, or `url` is empty. The **Logs** tab has a warning for each message that was not sent.
- **There is no `answerUrl`:** set both `publicBaseUrl` and `secret`.
