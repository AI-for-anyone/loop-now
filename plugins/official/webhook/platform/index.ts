// Webhook notifications: subscribes to inbox.received and POSTs the event to the configured callback URL.
// Delivery goes through the SDK's delivery library: the event is saved to plugin state before the ack, so a host or server restart
// loses nothing; failures are retried with backoff,
// and each delivery leaves a record in deliveries/<ULID> that shows on the plugin page.
//
// Managing external identities (plugins/SPEC.md §12): once an identity is handed to this plugin's identity channel, messages and
// questions sent to it arrive here as identity.inbound and are likewise POSTed to the callback URL, with an answerUrl; the other side
// signs its reply or answer and POSTs it to /hooks/webhook/answer, and the plugin sends it back to the platform as that identity.

import { createDelivery, type InboxMessage, type PluginAPI, SIGNATURE_HEADER, TIMESTAMP_HEADER, verifySignature } from '@loop-now/plugin-sdk'

interface WebhookConfig {
  url?: string
  recipients?: 'external' | 'all'
  retryLimit?: number
  manageIdentities?: boolean
  publicBaseUrl?: string
}

/**
 * A message pushed for a managed identity: the answer endpoint uses it to know which identity to act as
 * and which project's secret to verify the signature with.
 */
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

  // Redelivering does not help when the callback URL is wrong: log an error and acknowledge the event.
  const target = (config: WebhookConfig): string | null => {
    if (!config.url) return null
    if (!URL.canParse(config.url)) {
      api.log.error(`回调地址不是合法的地址：${config.url}`)
      return null
    }
    return config.url
  }

  // Keep only the most recently pushed entries; older messages no longer accept replies through the answer endpoint.
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
    // When the same event arrives again, the delivery library deduplicates by eventId and target, so it is not sent twice.
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
      // The answer endpoint accepts only signed requests, so no answerUrl is given without a secret.
      const answerUrl = base && signed ? `${base}/hooks/${api.id}/answer` : undefined
      // The event carries only an excerpt; the push meant for people includes the full text (if reading fails, it throws and the platform
      // redelivers).
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

  // Answer endpoint: POST /hooks/webhook/answer with body { messageId, questionId?, optionId? | text }, signed with the same secret as the push.
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
    // When the question is closed, a text answer is saved as a plain reply and answers no question.
    if (question && !result.answerTo) {
      return json(409, { ok: false, messageId: result.id, error: { code: 'question_closed', message: '回复已保存，但提问已关闭或已过期，没有被回答' } })
    }
    return json(200, { ok: true, messageId: result.id, answered: Boolean(result.answerTo) })
  })
}
