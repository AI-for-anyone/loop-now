// 宿主协议测试：经标准输入输出驱动构建好的宿主（先 npm run build），覆盖 api/plugin-host-protocol.md 的各项约定。

import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { after, afterEach, describe, it } from 'node:test'

import { Fixtures, HostHarness } from './harness.ts'

const fx = new Fixtures()
let host: HostHarness

after(() => fx.cleanup())
afterEach(async () => {
  assert.deepEqual(host.badLines, [], '标准输出只能有协议行')
  await host.close()
})

describe('启动与加载', () => {
  it('第一行是 ready，协议版本 1', async () => {
    host = await HostHarness.start()
    const first = host.messages[0]!
    assert.equal(first.type, 'ready')
    assert.equal(first.payload.protocol, 1)
    assert.match(first.payload.hostVersion, /^\d+\.\d+\.\d+/)
    assert.equal(first.id, undefined)
  })

  it('等异步 register 完成才回 result；按顺序加载多个插件', async () => {
    host = await HostHarness.start()
    const slow = fx.write('slow', {
      'index.ts': `export default async function (api) {
        await new Promise((r) => setTimeout(r, 300))
        api.log.info('registered')
      }`,
    })
    const quick = fx.write('quick', { 'index.ts': `export default function () {}` })
    const started = Date.now()
    const r1 = await host.load(slow)
    assert.deepEqual(r1.payload, { ok: true })
    assert.ok(Date.now() - started >= 280)
    const i = host.messages.findIndex((m) => m.type === 'log' && m.plugin === 'slow')
    const j = host.messages.indexOf(r1)
    assert.ok(i >= 0 && i < j, 'register 里的日志先于 result')
    const r2 = await host.load(quick)
    assert.equal(r2.type, 'result')
    assert.equal(r2.plugin, 'quick')
    assert.deepEqual(r2.payload, { ok: true })
  })

  it('模块求值出错只让这个插件加载失败，其他插件照常', async () => {
    host = await HostHarness.start()
    const broken = fx.write('broken', { 'index.ts': `throw new Error('坏掉的模块')\nexport default function () {}` }, { events: ['run.ended'] })
    const syntax = fx.write('syntax', { 'index.ts': `export default function ( {` })
    const good = fx.write('good', { 'index.ts': `export default function (api) { api.on('run.ended', () => api.log.info('good ran')) }` }, { events: ['run.ended'] })
    const r1 = await host.load(broken)
    assert.equal(r1.payload.ok, false)
    assert.match(r1.payload.error, /加载 index\.ts 失败：坏掉的模块/)
    const r2 = await host.load(syntax)
    assert.equal(r2.payload.ok, false)
    assert.match(r2.payload.error, /加载 index\.ts 失败/)
    assert.deepEqual((await host.load(good)).payload, { ok: true })
    assert.deepEqual((await host.event('good', { type: 'run.ended' })).payload, { ok: true })
    await host.waitLog('good', /good ran/)
    const ack = await host.event('broken', { type: 'run.ended' })
    assert.deepEqual(ack.payload, { ok: false, error: '插件未加载' })
  })

  it('register 抛错、没有默认导出、入口不存在都回 ok:false', async () => {
    host = await HostHarness.start()
    const throws = fx.write('throws', { 'index.ts': `export default function () { throw new Error('register 不行') }` })
    const none = fx.write('none', { 'index.ts': `export const x = 1` })
    const missing = { ...fx.write('missing', { 'other.ts': '' }) }
    const r1 = await host.load(throws)
    assert.deepEqual(r1.payload, { ok: false, error: 'register 失败：register 不行' })
    const r2 = await host.load(none)
    assert.equal(r2.payload.ok, false)
    assert.match(r2.payload.error, /没有默认导出 register 函数/)
    const r3 = await host.load(missing)
    assert.equal(r3.payload.ok, false)
    const r4 = await host.request('load', 'other', { ...throws })
    assert.deepEqual(r4.payload, { ok: false, error: 'load 载荷的 id 须与消息的 plugin 一致' })
  })

  it('订阅清单外的事件时加载失败', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'greedy',
      {
        'index.ts': `export default function (api) {
          api.on('run.ended', () => {})
          try { api.on('inbox.received', () => {}) } catch {}
        }`,
      },
      { events: ['run.ended'] },
    )
    const r = await host.load(p)
    assert.equal(r.payload.ok, false)
    assert.match(r.payload.error, /没有声明的事件 inbox\.received/)
    const act = fx.write('action', { 'index.ts': `export default function (api) { api.actions.register('resend', async () => ({ notice: 'x' })) }` })
    const r2 = await host.load(act)
    assert.equal(r2.payload.ok, false)
    assert.match(r2.payload.error, /操作按钮 resend 没有在清单 loopNow\.actions 中声明/)
  })

  it('支持 TypeScript（含 enum）、相对导入与宿主提供的 SDK', async () => {
    host = await HostHarness.start()
    const p = fx.write('ts', {
      'index.ts': `import type { PluginAPI } from '@loop-now/plugin-sdk'
        import { ulid, EVENTS } from '@loop-now/plugin-sdk'
        import { label } from './lib'
        enum Mode { On = 'on' }
        export default function register(api: PluginAPI) {
          api.log.info(label(Mode.On), ulid().length, EVENTS['run.ended'])
        }`,
      'lib.ts': `export const label = (m: string): string => 'mode=' + m`,
    })
    assert.deepEqual((await host.load(p)).payload, { ok: true })
    assert.equal((await host.waitLog('ts', /mode=/)).message, 'mode=on 26 observe')
  })
})

