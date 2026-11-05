// 用 test/fakeserver（Go 写的最小服务端：监听、写就绪文件、回答 /healthz）真的拉起、停掉、清理孤儿，在当前系统上跑：
// macOS／Linux 走 SIGTERM，Windows 走 taskkill。没有 go 时跳过，不算通过。
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
    console.log(`跳过服务端进程测试：编不出假服务端（${(err as Error).message.split('\n')[0]}）`)
    return false
  }
}

const make = (name: string, extraEnv: NodeJS.ProcessEnv = {}) => {
  const notes: string[] = []
  const states: ServerState[] = []
  const log = new RotatingLog(join(dir, name, 'server.log'))
  const server = new ServerProcess({
    bin,
    readyFile: join(dir, name, 'run', 'server-ready.json'),
    stampFile: join(dir, name, 'run', 'server-stamp.json'),
    env: { ...process.env, ...extraEnv },
    log,
    note: (text) => notes.push(text),
    choosePort: async () => 0,
  })
  server.on('state', (state) => states.push(state))
  return { server, notes, states, log, readyFile: join(dir, name, 'run', 'server-ready.json'), stampFile: join(dir, name, 'run', 'server-stamp.json') }
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

describe('服务端进程', () => {
  let available = false
  before(() => {
    available = buildFake()
  })

  test('拉起、就绪、健康、记下启动时刻，再停掉', async (t) => {
    if (!available) return t.skip('没有 go')
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
    assert.ok(info, 'processInfo 应查得到服务端')
    assert.equal(info.started, stamp.started)
    assert.equal(process.platform === 'win32' ? info.command.toLowerCase() : info.command, process.platform === 'win32' ? bin.toLowerCase() : bin)

    await server.stop()
    assert.equal(server.state, 'stopped')
    assert.equal(server.alive, false)
    assert.equal(await waitGone(stamp.pid, 5_000), true)
    assert.deepEqual(states, ['starting', 'running', 'stopping', 'stopped'])
    assert.ok(!notes.some((n) => n.includes('强制结束')), `应是自己退出（macOS SIGTERM，Windows 关 stdin）：${notes.join('\n')}`)
    // Windows：服务端是读到 stdin EOF 后自己停的（假服务端会写一句到 stderr）。
    if (process.platform === 'win32') assert.match(log.tail(5), /stdin closed, shutting down/)
  })

  test('关掉 stdin 时服务端自己优雅退出（LOOP_NOW_STOP_ON_STDIN_CLOSE=1，Windows 上壳靠它停服务端）', async (t) => {
    if (!available) return t.skip('没有 go')
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
    assert.equal(existsSync(readyFile), false, '优雅退出时删掉就绪文件')
  })

  test('启动时清理上次留下的孤儿：就绪文件里的 pid 正是这个可执行文件时停掉它', async (t) => {
    if (!available) return t.skip('没有 go')
    const { server, notes, readyFile } = make('b')
    // 模拟壳上次异常退出：直接拉一个服务端，让它把就绪文件写在壳要读的位置。
    const orphan = spawn(bin, [], { env: { ...process.env, LOOP_NOW_LISTEN_ADDR: '127.0.0.1:0', LOOP_NOW_READY_FILE: readyFile }, stdio: 'ignore', windowsHide: true })
    const orphanExit = new Promise<void>((resolve) => orphan.once('exit', () => resolve()))
    for (let i = 0; i < 100 && !existsSync(readyFile); i++) await sleep(100)
    assert.ok(existsSync(readyFile), '孤儿应写出就绪文件')
    assert.equal(alive(orphan.pid!), true)

    const ready = untilReady(server)
    await server.start()
    await ready
    assert.ok(notes.some((n) => n.includes(`停掉上次留下的服务端（pid ${orphan.pid}）`)), notes.join('\n'))
    await orphanExit
    assert.equal(alive(orphan.pid!), false)
    await server.stop()
  })

  test('就绪文件里的 pid 是别的程序时不动它', async (t) => {
    if (!available) return t.skip('没有 go')
    const { server, notes, readyFile } = make('c')
    // 自己（node）就是一个"别的程序"。
    mkdirSync(join(readyFile, '..'), { recursive: true })
    writeFileSync(readyFile, JSON.stringify({ url: 'http://127.0.0.1:1', pid: process.pid, version: 'x' }))
    const ready = untilReady(server)
    await server.start()
    await ready
    assert.ok(!notes.some((n) => n.includes('停掉上次留下的服务端')), notes.join('\n'))
    await server.stop()
  })

  test('就绪前退出：退出码 2 算配置错误，不重启', async (t) => {
    if (!available) return t.skip('没有 go')
    const { server } = make('d', { LOOP_NOW_FAKE_EXIT_CODE: '2' })
    const failed = untilState(server, 'failed')
    await server.start()
    await failed
    assert.deepEqual(server.failure, { kind: 'config' })
    assert.equal(server.alive, false)
  })
})
