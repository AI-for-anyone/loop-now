// 壳里不依赖 Electron 运行的纯逻辑：重启计数、端口选择、日志轮转、导航分类、隔离配置、设置文件、托盘菜单。
import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, describe, test } from 'node:test'
import { classifyNavigation } from './links'
import { RotatingLog } from './log'
import { choosePort, portFree, portOf } from './port'
import { gitUsable } from './git'
import { profileArg, profileEnv, profileFromFile, resolveProfile } from './profile'
import { RestartPolicy, restartWindowMs } from './restart'
import { SettingsFile } from './settings'
import { stringsFor } from './strings'
import { trayItems } from './tray'

const dir = mkdtempSync(join(tmpdir(), 'loop-now-desktop-test-'))
after(() => rmSync(dir, { recursive: true, force: true }))

describe('崩溃重启', () => {
  test('5 分钟内依次等 1、5、15 秒，第 4 次停下', () => {
    const policy = new RestartPolicy()
    assert.deepEqual([policy.next(0), policy.next(1_000), policy.next(2_000), policy.next(3_000)], [1_000, 5_000, 15_000, null])
  })

  test('超出 5 分钟的崩溃不再计数，手动重启清零', () => {
    const policy = new RestartPolicy()
    policy.next(0)
    policy.next(1_000)
    policy.next(2_000)
    assert.equal(policy.next(restartWindowMs + 1_500), 5_000)
    policy.reset()
    assert.equal(policy.next(restartWindowMs + 2_000), 1_000)
  })
})

describe('端口', () => {
  test('想用的端口空闲就用它，被占用或为 0 时交给系统选', async () => {
    const holder = createServer()
    await new Promise<void>((resolve) => holder.listen(0, '127.0.0.1', resolve))
    const taken = (holder.address() as { port: number }).port
    try {
      assert.equal(await portFree(taken), false)
      assert.equal(await choosePort(taken), 0)
    } finally {
      await new Promise((resolve) => holder.close(resolve))
    }
    assert.equal(await choosePort(taken), taken)
    assert.equal(await choosePort(0), 0)
  })

  test('从就绪文件的地址取端口', () => {
    assert.equal(portOf('http://127.0.0.1:8781'), 8781)
    assert.equal(portOf('http://127.0.0.1'), undefined)
  })
})

describe('日志', () => {
  test('超过上限就轮转，只保留 keep 份', () => {
    const file = join(dir, 'logs', 'server.log')
    const log = new RotatingLog(file, 10, 2)
    for (const chunk of ['aaaaaaaa\n', 'bbbbbbbb\n', 'cccccccc\n', 'dddddddd\n']) log.write(chunk)
    log.close()
    assert.equal(readFileSync(file, 'utf8'), 'dddddddd\n')
    assert.equal(readFileSync(`${file}.1`, 'utf8'), 'cccccccc\n')
    assert.equal(readFileSync(`${file}.2`, 'utf8'), 'bbbbbbbb\n')
    assert.equal(existsSync(`${file}.3`), false)
  })

  test('末尾几行只算这次运行', () => {
    const log = new RotatingLog(join(dir, 'logs', 'tail.log'))
    log.write('old\n')
    log.resetTail()
    log.write('one\ntwo\nthree\n')
    log.close()
    assert.equal(log.tail(2), 'two\nthree')
  })
})

describe('导航', () => {
  const origin = 'http://127.0.0.1:8780'
  test('服务端自己的源留在窗口里，http、https、mailto 交给系统浏览器，其余不处理', () => {
    assert.equal(classifyNavigation('http://127.0.0.1:8780/projects', origin), 'app')
    assert.equal(classifyNavigation('http://127.0.0.1:8781/', origin), 'external')
    assert.equal(classifyNavigation('https://github.com/AI-for-anyone/loop-now', origin), 'external')
    assert.equal(classifyNavigation('mailto:someone@example.com', origin), 'external')
    assert.equal(classifyNavigation('file:///etc/passwd', origin), 'deny')
    assert.equal(classifyNavigation('javascript:alert(1)', origin), 'deny')
    assert.equal(classifyNavigation('not a url', origin), 'deny')
  })

  test('新窗口不传源，同源页面也交给系统浏览器', () => {
    assert.equal(classifyNavigation('http://127.0.0.1:8780/projects'), 'external')
  })
})

