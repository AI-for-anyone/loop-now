// Uses test/fakeserver (a minimal server written in Go: listens, writes the ready file, answers /healthz) to really launch, stop and clean
// up orphans, on the current OS: macOS/Linux go through SIGTERM, Windows through taskkill. Skipped when go is missing, which does not count
// as passing.
import assert from 'node:assert/strict'
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import { after, before, describe, test } from 'node:test'
import { RotatingLog } from './log'
import { alive, processInfo, waitGone } from './process'
import { ServerProcess, type ServerState } from './server'

const dir = mkdtempSync(join(tmpdir(), 'loop-now-desktop-server-'))
after(() => rmSync(dir, { recursive: true, force: true }))

const exe = process.platform === 'win32' ? '.exe' : ''
const bin = join(dir, 'server', `loop-server${exe}`)

function buildFake(): boolean {
  try {
    execFileSync('go', ['build', '-o', bin, './test/fakeserver'], { cwd: join(__dirname, '..'), stdio: 'pipe', env: { ...process.env, CGO_ENABLED: '0' } })
    return true
  } catch (err) {
    console.log(`Skipping the server process tests: could not build the fake server (${(err as Error).message.split('\n')[0]})`)
    return false
  }
}

const make = (name: string, extraEnv: NodeJS.ProcessEnv = {}, external = false) => {
  const notes: string[] = []
  const states: ServerState[] = []
  const log = new RotatingLog(join(dir, name, 'server.log'))
  const tlsDir = join(dir, name, 'tls')
  const server = new ServerProcess({
    bin,
    readyFile: join(dir, name, 'run', 'server-ready.json'),
    stampFile: join(dir, name, 'run', 'server-stamp.json'),
    env: { ...process.env, ...extraEnv },
    log,
    note: (text) => notes.push(text),
    choosePort: async () => 0,
    external: external ? { tlsDir, choosePort: async () => 0 } : undefined,
  })
  server.on('state', (state) => states.push(state))
  return { server, notes, states, log, tlsDir, readyFile: join(dir, name, 'run', 'server-ready.json'), stampFile: join(dir, name, 'run', 'server-stamp.json') }
}

const untilReady = (server: ServerProcess) => new Promise<string>((resolve) => server.once('ready', resolve))
const untilState = (server: ServerProcess, wanted: ServerState) =>
  new Promise<void>((resolve) => {
    if (server.state === wanted) return resolve()
    const listener = (state: ServerState) => {
      if (state !== wanted) return
      server.off('state', listener)
      resolve()
    }
    server.on('state', listener)
  })

