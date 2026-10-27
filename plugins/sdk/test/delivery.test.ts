// 投递库测试：用内存里的假 PluginAPI 与本机 HTTP 服务，覆盖重试、退避、放弃、去重、重启后继续、保留条数与记录形状。

import assert from 'node:assert/strict'
import { createServer, type IncomingMessage, type Server } from 'node:http'
import { afterEach, describe, it } from 'node:test'

import { createDelivery, type DeliveryRecord, type PluginAPI, ULID_PATTERN, verifySignature } from '../src/index.ts'

interface FakeApi {
  api: PluginAPI
  state: Map<string, unknown>
  start(): Promise<void>
  stop(): Promise<void>
  logs: string[]
  secrets: Record<string, string>
}

function fakeApi(state = new Map<string, unknown>()): FakeApi {
  const hooks: Record<'start' | 'stop', (() => unknown)[]> = { start: [], stop: [] }
  const logs: string[] = []
  const secrets: Record<string, string> = {}
  const clone = <T>(v: T): T => (v === undefined ? v : JSON.parse(JSON.stringify(v)))
  const api = {
    id: 'test',
    version: '1.2.3',
    root: '/x',
    dataDir: '/x/data',
    lifecycle: {
      on: (phase: 'start' | 'stop', fn: () => unknown) => {
        hooks[phase].push(fn)
        return () => {}
      },
    },
    state: {
      get: async (key: string) => clone(state.get(key) ?? null),
      set: async (key: string, value: unknown) => void state.set(key, clone(value)),
      delete: async (key: string) => void state.delete(key),
      list: async (prefix: string) =>
        [...state.entries()]
          .filter(([k]) => k.startsWith(prefix))
          .sort(([a], [b]) => (a < b ? -1 : 1))
          .map(([key, value]) => ({ key, value: clone(value) })),
    },
    secrets: { get: (name: string) => secrets[name] },
    log: { debug: () => {}, info: () => {}, warn: (m: string) => logs.push(m), error: (m: string) => logs.push(m) },
  } as unknown as PluginAPI
  return {
    api,
    state,
    logs,
    secrets,
    start: async () => {
      for (const fn of hooks.start) await fn()
    },
    stop: async () => {
      for (const fn of hooks.stop) await fn()
    },
  }
}

interface Received {
  method: string
  url: string
  headers: IncomingMessage['headers']
  body: string
}

let servers: Server[] = []
afterEach(() => {
  for (const s of servers) {
    s.closeAllConnections()
    s.close()
  }
  servers = []
})

/** 本机服务：responder 决定每个请求的状态码（返回 null 表示一直不回应）。 */
async function serve(responder: (n: number, r: Received) => number | null): Promise<{ url: string; received: Received[] }> {
  const received: Received[] = []
  const server = createServer(async (req, res) => {
    let body = ''
    for await (const chunk of req) body += chunk
    const r = { method: req.method!, url: req.url!, headers: req.headers, body }
    received.push(r)
    const status = responder(received.length, r)
    if (status === null) return
    res.statusCode = status
    res.end(status >= 400 ? `error body ${status}` : 'ok')
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  servers.push(server)
  return { url: `http://127.0.0.1:${(server.address() as { port: number }).port}/hook`, received }
}

async function until<T>(what: string, fn: () => T | undefined | false | Promise<T | undefined | false>, timeoutMs = 5_000): Promise<T> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() > deadline) throw new Error(`等待${what}超时`)
    await new Promise((r) => setTimeout(r, 10))
  }
}

function record(f: FakeApi, id: string): DeliveryRecord {
  return f.state.get(`deliveries/${id}`) as DeliveryRecord
}

