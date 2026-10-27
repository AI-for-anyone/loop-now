// 冒烟测试：把官方插件 webhook、hello 从它们的目录加载进构建好的宿主，走一遍真实的事件；再跑 validate 子命令。

import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { createServer, type IncomingMessage } from 'node:http'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { after, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'

import { signBody, verifySignature } from '../../sdk/src/index.ts'
import { Fixtures, HOST_ENTRY, HostHarness, loadPayload, type PayloadOptions } from './harness.ts'

const official = fileURLToPath(new URL('../../official/', import.meta.url))
const fx = new Fixtures()
after(() => fx.cleanup())

// 与服务端一样按清单组装 load 载荷：事件、权限、渠道与 http 都取自 package.json 的 loopNow 段。
function officialPayload(id: string, config?: PayloadOptions['config'], apiUrl?: string) {
  const root = path.join(official, id)
  const m = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')).loopNow
  return loadPayload(id, root, path.join(root, m.platform), {
    events: m.events ?? [],
    permissions: m.permissions ?? [],
    manifest: { channels: m.channels ?? [], http: m.http === true },
    config,
    apiUrl,
  })
}

async function until<T>(what: string, fn: () => T | undefined | false, timeoutMs = 5_000): Promise<T> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    const v = fn()
    if (v) return v
    if (Date.now() > deadline) throw new Error(`等待${what}超时`)
    await new Promise((r) => setTimeout(r, 10))
  }
}