describe('观察类事件', () => {
  it('处理完才回 ack；同一插件串行', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'serial',
      {
        'index.ts': `export default function (api) {
          api.on('run.ended', async (event, ctx) => {
            api.log.info('begin ' + event.id + ' ' + ctx.attempt)
            await new Promise((r) => setTimeout(r, 200))
            await api.state.set('seen/' + event.id, { attempt: ctx.attempt })
            api.log.info('end ' + event.id)
          })
        }`,
      },
      { events: ['run.ended'], permissions: ['state'] },
    )
    await host.load(p)
    const a = host.event('serial', { id: 'E1', type: 'run.ended' }, { attempt: 2 })
    const b = host.event('serial', { id: 'E2', type: 'run.ended' })
    const [ackA, ackB] = await Promise.all([a, b])
    assert.deepEqual(ackA.payload, { ok: true })
    assert.equal(ackA.type, 'ack')
    assert.deepEqual(ackB.payload, { ok: true })
    const order = host.messages
      .filter((m) => (m.type === 'log' && m.plugin === 'serial') || m.type === 'ack')
      .map((m) => (m.type === 'ack' ? `ack ${m.id}` : m.payload.message))
    assert.deepEqual(order.slice(0, 3), ['begin E1 2', 'end E1', order[2]])
    assert.ok(order.indexOf('end E1') < order.indexOf('begin E2 1'), `E2 应在 E1 结束后开始：${order.join(', ')}`)
    assert.ok(order.findIndex((x) => x.startsWith('ack')) > order.indexOf('end E1'), 'ack 在处理完之后')
    assert.deepEqual(host.stateOf('serial').get('seen/E1'), { attempt: 2 })
  })

  it('处理函数抛错回 ok:false，宿主继续；没有处理函数的事件直接 ok', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'flaky',
      {
        'index.ts': `export default function (api) {
          api.on('run.ended', (event) => { if (event.status === 'failed') throw new Error('处理失败了') })
        }`,
      },
      { events: ['run.ended', 'run.started'] },
    )
    await host.load(p)
    const bad = await host.event('flaky', { type: 'run.ended', status: 'failed' })
    assert.deepEqual(bad.payload, { ok: false, error: '处理失败了' })
    const good = await host.event('flaky', { type: 'run.ended', status: 'completed' })
    assert.deepEqual(good.payload, { ok: true })
    assert.deepEqual((await host.event('flaky', { type: 'run.started' })).payload, { ok: true })
    const warned = host.logs('flaky').find((l) => l.level === 'warn' && /处理失败了/.test(l.message))!
    assert.match(warned.message, /^run\.ended 的处理函数出错：处理失败了\nat .*index\.ts:2:/)
    assert.equal(warned.message.split('\n').length, 2)
  })

  it('到 timeoutMs 回失败、触发 ctx.signal，队列放行下一条', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'slowpoke',
      {
        'index.ts': `export default function (api) {
          api.on('run.ended', async (event, ctx) => {
            if (event.status !== 'hang') return
            ctx.signal.addEventListener('abort', () => api.log.info('aborted: ' + ctx.signal.reason.message))
            await new Promise(() => {})
          })
        }`,
      },
      { events: ['run.ended'] },
    )
    await host.load(p)
    const started = Date.now()
    const hung = host.event('slowpoke', { type: 'run.ended', status: 'hang' }, { timeoutMs: 300 })
    const next = host.event('slowpoke', { type: 'run.ended', status: 'ok' })
    const ack = await hung
    assert.equal(ack.payload.ok, false)
    assert.match(ack.payload.error, /超过 300 毫秒/)
    assert.ok(Date.now() - started < 2_000)
    assert.deepEqual((await next).payload, { ok: true })
    assert.match((await host.waitLog('slowpoke', /aborted/)).message, /超过 300 毫秒/)
  })
})

describe('状态', () => {
  it('get/set/delete/list 经 Go 往返，项目级与全局分开', async () => {
    host = await HostHarness.start()
    const project = '6f1c8e5e-0000-4000-8000-000000000001'
    const p = fx.write(
      'stateful',
      {
        'index.ts': `export default function (api) {
          api.lifecycle.on('start', async () => {
            await api.state.set('a/1', { n: 1 })
            await api.state.set('a/2', [1, 2])
            await api.state.set('b', 'x')
            await api.state.set('a/1', { n: 'project' }, { projectId: '${project}' })
            const got = await api.state.get('a/1')
            const missing = await api.state.get('nope')
            const list = await api.state.list('a/')
            await api.state.delete('a/2')
            await api.state.delete('never-there')
            const after = await api.state.list('a/')
            const scoped = await api.state.get('a/1', { projectId: '${project}' })
            api.log.info(JSON.stringify({ got, missing, list, after, scoped }))
          })
        }`,
      },
      { permissions: ['state'] },
    )
    assert.deepEqual((await host.load(p)).payload, { ok: true })
    const log = JSON.parse((await host.waitLog('stateful', /"got"/)).message)
    assert.deepEqual(log, {
      got: { n: 1 },
      missing: null,
      list: [
        { key: 'a/1', value: { n: 1 } },
        { key: 'a/2', value: [1, 2] },
      ],
      after: [{ key: 'a/1', value: { n: 1 } }],
      scoped: { n: 'project' },
    })
    assert.deepEqual([...host.stateOf('stateful', project).keys()], ['a/1'])
  })

  it('没有 state 权限、key 不合规则时直接拒绝', async () => {
    host = await HostHarness.start()
    const p = fx.write('nostate', {
      'index.ts': `export default function (api) {
        api.lifecycle.on('start', async () => {
          const errors = []
          for (const f of [() => api.state.get('k'), () => api.state.set('k', 1)]) {
            try { await f() } catch (e) { errors.push(e.message) }
          }
          api.log.info(errors.join(' | '))
        })
      }`,
    })
    await host.load(p)
    assert.equal((await host.waitLog('nostate', /权限/)).message, '清单没有声明 state 权限 | 清单没有声明 state 权限')
    assert.equal(host.messages.filter((m) => m.type.startsWith('state_')).length, 0)
    const keys = fx.write(
      'badkeys',
      {
        'index.ts': `export default function (api) {
          api.lifecycle.on('start', async () => {
            const errors = []
            for (const f of [() => api.state.get(''), () => api.state.set('x'.repeat(513), 1), () => api.state.set('k', undefined)]) {
              try { await f() } catch (e) { errors.push(e.constructor.name) }
            }
            api.log.info(errors.join(','))
          })
        }`,
      },
      { permissions: ['state'] },
    )
    await host.load(keys)
    assert.equal((await host.waitLog('badkeys', /Error/)).message, 'RangeError,RangeError,TypeError')
  })
})