describe('createDelivery', () => {
  it('2xx 一次送达；记录是 SPEC §7 的形状；签名可以校验', async () => {
    const f = fakeApi()
    f.secrets.secret = 's3cret-value'
    const srv = await serve(() => 204)
    const d = createDelivery(f.api)
    await f.start()
    const first = await d.deliver({
      target: srv.url,
      body: { hello: 'world' },
      headers: { 'X-Custom': 'yes' },
      eventId: 'EV1',
      eventType: 'inbox.received',
      signing: { secretField: 'secret' },
    })
    assert.match(first.id, ULID_PATTERN)
    assert.equal(first.status, 'pending')
    const done = await until('送达', () => record(f, first.id).status === 'delivered' && record(f, first.id))
    assert.deepEqual(Object.keys(done).sort(), ['at', 'attempts', 'eventId', 'eventType', 'id', 'lastError', 'lastStatusCode', 'nextAttemptAt', 'status', 'target', 'updatedAt'])
    assert.deepEqual(
      { ...done, at: 'T', updatedAt: 'T' },
      { id: first.id, at: 'T', eventId: 'EV1', eventType: 'inbox.received', target: srv.url, status: 'delivered', attempts: 1, nextAttemptAt: null, lastStatusCode: 204, lastError: null, updatedAt: 'T' },
    )
    assert.equal(srv.received.length, 1)
    const req = srv.received[0]!
    assert.equal(req.method, 'POST')
    assert.equal(req.body, '{"hello":"world"}')
    assert.equal(req.headers['content-type'], 'application/json')
    assert.equal(req.headers['x-custom'], 'yes')
    assert.equal(req.headers['user-agent'], 'loop-now-plugin-test/1.2.3')
    assert.equal(req.headers['x-loop-now-delivery'], first.id)
    assert.ok(
      verifySignature({
        secret: 's3cret-value',
        body: req.body,
        signature: req.headers['x-loop-now-signature'] as string,
        timestamp: req.headers['x-loop-now-timestamp'] as string,
      }),
    )
    assert.ok(!verifySignature({ secret: 'wrong', body: req.body, signature: req.headers['x-loop-now-signature'] as string, timestamp: req.headers['x-loop-now-timestamp'] as string }))
    assert.ok(![...f.state.values()].some((v) => JSON.stringify(v).includes('s3cret-value')), '密钥不落盘')
    await f.stop()
  })

  it('可重试的失败按退避重试，记录在重试期间为 failed 并带 nextAttemptAt', async () => {
    const f = fakeApi()
    const srv = await serve((n) => (n < 3 ? 503 : 200))
    const d = createDelivery(f.api, { backoffMs: [80, 160] })
    await f.start()
    const r = await d.deliver({ target: srv.url, body: 'plain text' })
    const retrying = await until('第一次失败', () => record(f, r.id).attempts === 1 && record(f, r.id))
    assert.equal(retrying.status, 'failed')
    assert.equal(retrying.lastStatusCode, 503)
    assert.equal(retrying.lastError, 'HTTP 503：error body 503')
    const wait = Date.parse(retrying.nextAttemptAt!) - Date.parse(retrying.updatedAt)
    assert.ok(wait >= 79 && wait <= 120, `第一次退避 ${wait}ms`)
    const second = await until('第二次失败', () => record(f, r.id).attempts === 2 && record(f, r.id))
    const wait2 = Date.parse(second.nextAttemptAt!) - Date.parse(second.updatedAt)
    assert.ok(wait2 >= 159 && wait2 <= 200, `第二次退避 ${wait2}ms`)
    const done = await until('送达', () => record(f, r.id).status === 'delivered' && record(f, r.id))
    assert.equal(done.attempts, 3)
    assert.equal(done.lastError, null)
    assert.equal(srv.received[0]!.body, 'plain text')
    assert.notEqual(srv.received[0]!.headers['content-type'], 'application/json', '字符串正文原样发送')
    await f.stop()
  })

  it('次数用尽后 abandoned；不可重试的 4xx 立即 failed 且不再重试', async () => {
    const f = fakeApi()
    const down = await serve(() => 500)
    const reject = await serve(() => 422)
    const d = createDelivery(f.api, { backoffMs: [10], maxAttempts: 2 })
    await f.start()
    const a = await d.deliver({ target: down.url })
    const b = await d.deliver({ target: reject.url, maxAttempts: 5 })
    const gave = await until('放弃', () => record(f, a.id).status === 'abandoned' && record(f, a.id))
    assert.equal(gave.attempts, 2)
    assert.equal(gave.nextAttemptAt, null)
    const failed = await until('失败', () => record(f, b.id).status === 'failed' && record(f, b.id))
    assert.equal(failed.attempts, 1)
    assert.equal(failed.nextAttemptAt, null)
    assert.equal(failed.lastStatusCode, 422)
    await new Promise((r) => setTimeout(r, 100))
    assert.equal(down.received.length, 2)
    assert.equal(reject.received.length, 1)
    await f.stop()
  })

  it('同一事件对同一目标只投递一次', async () => {
    const f = fakeApi()
    const srv = await serve(() => 200)
    const d = createDelivery(f.api)
    await f.start()
    const [x, y] = await Promise.all([
      d.deliver({ target: srv.url, eventId: 'E1', body: { n: 1 } }),
      d.deliver({ target: srv.url, eventId: 'E1', body: { n: 2 } }),
    ])
    assert.equal(x.id, y.id)
    const other = await d.deliver({ target: `${srv.url}?other`, eventId: 'E1' })
    assert.notEqual(other.id, x.id)
    await until('两个都送达', () => srv.received.length === 2)
    await new Promise((r) => setTimeout(r, 50))
    assert.equal(srv.received.length, 2)
    assert.equal((await d.list()).length, 2)
    await f.stop()
  })

  it('停止后留在状态里的投递在下次加载时继续', async () => {
    const state = new Map<string, unknown>()
    let up = false
    const srv = await serve(() => (up ? 200 : 503))
    const first = fakeApi(state)
    const d1 = createDelivery(first.api, { backoffMs: [150] })
    await first.start()
    const r = await d1.deliver({ target: srv.url, eventId: 'E9' })
    await until('第一次失败', () => (state.get(`deliveries/${r.id}`) as DeliveryRecord).attempts === 1)
    await first.stop()
    await assert.rejects(d1.deliver({ target: srv.url }), /插件已停止/)
    up = true
    const second = fakeApi(state)
    const d2 = createDelivery(second.api, { backoffMs: [150] })
    await second.start()
    const done = await until('重启后送达', () => (state.get(`deliveries/${r.id}`) as DeliveryRecord).status === 'delivered')
    assert.ok(done)
    assert.equal((await d2.get(r.id))!.attempts, 2)
    assert.equal((await d2.deliver({ target: srv.url, eventId: 'E9' })).id, r.id, '重启后仍按事件去重')
    await second.stop()
  })

  it('只保留最近 keep 条终态记录，连同请求一起删除', async () => {
    const f = fakeApi()
    const srv = await serve(() => 200)
    const d = createDelivery(f.api, { keep: 3 })
    await f.start()
    const ids: string[] = []
    for (let i = 0; i < 5; i++) {
      ids.push((await d.deliver({ target: srv.url, body: { i } })).id)
      await until('送达', () => record(f, ids[i]!).status === 'delivered')
    }
    const keys = [...f.state.keys()].sort()
    assert.deepEqual(
      keys,
      [...ids.slice(2).map((id) => `deliveries/${id}`), ...ids.slice(2).map((id) => `delivery-requests/${id}`)].sort(),
    )
    await f.stop()
  })

  it('resend 重发终态记录，尝试次数累计；请求丢失时放弃', async () => {
    const f = fakeApi()
    let ok = false
    const srv = await serve(() => (ok ? 200 : 400))
    const d = createDelivery(f.api)
    await f.start()
    const r = await d.deliver({ target: srv.url })
    await until('失败', () => record(f, r.id).status === 'failed')
    ok = true
    const again = await d.resend(r.id)
    assert.equal(again.status, 'pending')
    const done = await until('重发送达', () => record(f, r.id).status === 'delivered' && record(f, r.id))
    assert.equal(done.attempts, 2)
    await assert.rejects(d.resend('nope'), /没有投递记录/)

    const lost = fakeApi()
    lost.state.set('deliveries/01JLOST0000000000000000000', { ...done, id: '01JLOST0000000000000000000', status: 'pending', attempts: 0, nextAttemptAt: new Date().toISOString() })
    createDelivery(lost.api)
    await lost.start()
    const gone = await until('放弃', () => (lost.state.get('deliveries/01JLOST0000000000000000000') as DeliveryRecord).status === 'abandoned')
    assert.ok(gone)
    await lost.stop()
    await f.stop()
  })

  it('超时与连接失败可重试；签名密钥缺失不重试；参数不合规则时 deliver 抛错', async () => {
    const f = fakeApi()
    const hang = await serve(() => null)
    const d = createDelivery(f.api, { timeoutMs: 100, backoffMs: [60_000] })
    await f.start()
    const t = await d.deliver({ target: hang.url })
    const timedOut = await until('超时', () => record(f, t.id).attempts === 1 && record(f, t.id))
    assert.equal(timedOut.status, 'failed')
    assert.match(timedOut.lastError!, /^超时/)
    assert.notEqual(timedOut.nextAttemptAt, null)
    const refused = await d.deliver({ target: 'http://127.0.0.1:9/closed' })
    const r = await until('连接失败', () => record(f, refused.id).attempts === 1 && record(f, refused.id))
    assert.match(r.lastError!, /^请求失败：/)
    assert.notEqual(r.nextAttemptAt, null)
    const unsigned = await d.deliver({ target: hang.url, signing: { secretField: 'missing' } })
    const u = await until('密钥缺失', () => record(f, unsigned.id).attempts === 1 && record(f, unsigned.id))
    assert.deepEqual([u.status, u.nextAttemptAt, u.lastError], ['failed', null, '签名密钥 missing 没有设置'])
    await assert.rejects(d.deliver({ target: 'ftp://x' }), /http 或 https/)
    await assert.rejects(d.deliver({ target: 'not a url' }), /不是合法的地址/)
    await assert.rejects(d.deliver({ target: hang.url, headers: { a: 1 as unknown as string } }), /须是字符串/)
    await f.stop()
  })
})
