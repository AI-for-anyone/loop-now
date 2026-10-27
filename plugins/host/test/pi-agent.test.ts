// 官方插件的 Agent 半边在真实的 Pi（RPC 模式）里加载并被调用：用进程内的离线模型（fixtures/pi-offline-provider.ts）按脚本发出工具调用。
// 没有可用的 Pi 时跳过（LOOP_NOW_TEST_PI_ROOT 指定 Pi 包目录）；playwright 另需准备好的数据目录（LOOP_NOW_TEST_PLAYWRIGHT_DATA，见 setup.mjs）。

import assert from 'node:assert/strict'
import { execFile, spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { createInterface } from 'node:readline'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'

const piRoot = process.env.LOOP_NOW_TEST_PI_ROOT || '/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent'
const piCli = path.join(piRoot, 'dist/bundle/cli.js')
const official = fileURLToPath(new URL('../../official/', import.meta.url))
const provider = fileURLToPath(new URL('./fixtures/pi-offline-provider.ts', import.meta.url))
const noPi = !existsSync(piCli) && `找不到 ${piCli}`

type Rec = { type?: string; id?: string; data?: { messages: { role: string; toolName?: string; isError?: boolean; content: { type: string; text?: string }[] }[] } }

interface PiRun {
  extensions: string[]
  calls: { name: string; arguments: Record<string, unknown> }[]
  env?: Record<string, string>
  mcp?: Record<string, unknown>
  noExtensions?: boolean
}

/** 跑一轮：发一条提示，等 agent_end，取回消息与模型每轮看到的上下文。 */
async function runPi(o: PiRun): Promise<{ messages: NonNullable<Rec['data']>['messages']; seen: string; records: Rec[] }> {
  const dir = mkdtempSync(path.join(tmpdir(), 'loop-now-pi-'))
  const agentDir = path.join(dir, 'agent')
  mkdirSync(agentDir)
  if (o.mcp) writeFileSync(path.join(agentDir, 'mcp.json'), JSON.stringify({ mcpServers: o.mcp }))
  const report = path.join(dir, 'report.jsonl')
  const flags = ['--mode', 'rpc', '--session', path.join(dir, 'session.jsonl'), '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes']
  if (o.mcp) flags.push('-e', 'builtin:mcp')
  for (const e of [provider, ...o.extensions]) flags.push('-e', e)
  const child = spawn(process.execPath, [piCli, ...flags, '--provider', 'test-offline', '--model', 'fixture'], {
    cwd: dir,
    env: { HOME: dir, PATH: '/usr/bin:/bin', PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: '1', PI_TELEMETRY: '0', PI_TEST_TOOL_CALLS: JSON.stringify(o.calls), PI_TEST_REPORT: report, ...o.env },
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  let stderr = ''
  child.stderr.on('data', (d) => (stderr += d))
  const exited = new Promise<void>((resolve) => child.once('exit', () => resolve()))
  const records: Rec[] = []
  const waiters: { pred: (r: Rec) => boolean; resolve: (r: Rec) => void }[] = []
  createInterface({ input: child.stdout }).on('line', (line) => {
    let r: Rec
    try {
      r = JSON.parse(line)
    } catch {
      return
    }
    records.push(r)
    for (const w of [...waiters]) if (w.pred(r)) waiters.splice(waiters.indexOf(w), 1) && w.resolve(r)
  })
  const waitFor = (pred: (r: Rec) => boolean) =>
    Promise.race([
      new Promise<Rec>((resolve) => waiters.push({ pred, resolve })),
      exited.then(() => Promise.reject(new Error(`Pi 提前退出：${stderr.slice(0, 1500)}`))),
    ])
  const send = (m: unknown) => child.stdin.write(`${JSON.stringify(m)}\n`)
  try {
    const ended = waitFor((r) => r.type === 'agent_end')
    send({ id: 'p', type: 'prompt', message: 'go' })
    await ended
    const reply = waitFor((r) => r.id === 'm')
    send({ id: 'm', type: 'get_messages' })
    const messages = (await reply).data!.messages
    assert.equal(records.some((r) => r.type === 'extension_error'), false, JSON.stringify(records.filter((r) => r.type === 'extension_error')))
    return { messages, seen: existsSync(report) ? readFileSync(report, 'utf8') : '', records }
  } finally {
    child.kill('SIGTERM')
    await exited
    rmSync(dir, { recursive: true, force: true })
  }
}

describe('官方插件的 Agent 半边', { skip: noPi }, () => {
  it('hello：注册 hello 工具，按插件配置问候', { timeout: 60_000 }, async () => {
    const { messages } = await runPi({
      extensions: [path.join(official, 'hello/agent/index.ts')],
      calls: [{ name: 'hello', arguments: { name: 'Loop' } }],
      env: { LOOP_NOW_PLUGIN_HELLO_CONFIG: '{"greeting":"Hi"}' },
    })
    const result = messages.find((m) => m.role === 'toolResult')!
    assert.equal(result.toolName, 'hello')
    assert.equal(result.content[0]!.text, 'Hi, Loop!')
  })

  it('playwright：扩展补上浏览器规则；MCP 启动器在没有准备浏览器时说明怎么准备', { timeout: 60_000 }, async () => {
    const { seen } = await runPi({ extensions: [path.join(official, 'playwright/agent/index.ts')], calls: [] })
    assert.match(seen, /专用浏览器（mcp__playwright_browser__ 开头的工具）/)
    assert.match(seen, /必须先得到用户明确同意/)
    const launch = path.join(official, 'playwright/mcp/launch.mjs')
    const empty = mkdtempSync(path.join(tmpdir(), 'loop-now-pw-empty-'))
    try {
      const err = await promisify(execFile)(process.execPath, [launch, '--data', empty, '--agent', 'a1']).catch((e) => e as { code: number; stderr: string })
      assert.equal((err as { code: number }).code, 1)
      assert.match((err as { stderr: string }).stderr, /专用浏览器还没有准备：在服务端所在机器上运行 node .*setup\.mjs/)
      const bad = await promisify(execFile)(process.execPath, [launch, '--data', 'relative', '--agent', 'a1']).catch((e) => e as { stderr: string })
      assert.match((bad as { stderr: string }).stderr, /--data 须是插件数据目录的绝对路径/)
    } finally {
      rmSync(empty, { recursive: true, force: true })
    }
  })

  const data = process.env.LOOP_NOW_TEST_PLAYWRIGHT_DATA
  const prepared = data && existsSync(path.join(data, 'browser/browser.json')) && existsSync(path.join(official, 'playwright/node_modules/@playwright/mcp'))
  it('playwright：Agent 经 MCP 用准备好的 Chromium 打开网页（无头）', { timeout: 120_000, skip: !prepared && '没有准备好的浏览器（设置 LOOP_NOW_TEST_PLAYWRIGHT_DATA 并在插件目录 npm ci）' }, async () => {
    const server = createServer((_req, res) => {
      res.setHeader('content-type', 'text/html; charset=utf-8')
      res.end('<!doctype html><title>Loop Now 测试页</title><h1>你好，浏览器</h1>')
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const url = `http://127.0.0.1:${(server.address() as { port: number }).port}/`
    const agent = '6f1c8e5e-0000-4000-8000-00000000c0de'
    try {
      // 与服务端合并写入的 mcp.json 一样：清单里的 browser 条目，占位符已替换、相对路径已改成绝对路径；测试另加 --headless。
      const manifest = JSON.parse(readFileSync(path.join(official, 'playwright/package.json'), 'utf8'))
      const entry = manifest.loopNow.mcpServers.browser
      const args = (entry.args as string[]).map((a) =>
        a === './mcp/launch.mjs' ? path.join(official, 'playwright/mcp/launch.mjs') : a.replace('${LOOP_NOW_PLUGIN_DATA}', data!).replace('${LOOP_NOW_AGENT_ID}', agent),
      )
      const { messages } = await runPi({
        extensions: [path.join(official, 'playwright/agent/index.ts')],
        mcp: { 'playwright-browser': { ...entry, command: process.execPath, args: [...args, '--', '--headless'] } },
        calls: [
          { name: 'mcp__playwright_browser__browser_navigate', arguments: { url } },
          { name: 'mcp__playwright_browser__browser_snapshot', arguments: {} },
        ],
      })
      const results = messages.filter((m) => m.role === 'toolResult')
      assert.equal(results.length, 2, JSON.stringify(messages).slice(0, 2000))
      for (const r of results) assert.equal(r.isError ?? false, false, JSON.stringify(r).slice(0, 2000))
      const text = (i: number) => results[i]!.content.map((c) => c.text ?? '').join('\n')
      assert.match(text(0), /Page Title: Loop Now 测试页/)
      assert.match(text(1), /你好，浏览器/)
      assert.ok(existsSync(path.join(data!, 'profiles', agent)), '按 Agent 分开的配置目录')
    } finally {
      server.close()
    }
  })

  it('playwright：同一 Agent 的配置目录正被占用时改用临时配置', { timeout: 30_000, skip: !prepared && '没有准备好的浏览器' }, async () => {
    const agent = '6f1c8e5e-0000-4000-8000-00000000beef'
    const profile = path.join(data!, 'profiles', agent)
    mkdirSync(profile, { recursive: true })
    const lock = path.join(profile, 'SingletonLock')
    rmSync(lock, { force: true })
    symlinkSync(`test-host-${process.pid}`, lock)
    const child = spawn(process.execPath, [path.join(official, 'playwright/mcp/launch.mjs'), '--data', data!, '--agent', agent], { stdio: ['pipe', 'pipe', 'pipe'] })
    try {
      let stderr = ''
      await new Promise<void>((resolve, reject) => {
        child.stderr.on('data', (d) => {
          stderr += d
          if (stderr.includes('这次使用临时配置')) resolve()
        })
        child.once('exit', (code) => reject(new Error(`启动器退出 ${code}：${stderr}`)))
      })
      assert.match(stderr, /正被另一个浏览器使用/)
    } finally {
      child.kill('SIGTERM')
      rmSync(profile, { recursive: true, force: true })
    }
  })
})