describe('账号', () => {
  it('accounts.list 经 Go 读项目账号；权限、种类、项目在本地先检查', async () => {
    host = await HostHarness.start()
    const project = '6f1c8e5e-0000-4000-8000-000000000003'
    const other = '6f1c8e5e-0000-4000-8000-000000000004'
    host.enabledIn.add(`acct/${project}`)
    host.accounts.set(`acct/${project}/lark`, [
      { id: 'a1', kind: 'lark', name: '机器人一', values: { appId: 'cli_1', appSecret: 's1' } },
      { id: 'a2', kind: 'lark', name: '机器人二', values: { appId: 'cli_2', appSecret: 's2' } },
    ])
    const p = fx.write(
      'acct',
      {
        'index.ts': `export default function (api) {
          api.lifecycle.on('start', async () => {
            const list = await api.accounts.list('${project}', 'lark')
            api.log.info('names ' + list.map((a) => a.name + ':' + a.values.appId).join(','))
            const errors = []
            for (const f of [() => api.accounts.list('${project}', 'github'), () => api.accounts.list('', 'lark'), () => api.accounts.list('${other}', 'lark')]) {
              try { await f() } catch (e) { errors.push(e.message) }
            }
            api.log.info('errors ' + errors.join(' | '))
          })
        }`,
      },
      { permissions: ['accounts.read'], manifest: { accounts: [{ kind: 'lark', label: '飞书', fields: {}, multiple: true }] } },
    )
    assert.deepEqual((await host.load(p)).payload, { ok: true })
    assert.equal((await host.waitLog('acct', /^names/)).message, 'names 机器人一:cli_1,机器人二:cli_2')
    assert.equal((await host.waitLog('acct', /^errors/)).message, 'errors 清单没有声明账号种类 github | projectId 无效 | 插件没有在这个项目里启用')
    assert.equal(host.messages.filter((m) => m.type === 'accounts_list').length, 2, '本地能判断的不发给 Go')
    const noPerm = fx.write('noacct', { 'index.ts': `export default function (api) { api.lifecycle.on('start', async () => { try { await api.accounts.list('${project}', 'lark') } catch (e) { api.log.info(e.message) } }) }` }, { manifest: { accounts: [{ kind: 'lark', label: '飞书', fields: {}, multiple: false }] } })
    await host.load(noPerm)
    assert.equal((await host.waitLog('noacct', /权限/)).message, '清单没有声明 accounts.read 权限')
  })
})

describe('卸载与生命周期', () => {
  it('unload 触发 stop、释放全部注册；再加载得到新的模块状态', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'cycle',
      {
        'index.ts': `let loads = 0
        export default function (api) {
          loads++
          let timer
          api.lifecycle.on('start', () => { timer = setInterval(() => {}, 1000); api.log.info('start ' + loads) })
          api.lifecycle.on('stop', () => { clearInterval(timer); api.log.info('stop ' + loads) })
          api.on('run.ended', () => api.log.info('event ' + loads))
          api.config.onChange(() => api.log.info('config ' + JSON.stringify(api.config.get())))
        }`,
      },
      { events: ['run.ended'] },
    )
    await host.load(p)
    await host.waitLog('cycle', /^start 1$/)
    assert.deepEqual((await host.request('config_changed', 'cycle', { config: { values: { a: 1 }, secrets: {}, projects: {} } })).payload, { ok: true })
    await host.waitLog('cycle', /^config \{"a":1\}$/)
    assert.deepEqual((await host.request('unload', 'cycle', {})).payload, { ok: true })
    assert.ok(host.logs('cycle').some((l) => l.message === 'stop 1'))
    assert.deepEqual((await host.event('cycle', { type: 'run.ended' })).payload, { ok: false, error: '插件未加载' })
    assert.deepEqual((await host.request('config_changed', 'cycle', { config: {} })).payload, { ok: false, error: '插件未加载' })
    assert.deepEqual((await host.request('unload', 'cycle', {})).payload, { ok: true }, 'unload 可重复')
    await host.load(p)
    await host.waitLog('cycle', /^start 1$/)
    await host.event('cycle', { type: 'run.ended' })
    assert.equal(host.logs('cycle').filter((l) => /^start/.test(l.message)).length, 2, '重新求值，模块状态从头开始')
  })

  it('注册返回的取消函数立即生效；start 里注册的处理函数同样可用', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'late',
      {
        'index.ts': `export default function (api) {
          const off = api.on('run.ended', () => api.log.info('removed handler'))
          off()
          off()
          api.lifecycle.on('start', () => { api.on('run.ended', () => api.log.info('late handler')) })
        }`,
      },
      { events: ['run.ended'] },
    )
    await host.load(p)
    assert.deepEqual((await host.event('late', { type: 'run.ended' })).payload, { ok: true })
    await host.waitLog('late', /late handler/)
    assert.ok(!host.logs('late').some((l) => l.message === 'removed handler'))
  })

  it('shutdown 卸载全部插件后退出', async () => {
    host = await HostHarness.start()
    const p = fx.write('bye', { 'index.ts': `export default function (api) { api.lifecycle.on('stop', () => api.log.info('bye')) }` })
    await host.load(p)
    const exit = await host.shutdown()
    assert.equal(exit.code, 0)
    assert.ok(host.logs('bye').some((l) => l.message === 'bye'))
  })

  it('标准输入关闭时也卸载后退出', async () => {
    host = await HostHarness.start()
    const p = fx.write('eof', { 'index.ts': `export default function (api) { api.lifecycle.on('stop', () => api.log.info('stopped')) }` })
    await host.load(p)
    ;(host as unknown as { child: { stdin: { end(): void } } }).child.stdin.end()
    assert.equal((await host.exited).code, 0)
    assert.ok(host.logs('eof').some((l) => l.message === 'stopped'))
  })
})

