// Webhook 通知：订阅 inbox.received，把事件 POST 到配置的回调地址。
// 投递交给 SDK 的投递库：先存进插件状态再回 ack，所以宿主或服务端重启不会丢；失败按退避重试，
// 每次投递在 deliveries/<ULID> 留一条记录，插件页可以看到。
//
// 代管外部身份（plugins/SPEC.md §12）：身份交给本插件的 identity 渠道后，发给它的消息与提问以 identity.inbound 送到这里，
// 同样 POST 到回调地址，并附 answerUrl；对方把回复或回答签名后 POST 到 /hooks/webhook/answer，插件以该身份送回平台。

import { createDelivery, type InboxMessage, type PluginAPI, SIGNATURE_HEADER, TIMESTAMP_HEADER, verifySignature } from '@loop-now/plugin-sdk'

interface WebhookConfig {
  url?: string
  recipients?: 'external' | 'all'
  retryLimit?: number
  manageIdentities?: boolean
  publicBaseUrl?: string
}

/** 推送过的代管身份消息：回答入口靠它知道以哪个身份、按哪个项目的密钥验签。 */
interface PushedMessage {
  identityId: string
  projectId: string | null
  questionId: string | null
  at: string
}

const PUSHED = 'identity-messages/'
const PUSHED_KEEP = 500

interface AnswerBody {
  messageId?: unknown
  questionId?: unknown
  optionId?: unknown
  text?: unknown
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json; charset=utf-8' } })
}

function fail(status: number, code: string, message: string): Response {
  return json(status, { ok: false, error: { code, message } })
}