describe('Server process', () => {
  let available = false
  before(() => {
    available = buildFake()
  })

  test('Starts, becomes ready and healthy, records the start time, then stops', async (t) => {
    if (!available) return t.skip('go is not installed')
    const { server, notes, states, stampFile, log } = make('a')
    const ready = untilReady(server)
    await server.start()
    const url = await ready
    assert.match(url, /^http:\/\/127\.0\.0\.1:\d+$/)
    assert.equal(server.state, 'running')
    assert.equal(server.alive, true)
    const response = await fetch(new URL('/healthz', url))
    await response.body?.cancel()
    assert.equal(response.status, 200)
    const stamp = JSON.parse(readFileSync(stampFile, 'utf8')) as { pid: number; started: string }
    const info = await processInfo(stamp.pid)
    assert.ok(info, 'processInfo should find the server')
    assert.equal(info.started, stamp.started)
    assert.equal(process.platform === 'win32' ? info.command.toLowerCase() : info.command, process.platform === 'win32' ? bin.toLowerCase() : bin)

    await server.stop()
    assert.equal(server.state, 'stopped')
    assert.equal(server.alive, false)
    assert.equal(await waitGone(stamp.pid, 5_000), true)
    assert.deepEqual(states, ['starting', 'running', 'stopping', 'stopped'])
    assert.ok(!notes.some((n) => n.includes('force-killing')), `should have exited by itself (SIGTERM on macOS, stdin closed on Windows): ${notes.join('\n')}`)
    // Windows: the server stopped by itself after reading EOF on stdin (the fake server writes a line to stderr).
    if (process.platform === 'win32') assert.match(log.tail(5), /stdin closed, shutting down/)
  })

  test('With the external TLS port: passes the listen address and the self-signed certificate directory to the server, reads the https address back from the ready file, and clears it after stopping', async (t) => {
    if (!available) return t.skip('go is not installed')
    const { server, notes, tlsDir, readyFile } = make('f', {}, true)
    const ready = untilReady(server)
    await server.start()
    const url = await ready
    assert.match(url, /^http:\/\/127\.0\.0\.1:\d+$/)
    assert.match(server.externalUrl ?? '', /^https:\/\/127\.0\.0\.1:\d+$/)
    assert.notEqual(new URL(server.externalUrl!).port, new URL(url).port)
    const written = JSON.parse(readFileSync(readyFile, 'utf8')) as { externalUrl?: string }
    assert.equal(written.externalUrl, server.externalUrl)
    // The fake server puts a certificate in the directory, as the real server does.
    assert.ok(existsSync(join(tlsDir, 'cert.pem')), 'the certificate directory should have been passed to the server')
    assert.ok(notes.some((n) => n.includes('external TLS port chosen by the system')), notes.join('\n'))
    assert.ok(notes.some((n) => n.includes(`external TLS port ${server.externalUrl}`)), notes.join('\n'))
    await server.stop()
    assert.equal(server.externalUrl, undefined)
  })

  test('Without the external port, none of its variables are passed to the server, and there is no https address once ready', async (t) => {
    if (!available) return t.skip('go is not installed')
    const { server, tlsDir } = make('g')
    const ready = untilReady(server)
    await server.start()
    await ready
    assert.equal(server.externalUrl, undefined)
    assert.equal(existsSync(tlsDir), false)
    await server.stop()
  })

  test('Closing stdin makes the server exit gracefully by itself (LOOP_NOW_STOP_ON_STDIN_CLOSE=1; on Windows the shell stops the server this way)', async (t) => {
    if (!available) return t.skip('go is not installed')
    const readyFile = join(dir, 'e', 'server-ready.json')
    const child = spawn(bin, [], {
      env: { ...process.env, LOOP_NOW_LISTEN_ADDR: '127.0.0.1:0', LOOP_NOW_READY_FILE: readyFile, LOOP_NOW_STOP_ON_STDIN_CLOSE: '1' },
      stdio: ['pipe', 'ignore', 'ignore'],
      windowsHide: true,
    })
    const exit = new Promise<{ code: number | null; signal: string | null }>((resolve) => child.once('exit', (code, signal) => resolve({ code, signal })))
    for (let i = 0; i < 100 && !existsSync(readyFile); i++) await sleep(100)
    assert.ok(existsSync(readyFile))
    child.stdin!.end()
    const result = await Promise.race([exit, sleep(10_000).then(() => 'timeout' as const)])
    assert.deepEqual(result, { code: 0, signal: null })
    assert.equal(existsSync(readyFile), false, 'A graceful exit deletes the ready file')
  })

  test('At startup, cleans up an orphan left from last time: stops the pid in the ready file when it is this very executable', async (t) => {
    if (!available) return t.skip('go is not installed')
    const { server, notes, readyFile } = make('b')
    // Simulates the shell having exited abnormally last time: launch a server directly and have it write the ready file where the shell
    // will read it.
    const orphan = spawn(bin, [], { env: { ...process.env, LOOP_NOW_LISTEN_ADDR: '127.0.0.1:0', LOOP_NOW_READY_FILE: readyFile }, stdio: 'ignore', windowsHide: true })
    const orphanExit = new Promise<void>((resolve) => orphan.once('exit', () => resolve()))
    for (let i = 0; i < 100 && !existsSync(readyFile); i++) await sleep(100)
    assert.ok(existsSync(readyFile), 'the orphan should write its ready file')
    assert.equal(alive(orphan.pid!), true)

    const ready = untilReady(server)
    await server.start()
    await ready
    assert.ok(notes.some((n) => n.includes(`Stopping the server left over from the last run (pid ${orphan.pid})`)), notes.join('\n'))
    await orphanExit
    assert.equal(alive(orphan.pid!), false)
    await server.stop()
  })

  test('Leaves the pid in the ready file alone when it belongs to another program', async (t) => {
    if (!available) return t.skip('go is not installed')
    const { server, notes, readyFile } = make('c')
    // This process (node) is itself "another program".
    mkdirSync(join(readyFile, '..'), { recursive: true })
    writeFileSync(readyFile, JSON.stringify({ url: 'http://127.0.0.1:1', pid: process.pid, version: 'x' }))
    const ready = untilReady(server)
    await server.start()
    await ready
    assert.ok(!notes.some((n) => n.includes('Stopping the server left over from the last run')), notes.join('\n'))
    await server.stop()
  })

  test('Exiting before ready: exit code 2 is a configuration error and is not restarted', async (t) => {
    if (!available) return t.skip('go is not installed')
    const { server } = make('d', { LOOP_NOW_FAKE_EXIT_CODE: '2' })
    const failed = untilState(server, 'failed')
    await server.start()
    await failed
    assert.deepEqual(server.failure, { kind: 'config' })
    assert.equal(server.alive, false)
  })
})