describe('隔离配置', () => {
  test('命令行参数优先于环境变量', () => {
    assert.equal(profileArg(['electron', '.', '--profile=try'], { LOOP_NOW_DESKTOP_PROFILE: 'env' }), 'try')
    assert.equal(profileArg(['electron', '.'], { LOOP_NOW_DESKTOP_PROFILE: 'env' }), 'env')
    assert.equal(profileArg(['electron', '.'], {}), undefined)
  })

  test('名字放在自己的位置，工作根在 HOME 之外', () => {
    const p = resolveProfile('try', '/Users/someone')
    assert.equal(p.label, 'try')
    assert.equal(p.dataDir, '/Users/someone/Library/Application Support/Loop Now Profiles/try/data')
    assert.equal(p.workRoot, '/Users/Shared/loop-now-profiles/try')
    assert.deepEqual(profileEnv(p), {
      LOOP_NOW_DATA_DIR: p.dataDir,
      LOOP_NOW_CONTEXT_DIR: '/Users/someone/Library/Application Support/Loop Now Profiles/try/context',
      LOOP_NOW_WORK_ROOT: p.workRoot,
    })
  })

  test('绝对路径时全部放在它下面', () => {
    const p = resolveProfile('/private/tmp/x/')
    assert.deepEqual([p.label, p.root], ['x', '/private/tmp/x'])
    assert.deepEqual([p.shellDir, p.logDir, p.dataDir, p.contextDir, p.workRoot], ['/private/tmp/x/shell', '/private/tmp/x/logs', '/private/tmp/x/data', '/private/tmp/x/context', '/private/tmp/x/work'])
  })

  test('从 Finder 打开时读 desktop.json 的 profile；没有或格式不对时为空', () => {
    const file = join(dir, 'desktop.json')
    assert.equal(profileFromFile(file), undefined)
    writeFileSync(file, JSON.stringify({ profile: 'try' }))
    assert.equal(profileFromFile(file), 'try')
    writeFileSync(file, JSON.stringify({ profile: 3 }))
    assert.equal(profileFromFile(file), undefined)
  })

  test('不合规的名字报错', () => {
    assert.throws(() => resolveProfile('../live'))
    assert.throws(() => resolveProfile('Try'))
  })
})

describe('设置文件', () => {
  test('读不到或格式不对时为空，更新时合并', () => {
    const file = join(dir, 'settings', 'settings.json')
    const settings = new SettingsFile(file)
    assert.deepEqual(settings.read(), {})
    settings.update({ port: 8780 })
    assert.deepEqual(settings.read(), { port: 8780 })
    writeFileSync(file, 'not json')
    assert.deepEqual(settings.read(), {})
  })
})

describe('托盘菜单', () => {
  const s = stringsFor('zh-CN')
  const labels = (items: ReturnType<typeof trayItems>) => items.map((i) => (i === 'separator' ? '—' : `${i.label}${i.enabled ? '' : '（不可点）'}`))

  test('运行中：状态行带主机和端口，可在浏览器打开', () => {
    assert.deepEqual(labels(trayItems('running', 'http://127.0.0.1:8780', undefined, s)), [
      '运行中 · 127.0.0.1:8780（不可点）',
      '—',
      '打开窗口',
      '在默认浏览器打开',
      '—',
      '查看日志',
      '重启服务',
      '—',
      '退出 Loop Now',
    ])
  })

  test('启动中不能在浏览器打开、不能重启；隔离配置在状态行带名字', () => {
    const items = labels(trayItems('starting', undefined, 'try', s))
    assert.equal(items[0], '正在启动… (try)（不可点）')
    assert.equal(items[3], '在默认浏览器打开（不可点）')
    assert.equal(items[6], '重启服务（不可点）')
  })

  test('多次崩溃停下后可以手动重启', () => {
    const items = labels(trayItems('crashed', undefined, undefined, s))
    assert.equal(items[0], '已停止：多次崩溃（不可点）')
    assert.equal(items[6], '重启服务')
  })

  test('中英文两套文字同形', () => {
    const en = stringsFor('en-US')
    assert.equal(stringsFor('zh-Hans-CN'), s)
    assert.deepEqual(Object.keys(en.tray), Object.keys(s.tray))
    assert.equal(labels(trayItems('degraded', 'http://127.0.0.1:9000', undefined, en))[0], 'Database unavailable · 127.0.0.1:9000（不可点）')
  })
})

describe('git 检测', () => {
  test('xcode-select -p 失败时不运行 git（它只是占位，会弹系统的安装框）', async () => {
    const calls: string[] = []
    const usable = await gitUsable(async (file) => {
      calls.push(file)
      return false
    })
    assert.equal(usable, false)
    assert.deepEqual(calls, ['/usr/bin/xcode-select'])
  })

  test('有命令行工具时以 git --version 为准', async () => {
    assert.equal(await gitUsable(async () => true), true)
    assert.equal(await gitUsable(async (file) => file !== '/usr/bin/git'), false)
  })
})