describe('官方插件 webhook', () => {
  it('收件箱消息签名后 POST 到回调地址；重复送达不重发；按 recipients 过滤', async () => {
    const received: { headers: IncomingMessage['headers']; body: string; url: string }[] = []
    const server = createServer(async (req, res) => {
      let body = ''
      for await (const chunk of req) body += chunk
      received.push({ headers: req.headers, body, url: req.url! })
      res.end('ok')
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const base = `http://127.0.0.1:${(server.address() as { port: number }).port}`
    const project = '6f1c8e5e-0000-4000-8000-0000000000aa'
    const other = '6f1c8e5e-0000-4000-8000-0000000000bb'
    const host = await HostHarness.start()
    try {
      const payload = officialPayload('webhook', {
        values: { url: `${base}/global`, retryLimit: 2 },
        secrets: { secret: 'whsec-global-0001' },
        projects: { [other]: { values: { url: `${base}/other`, recipients: 'all' }, secrets: {} } },
      })
      assert.deepEqual((await host.load(payload)).payload, { ok: true })
      const toExternal = {
        id: '01JWEBHOOK00000000000000001',
        type: 'inbox.received',
        projectId: project,
        messageId: 'm1',
        threadId: 't1',
        from: { kind: 'agent', id: 'a1', address: 'manager@demo' },
        to: { kind: 'external', id: 'x1', address: '@bot' },
        isQuestion: false,
        excerpt: 'hello there',
      }
      assert.deepEqual((await host.event('webhook', toExternal)).payload, { ok: true })
      const hit = await until('第一次 POST', () => received[0])
      assert.equal(hit.url, '/global')
      assert.equal(JSON.parse(hit.body).id, toExternal.id)
      assert.equal(JSON.parse(hit.body).excerpt, 'hello there')
      assert.match(String(hit.headers['user-agent']), /^loop-now-webhook\/1\.0\.0$/)
      assert.ok(
        verifySignature({
          secret: 'whsec-global-0001',
          body: hit.body,
          signature: hit.headers['x-loop-now-signature'] as string,
          timestamp: hit.headers['x-loop-now-timestamp'] as string,
        }),
      )
      const records = await until('投递记录', () => {
        const rs = [...host.stateOf('webhook').entries()].filter(([k]) => k.startsWith('deliveries/'))
        return rs.length === 1 && (rs[0]![1] as { status: string }).status === 'delivered' && rs
      })
      const [key, record] = records[0]! as [string, Record<string, unknown>]
      assert.equal(key, `deliveries/${record.id}`)
      assert.equal(hit.headers['x-loop-now-delivery'], record.id)
      assert.deepEqual([record.eventId, record.eventType, record.target, record.attempts], [toExternal.id, 'inbox.received', `${base}/global`, 1])

      // 至少送达一次：同一事件再来一次（第 2 次投递）不会再 POST。
      assert.deepEqual((await host.event('webhook', toExternal, { attempt: 2 })).payload, { ok: true })
      // 发给 Agent 的消息默认不推送；另一个项目配置了 all，推送到它自己的地址。
      await host.event('webhook', { ...toExternal, id: '01JWEBHOOK00000000000000002', to: { kind: 'agent', id: 'a2', address: 'dev@demo' } })
      await host.event('webhook', { ...toExternal, id: '01JWEBHOOK00000000000000003', projectId: other, to: { kind: 'agent', id: 'a3', address: 'qa@other' } })
      await until('第二次 POST', () => received.length >= 2)
      await new Promise((r) => setTimeout(r, 100))
      assert.deepEqual(
        received.map((r) => [r.url, JSON.parse(r.body).id, r.headers['x-loop-now-signature'] === undefined ? 'unsigned' : 'signed']),
        [
          ['/global', '01JWEBHOOK00000000000000001', 'signed'],
          ['/other', '01JWEBHOOK00000000000000003', 'signed'],
        ],
      )
      assert.ok(!JSON.stringify(host.messages).includes('whsec-global-0001'), '密钥不出现在协议消息里')
      assert.deepEqual((await host.request('unload', 'webhook', {})).payload, { ok: true })
    } finally {
      await host.close()
      server.closeAllConnections()
      server.close()
    }
  })

  it('没有配置回调地址时直接确认，不写投递记录', async () => {
    const host = await HostHarness.start()
    try {
      await host.load(officialPayload('webhook'))
      const ack = await host.event('webhook', { type: 'inbox.received', to: { kind: 'external', id: 'x', address: '@x' } })
      assert.deepEqual(ack.payload, { ok: true })
      assert.equal([...host.stateOf('webhook').keys()].filter((k) => k.startsWith('deliveries/')).length, 0)
    } finally {
      await host.close()
    }
  })
})

describe('官方插件 webhook 代管外部身份', () => {
  it('代管身份的提问推送时带 answerUrl；对方签名后 POST 到回答入口，插件以该身份回答或回复', async () => {
    const pushed: { body: string; headers: IncomingMessage['headers'] }[] = []
    const hook = createServer(async (req, res) => {
      let body = ''
      for await (const chunk of req) body += chunk
      pushed.push({ body, headers: req.headers })
      res.end('ok')
    })
    const calls: { method?: string; url?: string; body: Record<string, unknown> | null }[] = []
    const platform = createServer(async (req, res) => {
      let body = ''
      for await (const chunk of req) body += chunk
      const parsed = body ? (JSON.parse(body) as Record<string, unknown>) : null
      calls.push({ method: req.method, url: req.url, body: parsed })
      res.setHeader('content-type', 'application/json')
      if (req.method === 'GET') {
        res.end(JSON.stringify({ id: req.url!.split('/').pop(), threadId: 't1', text: `全文 ${req.url!.split('/').pop()}` }))
        return
      }
      if (parsed?.optionId === 'stale') {
        res.statusCode = 409
        res.end(JSON.stringify({ error: { code: 'already_answered', message: '这个提问已经回答过了' } }))
        return
      }
      res.statusCode = 201
      const answered = req.url?.includes('/q1/') && parsed?.text !== 'late'
      res.end(JSON.stringify({ id: `r${calls.filter((c) => c.method === 'POST').length}`, threadId: 't1', text: 'x', answerTo: answered ? { messageId: 'q1', optionId: parsed?.optionId ?? null, optionLabel: null } : null }))
    })
    await Promise.all([hook, platform].map((s) => new Promise<void>((resolve) => s.listen(0, '127.0.0.1', resolve))))
    const port = (s: typeof hook) => (s.address() as { port: number }).port
    const identity = '6f1c8e5e-0000-4000-8000-0000000000e2'
    const secret = 'whsec-identity-0001'
    const host = await HostHarness.start()
    try {
      const payload = officialPayload(
        'webhook',
        { values: { url: `http://127.0.0.1:${port(hook)}/in`, manageIdentities: true, publicBaseUrl: 'https://loop.example.com:8781/' }, secrets: { secret }, projects: {} },
        `http://127.0.0.1:${port(platform)}/api/v1`,
      )
      assert.deepEqual((await host.load(payload)).payload, { ok: true })
      const question = {
        type: 'identity.inbound', identityId: identity, channelId: 'identity', messageId: 'q1', threadId: 't1',
        from: { kind: 'agent', agentId: 'a1', name: 'manager' }, nodeRunId: 'n1', isQuestion: true,
        question: { id: 'q1', options: [{ id: 'yes', label: '发布' }, { id: 'no', label: '不发' }], allowText: true, deadline: null }, excerpt: '要不要发布？',
      }
      assert.deepEqual((await host.event('webhook', question)).payload, { ok: true })
      const first = await until('推送提问', () => pushed[0])
      const sent = JSON.parse(first.body)
      assert.equal(sent.answerUrl, 'https://loop.example.com:8781/hooks/webhook/answer')
      assert.deepEqual(sent.question.options, question.question.options)
      assert.equal(sent.text, '全文 q1', '推送附全文')
      assert.ok(verifySignature({ secret, body: first.body, signature: first.headers['x-loop-now-signature'] as string, timestamp: first.headers['x-loop-now-timestamp'] as string }))
      const plain = await host.event('webhook', { ...question, messageId: 'm2', isQuestion: false, question: null, excerpt: '发布完了告诉我' })
      assert.deepEqual(plain.payload, { ok: true })
      await until('推送消息', () => pushed[1])
      assert.equal(JSON.parse(pushed[1]!.body).answerUrl, 'https://loop.example.com:8781/hooks/webhook/answer', '普通消息也可以经回答入口回复')

      const answer = async (body: unknown, opts: { sign?: string; ts?: number } = {}) => {
        const raw = JSON.stringify(body)
        const ts = opts.ts ?? Math.floor(Date.now() / 1000)
        const r = await host.request('http', 'webhook', {
          via: 'external', method: 'POST', path: '/answer', query: '',
          headers: { 'content-type': ['application/json'], 'x-loop-now-timestamp': [String(ts)], 'x-loop-now-signature': [signBody(opts.sign ?? secret, ts, raw)] },
          body: Buffer.from(raw).toString('base64'), timeoutMs: 5000,
        })
        assert.equal(r.payload.ok, true, JSON.stringify(r.payload))
        return { status: r.payload.value.status as number, body: JSON.parse(Buffer.from(r.payload.value.body, 'base64').toString()) }
      }
      assert.deepEqual(await answer({ messageId: 'q1', optionId: 'yes' }), { status: 200, body: { ok: true, messageId: 'r1', answered: true } })
      assert.deepEqual(await answer({ messageId: 'm2', text: '好的' }), { status: 200, body: { ok: true, messageId: 'r2', answered: false } })
      assert.equal((await answer({ messageId: 'q1', text: 'late' })).status, 409, '提问已关闭时文字回答只存成回复')
      assert.deepEqual((await answer({ messageId: 'q1', optionId: 'stale' })).body.error.code, 'already_answered')
      assert.equal((await answer({ messageId: 'q1', optionId: 'yes' }, { sign: 'wrong-secret' })).status, 401)
      assert.equal((await answer({ messageId: 'q1', optionId: 'yes' }, { ts: Math.floor(Date.now() / 1000) - 3600 })).status, 401, '过旧的签名拒绝')
      assert.equal((await answer({ messageId: 'unknown', optionId: 'yes' })).status, 404)
      assert.equal((await answer({ messageId: 'q1', optionId: 'yes', text: 'both' })).status, 422)
      assert.deepEqual(
        calls.filter((c) => c.method === 'POST').map((c) => [c.method, c.url, c.body]),
        [
          ['POST', '/api/v1/inbox/messages/q1/reply', { asIdentityId: identity, optionId: 'yes' }],
          ['POST', '/api/v1/inbox/messages/m2/reply', { text: '好的', asIdentityId: identity }],
          ['POST', '/api/v1/inbox/messages/q1/reply', { asIdentityId: identity, text: 'late' }],
          ['POST', '/api/v1/inbox/messages/q1/reply', { asIdentityId: identity, optionId: 'stale' }],
        ],
      )
      assert.deepEqual(calls.filter((c) => c.method === 'GET').map((c) => c.url), [`/api/v1/external-identities/${identity}/inbox/q1`, `/api/v1/external-identities/${identity}/inbox/m2`])
      assert.ok(!JSON.stringify(host.messages).includes(secret))
    } finally {
      await host.close()
      for (const s of [hook, platform]) {
        s.closeAllConnections()
        s.close()
      }
    }
  })

  it('没有打开“代管外部身份”时不推送也不接受回答', async () => {
    const host = await HostHarness.start()
    try {
      await host.load(officialPayload('webhook', { values: { url: 'http://127.0.0.1:9/in' }, secrets: { secret: 'whsec-x-0001' }, projects: {} }))
      const ack = await host.event('webhook', { type: 'identity.inbound', identityId: 'i1', channelId: 'identity', messageId: 'm1', question: null })
      assert.deepEqual(ack.payload, { ok: true })
      await host.waitLog('webhook', /没有打开“代管外部身份”，消息 m1 没有推送/)
      const r = await host.request('http', 'webhook', { via: 'external', method: 'POST', path: '/answer', headers: {}, body: '' })
      assert.equal(r.payload.value.status, 403)
    } finally {
      await host.close()
    }
  })
})

describe('官方插件 hello', () => {
  it('平台半边在 run.ended 时写插件日志', async () => {
    const host = await HostHarness.start()
    try {
      assert.deepEqual((await host.load(officialPayload('hello'))).payload, { ok: true })
      const ack = await host.event('hello', {
        type: 'run.ended',
        agentId: 'agent-1',
        origin: 'console',
        status: 'completed',
        endReason: null,
        errorCode: null,
        tokens: { input: 10, output: 5, cacheRead: 0, cacheWrite: 0, total: 15 },
      })
      assert.deepEqual(ack.payload, { ok: true })
      assert.equal((await host.waitLog('hello', /一轮结束/)).message, 'Agent agent-1 的一轮结束：completed，用了 15 个 token')
      assert.deepEqual(host.badLines, [])
    } finally {
      await host.close()
    }
  })
})

describe('validate 子命令', () => {
  const run = promisify(execFile)
  const validate = async (...args: string[]) => {
    try {
      const { stdout } = await run(process.execPath, [HOST_ENTRY, 'validate', ...args])
      return { code: 0, result: JSON.parse(stdout) }
    } catch (err) {
      const e = err as { code: number; stdout: string }
      return { code: e.code, result: JSON.parse(e.stdout) }
    }
  }

  it('官方插件加载核对通过', async () => {
    for (const id of ['webhook', 'hello', 'playwright']) {
      const { code, result } = await validate(path.join(official, id), '--load', '--platform-version', '2026.11')
      assert.equal(code, 0)
      assert.deepEqual(result.findings, [])
      assert.equal(result.manifest.id, id)
    }
  })

  it('--load 报出代码订阅了清单外的事件，插件输出不混进 JSON', async () => {
    const p = fx.write('greedy', {
      'package.json': JSON.stringify({
        name: 'greedy',
        version: '1.0.0',
        description: 'x',
        keywords: ['loop-now-plugin'],
        loopNow: { id: 'greedy', displayName: 'G', platform: './index.ts', events: ['run.ended', 'run.started'] },
      }),
      'README.md': '',
      'index.ts': `export default function (api) {
        console.log('noise on stdout')
        api.on('run.ended', () => {})
        api.on('inbox.received', () => {})
      }`,
    })
    const { code, result } = await validate(p.root, '--load')
    assert.equal(code, 1)
    assert.deepEqual(
      result.findings.map((f: { level: string; message: string }) => [f.level, f.message]),
      [
        ['error', '订阅了清单 loopNow.events 中没有声明的事件 inbox.received'],
        ['warning', '声明了 run.started，但 register 里没有订阅（可能在 start 里订阅）'],
      ],
    )
    const statik = await validate(p.root)
    assert.equal(statik.code, 0, '不加 --load 时只做静态检查')
  })
})
