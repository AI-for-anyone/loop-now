// 壳里不依赖 Electron 运行的纯逻辑：重启计数、端口选择、日志轮转、导航分类、隔离配置、设置文件、托盘菜单。
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, describe, test } from 'node:test'
import type { AppUpdater } from 'electron-updater'
import { GitHubProvider } from 'electron-updater/out/providers/GitHubProvider'
import type { ProviderRuntimeOptions } from 'electron-updater/out/providers/Provider'
import { parseBadge, parseNotification } from './bridge'
import { installScript, linkState } from './cli'
import { classifyNavigation, handOffOnce } from './links'
import { RotatingLog } from './log'
import { choosePort, portFree, portOf } from './port'
import { gitUsable } from './git'
import { defaultProfile, profileArg, profileEnv, profileFromFile, resolveProfile } from './profile'
import { RestartPolicy, restartWindowMs } from './restart'
import { SettingsFile } from './settings'
import { stringsFor } from './strings'
import { trayItems } from './tray'
import { UpdateChecker, type Updater } from './updates'

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

  test('授权弹窗只交一次：同一次导航的 did-start-navigation 与 will-navigate 只打开一个页面，about:blank 不算', () => {
    const opened: string[] = []
    const handOff = handOffOnce((url) => opened.push(url))
    const url = 'https://auth.openai.com/oauth/authorize?state=x'
    handOff('about:blank')
    handOff(url)
    handOff(url)
    handOff('https://auth.openai.com/log-in')
    assert.deepEqual(opened, [url])
  })
})

