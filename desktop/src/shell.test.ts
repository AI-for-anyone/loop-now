// 壳里不依赖 Electron 运行的纯逻辑：重启计数、端口选择、日志轮转、导航分类、隔离配置、设置文件、托盘菜单，以及 Windows 的分支
// （路径、git 检测、PATH 安装、进程核对、未读数）——平台通过参数注入，在哪个系统上都跑同一套。
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, describe, test } from 'node:test'
import type { AppUpdater } from 'electron-updater'
import { GitHubProvider } from 'electron-updater/out/providers/GitHubProvider'
import type { ProviderRuntimeOptions } from 'electron-updater/out/providers/Provider'
import { badgePlan, dotBitmap } from './badge'
import { parseBadge, parseNotification } from './bridge'
import { addToUserPathScript, installScript, linkState, userPathHas } from './cli'
import { externalCertPath, loopctlConfigCommand } from './external'
import { classifyNavigation, handOffOnce } from './links'
import { RotatingLog } from './log'
import { badgeStyle, cliBinary, cliInstallMode, serverBinary, trayIcon, windowChrome, windowsWorkRootBase } from './platform'
import { choosePort, portFree, portOf } from './port'
import { isOurServer, parseWindowsProcess, spawnPlan, taskkillArgs, windowsProcessQuery } from './process'
import { findInPath, gitUsable } from './git'
import { defaultProfile, overridesFromFile, profileArg, profileEnv, profileFromFile, profileSummary, resolveProfile } from './profile'
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
    const p = resolveProfile('try', '/Users/someone', {}, 'darwin')
    assert.equal(p.label, 'try')
    assert.equal(p.shellDir, '/Users/someone/Library/Application Support/Loop Now/try/shell')
    assert.equal(p.logDir, '/Users/someone/Library/Logs/Loop Now/try')
    assert.deepEqual(profileEnv(p), {
      LOOP_NOW_DATA_DIR: '/Users/someone/Library/Application Support/Loop Now/try/data',
      LOOP_NOW_CONTEXT_DIR: '/Users/someone/Library/Application Support/Loop Now/try/context',
      LOOP_NOW_WORK_ROOT: '/Users/Shared/loop-now-desktop/try',
    })
  })

  test('默认配置也是一个配置，没有指定目录时从不落到服务端的默认目录', () => {
    const p = resolveProfile(defaultProfile, '/Users/someone', {}, 'darwin')
    assert.equal(p.dataDir, '/Users/someone/Library/Application Support/Loop Now/default/data')
    assert.equal(p.contextDir, '/Users/someone/Library/Application Support/Loop Now/default/context')
    assert.equal(p.workRoot, '/Users/Shared/loop-now-desktop/default')
    assert.deepEqual(p.overrides, {})
    for (const d of [p.dataDir, p.contextDir, p.workRoot]) assert.ok(!d.includes('.loop_now') && !d.startsWith('/Users/Shared/loop-now/'))
    assert.equal(
      profileSummary(p),
      '配置 default，数据目录 /Users/someone/Library/Application Support/Loop Now/default/data，上下文目录 /Users/someone/Library/Application Support/Loop Now/default/context，工作根 /Users/Shared/loop-now-desktop/default',
    )
  })

  test('desktop.json 指定的目录换掉默认位置，交给服务端，日志首行标出', () => {
    const p = resolveProfile(defaultProfile, '/Users/someone', { workRoot: '/Users/Shared/loop-now' }, 'darwin')
    assert.deepEqual(profileEnv(p), {
      LOOP_NOW_DATA_DIR: '/Users/someone/Library/Application Support/Loop Now/default/data',
      LOOP_NOW_CONTEXT_DIR: '/Users/someone/Library/Application Support/Loop Now/default/context',
      LOOP_NOW_WORK_ROOT: '/Users/Shared/loop-now',
    })
    assert.equal(p.shellDir, '/Users/someone/Library/Application Support/Loop Now/default/shell')
    assert.match(profileSummary(p), /，工作根 \/Users\/Shared\/loop-now（desktop\.json 指定）$/)
    assert.doesNotMatch(profileSummary(p), /data（desktop\.json 指定）/)

    const all = resolveProfile('/private/tmp/x', undefined, { workRoot: '/Users/Shared/w', dataDir: '/Users/someone/.loop_now', contextDir: '/Users/someone/.loop_now/context' }, 'darwin')
    assert.deepEqual([all.shellDir, all.logDir, all.dataDir, all.contextDir, all.workRoot], ['/private/tmp/x/shell', '/private/tmp/x/logs', '/Users/someone/.loop_now', '/Users/someone/.loop_now/context', '/Users/Shared/w'])
  })

  test('desktop.json 按配置名取指定的目录；没有文件、没有这一项时不指定，写错时报错', () => {
    const file = join(dir, 'overrides.json')
    assert.deepEqual(overridesFromFile(file, defaultProfile, 'darwin'), {})
    writeFileSync(file, 'not json')
    assert.deepEqual(overridesFromFile(file, defaultProfile, 'darwin'), {})
    writeFileSync(file, JSON.stringify({ profile: 'try' }))
    assert.deepEqual(overridesFromFile(file, defaultProfile, 'darwin'), {})
    writeFileSync(file, JSON.stringify({ profile: 'default', profiles: { default: { workRoot: '/Users/Shared/loop-now/', dataDir: '/Users/someone/.loop_now' } } }))
    assert.deepEqual(overridesFromFile(file, defaultProfile, 'darwin'), { workRoot: '/Users/Shared/loop-now', dataDir: '/Users/someone/.loop_now' })
    assert.deepEqual(overridesFromFile(file, 'try', 'darwin'), {})
    assert.deepEqual(overridesFromFile(file, 'constructor', 'darwin'), {})
    for (const [entry, message] of [
      [{ workRoot: 'loop-now' }, /profiles\.default\.workRoot 应是绝对路径："loop-now"/],
      [{ workRoot: 3 }, /应是绝对路径：3/],
      [{ workroot: '/Users/Shared/loop-now' }, /只能写 workRoot、dataDir、contextDir，不认识 workroot/],
      ['/Users/Shared/loop-now', /profiles\.default 应是对象/],
    ] as const) {
      writeFileSync(file, JSON.stringify({ profiles: { default: entry } }))
      assert.throws(() => overridesFromFile(file, defaultProfile, 'darwin'), message)
    }
  })

  test('绝对路径时全部放在它下面', () => {
    const p = resolveProfile('/private/tmp/x/', undefined, {}, 'darwin')
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

  test('Windows：名字放在 %LOCALAPPDATA%\\Loop Now\\<名字>，日志也在其中，工作根在 platform.ts 定的位置', () => {
    const env = { LOCALAPPDATA: 'C:\\Users\\someone\\AppData\\Local' }
    const p = resolveProfile('try', 'C:\\Users\\someone', {}, 'win32', env)
    assert.equal(p.label, 'try')
    assert.equal(p.root, 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try')
    assert.equal(p.shellDir, 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try\\shell')
    assert.equal(p.logDir, 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try\\logs')
    assert.deepEqual(profileEnv(p), {
      LOOP_NOW_DATA_DIR: 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try\\data',
      LOOP_NOW_CONTEXT_DIR: 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try\\context',
      LOOP_NOW_WORK_ROOT: 'C:\\ProgramData\\loop-now-desktop\\try',
    })
    assert.equal(p.workRoot, `${windowsWorkRootBase('C:\\ProgramData')}\\try`)
    // 工作根不得在 %USERPROFILE% 之内（服务端的 PrepareWorkRoot 会拒绝）；ProgramData 挪了位置时跟着走。
    assert.ok(!p.workRoot.toLowerCase().startsWith('c:\\users\\someone\\'))
    assert.equal(resolveProfile('try', 'C:\\Users\\someone', {}, 'win32', { ...env, ProgramData: 'D:\\ProgramData' }).workRoot, 'D:\\ProgramData\\loop-now-desktop\\try')
    // 没有 LOCALAPPDATA 时按 HOME 推。
    assert.equal(resolveProfile('try', 'C:\\Users\\someone', {}, 'win32', {}).dataDir, 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try\\data')
  })

  test('Windows：绝对路径时全部放在它下面，末尾的反斜杠去掉；desktop.json 的目录也按 Windows 判断绝对路径', () => {
    const p = resolveProfile('D:\\loop\\x\\', undefined, {}, 'win32', {})
    assert.deepEqual([p.label, p.root], ['x', 'D:\\loop\\x'])
    assert.deepEqual([p.shellDir, p.logDir, p.dataDir, p.contextDir, p.workRoot], ['D:\\loop\\x\\shell', 'D:\\loop\\x\\logs', 'D:\\loop\\x\\data', 'D:\\loop\\x\\context', 'D:\\loop\\x\\work'])
    assert.equal(resolveProfile('D:\\', undefined, {}, 'win32', {}).root, 'D:\\')
    const file = join(dir, 'overrides-win.json')
    writeFileSync(file, JSON.stringify({ profiles: { default: { workRoot: 'D:\\work\\' } } }))
    assert.deepEqual(overridesFromFile(file, defaultProfile, 'win32'), { workRoot: 'D:\\work' })
    writeFileSync(file, JSON.stringify({ profiles: { default: { workRoot: 'work' } } }))
    assert.throws(() => overridesFromFile(file, defaultProfile, 'win32'), /应是绝对路径："work"/)
    const migrated = resolveProfile(defaultProfile, 'C:\\Users\\someone', { workRoot: 'D:\\work' }, 'win32', {})
    assert.equal(profileEnv(migrated).LOOP_NOW_WORK_ROOT, 'D:\\work')
    assert.match(profileSummary(migrated), /工作根 D:\\work（desktop\.json 指定）$/)
  })
})

describe('平台差异', () => {
  test('Windows：系统标题栏、.ico 托盘图标、exe 文件名、任务栏 overlay、PATH 安装；macOS：hiddenInset、Template 图、Dock 角标、链接', () => {
    assert.deepEqual(windowChrome('win32'), {})
    assert.deepEqual(windowChrome('darwin'), { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 18, y: 16 } })
    assert.deepEqual([trayIcon('win32'), trayIcon('darwin')], ['tray.ico', 'trayTemplate.png'])
    assert.deepEqual([serverBinary('win32'), serverBinary('darwin'), cliBinary('win32'), cliBinary('darwin')], ['loop-server.exe', 'loop-server', 'loopctl.exe', 'loopctl'])
    assert.deepEqual([badgeStyle('win32'), badgeStyle('darwin')], ['overlay', 'dock'])
    assert.deepEqual([cliInstallMode('win32'), cliInstallMode('darwin')], ['path', 'symlink'])
    for (const name of ['tray.ico', 'trayTemplate.png', 'trayTemplate@2x.png', 'icon.png']) assert.ok(existsSync(join(__dirname, '..', 'assets', name)), name)
    assert.ok(existsSync(join(__dirname, '..', 'build', 'icon.ico')))
  })

  test('未读数：macOS 设 Dock 角标；Windows 有未读就放 overlay，变多时闪任务栏', () => {
    assert.deepEqual(badgePlan(0, 3, 'darwin'), { dock: 3 })
    assert.deepEqual(badgePlan(3, 0, 'darwin'), { dock: 0 })
    assert.deepEqual(badgePlan(0, 3, 'win32'), { overlay: true, flash: true })
    assert.deepEqual(badgePlan(3, 2, 'win32'), { overlay: true, flash: false })
    assert.deepEqual(badgePlan(2, 0, 'win32'), { overlay: false, flash: false })
    const dot = dotBitmap(16)
    assert.equal(dot.length, 16 * 16 * 4)
    assert.equal(dot[0 * 4 + 3], 0) // 角上透明
    assert.equal(dot[(8 * 16 + 8) * 4 + 3], 255) // 中心不透明
  })
})

describe('服务端进程核对（孤儿清理）', () => {
  const bin = 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\server\\loop-server.exe'
  test('Windows：可执行文件路径不分大小写，启动时刻和壳记下的一致才算；pid 被复用时不认', () => {
    const info = { command: bin.toUpperCase(), started: '133700000000000000' }
    assert.equal(isOurServer(info, bin, 4321, undefined, 'win32'), true)
    assert.equal(isOurServer(info, bin, 4321, { pid: 4321, started: '133700000000000000' }, 'win32'), true)
    assert.equal(isOurServer(info, bin, 4321, { pid: 4321, started: '133799999999999999' }, 'win32'), false)
    assert.equal(isOurServer(info, bin, 4321, { pid: 9999, started: 'x' }, 'win32'), true)
    assert.equal(isOurServer({ command: 'C:\\Windows\\notepad.exe', started: '1' }, bin, 4321, undefined, 'win32'), false)
    assert.equal(isOurServer(undefined, bin, 4321, undefined, 'win32'), false)
  })

  test('macOS：命令行必须正好是可执行文件路径', () => {
    const mac = '/Applications/Loop Now.app/Contents/Resources/server/loop-server'
    assert.equal(isOurServer({ command: mac, started: 'Wed Nov  5 10:00:00 2026' }, mac, 1, undefined, 'darwin'), true)
    assert.equal(isOurServer({ command: mac.toUpperCase(), started: '' }, mac, 1, undefined, 'darwin'), false)
    assert.equal(isOurServer({ command: `${mac} --x`, started: '' }, mac, 1, undefined, 'darwin'), false)
  })

  test('Windows：握着服务端的 stdin 并带 LOOP_NOW_STOP_ON_STDIN_CLOSE=1，关 stdin 即优雅停止；macOS 不给 stdin', () => {
    assert.deepEqual(spawnPlan('win32'), { stdin: 'pipe', env: { LOOP_NOW_STOP_ON_STDIN_CLOSE: '1' } })
    assert.deepEqual(spawnPlan('darwin'), { stdin: 'ignore', env: {} })
  })

  test('Windows：PowerShell 查进程的输出按 JSON 读；超时或孤儿用 taskkill 整棵树强制结束', () => {
    assert.match(windowsProcessQuery(4321), /ProcessId=4321/)
    assert.deepEqual(parseWindowsProcess('{"ExecutablePath":"C:\\\\x\\\\loop-server.exe","Started":133700000000000000}'), { command: 'C:\\x\\loop-server.exe', started: '133700000000000000' })
    assert.equal(parseWindowsProcess('{"ExecutablePath":null,"Started":1}'), undefined)
    assert.equal(parseWindowsProcess(''), undefined)
    assert.deepEqual(taskkillArgs(4321), ['/PID', '4321', '/T', '/F'])
  })
})

describe('设置文件', () => {
  test('读不到或格式不对时为空，更新时合并', () => {
    const file = join(dir, 'settings', 'settings.json')
    const settings = new SettingsFile(file)
    assert.deepEqual(settings.read(), {})
    settings.update({ port: 8780 })
    assert.deepEqual(settings.read(), { port: 8780 })
    settings.update({ externalPort: 8781 })
    assert.deepEqual(settings.read(), { port: 8780, externalPort: 8781 })
    writeFileSync(file, 'not json')
    assert.deepEqual(settings.read(), {})
  })
})

describe('托盘菜单', () => {
  const s = stringsFor('zh-CN')
  const labels = (items: ReturnType<typeof trayItems>) => items.map((i) => (i === 'separator' ? '—' : `${i.label}${i.enabled ? '' : '（不可点）'}`))
  const profile = (name: string) => resolveProfile(name, '/Users/someone', {}, 'darwin')

  test('运行中：状态行带主机和端口，可在浏览器打开；开了对外 TLS 端口时可看外部身份连接方式', () => {
    assert.deepEqual(labels(trayItems('running', 'http://127.0.0.1:8780', profile(defaultProfile), s, true, undefined, 'https://127.0.0.1:8781')), [
      '运行中 · 127.0.0.1:8780（不可点）',
      '配置：默认（不可点）',
      '—',
      '打开窗口',
      '在默认浏览器打开',
      '—',
      '查看日志',
      '安装命令行工具…',
      '外部身份连接方式…',
      '重启服务',
      '—',
      '退出 Loop Now',
    ])
    // 没开对外端口（旧服务端的就绪文件没有 externalUrl）时不可点。
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', profile(defaultProfile), s))[8], '外部身份连接方式…（不可点）')
  })

  test('启动中不能在浏览器打开、不能看连接方式、不能重启；第二行是当前配置', () => {
    const items = labels(trayItems('starting', undefined, profile('try'), s, true, undefined, 'https://127.0.0.1:8781'))
    assert.deepEqual(items.slice(0, 2), ['正在启动…（不可点）', '配置：try（不可点）'])
    assert.equal(items[4], '在默认浏览器打开（不可点）')
    assert.equal(items[8], '外部身份连接方式…（不可点）')
    assert.equal(items[9], '重启服务（不可点）')
  })

  test('desktop.json 指定了目录时，配置行跟上实际用的路径', () => {
    const migrated = resolveProfile(defaultProfile, '/Users/someone', { workRoot: '/Users/Shared/loop-now' }, 'darwin')
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', migrated, s))[1], '配置：默认 · 工作根 /Users/Shared/loop-now（不可点）')
    const all = resolveProfile('try', '/Users/someone', { contextDir: '/Volumes/c', dataDir: '/Volumes/d', workRoot: '/Volumes/w' }, 'darwin')
    assert.equal(
      labels(trayItems('running', 'http://127.0.0.1:8780', all, stringsFor('en-US')))[1],
      'Profile: try · work root /Volumes/w · data /Volumes/d · context /Volumes/c（不可点）',
    )
  })

  test('多次崩溃停下后可以手动重启', () => {
    const items = labels(trayItems('crashed', undefined, undefined, s))
    assert.equal(items[0], '已停止：多次崩溃（不可点）')
    assert.equal(items[9], '重启服务')
  })

  test('开发时（没打包）不能安装命令行工具', () => {
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', undefined, s, false))[7], '安装命令行工具…（不可点）')
  })

  test('中英文两套文字同形', () => {
    const en = stringsFor('en-US')
    assert.equal(stringsFor('zh-Hans-CN'), s)
    assert.deepEqual(Object.keys(en.tray), Object.keys(s.tray))
    assert.deepEqual(labels(trayItems('degraded', 'http://127.0.0.1:9000', profile(defaultProfile), en)).slice(0, 2), ['Database unavailable · 127.0.0.1:9000（不可点）', 'Profile: default（不可点）'])
    assert.deepEqual(Object.keys(en.tray.dirs), Object.keys(s.tray.dirs))
    assert.deepEqual(Object.keys(en.external), Object.keys(s.external))
  })
})

describe('外部身份连接方式', () => {
  test('证书在壳的 tls 目录下；loopctl 配置命令带 https 地址、加引号的 CA 路径，令牌从标准输入读', () => {
    // 路径分隔符跟着运行的平台走（Windows 上是反斜杠），所以期望值也用 join 拼。
    const tlsDir = join('/Users/someone/Library/Application Support/Loop Now/default/shell', 'tls')
    const caFile = externalCertPath(tlsDir)
    assert.equal(caFile, join(tlsDir, 'cert.pem'))
    assert.equal(
      loopctlConfigCommand('https://127.0.0.1:8781', '/Users/someone/Library/Application Support/Loop Now/default/shell/tls/cert.pem'),
      'loopctl config set default --server https://127.0.0.1:8781 --ca-file "/Users/someone/Library/Application Support/Loop Now/default/shell/tls/cert.pem" --token-stdin',
    )
    const detail = stringsFor('zh-CN').external.detail('https://127.0.0.1:8781', caFile, 'CMD')
    assert.match(detail, /https:\/\/127\.0\.0\.1:8781/)
    assert.match(detail, /cert\.pem/)
    assert.match(detail, /\nCMD\n/)
  })
})

describe('git 检测', () => {
  test('xcode-select -p 失败时不运行 git（它只是占位，会弹系统的安装框）', async () => {
    const calls: string[] = []
    const usable = await gitUsable(async (file) => {
      calls.push(file)
      return false
    }, 'darwin')
    assert.equal(usable, false)
    assert.deepEqual(calls, ['/usr/bin/xcode-select'])
  })

  test('有命令行工具时以 git --version 为准', async () => {
    assert.equal(await gitUsable(async () => true, 'darwin'), true)
    assert.equal(await gitUsable(async (file) => file !== '/usr/bin/git', 'darwin'), false)
  })

  test('Windows：PATH 里找 git.exe，找到再跑 git --version，找不到就是没装，不碰 xcode-select', async () => {
    const env = { PATH: 'C:\\Windows\\system32;C:\\Program Files\\Git\\cmd;' }
    const exists = (file: string) => file === 'C:\\Program Files\\Git\\cmd\\git.exe'
    assert.equal(findInPath('git.exe', env.PATH, 'win32', exists), 'C:\\Program Files\\Git\\cmd\\git.exe')
    assert.equal(findInPath('git.exe', env.PATH, 'win32', () => false), undefined)
    const calls: string[] = []
    const run = async (file: string) => {
      calls.push(file)
      return true
    }
    assert.equal(await gitUsable(run, 'win32', env, exists), true)
    assert.deepEqual(calls, ['C:\\Program Files\\Git\\cmd\\git.exe'])
    assert.equal(await gitUsable(run, 'win32', env, () => false), false)
    assert.equal(await gitUsable(run, 'win32', { Path: env.PATH }, exists), true)
    assert.equal(await gitUsable(async () => false, 'win32', env, exists), false)
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
  // macOS 专用（Windows 走 PATH，不建链接；Windows 上 readlink 还会把相对目标解析成绝对路径）。
  test('看出链接是不存在、已指向自带的 loopctl，还是别的', { skip: process.platform === 'win32' && 'macOS 专用' }, async () => {
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

  test('Windows：看用户 PATH 里有没有随包的 bin 目录（不分大小写、末尾反斜杠不算），没有才追加', () => {
    const dir = 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\bin'
    assert.equal(userPathHas(`C:\\Go\\bin;${dir}\\;D:\\x`, dir), true)
    assert.equal(userPathHas(dir.toUpperCase(), dir), true)
    assert.equal(userPathHas('C:\\Go\\bin;;', dir), false)
    assert.equal(userPathHas('', dir), false)
    const script = addToUserPathScript("C:\\Users\\o'neil\\bin")
    assert.ok(script.startsWith(`$dir = 'C:\\Users\\o''neil\\bin'; `))
    assert.match(script, /GetEnvironmentVariable\('Path', 'User'\)/)
    assert.match(script, /SetEnvironmentVariable\('Path', \$path, 'User'\)/)
    assert.doesNotMatch(script, /setx/i)
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

  // 用 electron-updater 真实的 GitHub 提供方读一份假的 releases.atom（tags 从新到旧）：每个 release 里只有 latest-mac.yml
  // 与 latest.yml，别的文件一律 404。updater 的设置来自 UpdateChecker，提供方的 options 来自打包配置。
  const latestRelease = async (current: string, tags: string[], platform: 'darwin' | 'win32' = 'darwin') => {
    const updater = fake()
    new UpdateChecker(updater, () => undefined)
    const fetched: string[] = []
    const entry = (tag: string) => `<entry><title>${tag}</title><link rel="alternate" type="text/html" href="https://github.com/AI-for-anyone/loop-now/releases/tag/${tag}"/><content>notes</content></entry>`
    const executor = {
      request: async ({ path }: { path: string }) => {
        fetched.push(path.replace('/AI-for-anyone/loop-now/releases', ''))
        if (path.endsWith('.atom')) return `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">${tags.map(entry).join('')}</feed>`
        const version = /\/download\/v([^/]+)\/latest(-mac)?\.yml$/.exec(path)?.[1]
        if (version) return `version: ${version}\npath: update.zip\nsha512: x\nfiles: [{ url: update.zip, sha512: x }]\n`
        throw new Error(`404 ${path}`)
      },
    }
    // 提供方只用到 updater 的 allowPrerelease、channel 与当前版本（semver 也认字符串）。
    const provider = new GitHubProvider(publish(), { ...updater, currentVersion: current, fullChangelog: false } as unknown as AppUpdater, {
      executor: executor as unknown as ProviderRuntimeOptions['executor'],
      platform,
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

  test('Windows 读的是 latest.yml，同样只提示去下载页（打包配置里 win 不设 channel、不自动装）', async () => {
    assert.deepEqual(await latestRelease('0.2.0', ['v0.2.1', 'v0.2.0'], 'win32'), { version: '0.2.1', fetched: ['.atom', '/download/v0.2.1/latest.yml'] })
    const saved = process.env.LOOP_NOW_DESKTOP_BUNDLE
    process.env.LOOP_NOW_DESKTOP_BUNDLE = dir
    try {
      const config = require('../electron-builder.config.js')
      assert.deepEqual(config.win.target, [{ target: 'nsis', arch: 'x64' }])
      assert.deepEqual([config.nsis.oneClick, config.nsis.perMachine, config.nsis.allowToChangeInstallationDirectory], [false, false, true])
      assert.equal(config.nsis.artifactName, 'loop-now-desktop-${version}-${os}-${arch}.${ext}')
      assert.equal(config.win.channel, undefined)
      assert.equal(config.win.publisherName, undefined)
    } finally {
      if (saved === undefined) delete process.env.LOOP_NOW_DESKTOP_BUNDLE
      else process.env.LOOP_NOW_DESKTOP_BUNDLE = saved
    }
  })

  test('有新版本时菜单栏多一项去下载页', () => {
    const items = trayItems('running', 'http://127.0.0.1:8780', undefined, stringsFor('zh-CN'), true, '0.2.0')
    assert.deepEqual(items[2], { label: '有新版本 0.2.0，去下载…', enabled: true, action: 'update' })
  })
})