describe('同步调用', () => {
  it('intercept：第一个 block 生效，其余放行', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'guard',
      {
        'index.ts': `export default function (api) {
          api.on('node.before_submit', (e) => e.edgeId === 'deny' ? { block: true, reason: '不许提交' } : undefined)
          api.on('node.before_submit', () => ({ block: true }))
          api.on('task.before_start', () => {})
        }`,
      },
      { events: ['node.before_submit', 'task.before_start'] },
    )
    await host.load(p)
    assert.deepEqual((await host.call('guard', 'intercept', { type: 'node.before_submit', edgeId: 'deny' })).payload, {
      ok: true,
      value: { block: true, reason: '不许提交' },
    })
    assert.deepEqual((await host.call('guard', 'intercept', { type: 'node.before_submit', edgeId: 'ok' })).payload, {
      ok: true,
      value: { block: true, reason: '没有说明原因' },
    })
    assert.deepEqual((await host.call('guard', 'intercept', { type: 'task.before_start' })).payload, { ok: true, value: {} })
  })

  it('modify：本插件内按顺序合并 patch，固定字段不能改', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'shaper',
      {
        'index.ts': `export default function (api) {
          api.on('sleep.before_schedule', (e) => ({ patch: { durationSeconds: e.durationSeconds * 2, id: 'evil' } }))
          api.on('sleep.before_schedule', (e) => ({ patch: { reason: 'saw ' + e.durationSeconds } }))
        }`,
      },
      { events: ['sleep.before_schedule', 'message.before_enqueue'] },
    )
    await host.load(p)
    const r = await host.call('shaper', 'modify', { type: 'sleep.before_schedule', durationSeconds: 60, reason: null })
    assert.deepEqual(r.payload, { ok: true, value: { patch: { durationSeconds: 120, reason: 'saw 120' } } })
    assert.ok(host.logs('shaper').some((l) => /不能改固定字段 id/.test(l.message)))
    assert.deepEqual((await host.call('shaper', 'modify', { type: 'message.before_enqueue', text: 'x' })).payload, { ok: true, value: {} })
  })

  it('claim：第一个 handled 的对象原样返回', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'claimer',
      {
        'index.ts': `export default function (api) {
          api.on('console.command', (e) => e.command === 'mine' ? { handled: true, reply: 'got ' + e.text } : { handled: false })
        }`,
      },
      { events: ['console.command'] },
    )
    await host.load(p)
    assert.deepEqual((await host.call('claimer', 'claim', { type: 'console.command', command: 'mine', text: '/mine hi' })).payload, {
      ok: true,
      value: { handled: true, reply: 'got /mine hi' },
    })
    assert.deepEqual((await host.call('claimer', 'claim', { type: 'console.command', command: 'other', text: '' })).payload, {
      ok: true,
      value: { handled: false },
    })
  })

  it('超时回失败并触发 signal；抛错回 ok:false；未知种类拒绝', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'stuck',
      {
        'index.ts': `export default function (api) {
          api.on('run.before_start', async (e, ctx) => {
            if (e.origin === 'boom') throw new Error('检查出错')
            await new Promise((resolve) => ctx.signal.addEventListener('abort', resolve))
            api.log.info('signal fired after ' + (Date.now() - Number(e.messageId)) + 'ms')
            return { block: true, reason: 'too late' }
          })
        }`,
      },
      { events: ['run.before_start'] },
    )
    await host.load(p)
    const t0 = Date.now()
    const r = await host.call('stuck', 'intercept', { type: 'run.before_start', origin: 'console', messageId: String(t0) }, 250)
    assert.equal(r.payload.ok, false)
    assert.match(r.payload.error, /超过 250 毫秒/)
    const elapsed = Number(/after (\d+)ms/.exec((await host.waitLog('stuck', /signal fired/)).message)![1])
    assert.ok(elapsed >= 240 && elapsed < 2_000, `signal 在 ${elapsed}ms 触发`)
    assert.deepEqual((await host.call('stuck', 'intercept', { type: 'run.before_start', origin: 'boom' })).payload, { ok: false, error: '检查出错' })
    assert.deepEqual((await host.call('stuck', 'veto', { type: 'run.before_start' })).payload, { ok: false, error: '未知的调用种类 veto' })
    assert.deepEqual((await host.call('nobody', 'intercept', { type: 'run.before_start' })).payload, { ok: false, error: '插件未加载' })
  })
})