describe('配置', () => {
  test('命令行参数优先于环境变量', () => {
    assert.equal(profileArg(['electron', '.', '--profile=try'], { LOOP_NOW_DESKTOP_PROFILE: 'env' }), 'try')
    assert.equal(profileArg(['electron', '.'], { LOOP_NOW_DESKTOP_PROFILE: 'env' }), 'env')
    assert.equal(profileArg(['electron', '.'], {}), undefined)
  })

  test('名字放在壳自己的位置，工作根在 HOME 之外', () => {
    const p = resolveProfile('try', '/Users/someone')
    assert.equal(p.label, 'try')
    assert.equal(p.shellDir, '/Users/someone/Library/Application Support/Loop Now/try/shell')
    assert.equal(p.logDir, '/Users/someone/Library/Logs/Loop Now/try')
    assert.deepEqual(profileEnv(p), {
      LOOP_NOW_DATA_DIR: '/Users/someone/Library/Application Support/Loop Now/try/data',
      LOOP_NOW_CONTEXT_DIR: '/Users/someone/Library/Application Support/Loop Now/try/context',
      LOOP_NOW_WORK_ROOT: '/Users/Shared/loop-now-desktop/try',
    })
  })

  test('默认配置也是一个配置，从不落到服务端的默认目录', () => {
    const p = resolveProfile(defaultProfile, '/Users/someone')
    assert.equal(p.dataDir, '/Users/someone/Library/Application Support/Loop Now/default/data')
    assert.equal(p.contextDir, '/Users/someone/Library/Application Support/Loop Now/default/context')
    assert.equal(p.workRoot, '/Users/Shared/loop-now-desktop/default')
    for (const d of [p.dataDir, p.contextDir, p.workRoot]) assert.ok(!d.includes('.loop_now') && !d.startsWith('/Users/Shared/loop-now/'))
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
    assert.deepEqual(labels(trayItems('running', 'http://127.0.0.1:8780', defaultProfile, s)), [
      '运行中 · 127.0.0.1:8780（不可点）',
      '配置：默认（不可点）',
      '—',
      '打开窗口',
      '在默认浏览器打开',
      '—',
      '查看日志',
      '安装命令行工具…',
      '重启服务',
      '—',
      '退出 Loop Now',
    ])
  })

  test('启动中不能在浏览器打开、不能重启；第二行是当前配置', () => {
    const items = labels(trayItems('starting', undefined, 'try', s))
    assert.deepEqual(items.slice(0, 2), ['正在启动…（不可点）', '配置：try（不可点）'])
    assert.equal(items[4], '在默认浏览器打开（不可点）')
    assert.equal(items[8], '重启服务（不可点）')
  })

  test('多次崩溃停下后可以手动重启', () => {
    const items = labels(trayItems('crashed', undefined, undefined, s))
    assert.equal(items[0], '已停止：多次崩溃（不可点）')
    assert.equal(items[8], '重启服务')
  })

  test('开发时（没打包）不能安装命令行工具', () => {
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', undefined, s, false))[7], '安装命令行工具…（不可点）')
  })

  test('中英文两套文字同形', () => {
    const en = stringsFor('en-US')
    assert.equal(stringsFor('zh-Hans-CN'), s)
    assert.deepEqual(Object.keys(en.tray), Object.keys(s.tray))
    assert.deepEqual(labels(trayItems('degraded', 'http://127.0.0.1:9000', defaultProfile, en)).slice(0, 2), ['Database unavailable · 127.0.0.1:9000（不可点）', 'Profile: default（不可点）'])
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

describe('页面发来的通知与未读数', () => {
  const ok = { id: 'm1', title: '待你回复：审查员', body: '要合并吗？', path: '/agents/a/chat' }

  test('合规的通知原样收下，过长的标题与正文截断', () => {
    assert.deepEqual(parseNotification(ok), ok)
    const long = parseNotification({ ...ok, title: 'x'.repeat(500), body: 'y'.repeat(900) })
    assert.equal(long?.title.length, 200)
    assert.equal(long?.body.length, 500)
  })

  test('地址必须是控制台里的路径，其余丢掉', () => {
    for (const path of ['https://evil.example', '//evil.example/x', '/\\evil.example', 'agents/a', '']) assert.equal(parseNotification({ ...ok, path }), undefined, path)
    assert.equal(parseNotification({ ...ok, id: '' }), undefined)
    assert.equal(parseNotification('not an object'), undefined)
  })

  test('未读数是非负整数', () => {
    assert.equal(parseBadge(3), 3)
    assert.equal(parseBadge(0), 0)
    for (const v of [-1, 1.5, '3', null]) assert.equal(parseBadge(v), undefined)
  })
})

describe('安装命令行工具', () => {
  test('看出链接是不存在、已指向自带的 loopctl，还是别的', async () => {
    const target = '/Applications/Loop Now.app/Contents/Resources/bin/loopctl'
    const link = join(dir, 'cli', 'loopctl')
    mkdirSync(join(dir, 'cli'), { recursive: true })
    assert.deepEqual(await linkState(link, target), { state: 'missing' })
    symlinkSync(target, link)
    assert.deepEqual(await linkState(link, target), { state: 'ours' })
    rmSync(link)
    symlinkSync('/opt/homebrew/bin/loopctl', link)
    assert.deepEqual(await linkState(link, target), { state: 'other', points: '/opt/homebrew/bin/loopctl' })
    rmSync(link)
    writeFileSync(link, '')
    assert.deepEqual(await linkState(link, target), { state: 'other' })
  })

  test('AppleScript 里的路径转义后用 quoted form 交给 shell', () => {
    assert.equal(
      installScript('/Applications/Loop "Now".app/x\\y'),
      'do shell script "mkdir -p " & quoted form of "/usr/local/bin" & " && ln -sfn " & quoted form of "/Applications/Loop \\"Now\\".app/x\\\\y" & " " & quoted form of "/usr/local/bin/loopctl" with administrator privileges',
    )
  })
})

describe('检查更新', () => {
  const fake = () => {
    let listener: ((info: { version: string }) => void) | undefined
    const updater: Updater & { found: (version: string) => void; fail?: Error } = {
      autoDownload: true,
      autoInstallOnAppQuit: true,
      allowPrerelease: false,
      allowDowngrade: true,
      channel: null,
      logger: null,
      on: (_event, l) => (listener = l),
      checkForUpdates: async () => {
        if (updater.fail) throw updater.fail
      },
      found: (version) => listener?.({ version }),
    }
    return updater
  }

  // 打包配置的 publish 会写进 app-update.yml，提供方的 options 就是它。读配置要求有发行包目录，读的时候临时给一个。
  const publish = () => {
    const saved = process.env.LOOP_NOW_DESKTOP_BUNDLE
    process.env.LOOP_NOW_DESKTOP_BUNDLE = dir
    try {
      return require('../electron-builder.config.js').publish
    } finally {
      if (saved === undefined) delete process.env.LOOP_NOW_DESKTOP_BUNDLE
      else process.env.LOOP_NOW_DESKTOP_BUNDLE = saved
    }
  }

  // 用 electron-updater 真实的 GitHub 提供方读一份假的 releases.atom（tags 从新到旧）：每个 release 里只有 latest-mac.yml，
  // 别的文件一律 404。updater 的设置来自 UpdateChecker，提供方的 options 来自打包配置。
  const latestRelease = async (current: string, tags: string[]) => {
    const updater = fake()
    new UpdateChecker(updater, () => undefined)
    const fetched: string[] = []
    const entry = (tag: string) => `<entry><title>${tag}</title><link rel="alternate" type="text/html" href="https://github.com/AI-for-anyone/loop-now/releases/tag/${tag}"/><content>notes</content></entry>`
    const executor = {
      request: async ({ path }: { path: string }) => {
        fetched.push(path.replace('/AI-for-anyone/loop-now/releases', ''))
        if (path.endsWith('.atom')) return `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">${tags.map(entry).join('')}</feed>`
        const version = /\/download\/v([^/]+)\/latest-mac\.yml$/.exec(path)?.[1]
        if (version) return `version: ${version}\npath: update.zip\nsha512: x\nfiles: [{ url: update.zip, sha512: x }]\n`
        throw new Error(`404 ${path}`)
      },
    }
    // 提供方只用到 updater 的 allowPrerelease、channel 与当前版本（semver 也认字符串）。
    const provider = new GitHubProvider(publish(), { ...updater, currentVersion: current, fullChangelog: false } as unknown as AppUpdater, {
      executor: executor as unknown as ProviderRuntimeOptions['executor'],
      platform: 'darwin',
      isUseMultipleRangeRequest: true,
    })
    const { version } = await provider.getLatestVersion()
    return { version, fetched }
  }

  test('正式版与预发布都检查、不设通道、不自动下载安装、不降级；同一版本只提示一次', () => {
    const updater = fake()
    const checker = new UpdateChecker(updater, () => undefined)
    assert.deepEqual([updater.autoDownload, updater.autoInstallOnAppQuit, updater.allowPrerelease, updater.allowDowngrade, updater.channel], [false, false, true, false, null])
    assert.equal(publish().channel, undefined)
    const seen: string[] = []
    checker.on('available', (v) => seen.push(v))
    updater.found('0.2.0')
    updater.found('0.2.0')
    updater.found('0.2.1')
    assert.deepEqual(seen, ['0.2.0', '0.2.1'])
    assert.equal(checker.available, '0.2.1')
  })

  test('检查失败只记日志', async () => {
    const updater = fake()
    const notes: string[] = []
    updater.fail = new Error('找不到 latest-mac.yml')
    await new UpdateChecker(updater, (n) => notes.push(n)).check()
    assert.deepEqual(notes, ['检查更新失败：找不到 latest-mac.yml'])
  })

  test('已装正式版：最新的是预发布或正式版都检查得到', async () => {
    // 预发布的 release 先找 rc-mac.yml，没有就读 latest-mac.yml。
    assert.deepEqual(await latestRelease('0.2.0', ['v0.3.0-rc.1', 'v0.2.1', 'v0.2.0']), {
      version: '0.3.0-rc.1',
      fetched: ['.atom', '/download/v0.3.0-rc.1/rc-mac.yml', '/download/v0.3.0-rc.1/latest-mac.yml'],
    })
    assert.deepEqual(await latestRelease('0.2.0', ['v0.2.1', 'v0.2.1-rc.1', 'v0.2.0']), {
      version: '0.2.1',
      fetched: ['.atom', '/download/v0.2.1/latest-mac.yml'],
    })
  })

  test('有新版本时菜单栏多一项去下载页', () => {
    const items = trayItems('running', 'http://127.0.0.1:8780', undefined, stringsFor('zh-CN'), true, '0.2.0')
    assert.deepEqual(items[2], { label: '有新版本 0.2.0，去下载…', enabled: true, action: 'update' })
  })
})
