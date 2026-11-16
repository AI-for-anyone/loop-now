// The Agent sides of the official plugins are loaded and called in a real Pi (RPC mode): an in-process offline model
// (fixtures/pi-offline-provider.ts) makes scripted tool calls.
// Skipped when no Pi is available (LOOP_NOW_TEST_PI_ROOT points to the Pi package directory); playwright also needs a prepared data
// directory (LOOP_NOW_TEST_PLAYWRIGHT_DATA, see setup.mjs).

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
const noPi = !existsSync(piCli) && `${piCli} not found`

type Rec = { type?: string; id?: string; data?: { messages: { role: string; toolName?: string; isError?: boolean; content: { type: string; text?: string }[] }[] } }

interface PiRun {
  extensions: string[]
  calls: { name: string; arguments: Record<string, unknown> }[]
  env?: Record<string, string>
  mcp?: Record<string, unknown>
  noExtensions?: boolean
}

/** Runs one turn: sends a prompt, waits for agent_end, and returns the messages plus the context the model saw on each turn. */
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
      exited.then(() => Promise.reject(new Error(`Pi exited early: ${stderr.slice(0, 1500)}`))),
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

describe('The Agent side of the official plugins', { skip: noPi }, () => {
  it('hello: registers the hello tool and greets as the plugin config says', { timeout: 60_000 }, async () => {
    const { messages } = await runPi({
      extensions: [path.join(official, 'hello/agent/index.ts')],
      calls: [{ name: 'hello', arguments: { name: 'Loop' } }],
      env: { LOOP_NOW_PLUGIN_HELLO_CONFIG: '{"greeting":"Hi"}' },
    })
    const result = messages.find((m) => m.role === 'toolResult')!
    assert.equal(result.toolName, 'hello')
    assert.equal(result.content[0]!.text, 'Hi, Loop!')
  })

  it('playwright: the extension adds the browser rules; without a prepared browser the MCP launcher explains how to prepare one', { timeout: 60_000 }, async () => {
    const { seen } = await runPi({ extensions: [path.join(official, 'playwright/agent/index.ts')], calls: [] })
    assert.match(seen, /The dedicated browser \(tools starting with mcp__playwright_browser__\)/)
    assert.match(seen, /require the user's explicit consent first/)
    const launch = path.join(official, 'playwright/mcp/launch.mjs')
    const empty = mkdtempSync(path.join(tmpdir(), 'loop-now-pw-empty-'))
    try {
      const err = await promisify(execFile)(process.execPath, [launch, '--data', empty, '--agent', 'a1']).catch((e) => e as { code: number; stderr: string })
      assert.equal((err as { code: number }).code, 1)
      assert.match((err as { stderr: string }).stderr, /The dedicated browser is not prepared yet: on the server's machine, run node .*setup\.mjs/)
      const bad = await promisify(execFile)(process.execPath, [launch, '--data', 'relative', '--agent', 'a1']).catch((e) => e as { stderr: string })
      assert.match((bad as { stderr: string }).stderr, /--data must be the absolute path of the plugin data directory/)
    } finally {
      rmSync(empty, { recursive: true, force: true })
    }
  })

  const data = process.env.LOOP_NOW_TEST_PLAYWRIGHT_DATA
  const prepared = data && existsSync(path.join(data, 'browser/browser.json')) && existsSync(path.join(official, 'playwright/node_modules/@playwright/mcp'))
  it('playwright: the Agent opens a page through MCP with the prepared Chromium (headless)', { timeout: 120_000, skip: !prepared && 'no prepared browser (set LOOP_NOW_TEST_PLAYWRIGHT_DATA and run npm ci in the plugin folder)' }, async () => {
    const server = createServer((_req, res) => {
      res.setHeader('content-type', 'text/html; charset=utf-8')
      res.end('<!doctype html><title>Loop Now test page</title><h1>Hello, browser</h1>')
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    const url = `http://127.0.0.1:${(server.address() as { port: number }).port}/`
    const agent = '6f1c8e5e-0000-4000-8000-00000000c0de'
    try {
      // Same as the mcp.json the server merges and writes: the manifest's browser entry with placeholders replaced and relative paths made
      // absolute; the test adds --headless.
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
      assert.match(text(0), /Page Title: Loop Now test page/)
      assert.match(text(1), /Hello, browser/)
      assert.ok(existsSync(path.join(data!, 'profiles', agent)), 'a separate profile folder per Agent')
    } finally {
      server.close()
    }
  })

  it("playwright: uses a temporary profile while the same Agent's profile directory is in use", { timeout: 30_000, skip: !prepared && 'no prepared browser' }, async () => {
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
          if (stderr.includes('using a temporary profile this time')) resolve()
        })
        child.once('exit', (code) => reject(new Error(`the launcher exited with code ${code}: ${stderr}`)))
      })
      assert.match(stderr, /is in use by another browser/)
    } finally {
      child.kill('SIGTERM')
      rmSync(profile, { recursive: true, force: true })
    }
  })
})