describe('操作按钮、HTTP 入口、定时与渠道', () => {
  it('action：把 input 与 where 交给处理函数，返回 {notice, url?}', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'actor',
      {
        'index.ts': `export default function (api) {
          api.actions.register('resend', async (input, ctx) => ({ notice: 'resent ' + input.id + ' in ' + ctx.where.projectId + ' via ' + ctx.actionId, url: '/x' }))
          api.actions.register('plain', () => ({ notice: 'ok' }))
          api.actions.register('broken', () => ({ nope: true }))
          api.actions.register('slow', (input, ctx) => new Promise((r) => ctx.signal.addEventListener('abort', r)))
        }`,
      },
      { manifest: { actions: [{ id: 'resend' }, { id: 'plain' }, { id: 'broken' }, { id: 'slow' }] } },
    )
    assert.deepEqual((await host.load(p)).payload, { ok: true })
    const r = await host.request('action', 'actor', { actionId: 'resend', where: { projectId: 'p1' }, input: { id: 'd1' }, timeoutMs: 1000 })
    assert.deepEqual(r.payload, { ok: true, value: { notice: 'resent d1 in p1 via resend', url: '/x' } })
    assert.deepEqual((await host.request('action', 'actor', { actionId: 'plain', where: {}, input: {} })).payload, { ok: true, value: { notice: 'ok' } })
    assert.deepEqual((await host.request('action', 'actor', { actionId: 'broken', where: {}, input: {} })).payload, { ok: false, error: '操作的返回值须是 { notice, url? }' })
    assert.deepEqual((await host.request('action', 'actor', { actionId: 'missing', where: {}, input: {} })).payload, { ok: false, error: '插件没有注册操作按钮 missing' })
    const slow = await host.request('action', 'actor', { actionId: 'slow', where: {}, input: {}, timeoutMs: 150 })
    assert.equal(slow.payload.ok, false)
    assert.match(slow.payload.error, /超过 150 毫秒/)
  })

  it('http：请求的方法、路径、查询、头与 base64 正文原样交给插件，回应原样带回', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'hooky',
      {
        'index.ts': `export default function (api) {
          api.http.handle(async (req, ctx) => {
            const url = new URL(req.url)
            if (url.pathname === '/bad') return 'not a response'
            const body = await req.text()
            const headers = new Headers({ 'content-type': 'application/json', 'x-multi': 'a', connection: 'close' })
            headers.append('set-cookie', 'a=1')
            headers.append('set-cookie', 'b=2')
            return new Response(JSON.stringify({ method: req.method, path: url.pathname, q: url.searchParams.get('a'), sig: req.headers.get('x-sig'), multi: req.headers.get('x-list'), body, via: ctx.via }), { status: 201, headers })
          })
        }`,
      },
      { manifest: { http: true } },
    )
    await host.load(p)
    const r = await host.request('http', 'hooky', {
      via: 'external',
      method: 'POST',
      path: '/callback',
      query: 'a=1&b=2',
      headers: { 'x-sig': ['abc'], 'x-list': ['one', 'two'], 'content-type': ['text/plain'] },
      body: Buffer.from('payload 你好').toString('base64'),
      timeoutMs: 2000,
    })
    assert.equal(r.payload.ok, true)
    const v = r.payload.value
    assert.equal(v.status, 201)
    assert.deepEqual(v.headers, { 'content-type': ['application/json'], 'x-multi': ['a'], 'set-cookie': ['a=1', 'b=2'] })
    assert.deepEqual(JSON.parse(Buffer.from(v.body, 'base64').toString()), {
      method: 'POST', path: '/callback', q: '1', sig: 'abc', multi: 'one, two', body: 'payload 你好', via: 'external',
    })
    const get = await host.request('http', 'hooky', { via: 'console', method: 'GET', path: '/x', query: '', headers: {}, body: '' })
    assert.equal(JSON.parse(Buffer.from(get.payload.value.body, 'base64').toString()).via, 'console')
    assert.deepEqual((await host.request('http', 'hooky', { method: 'GET', path: '/bad', headers: {}, body: '' })).payload, { ok: false, error: 'HTTP 处理函数须返回 Response' })
    const none = fx.write('nohttp', { 'index.ts': 'export default function () {}' }, { manifest: { http: true } })
    await host.load(none)
    assert.deepEqual((await host.request('http', 'nohttp', { method: 'GET', path: '/', headers: {}, body: '' })).payload, { ok: false, error: '插件没有注册 HTTP 处理函数' })
  })

  it('schedule：处理完回 ok；抛错回失败；未注册的条目拒绝', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'cron',
      {
        'index.ts': `export default function (api) {
          api.schedule.on('sweep', async (ctx) => { api.log.info('sweep ' + ctx.scheduleId + ' ' + ctx.cron + ' ' + ctx.at) })
          api.schedule.on('fail', () => { throw new Error('清理失败') })
        }`,
      },
      { manifest: { schedules: [{ id: 'sweep' }, { id: 'fail' }] } },
    )
    await host.load(p)
    const r = await host.request('schedule', 'cron', { scheduleId: 'sweep', cron: '*/10 * * * *', at: '2026-10-27T10:00:00Z', timeoutMs: 1000 })
    assert.deepEqual(r.payload, { ok: true })
    assert.equal((await host.waitLog('cron', /^sweep/)).message, 'sweep sweep */10 * * * * 2026-10-27T10:00:00Z')
    assert.deepEqual((await host.request('schedule', 'cron', { scheduleId: 'fail', cron: '* * * * *', at: '' })).payload, { ok: false, error: '清理失败' })
    assert.deepEqual((await host.request('schedule', 'cron', { scheduleId: 'other' })).payload, { ok: false, error: '插件没有注册定时任务 other' })
  })

  it('渠道：channel.outbound 按 channelId 交给 outbound；send/reply/read/answer 以插件身份调用收件箱接口', async () => {
    const seen: { method?: string; url?: string; auth?: string; body: unknown }[] = []
    const server = createServer(async (req, res) => {
      let body = ''
      for await (const chunk of req) body += chunk
      seen.push({ method: req.method, url: req.url, auth: req.headers.authorization, body: body ? JSON.parse(body) : null })
      res.setHeader('content-type', 'application/json')
      if (req.url?.includes('/m-missing/')) {
        res.statusCode = 404
        res.end(JSON.stringify({ error: { code: 'not_found', message: '消息不存在' } }))
        return
      }
      res.statusCode = req.url === '/api/v1/inbox/messages' ? 201 : 200
      res.end(JSON.stringify({ id: 'm-new', threadId: 't1', text: req.url?.endsWith('/read') ? 'full body' : 'sent' }))
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const apiUrl = `http://127.0.0.1:${(server.address() as { port: number }).port}/api/v1`
    try {
      host = await HostHarness.start()
      const p = fx.write(
        'lark',
        {
          'index.ts': `export default function (api) {
            api.channels.register('lark', {
              outbound: async (event, ctx) => {
                if (event.question) {
                  await api.channels.answer('lark', event.question.id, { optionId: event.question.options[0].id })
                  await api.channels.answer('lark', event.question.id, { text: '都行' })
                  await api.channels.answer('lark', event.question.id, { optionId: 'o2', text: '下周再发' })
                  for (const bad of [{}, { optionId: 'o1', text: 7 }, { text: '' }]) {
                    try { await api.channels.answer('lark', event.question.id, bad) } catch (e) { api.log.info('bad answer: ' + e.message) }
                  }
                  try { await api.channels.answer('wechat', event.question.id, { text: 'x' }) } catch (e) { api.log.info('bad channel: ' + e.message) }
                  api.log.info('answered ' + event.isQuestion)
                  return
                }
                const full = await api.channels.read(event.messageId)
                await api.channels.reply(event.messageId, { text: 'ack ' + full.text })
                api.log.info('outbound ' + event.channelId + ' ' + event.excerpt + ' -> ' + full.text + ' attempt ' + ctx.attempt)
              },
            })
            api.lifecycle.on('start', async () => {
              const m = await api.channels.send('lark', { to: 'demo/manager', text: '用户说你好', senderName: '张三' })
              api.log.info('sent ' + m.id)
              try { await api.channels.send('wechat', { to: 'x', text: 'y' }) } catch (e) { api.log.info('undeclared: ' + e.message) }
              try { await api.channels.read('m-missing') } catch (e) { api.log.info('missing: ' + e.message + ' ' + e.status) }
            })
          }`,
        },
        { permissions: ['inbox.send', 'inbox.read'], manifest: { channels: [{ id: 'lark' }] }, apiUrl, token: 'lnp_channel' },
      )
      assert.deepEqual((await host.load(p)).payload, { ok: true })
      await host.waitLog('lark', /^sent m-new$/)
      await host.waitLog('lark', /^undeclared: 渠道 wechat 没有在清单 loopNow\.channels 中声明$/)
      await host.waitLog('lark', /^missing: 平台返回 404：消息不存在 404$/)
      const ack = await host.event('lark', { type: 'channel.outbound', channelId: 'lark', messageId: 'm9', threadId: 't1', from: { kind: 'agent', id: 'a', address: 'demo/manager' }, excerpt: '好的' }, { attempt: 2 })
      assert.deepEqual(ack.payload, { ok: true })
      assert.equal((await host.waitLog('lark', /^outbound/)).message, 'outbound lark 好的 -> full body attempt 2')
      assert.deepEqual(
        seen.map((s) => [s.method, s.url, s.auth, s.body]),
        [
          ['POST', '/api/v1/inbox/messages', 'Bearer lnp_channel', { to: 'demo/manager', text: '用户说你好', channelId: 'lark', senderName: '张三' }],
          ['POST', '/api/v1/inbox/messages/m-missing/read', 'Bearer lnp_channel', {}],
          ['POST', '/api/v1/inbox/messages/m9/read', 'Bearer lnp_channel', {}],
          ['POST', '/api/v1/inbox/messages/m9/reply', 'Bearer lnp_channel', { text: 'ack full body' }],
        ],
      )
      seen.length = 0
      const question = { id: 'q1', options: [{ id: 'o1', label: '发布' }, { id: 'o2', label: '不发' }], allowText: true, deadline: null }
      const asked = await host.event('lark', { type: 'channel.outbound', channelId: 'lark', messageId: 'q1', threadId: 't1', from: { kind: 'agent', id: 'a', address: 'demo/manager' }, isQuestion: true, question, excerpt: '发吗？' })
      assert.deepEqual(asked.payload, { ok: true })
      await host.waitLog('lark', /^answered true$/)
      assert.deepEqual(
        host.logs('lark').filter((l) => /^bad /.test(l.message)).map((l) => l.message),
        ['bad answer: 须给出 optionId 或 text', 'bad answer: text 须是非空字符串', 'bad answer: text 须是非空字符串', 'bad channel: 渠道 wechat 没有在清单 loopNow.channels 中声明'],
        '本地校验，不发请求',
      )
      assert.deepEqual(
        seen.map((s) => [s.method, s.url, s.body]),
        [
          ['POST', '/api/v1/inbox/messages/q1/reply', { optionId: 'o1' }],
          ['POST', '/api/v1/inbox/messages/q1/reply', { text: '都行' }],
          ['POST', '/api/v1/inbox/messages/q1/reply', { optionId: 'o2', text: '下周再发' }],
        ],
      )
      const stray = await host.event('lark', { type: 'channel.outbound', channelId: 'other', messageId: 'm10' })
      assert.deepEqual(stray.payload, { ok: false, error: '没有渠道处理 channel.outbound（channelId other）' }, '没有处理者时回失败，平台重投')
      const noPerm = fx.write('mute', { 'index.ts': `export default function (api) { api.lifecycle.on('start', async () => {
        try { await api.channels.send('c', { to: 'a', text: 'b' }) } catch (e) { api.log.info(e.message) }
        try { await api.channels.answer('c', 'q1', { optionId: 'o1' }) } catch (e) { api.log.info('answer: ' + e.message) }
      }) }` }, { manifest: { channels: [{ id: 'c' }] }, apiUrl })
      await host.load(noPerm)
      await host.waitLog('mute', /^清单没有声明 inbox\.send 权限$/)
      await host.waitLog('mute', /^answer: 清单没有声明 inbox\.send 权限$/)
    } finally {
      server.close()
    }
  })
})