export default function register(api: PluginAPI) {
  const delivery = createDelivery(api, { userAgent: `loop-now-webhook/${api.version}` })

  // 回调地址有误时重投也没用：记一条错误，确认这个事件。
  const target = (config: WebhookConfig): string | null => {
    if (!config.url) return null
    if (!URL.canParse(config.url)) {
      api.log.error(`回调地址不是合法的地址：${config.url}`)
      return null
    }
    return config.url
  }

  // 只留最近推送的若干条；更早的消息不再接受经回答入口回复。
  const prunePushed = async () => {
    const all = await api.state.list<PushedMessage>(PUSHED)
    if (all.length <= PUSHED_KEEP) return
    const oldest = all.sort((a, b) => a.value.at.localeCompare(b.value.at)).slice(0, all.length - PUSHED_KEEP)
    for (const { key } of oldest) await api.state.delete(key)
  }

  api.on('inbox.received', async (event) => {
    const config = api.config.get<WebhookConfig>(event.projectId)
    const url = target(config)
    if (!url) return
    if ((config.recipients ?? 'external') === 'external' && event.to.kind !== 'external') return
    const signed = api.secrets.get('secret', event.projectId) !== undefined
    // 同一事件重复送达时，投递库按 eventId 与目标去重，不会发两次。
    await delivery.deliver({
      target: url,
      method: 'POST',
      body: event,
      eventId: event.id,
      eventType: event.type,
      signing: signed ? { secretField: 'secret', projectId: event.projectId } : undefined,
      maxAttempts: (config.retryLimit ?? 4) + 1,
    })
  })

  api.channels.register('identity', {
    identityInbound: async (event) => {
      const config = api.config.get<WebhookConfig>(event.projectId)
      if (!api.config.get<WebhookConfig>().manageIdentities) {
        api.log.warn(`外部身份 ${event.identityId} 交给了本插件代管，但没有打开“代管外部身份”，消息 ${event.messageId} 没有推送`)
        return
      }
      const url = target(config)
      if (!url) {
        api.log.warn(`没有设置回调地址，代管身份 ${event.identityId} 的消息 ${event.messageId} 没有推送`)
        return
      }
      const signed = api.secrets.get('secret', event.projectId) !== undefined
      const base = api.config.get<WebhookConfig>().publicBaseUrl?.replace(/\/+$/, '')
      // 回答入口只接受签名的请求，所以没有密钥时不给 answerUrl。
      const answerUrl = base && signed ? `${base}/hooks/${api.id}/answer` : undefined
      // 事件只带摘要；给人看的推送附上全文（读失败时抛错，平台重投）。
      const full = await api.identities.read(event.identityId, event.messageId)
      const pushed: PushedMessage = { identityId: event.identityId, projectId: event.projectId, questionId: event.question?.id ?? null, at: event.at }
      await api.state.set(PUSHED + event.messageId, pushed)
      await prunePushed()
      await delivery.deliver({
        target: url,
        method: 'POST',
        body: { ...event, text: full.text, ...(answerUrl ? { answerUrl } : {}) },
        eventId: event.id,
        eventType: event.type,
        signing: signed ? { secretField: 'secret', projectId: event.projectId } : undefined,
        maxAttempts: (config.retryLimit ?? 4) + 1,
      })
    },
  })

  // 回答入口：POST /hooks/webhook/answer，正文 { messageId, questionId?, optionId? | text }，用推送时的同一密钥签名。
  api.http.handle(async (request) => {
    const path = new URL(request.url).pathname
    if (path !== '/answer') return fail(404, 'not_found', `没有 ${path}`)
    if (request.method !== 'POST') return fail(405, 'method_not_allowed', '只接受 POST')
    if (!api.config.get<WebhookConfig>().manageIdentities) return fail(403, 'forbidden', '没有打开“代管外部身份”')
    const raw = await request.text()
    let body: AnswerBody
    try {
      body = JSON.parse(raw) as AnswerBody
    } catch {
      return fail(400, 'bad_request', '正文须是 JSON')
    }
    const str = (v: unknown) => (typeof v === 'string' && v !== '' ? v : undefined)
    const messageId = str(body?.messageId)
    const questionId = str(body?.questionId)
    const optionId = str(body?.optionId)
    const text = str(body?.text)
    if (!messageId) return fail(422, 'validation_failed', '缺少 messageId')
    if ((optionId === undefined) === (text === undefined)) return fail(422, 'validation_failed', 'optionId 与 text 须给出其中一个')
    const pushed = await api.state.get<PushedMessage>(PUSHED + messageId)
    if (!pushed) return fail(404, 'not_found', '这条消息不是本插件推送的，或已过期')
    const secret = api.secrets.get('secret', pushed.projectId)
    if (!secret) return fail(403, 'forbidden', '没有设置签名密钥，不接受回答')
    if (!verifySignature({ secret, body: raw, signature: request.headers.get(SIGNATURE_HEADER), timestamp: request.headers.get(TIMESTAMP_HEADER) })) {
      return fail(401, 'bad_signature', '签名不对或已过期')
    }
    const question = questionId ?? pushed.questionId
    let result: InboxMessage
    try {
      if (optionId !== undefined) {
        if (!question) return fail(422, 'validation_failed', '这条消息不是提问，不能选选项')
        result = await api.identities.answer(pushed.identityId, question, { optionId })
      } else if (question) {
        result = await api.identities.answer(pushed.identityId, question, { text: text! })
      } else {
        result = await api.identities.reply(pushed.identityId, messageId, { text: text! })
      }
    } catch (err) {
      const e = err as Error & { status?: number; code?: string | null }
      const status = e.status && [403, 404, 409, 413, 422].includes(e.status) ? e.status : 502
      return fail(status, e.code ?? 'platform_error', e.message)
    }
    // 提问已关闭时文字回答会存成普通回复、不回答任何提问。
    if (question && !result.answerTo) {
      return json(409, { ok: false, messageId: result.id, error: { code: 'question_closed', message: '回复已保存，但提问已关闭或已过期，没有被回答' } })
    }
    return json(200, { ok: true, messageId: result.id, answered: Boolean(result.answerTo) })
  })
}