describe('代管外部身份', () => {
  const identity = '6f1c8e5e-0000-4000-8000-0000000000e1'
  const manifest = { channels: [{ id: 'mail', manageIdentities: true }, { id: 'plain', manageIdentities: false }] } as never

  it('identity.inbound 按 channelId 交给 identityInbound；identities.read/reply/answer 以身份名义调用收件箱接口', async () => {
    const seen: { method?: string; url?: string; auth?: string; body: unknown }[] = []
    const server = createServer(async (req, res) => {
      let body = ''
      for await (const chunk of req) body += chunk
      seen.push({ method: req.method, url: req.url, auth: req.headers.authorization, body: body ? JSON.parse(body) : null })
      res.setHeader('content-type', 'application/json')
      if (req.url?.includes('/q-closed/')) {
        res.statusCode = 409
        res.end(JSON.stringify({ error: { code: 'question_closed', message: '提问已关闭', details: { state: 'closed' } } }))
        return
      }
      res.statusCode = req.method === 'GET' ? 200 : 201
      res.end(JSON.stringify(req.method === 'GET' ? { id: 'q1', threadId: 't1', text: '要不要发布？' } : { id: 'r1', threadId: 't1', text: 'ok', answerTo: { messageId: 'q1', optionId: 'yes', optionLabel: '发布' } }))
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const apiUrl = `http://127.0.0.1:${(server.address() as { port: number }).port}/api/v1`
    try {
      host = await HostHarness.start()
      const p = fx.write(
        'mailer',
        {
          'index.ts': `export default function (api) {
            api.channels.register('mail', {
              identityInbound: async (event, ctx) => {
                const full = await api.identities.read(event.identityId, event.messageId)
                const answered = await api.identities.answer(event.identityId, event.question.id, { optionId: event.question.options[0].id })
                await api.identities.reply(event.identityId, event.messageId, { text: '收到' })
                await api.identities.answer(event.identityId, 'q2', { text: '晚点再说' })
                try { await api.identities.answer(event.identityId, 'q-closed', { optionId: 'yes' }) } catch (e) { api.log.info('closed ' + e.status + ' ' + e.code + ' ' + e.details.state) }
                const errors = []
                for (const f of [() => api.identities.answer(event.identityId, 'q3', {}), () => api.identities.answer(event.identityId, 'q3', { optionId: 'a', text: '' }), () => api.identities.reply(event.identityId, '', { text: 'x' })]) {
                  try { await f() } catch (e) { errors.push(e.message) }
                }
                api.log.info('inbound ' + event.channelId + ' ' + full.text + ' -> ' + answered.answerTo.optionLabel + ' attempt ' + ctx.attempt + ' errors ' + errors.join(' | '))
              },
            })
          }`,
        },
        { permissions: ['identities.manage'], manifest, apiUrl, token: 'lnp_identity' },
      )
      assert.deepEqual((await host.load(p)).payload, { ok: true })
      const ack = await host.event(
        'mailer',
        {
          type: 'identity.inbound', identityId: identity, channelId: 'mail', messageId: 'q1', threadId: 't1',
          from: { kind: 'agent', agentId: 'a1', name: 'manager' }, nodeRunId: null, isQuestion: true,
          question: { id: 'q1', options: [{ id: 'yes', label: '发布' }, { id: 'no', label: '不发' }], allowText: true, deadline: null }, excerpt: '要不要发布？',
        },
        { attempt: 3 },
      )
      assert.deepEqual(ack.payload, { ok: true })
      assert.equal((await host.waitLog('mailer', /^inbound/)).message, 'inbound mail 要不要发布？ -> 发布 attempt 3 errors 须给出 optionId 或 text | text 须是非空字符串 | 须给出 messageId')
      assert.equal((await host.waitLog('mailer', /^closed/)).message, 'closed 409 question_closed closed')
      assert.deepEqual(
        seen.map((s) => [s.method, s.url, s.auth, s.body]),
        [
          ['GET', `/api/v1/external-identities/${identity}/inbox/q1`, 'Bearer lnp_identity', null],
          ['POST', '/api/v1/inbox/messages/q1/reply', 'Bearer lnp_identity', { asIdentityId: identity, optionId: 'yes' }],
          ['POST', '/api/v1/inbox/messages/q1/reply', 'Bearer lnp_identity', { text: '收到', asIdentityId: identity }],
          ['POST', '/api/v1/inbox/messages/q2/reply', 'Bearer lnp_identity', { asIdentityId: identity, text: '晚点再说' }],
          ['POST', '/api/v1/inbox/messages/q-closed/reply', 'Bearer lnp_identity', { asIdentityId: identity, optionId: 'yes' }],
        ],
      )
      const stray = await host.event('mailer', { type: 'identity.inbound', identityId: identity, channelId: 'gone', messageId: 'm2' })
      assert.deepEqual(stray.payload, { ok: false, error: '没有渠道处理 identity.inbound（channelId gone）' })
    } finally {
      server.close()
    }
  })

  it('清单不允许代管时加载失败或调用被本地拒绝', async () => {
    host = await HostHarness.start()
    const onPlain = fx.write('onplain', { 'index.ts': `export default function (api) { api.channels.register('plain', { identityInbound: () => {} }) }` }, { permissions: ['identities.manage'], manifest })
    const r1 = await host.load(onPlain)
    assert.deepEqual(r1.payload, { ok: false, error: '渠道 plain 没有在清单里声明 manageIdentities，不能注册 identityInbound' })
    const noPerm = fx.write('noperm', { 'index.ts': `export default function (api) { api.channels.register('mail', { identityInbound: () => {} }) }` }, { manifest })
    assert.deepEqual((await host.load(noPerm)).payload, { ok: false, error: '注册 identityInbound 须在清单里声明 identities.manage 权限' })
    const calls = fx.write(
      'calls',
      {
        'index.ts': `export default function (api) {
          api.lifecycle.on('start', async () => {
            try { await api.identities.read('${identity}', 'm1') } catch (e) { api.log.info(e.message) }
          })
        }`,
      },
      { manifest: { channels: [{ id: 'plain', manageIdentities: false }] } as never },
    )
    await host.load(calls)
    assert.equal((await host.waitLog('calls', /权限/)).message, '清单没有声明 identities.manage 权限')
    const noChannel = fx.write(
      'nochannel',
      { 'index.ts': `export default function (api) { api.lifecycle.on('start', async () => { try { await api.identities.reply('${identity}', 'm1', { text: 'x' }) } catch (e) { api.log.info(e.message) } }) }` },
      { permissions: ['identities.manage'] },
    )
    await host.load(noChannel)
    assert.equal((await host.waitLog('nochannel', /渠道/)).message, '清单 loopNow.channels 里没有声明 manageIdentities 的渠道')
  })
})

describe('配置、密钥与输出', () => {
  it('config.get 叠加项目值，secrets.get 项目优先；日志遮住 secret 与令牌', async () => {
    host = await HostHarness.start()
    const project = '6f1c8e5e-0000-4000-8000-000000000002'
    const p = fx.write(
      'conf',
      {
        'index.ts': `export default function (api) {
          api.on('run.ended', (e) => {
            const c = api.config.get(e.projectId)
            const g = api.config.get()
            api.log.info(JSON.stringify({ c, g, ps: api.config.projects(), s: api.secrets.get('key', e.projectId), gs: api.secrets.get('key'), none: api.secrets.get('nope') ?? null }))
            console.log('token is lnp_secret_token_1234 and key is', api.secrets.get('key'))
            process.stdout.write('raw out global-key-123\\n')
          })
        }`,
      },
      {
        events: ['run.ended'],
        token: 'lnp_secret_token_1234',
        config: {
          values: { url: 'https://g', limit: 3 },
          secrets: { key: 'global-key-123' },
          projects: { [project]: { values: { url: 'https://p' }, secrets: { key: 'project-key-456' } } },
        },
      },
    )
    await host.load(p)
    await host.event('conf', { type: 'run.ended', projectId: project })
    const parsed = host.logs('conf').map((l) => l.message)
    const data = JSON.parse(parsed.find((m) => m.startsWith('{'))!.replace(/\*\*\*/g, 'MASKED'))
    assert.deepEqual(data, {
      c: { url: 'https://p', limit: 3 },
      g: { url: 'https://g', limit: 3 },
      ps: [project],
      s: 'MASKED',
      gs: 'MASKED',
      none: null,
    })
    assert.ok(parsed.includes('token is *** and key is ***'), parsed.join('\n'))
    await host.waitStderr((lines) => lines.includes('[conf] raw out ***'))
    assert.ok(!JSON.stringify(host.messages).includes('global-key-123'))
    assert.ok(!host.stderr.join('\n').includes('project-key-456'))
  })

  it('插件写到标准输出与标准错误的内容逐行加前缀；未捕获的异常归到插件，宿主不退出', async () => {
    host = await HostHarness.start()
    const p = fx.write(
      'noisy',
      {
        'index.ts': `export default function (api) {
          api.on('run.ended', () => {
            process.stdout.write('part one ')
            process.stdout.write('and two\\nsecond line\\n')
            process.stderr.write('to stderr\\n')
            console.error('as error')
            setTimeout(() => { throw new Error('定时器里炸了') }, 10)
            Promise.reject(new Error('没人接的拒绝'))
          })
        }`,
      },
      { events: ['run.ended'] },
    )
    await host.load(p)
    assert.deepEqual((await host.event('noisy', { type: 'run.ended' })).payload, { ok: true })
    const uncaught = await host.waitLog('noisy', /^未捕获的异常：定时器里炸了/)
    assert.equal(uncaught.message.split('\n').length, 2, '只带说明与插件代码里的一帧')
    assert.ok(uncaught.message.split('\n')[1]!.includes(p.root), uncaught.message)
    await host.waitLog('noisy', /^未处理的 Promise 拒绝：没人接的拒绝/)
    await host.waitStderr((lines) => lines.includes('[noisy] to stderr'))
    assert.deepEqual(
      host.stderr.filter((l) => l.startsWith('[noisy]')),
      ['[noisy] part one and two', '[noisy] second line', '[noisy] to stderr'],
    )
    assert.ok(host.logs('noisy').some((l) => l.level === 'error' && l.message === 'as error'))
    const quick = fx.write('after', { 'index.ts': 'export default function () {}' })
    assert.deepEqual((await host.load(quick)).payload, { ok: true }, '宿主仍在运行')
  })

  it('platform.fetch 带插件令牌调用 /api/v1，对象正文按 JSON 发送', async () => {
    const seen: { method?: string; url?: string; auth?: string; type?: string; body: string }[] = []
    const server = createServer(async (req, res) => {
      let body = ''
      for await (const chunk of req) body += chunk
      seen.push({ method: req.method, url: req.url, auth: req.headers.authorization, type: req.headers['content-type'], body })
      res.setHeader('content-type', 'application/json')
      res.end(JSON.stringify({ ok: true }))
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const port = (server.address() as { port: number }).port
    try {
      host = await HostHarness.start()
      const p = fx.write(
        'caller',
        {
          'index.ts': `export default function (api) {
            api.on('run.ended', async (e, ctx) => {
              const res = await api.platform.fetch('/agents/' + e.agentId + '/wake', { method: 'POST', body: { why: 'test' }, signal: ctx.signal })
              api.log.info('status ' + res.status + ' ' + JSON.stringify(await res.json()))
            })
          }`,
        },
        { events: ['run.ended'], token: 'lnp_fetch_token', apiUrl: `http://127.0.0.1:${port}/api/v1/` },
      )
      await host.load(p)
      assert.deepEqual((await host.event('caller', { type: 'run.ended', agentId: 'a1' })).payload, { ok: true })
      assert.equal((await host.waitLog('caller', /status/)).message, 'status 200 {"ok":true}')
      assert.deepEqual(seen, [{ method: 'POST', url: '/api/v1/agents/a1/wake', auth: 'Bearer lnp_fetch_token', type: 'application/json', body: '{"why":"test"}' }])
    } finally {
      server.close()
    }
  })
})

