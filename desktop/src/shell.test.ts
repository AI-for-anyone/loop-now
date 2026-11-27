// Pure logic in the shell that runs without Electron: restart counting, port choice, log rotation, navigation classification, separate
// profiles, the settings file, the tray menu, and the Windows branches (paths, git detection, PATH install, process checks, unread count).
// The platform is injected as a parameter, so the same suite runs on any OS.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readlinkSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, describe, test } from 'node:test'
import type { AppUpdater } from 'electron-updater'
import { GitHubProvider } from 'electron-updater/out/providers/GitHubProvider'
import type { ProviderRuntimeOptions } from 'electron-updater/out/providers/Provider'
import { badgePlan, dotBitmap } from './badge'
import { parseBadge, parseNotification } from './bridge'
import {
  addToUserPathScript,
  cliInstallHandler,
  cliLink,
  cliStatusHandler,
  installLinkDirect,
  installScript,
  linkState,
  maybePromptCliInstall,
  readCliStatus,
  resolveCliLocation,
  runFirstShowChecks,
  userPathHas,
  type CliDialogMessage,
  type CliLocation,
} from './cli'
import { externalCertPath, loopctlConfigCommand } from './external'
import { classifyNavigation, handOffOnce } from './links'
import { RotatingLog } from './log'
import { badgeStyle, cliBinary, cliInstallMode, serverBinary, trayIcon, windowChrome, windowsWorkRootBase } from './platform'
import { choosePort, portFree, portOf } from './port'
import { isOurServer, parseWindowsProcess, spawnPlan, taskkillArgs, windowsProcessQuery } from './process'
import { findInPath, gitUsable } from './git'
import { appExecutable, maxPathLength, pickPathHandler } from './pick-path'
import { defaultProfile, overridesFromFile, profileArg, profileEnv, profileFromFile, profileSummary, resolveProfile } from './profile'
import { RestartPolicy, restartWindowMs } from './restart'
import { SettingsFile } from './settings'
import { errorText, LocalizedError, stringsFor } from './strings'
import { trayItems } from './tray'
import { UpdateChecker, type Updater } from './updates'

const dir = mkdtempSync(join(tmpdir(), 'loop-now-desktop-test-'))
after(() => rmSync(dir, { recursive: true, force: true }))

describe('Crash restarts', () => {
  test('Within 5 minutes, waits 1, 5 and 15 seconds in turn and stops at the 4th crash', () => {
    const policy = new RestartPolicy()
    assert.deepEqual([policy.next(0), policy.next(1_000), policy.next(2_000), policy.next(3_000)], [1_000, 5_000, 15_000, null])
  })

  test('Crashes older than 5 minutes no longer count, and a manual restart resets the count', () => {
    const policy = new RestartPolicy()
    policy.next(0)
    policy.next(1_000)
    policy.next(2_000)
    assert.equal(policy.next(restartWindowMs + 1_500), 5_000)
    policy.reset()
    assert.equal(policy.next(restartWindowMs + 2_000), 1_000)
  })
})

describe('Port', () => {
  test('Uses the wanted port when it is free, and lets the system choose when it is taken or 0', async () => {
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

  test('Takes the port from the address in the ready file', () => {
    assert.equal(portOf('http://127.0.0.1:8781'), 8781)
    assert.equal(portOf('http://127.0.0.1'), undefined)
  })
})

describe('Log', () => {
  test('Rotates past the size limit and keeps only `keep` old files', () => {
    const file = join(dir, 'logs', 'server.log')
    const log = new RotatingLog(file, 10, 2)
    for (const chunk of ['aaaaaaaa\n', 'bbbbbbbb\n', 'cccccccc\n', 'dddddddd\n']) log.write(chunk)
    log.close()
    assert.equal(readFileSync(file, 'utf8'), 'dddddddd\n')
    assert.equal(readFileSync(`${file}.1`, 'utf8'), 'cccccccc\n')
    assert.equal(readFileSync(`${file}.2`, 'utf8'), 'bbbbbbbb\n')
    assert.equal(existsSync(`${file}.3`), false)
  })

  test('The last lines cover only this run', () => {
    const log = new RotatingLog(join(dir, 'logs', 'tail.log'))
    log.write('old\n')
    log.resetTail()
    log.write('one\ntwo\nthree\n')
    log.close()
    assert.equal(log.tail(2), 'two\nthree')
  })
})

describe('Navigation', () => {
  const origin = 'http://127.0.0.1:8780'
  test("The server's own origin stays in the window; http, https and mailto go to the system browser; anything else is ignored", () => {
    assert.equal(classifyNavigation('http://127.0.0.1:8780/projects', origin), 'app')
    assert.equal(classifyNavigation('http://127.0.0.1:8781/', origin), 'external')
    assert.equal(classifyNavigation('https://github.com/AI-for-anyone/loop-now', origin), 'external')
    assert.equal(classifyNavigation('mailto:someone@example.com', origin), 'external')
    assert.equal(classifyNavigation('file:///etc/passwd', origin), 'deny')
    assert.equal(classifyNavigation('javascript:alert(1)', origin), 'deny')
    assert.equal(classifyNavigation('not a url', origin), 'deny')
  })

  test('A new window passes no origin, so even same-origin pages go to the system browser', () => {
    assert.equal(classifyNavigation('http://127.0.0.1:8780/projects'), 'external')
  })

  test('An authorization popup is handed off once: did-start-navigation and will-navigate of the same navigation open one page, and about:blank does not count', () => {
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

describe('Profiles', () => {
  // A profile error is a LocalizedError: its message is the English text (the enUS table), and the zhCN table gives the Chinese one.
  const localized = (english: RegExp, chinese: RegExp) => (err: unknown) => {
    assert.ok(err instanceof LocalizedError)
    assert.match(err.message, english)
    assert.equal(err.text(stringsFor('en-US')), err.message)
    assert.match(err.text(stringsFor('zh-CN')), chinese)
    return true
  }

  test('The command line argument takes precedence over the environment variable', () => {
    assert.equal(profileArg(['electron', '.', '--profile=try'], { LOOP_NOW_DESKTOP_PROFILE: 'env' }), 'try')
    assert.equal(profileArg(['electron', '.'], { LOOP_NOW_DESKTOP_PROFILE: 'env' }), 'env')
    assert.equal(profileArg(['electron', '.'], {}), undefined)
  })

  test("A named profile lives in the shell's own location, with the work root outside HOME", () => {
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

  test("The default profile is a profile too: without specified directories it never falls back to the server's default directories", () => {
    const p = resolveProfile(defaultProfile, '/Users/someone', {}, 'darwin')
    assert.equal(p.dataDir, '/Users/someone/Library/Application Support/Loop Now/default/data')
    assert.equal(p.contextDir, '/Users/someone/Library/Application Support/Loop Now/default/context')
    assert.equal(p.workRoot, '/Users/Shared/loop-now-desktop/default')
    assert.deepEqual(p.overrides, {})
    for (const d of [p.dataDir, p.contextDir, p.workRoot]) assert.ok(!d.includes('.loop_now') && !d.startsWith('/Users/Shared/loop-now/'))
    assert.equal(
      profileSummary(p),
      'profile default, data directory /Users/someone/Library/Application Support/Loop Now/default/data, context directory /Users/someone/Library/Application Support/Loop Now/default/context, work root /Users/Shared/loop-now-desktop/default',
    )
  })

  test('Directories specified in desktop.json replace the default locations, are passed to the server, and are noted on the first log line', () => {
    const p = resolveProfile(defaultProfile, '/Users/someone', { workRoot: '/Users/Shared/loop-now' }, 'darwin')
    assert.deepEqual(profileEnv(p), {
      LOOP_NOW_DATA_DIR: '/Users/someone/Library/Application Support/Loop Now/default/data',
      LOOP_NOW_CONTEXT_DIR: '/Users/someone/Library/Application Support/Loop Now/default/context',
      LOOP_NOW_WORK_ROOT: '/Users/Shared/loop-now',
    })
    assert.equal(p.shellDir, '/Users/someone/Library/Application Support/Loop Now/default/shell')
    assert.match(profileSummary(p), /, work root \/Users\/Shared\/loop-now \(set in desktop\.json\)$/)
    assert.doesNotMatch(profileSummary(p), /data \(set in desktop\.json\)/)

    const all = resolveProfile('/private/tmp/x', undefined, { workRoot: '/Users/Shared/w', dataDir: '/Users/someone/.loop_now', contextDir: '/Users/someone/.loop_now/context' }, 'darwin')
    assert.deepEqual([all.shellDir, all.logDir, all.dataDir, all.contextDir, all.workRoot], ['/private/tmp/x/shell', '/private/tmp/x/logs', '/Users/someone/.loop_now', '/Users/someone/.loop_now/context', '/Users/Shared/w'])
  })

  test('desktop.json gives directories by profile name; nothing is specified without the file or the entry, and a malformed entry is an error', () => {
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
    for (const [entry, english, chinese] of [
      [{ workRoot: 'loop-now' }, /^profiles\.default\.workRoot in desktop\.json should be an absolute path: "loop-now"$/, /^desktop\.json 的 profiles\.default\.workRoot 应是绝对路径："loop-now"$/],  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError)
      [{ workRoot: 3 }, /should be an absolute path: 3$/, /应是绝对路径：3$/],  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError)
      [{ workroot: '/Users/Shared/loop-now' }, /^profiles\.default in desktop\.json may only set workRoot, dataDir, contextDir; unknown key: workroot$/, /^desktop\.json 的 profiles\.default 只能写 workRoot、dataDir、contextDir，不认识 workroot$/],  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError)
      ['/Users/Shared/loop-now', /^profiles\.default in desktop\.json should be an object\.$/, /^desktop\.json 的 profiles\.default 应是对象$/],  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError)
    ] as const) {
      writeFileSync(file, JSON.stringify({ profiles: { default: entry } }))
      assert.throws(() => overridesFromFile(file, defaultProfile, 'darwin'), localized(english, chinese))
    }
  })

  test('With an absolute path, everything goes under it', () => {
    const p = resolveProfile('/private/tmp/x/', undefined, {}, 'darwin')
    assert.deepEqual([p.label, p.root], ['x', '/private/tmp/x'])
    assert.deepEqual([p.shellDir, p.logDir, p.dataDir, p.contextDir, p.workRoot], ['/private/tmp/x/shell', '/private/tmp/x/logs', '/private/tmp/x/data', '/private/tmp/x/context', '/private/tmp/x/work'])
  })

  test('When opened from Finder, reads the profile from desktop.json; empty when missing or malformed', () => {
    const file = join(dir, 'desktop.json')
    assert.equal(profileFromFile(file), undefined)
    writeFileSync(file, JSON.stringify({ profile: 'try' }))
    assert.equal(profileFromFile(file), 'try')
    writeFileSync(file, JSON.stringify({ profile: 3 }))
    assert.equal(profileFromFile(file), undefined)
  })

  test('An invalid name is an error', () => {
    assert.throws(() => resolveProfile('../live'))
    assert.throws(
      () => resolveProfile('Try'),
      localized(
        /^A profile name may only use lowercase letters, digits, underscores and hyphens, up to 32 characters: Try$/,
        /^配置名只能用小写字母、数字、下划线与连字符，最长 32 个字符：Try$/,  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError)
      ),
    )
  })

  test('Windows: a named profile lives in %LOCALAPPDATA%\\Loop Now\\<name>, logs included, and the work root is where platform.ts puts it', () => {
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
    // The work root must not be inside %USERPROFILE% (the server's PrepareWorkRoot would reject it); it follows ProgramData when that has moved.
    assert.ok(!p.workRoot.toLowerCase().startsWith('c:\\users\\someone\\'))
    assert.equal(resolveProfile('try', 'C:\\Users\\someone', {}, 'win32', { ...env, ProgramData: 'D:\\ProgramData' }).workRoot, 'D:\\ProgramData\\loop-now-desktop\\try')
    // Without LOCALAPPDATA, it is derived from HOME.
    assert.equal(resolveProfile('try', 'C:\\Users\\someone', {}, 'win32', {}).dataDir, 'C:\\Users\\someone\\AppData\\Local\\Loop Now\\try\\data')
  })

  test('Windows: with an absolute path, everything goes under it without the trailing backslash; desktop.json directories are judged absolute by Windows rules too', () => {
    const p = resolveProfile('D:\\loop\\x\\', undefined, {}, 'win32', {})
    assert.deepEqual([p.label, p.root], ['x', 'D:\\loop\\x'])
    assert.deepEqual([p.shellDir, p.logDir, p.dataDir, p.contextDir, p.workRoot], ['D:\\loop\\x\\shell', 'D:\\loop\\x\\logs', 'D:\\loop\\x\\data', 'D:\\loop\\x\\context', 'D:\\loop\\x\\work'])
    assert.equal(resolveProfile('D:\\', undefined, {}, 'win32', {}).root, 'D:\\')
    const file = join(dir, 'overrides-win.json')
    writeFileSync(file, JSON.stringify({ profiles: { default: { workRoot: 'D:\\work\\' } } }))
    assert.deepEqual(overridesFromFile(file, defaultProfile, 'win32'), { workRoot: 'D:\\work' })
    writeFileSync(file, JSON.stringify({ profiles: { default: { workRoot: 'work' } } }))
    assert.throws(() => overridesFromFile(file, defaultProfile, 'win32'), localized(/should be an absolute path: "work"$/, /应是绝对路径："work"$/))  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError)
    const migrated = resolveProfile(defaultProfile, 'C:\\Users\\someone', { workRoot: 'D:\\work' }, 'win32', {})
    assert.equal(profileEnv(migrated).LOOP_NOW_WORK_ROOT, 'D:\\work')
    assert.match(profileSummary(migrated), /work root D:\\work \(set in desktop\.json\)$/)
  })
})

describe('Platform differences', () => {
  test('Windows: system title bar, .ico tray icon, exe file names, taskbar overlay, PATH install; macOS: hiddenInset, template image, Dock badge, link', () => {
    assert.deepEqual(windowChrome('win32'), {})
    assert.deepEqual(windowChrome('darwin'), { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 18, y: 16 } })
    assert.deepEqual([trayIcon('win32'), trayIcon('darwin')], ['tray.ico', 'trayTemplate.png'])
    assert.deepEqual([serverBinary('win32'), serverBinary('darwin'), cliBinary('win32'), cliBinary('darwin')], ['loop-server.exe', 'loop-server', 'loopctl.exe', 'loopctl'])
    assert.deepEqual([badgeStyle('win32'), badgeStyle('darwin')], ['overlay', 'dock'])
    assert.deepEqual([cliInstallMode('win32'), cliInstallMode('darwin')], ['path', 'symlink'])
    for (const name of ['tray.ico', 'trayTemplate.png', 'trayTemplate@2x.png', 'icon.png']) assert.ok(existsSync(join(__dirname, '..', 'assets', name)), name)
    assert.ok(existsSync(join(__dirname, '..', 'build', 'icon.ico')))
  })

  test('Unread count: macOS sets the Dock badge; Windows shows an overlay while anything is unread and flashes the taskbar when the count grows', () => {
    assert.deepEqual(badgePlan(0, 3, 'darwin'), { dock: 3 })
    assert.deepEqual(badgePlan(3, 0, 'darwin'), { dock: 0 })
    assert.deepEqual(badgePlan(0, 3, 'win32'), { overlay: true, flash: true })
    assert.deepEqual(badgePlan(3, 2, 'win32'), { overlay: true, flash: false })
    assert.deepEqual(badgePlan(2, 0, 'win32'), { overlay: false, flash: false })
    const dot = dotBitmap(16)
    assert.equal(dot.length, 16 * 16 * 4)
    assert.equal(dot[0 * 4 + 3], 0) // corner transparent
    assert.equal(dot[(8 * 16 + 8) * 4 + 3], 255) // center opaque
  })
})

describe('Checking the server process (orphan cleanup)', () => {
  const bin = 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\server\\loop-server.exe'
  test('Windows: the executable path is case-insensitive and the start time must match what the shell recorded; a reused pid is not ours', () => {
    const info = { command: bin.toUpperCase(), started: '133700000000000000' }
    assert.equal(isOurServer(info, bin, 4321, undefined, 'win32'), true)
    assert.equal(isOurServer(info, bin, 4321, { pid: 4321, started: '133700000000000000' }, 'win32'), true)
    assert.equal(isOurServer(info, bin, 4321, { pid: 4321, started: '133799999999999999' }, 'win32'), false)
    assert.equal(isOurServer(info, bin, 4321, { pid: 9999, started: 'x' }, 'win32'), true)
    assert.equal(isOurServer({ command: 'C:\\Windows\\notepad.exe', started: '1' }, bin, 4321, undefined, 'win32'), false)
    assert.equal(isOurServer(undefined, bin, 4321, undefined, 'win32'), false)
  })

  test('macOS: the command line must be exactly the executable path', () => {
    const mac = '/Applications/Loop Now.app/Contents/Resources/server/loop-server'
    assert.equal(isOurServer({ command: mac, started: 'Wed Nov  5 10:00:00 2026' }, mac, 1, undefined, 'darwin'), true)
    assert.equal(isOurServer({ command: mac.toUpperCase(), started: '' }, mac, 1, undefined, 'darwin'), false)
    assert.equal(isOurServer({ command: `${mac} --x`, started: '' }, mac, 1, undefined, 'darwin'), false)
  })

  test("Windows: holds the server's stdin and sets LOOP_NOW_STOP_ON_STDIN_CLOSE=1, so closing stdin stops it gracefully; macOS gives no stdin", () => {
    assert.deepEqual(spawnPlan('win32'), { stdin: 'pipe', env: { LOOP_NOW_STOP_ON_STDIN_CLOSE: '1' } })
    assert.deepEqual(spawnPlan('darwin'), { stdin: 'ignore', env: {} })
  })

  test("Windows: PowerShell's process query output is read as JSON; on a timeout, or for an orphan, taskkill force-ends the whole tree", () => {
    assert.match(windowsProcessQuery(4321), /ProcessId=4321/)
    assert.deepEqual(parseWindowsProcess('{"ExecutablePath":"C:\\\\x\\\\loop-server.exe","Started":133700000000000000}'), { command: 'C:\\x\\loop-server.exe', started: '133700000000000000' })
    assert.equal(parseWindowsProcess('{"ExecutablePath":null,"Started":1}'), undefined)
    assert.equal(parseWindowsProcess(''), undefined)
    assert.deepEqual(taskkillArgs(4321), ['/PID', '4321', '/T', '/F'])
  })
})

describe('Settings file', () => {
  test('Empty when unreadable or malformed; an update merges', () => {
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

describe('Tray menu', () => {
  const s = stringsFor('en-US')
  const labels = (items: ReturnType<typeof trayItems>) => items.map((i) => (i === 'separator' ? '—' : `${i.label}${i.enabled ? '' : ' (disabled)'}`))
  const profile = (name: string) => resolveProfile(name, '/Users/someone', {}, 'darwin')

  test('While running: the status line shows host and port, the browser item works, and the external identity connection is available when the external TLS port is open', () => {
    assert.deepEqual(labels(trayItems('running', 'http://127.0.0.1:8780', profile(defaultProfile), s, true, undefined, 'https://127.0.0.1:8781')), [
      'Running · 127.0.0.1:8780 (disabled)',
      'Profile: default (disabled)',
      '—',
      'Open Window',
      'Open in Browser',
      '—',
      'Show Logs',
      'Install Command Line Tool…',
      'External Identity Connection…',
      'Restart Server',
      '—',
      'Quit Loop Now',
    ])
    // Disabled when the external port is not open (an older server's ready file has no externalUrl).
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', profile(defaultProfile), s))[8], 'External Identity Connection… (disabled)')
  })

  test('While starting: no browser, no connection details, no restart; the second line is the current profile', () => {
    const items = labels(trayItems('starting', undefined, profile('try'), s, true, undefined, 'https://127.0.0.1:8781'))
    assert.deepEqual(items.slice(0, 2), ['Starting… (disabled)', 'Profile: try (disabled)'])
    assert.equal(items[4], 'Open in Browser (disabled)')
    assert.equal(items[8], 'External Identity Connection… (disabled)')
    assert.equal(items[9], 'Restart Server (disabled)')
  })

  test('When desktop.json specifies directories, the profile line is followed by the paths actually used', () => {
    const migrated = resolveProfile(defaultProfile, '/Users/someone', { workRoot: '/Users/Shared/loop-now' }, 'darwin')
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', migrated, s))[1], 'Profile: default · work root /Users/Shared/loop-now (disabled)')
    const all = resolveProfile('try', '/Users/someone', { contextDir: '/Volumes/c', dataDir: '/Volumes/d', workRoot: '/Volumes/w' }, 'darwin')
    assert.equal(
      labels(trayItems('running', 'http://127.0.0.1:8780', all, stringsFor('zh-CN')))[1],
      '配置：try · 工作根 /Volumes/w · 数据目录 /Volumes/d · 上下文目录 /Volumes/c (disabled)',  // check-english: zh (asserts both languages: the Chinese tray text)
    )
  })

  test('After stopping on repeated crashes, the server can be restarted by hand', () => {
    const items = labels(trayItems('crashed', undefined, undefined, s))
    assert.equal(items[0], 'Stopped after repeated crashes (disabled)')
    assert.equal(items[9], 'Restart Server')
  })

  test('In development (not packaged) the command line tool cannot be installed', () => {
    assert.equal(labels(trayItems('running', 'http://127.0.0.1:8780', undefined, s, false))[7], 'Install Command Line Tool… (disabled)')
  })

  test('The Chinese and English tables have the same shape', () => {
    const en = stringsFor('en-US')
    const zh = stringsFor('zh-CN')
    assert.equal(stringsFor('zh-Hans-CN'), zh)
    assert.deepEqual(Object.keys(en.tray), Object.keys(zh.tray))
    assert.deepEqual(labels(trayItems('degraded', 'http://127.0.0.1:9000', profile(defaultProfile), en)).slice(0, 2), ['Database unavailable · 127.0.0.1:9000 (disabled)', 'Profile: default (disabled)'])
    assert.deepEqual(Object.keys(en.tray.dirs), Object.keys(zh.tray.dirs))
    assert.deepEqual(Object.keys(en.external), Object.keys(zh.external))
    assert.deepEqual(Object.keys(en), Object.keys(zh))
    assert.deepEqual(Object.keys(en.cli), Object.keys(zh.cli))
    assert.deepEqual(Object.keys(en.profile), Object.keys(zh.profile))
  })

  test('Errors the shell shows itself get text in the UI language and an English message; other errors are shown as they are', () => {
    const err = new LocalizedError((t) => t.cli.pathMissing('C:\\Program Files\\Loop Now\\resources\\bin'))
    assert.equal(err.message, 'The user PATH was written, but C:\\Program Files\\Loop Now\\resources\\bin is not in it.')
    assert.equal(errorText(err, stringsFor('en-US')), err.message)
    assert.equal(errorText(err, stringsFor('zh-CN')), 'PATH 写完后没有 C:\\Program Files\\Loop Now\\resources\\bin')  // check-english: zh (asserts both languages: the Chinese text of a LocalizedError from cli)
    assert.equal(errorText(new Error('Access is denied.'), s), 'Access is denied.')
  })
})

describe('External identity connection', () => {
  test("The certificate is in the shell's tls directory; the loopctl config command carries the https address and the quoted CA path, and reads the token from standard input", () => {
    // The path separator follows the running platform (backslash on Windows), so the expected value is built with join too.
    const tlsDir = join('/Users/someone/Library/Application Support/Loop Now/default/shell', 'tls')
    const caFile = externalCertPath(tlsDir)
    assert.equal(caFile, join(tlsDir, 'cert.pem'))
    assert.equal(
      loopctlConfigCommand('https://127.0.0.1:8781', '/Users/someone/Library/Application Support/Loop Now/default/shell/tls/cert.pem'),
      'loopctl config set default --server https://127.0.0.1:8781 --ca-file "/Users/someone/Library/Application Support/Loop Now/default/shell/tls/cert.pem" --token-stdin',
    )
    const detail = stringsFor('en-US').external.detail('https://127.0.0.1:8781', caFile, 'CMD')
    assert.match(detail, /https:\/\/127\.0\.0\.1:8781/)
    assert.match(detail, /cert\.pem/)
    assert.match(detail, /\nCMD\n/)
  })
})

describe('git detection', () => {
  test("Does not run git when xcode-select -p fails (git is only a stub then and would open the system's install dialog)", async () => {
    const calls: string[] = []
    const usable = await gitUsable(async (file) => {
      calls.push(file)
      return false
    }, 'darwin')
    assert.equal(usable, false)
    assert.deepEqual(calls, ['/usr/bin/xcode-select'])
  })

  test('With the command line tools installed, git --version decides', async () => {
    assert.equal(await gitUsable(async () => true, 'darwin'), true)
    assert.equal(await gitUsable(async (file) => file !== '/usr/bin/git', 'darwin'), false)
  })

  test('Windows: looks for git.exe on PATH and runs git --version when found; not found means not installed, and xcode-select is never touched', async () => {
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

describe('Notifications and unread count sent by the page', () => {
  const ok = { id: 'm1', title: 'Waiting for your reply: reviewer', body: 'Merge it?', path: '/agents/a/chat' }

  test('A valid notification is taken as is; an overlong title and body are truncated', () => {
    assert.deepEqual(parseNotification(ok), ok)
    const long = parseNotification({ ...ok, title: 'x'.repeat(500), body: 'y'.repeat(900) })
    assert.equal(long?.title.length, 200)
    assert.equal(long?.body.length, 500)
  })

  test('The address must be a path in the console; anything else is dropped', () => {
    for (const path of ['https://evil.example', '//evil.example/x', '/\\evil.example', 'agents/a', '']) assert.equal(parseNotification({ ...ok, path }), undefined, path)
    assert.equal(parseNotification({ ...ok, id: '' }), undefined)
    assert.equal(parseNotification('not an object'), undefined)
  })

  test('The unread count is a non-negative integer', () => {
    assert.equal(parseBadge(3), 3)
    assert.equal(parseBadge(0), 0)
    for (const v of [-1, 1.5, '3', null]) assert.equal(parseBadge(v), undefined)
  })
})

describe('Choosing a folder or file for the page', () => {
  type Shown = { properties: string[]; defaultPath?: string }
  // The system dialog, faked: records what it was asked to show and answers with the given result.
  const fakeDialog = (result: { canceled: boolean; filePaths: string[] }) => {
    const shown: Shown[] = []
    return { shown, show: async (options: Shown) => (shown.push(options), result) }
  }
  const picked = { canceled: false, filePaths: ['/Users/me/code/app'] }

  test('A folder opens the folder dialog (with “New Folder”), a file the file dialog; the chosen path is returned', async () => {
    const dialog = fakeDialog(picked)
    const pick = pickPathHandler({ trusted: () => true, show: dialog.show, platform: 'darwin' })
    assert.equal(await pick({}, { kind: 'directory' }), '/Users/me/code/app')
    assert.equal(await pick({}, { kind: 'file' }), '/Users/me/code/app')
    // On macOS the file dialog shows hidden files, so programs in ~/.local/bin or /opt/homebrew can be chosen without ⌘⇧.
    assert.deepEqual(
      dialog.shown.map((options) => options.properties),
      [['openDirectory', 'createDirectory'], ['openFile', 'showHiddenFiles']],
    )
    const windows = fakeDialog(picked)
    await pickPathHandler({ trusted: () => true, show: windows.show, platform: 'win32' })({}, { kind: 'file' })
    assert.deepEqual(windows.shown[0]?.properties, ['openFile'])
  })

  test('A chosen file goes through resolveFile (the .app check); a chosen folder does not', async () => {
    const resolved: string[] = []
    const resolveFile = async (path: string) => (resolved.push(path), `${path}/Contents/MacOS/App`)
    const pick = pickPathHandler({ trusted: () => true, show: fakeDialog({ canceled: false, filePaths: ['/Applications/App.app'] }).show, platform: 'darwin', resolveFile })
    assert.equal(await pick({}, { kind: 'file' }), '/Applications/App.app/Contents/MacOS/App')
    assert.equal(await pick({}, { kind: 'directory' }), '/Applications/App.app')
    assert.deepEqual(resolved, ['/Applications/App.app'])
  })

  describe('A chosen .app bundle on macOS stands for the program inside it', () => {
    // A bundle as macOS lays it out: Contents/Info.plist names the executable in Contents/MacOS.
    const bundle = (name: string, plist?: string, executable?: string) => {
      const app = join(dir, 'apps', `${name}.app`)
      mkdirSync(join(app, 'Contents', 'MacOS'), { recursive: true })
      if (plist !== undefined) writeFileSync(join(app, 'Contents', 'Info.plist'), plist)
      if (executable) writeFileSync(join(app, 'Contents', 'MacOS', executable), '#!/bin/sh\n')
      return app
    }
    const infoPlist = (executable: string) =>
      `<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n<plist version="1.0">\n<dict>\n\t<key>CFBundleName</key>\n\t<string>Hermes</string>\n\t<key>CFBundleExecutable</key>\n\t<string>${executable}</string>\n</dict>\n</plist>\n`

    test('A normal .app becomes Contents/MacOS/<CFBundleExecutable>', async () => {
      const app = bundle('Hermes', infoPlist('hermes &amp; co'), 'hermes & co')
      assert.equal(await appExecutable(app, 'darwin'), join(app, 'Contents', 'MacOS', 'hermes & co'))
      assert.equal(await appExecutable(`${app}/`, 'darwin'), join(app, 'Contents', 'MacOS', 'hermes & co'))
    })

    test('A binary Info.plist is read too', { skip: process.platform !== 'darwin' && 'plutil is only on macOS' }, async () => {
      const app = bundle('Binary', infoPlist('binary-app'), 'binary-app')
      execFileSync('plutil', ['-convert', 'binary1', join(app, 'Contents', 'Info.plist')])
      assert.equal(await appExecutable(app, 'darwin'), join(app, 'Contents', 'MacOS', 'binary-app'))
    })

    test('Without a usable Info.plist, the .app path is kept as is, without an error', async () => {
      const cases = [
        bundle('NoPlist'),
        bundle('NoKey', '<plist version="1.0"><dict><key>CFBundleName</key><string>x</string></dict></plist>'),
        bundle('Missing', infoPlist('not-there')),
        bundle('Outside', infoPlist('../../../../bin/sh')),
        bundle('Garbage', 'not a plist at all'),
      ]
      for (const app of cases) assert.equal(await appExecutable(app, 'darwin'), app, app)
    })

    test('An ordinary file, or any path on another system, is kept as is', async () => {
      const file = join(dir, 'apps', 'hermes')
      writeFileSync(file, '#!/bin/sh\n')
      assert.equal(await appExecutable(file, 'darwin'), file)
      const app = bundle('Elsewhere', infoPlist('elsewhere'), 'elsewhere')
      assert.equal(await appExecutable(app, 'win32'), app)
      assert.equal(await appExecutable(app, 'linux'), app)
    })
  })

  test('Cancelling returns null', async () => {
    for (const result of [{ canceled: true, filePaths: [] }, { canceled: false, filePaths: [] }]) {
      const pick = pickPathHandler({ trusted: () => true, show: fakeDialog(result).show, platform: 'darwin' })
      assert.equal(await pick({}, { kind: 'directory' }), null)
    }
  })

  test('A call that does not come from the console page is refused without opening a dialog', async () => {
    const dialog = fakeDialog(picked)
    const sender = { from: 'status page' }
    const pick = pickPathHandler({ trusted: (event) => event !== sender, show: dialog.show, platform: 'darwin' })
    await assert.rejects(pick(sender, { kind: 'directory' }))
    assert.equal(dialog.shown.length, 0)
  })

  test('A malformed request is refused without opening a dialog', async () => {
    const dialog = fakeDialog(picked)
    const pick = pickPathHandler({ trusted: () => true, show: dialog.show, platform: 'darwin' })
    for (const value of [undefined, null, 'directory', {}, { kind: 'folder' }, { kind: 'openDirectory' }, { kind: 'file', defaultPath: 3 }, { kind: 'file', defaultPath: `/${'x'.repeat(maxPathLength)}` }, { kind: 'file', defaultPath: '/tmp/a\0b' }])
      await assert.rejects(pick({}, value), Error, JSON.stringify(value))
    assert.equal(dialog.shown.length, 0)
  })

  test('The dialog starts at the current value only when it is an absolute path on this system', async () => {
    const mac = fakeDialog(picked)
    const pickMac = pickPathHandler({ trusted: () => true, show: mac.show, platform: 'darwin' })
    for (const defaultPath of ['/Users/me/code', 'hermes', '~/code', '', '   ']) await pickMac({}, { kind: 'directory', defaultPath })
    assert.deepEqual(
      mac.shown.map((options) => options.defaultPath),
      ['/Users/me/code', undefined, undefined, undefined, undefined],
    )
    const windows = fakeDialog(picked)
    const pickWindows = pickPathHandler({ trusted: () => true, show: windows.show, platform: 'win32' })
    for (const defaultPath of ['C:\\Users\\me\\code', 'code\\app']) await pickWindows({}, { kind: 'directory', defaultPath })
    assert.deepEqual(
      windows.shown.map((options) => options.defaultPath),
      ['C:\\Users\\me\\code', undefined],
    )
  })
})

describe('Installing the command line tool', () => {
  // macOS only (Windows goes through PATH and creates no link; on Windows, readlink would also resolve a relative target to an absolute path).
  test('Tells whether the link is missing, already points to the bundled loopctl, or is something else', { skip: process.platform === 'win32' && 'macOS only' }, async () => {
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

  test("Windows: checks whether the user PATH has the bundled bin directory (case-insensitive, ignoring a trailing backslash) and appends it only when it doesn't", () => {
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

  test('In AppleScript the path is escaped and handed to the shell with quoted form', () => {
    assert.equal(
      installScript('/Applications/Loop "Now".app/x\\y'),
      'do shell script "mkdir -p " & quoted form of "/usr/local/bin" & " && ln -sfn " & quoted form of "/Applications/Loop \\"Now\\".app/x\\\\y" & " " & quoted form of "/usr/local/bin/loopctl" with administrator privileges',
    )
  })

  let cliSeq = 0
  const freshSettings = (initial?: Record<string, unknown>) => {
    const folder = join(dir, `cli-settings-${++cliSeq}`)
    const file = join(folder, 'settings.json')
    const sf = new SettingsFile(file)
    if (initial) {
      mkdirSync(folder, { recursive: true })
      writeFileSync(file, JSON.stringify(initial, null, 2) + '\n')
    }
    return { file, sf }
  }
  const macPackaged: CliLocation = {
    available: true,
    devOverride: false,
    mode: 'symlink',
    link: cliLink,
    target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
    binDir: '/Applications/Loop Now.app/Contents/Resources/bin',
  }
  const winPackaged: CliLocation = {
    available: true,
    devOverride: false,
    mode: 'path',
    link: 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\bin',
    target: 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\bin\\loopctl.exe',
    binDir: 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\bin',
  }
  const enStrings = () => stringsFor('en-US')

  test('Packaged builds ignore development override environment variables; dev enables CLI only with an absolute LOOP_NOW_DESKTOP_CLI_LINK', () => {
    const env = {
      LOOP_NOW_DESKTOP_CLI_LINK: '/tmp/override/loopctl',
      LOOP_NOW_DESKTOP_CLI_TARGET: '/tmp/override/bin/loopctl',
    }
    assert.deepEqual(
      resolveCliLocation({
        packaged: true,
        platform: 'darwin',
        resourcesPath: '/Applications/Loop Now.app/Contents/Resources',
        serverBin: '/tmp/other/loop-server',
        env,
      }),
      macPackaged,
    )
    assert.deepEqual(
      resolveCliLocation({
        packaged: true,
        platform: 'win32',
        resourcesPath: 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources',
        env,
      }),
      winPackaged,
    )

    assert.equal(resolveCliLocation({ packaged: false, platform: 'darwin', resourcesPath: '/dev/resources', serverBin: '/dev/bundle/loop-server', env: {} }).available, false)
    assert.equal(
      resolveCliLocation({
        packaged: false,
        platform: 'darwin',
        resourcesPath: '/dev/resources',
        serverBin: '/dev/bundle/loop-server',
        env: { LOOP_NOW_DESKTOP_CLI_LINK: 'relative/loopctl' },
      }).available,
      false,
    )
    assert.deepEqual(
      resolveCliLocation({
        packaged: false,
        platform: 'darwin',
        resourcesPath: '/dev/resources',
        serverBin: '/Applications/Loop Now.app/Contents/Resources/server/loop-server',
        env: { LOOP_NOW_DESKTOP_CLI_LINK: '/tmp/verify-cli/loopctl' },
        exists: (file) => file === '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
      }),
      {
        available: true,
        devOverride: true,
        mode: 'symlink',
        link: '/tmp/verify-cli/loopctl',
        target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
        binDir: '/Applications/Loop Now.app/Contents/Resources/bin',
      },
    )
  })

  test('Development direct symlink install creates and updates a symlink in a temporary folder without osascript', { skip: process.platform === 'win32' && 'macOS only' }, async () => {
    const tempDir = join(dir, 'cli-direct-link')
    const link = join(tempDir, 'bin', 'loopctl')
    const target1 = join(tempDir, 'loopctl-v1')
    const target2 = join(tempDir, 'loopctl-v2')
    mkdirSync(tempDir, { recursive: true })
    writeFileSync(target1, '#!/bin/sh\necho v1\n', { mode: 0o755 })
    writeFileSync(target2, '#!/bin/sh\necho v2\n', { mode: 0o755 })

    assert.equal(await installLinkDirect(target1, link), 'done')
    assert.equal(lstatSync(link).isSymbolicLink(), true)
    assert.equal(readlinkSync(link), target1)
    assert.deepEqual(await linkState(link, target1), { state: 'ours' })

    assert.equal(await installLinkDirect(target2, link), 'done')
    assert.equal(readlinkSync(link), target2)
    assert.deepEqual(await linkState(link, target2), { state: 'ours' })
  })

  test('Reports CLI status accurately on macOS (installed, not_installed, other, moveFirst, dev unavailable) and Windows (installed, not_installed, error)', async () => {
    assert.deepEqual(
      await readCliStatus({ location: macPackaged, inApplications: true, externalAvailable: true, checkLink: async () => ({ state: 'ours' }) }),
      { state: 'installed', mode: 'symlink', location: cliLink, target: macPackaged.target, externalAvailable: true },
    )
    assert.deepEqual(
      await readCliStatus({ location: macPackaged, inApplications: false, externalAvailable: true, checkLink: async () => ({ state: 'missing' }) }),
      { state: 'not_installed', mode: 'symlink', location: cliLink, target: macPackaged.target, moveFirst: true, externalAvailable: true },
    )
    assert.deepEqual(
      await readCliStatus({
        location: macPackaged,
        inApplications: true,
        externalAvailable: true,
        checkLink: async () => ({ state: 'other', points: '/opt/homebrew/bin/loopctl' }),
      }),
      { state: 'other', mode: 'symlink', location: cliLink, target: macPackaged.target, points: '/opt/homebrew/bin/loopctl', externalAvailable: true },
    )
    assert.deepEqual(
      await readCliStatus({ location: macPackaged, inApplications: true, externalAvailable: true, checkLink: async () => ({ state: 'other' }) }),
      { state: 'other', mode: 'symlink', location: cliLink, target: macPackaged.target, externalAvailable: true },
    )
    assert.deepEqual(
      await readCliStatus({ location: { ...macPackaged, available: false }, inApplications: false, externalAvailable: false }),
      { state: 'unavailable', mode: 'symlink', reason: 'not_packaged', externalAvailable: false },
    )
    assert.deepEqual(
      await readCliStatus({ location: winPackaged, inApplications: false, externalAvailable: true, readUserPath: async () => `C:\\Go\\bin;${winPackaged.binDir}\\` }),
      { state: 'installed', mode: 'path', location: winPackaged.binDir, target: winPackaged.target, externalAvailable: true },
    )
    assert.deepEqual(
      await readCliStatus({ location: winPackaged, inApplications: false, externalAvailable: true, readUserPath: async () => 'C:\\Go\\bin' }),
      { state: 'not_installed', mode: 'path', location: winPackaged.binDir, target: winPackaged.target, externalAvailable: true },
    )
    assert.deepEqual(
      await readCliStatus({ location: winPackaged, inApplications: false, externalAvailable: false, readUserPath: async () => new Error('powershell failed') }),
      { state: 'unavailable', mode: 'path', reason: 'error', error: 'powershell failed', externalAvailable: false },
    )
  })

  test('First-launch prompt: asks once, remembers Later across restarts, and never asks again', async () => {
    const { file, sf } = freshSettings()
    const shown: CliDialogMessage[] = []
    const outcome = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: sf,
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      showDialog: async (options) => {
        shown.push(options)
        return { response: 1 }
      },
    })
    assert.equal(outcome, 'later')
    assert.equal(shown.length, 1)
    assert.deepEqual(shown[0]?.buttons, ['Install', 'Later'])
    assert.match(shown[0]!.detail, /Claude Code|Codex/)
    assert.match(shown[0]!.detail, /administrator password/i)
    assert.equal(sf.read().cliPrompted, true)

    // Survives restart (new SettingsFile reading the same settings.json).
    const second = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: new SettingsFile(file),
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      showDialog: async () => {
        throw new Error('should not prompt again')
      },
    })
    assert.equal(second, 'skipped_already_prompted')
  })

  test('First-launch prompt: quitting or closing the window while the prompt is open does not record cliPrompted, so the next launch asks again', async () => {
    const { file, sf } = freshSettings()
    let quitting = false
    let shownCount = 0
    const interrupted = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: sf,
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      answered: () => !quitting,
      showDialog: async () => {
        shownCount++
        // App starts quitting while the dialog is open, causing Electron to resolve showMessageBox with cancelId (1).
        quitting = true
        return { response: 1 }
      },
    })
    assert.equal(interrupted, 'interrupted')
    assert.equal(shownCount, 1)
    assert.equal(new SettingsFile(file).read().cliPrompted, undefined)

    // Next launch still asks, and choosing Later records cliPrompted.
    const nextLaunch = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: new SettingsFile(file),
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      answered: () => true,
      showDialog: async () => {
        shownCount++
        return { response: 1 }
      },
    })
    assert.equal(nextLaunch, 'later')
    assert.equal(shownCount, 2)
    assert.equal(new SettingsFile(file).read().cliPrompted, true)

    // If quitting starts while checking linkState (before the dialog opens), neither dialog nor cliPrompted happens.
    const preQuit = freshSettings()
    const beforeOpen = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: preQuit.sf,
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      answered: () => false,
      showDialog: async () => {
        throw new Error('should not open dialog while quitting')
      },
    })
    assert.equal(beforeOpen, 'interrupted')
    assert.equal(preQuit.sf.read().cliPrompted, undefined)
  })

  test('First-launch prompt: choosing Install runs directly, shows the next-step done dialog, and does not ask again; cancelling password still records asked', async () => {
    const { file, sf } = freshSettings()
    const shown: CliDialogMessage[] = []
    const installed: Array<{ target: string; link: string }> = []
    const outcome = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: sf,
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      installLink: async (target, link) => {
        installed.push({ target, link })
        return 'done'
      },
      showDialog: async (options) => {
        shown.push(options)
        return { response: 0 }
      },
    })
    assert.equal(outcome, 'installed')
    assert.deepEqual(installed, [{ target: macPackaged.target, link: macPackaged.link }])
    assert.equal(shown.length, 2)
    assert.equal(shown[1]?.message, 'loopctl is installed')
    assert.match(shown[1]!.detail, /external identity/i)
    assert.match(shown[1]!.detail, /External Identity Connection…/)
    assert.equal(new SettingsFile(file).read().cliPrompted, true)

    const second = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: new SettingsFile(file),
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      showDialog: async () => {
        throw new Error('should not prompt again')
      },
    })
    assert.equal(second, 'skipped_already_prompted')

    // Cancelling the macOS password prompt shows no error dialog and still remembers cliPrompted.
    const cancelRun = freshSettings()
    const cancelShown: CliDialogMessage[] = []
    const cancelled = await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: cancelRun.sf,
      strings: enStrings,
      checkLink: async () => ({ state: 'missing' }),
      installLink: async () => 'cancelled',
      showDialog: async (options) => {
        cancelShown.push(options)
        return { response: 0 }
      },
    })
    assert.equal(cancelled, 'cancelled')
    assert.equal(cancelShown.length, 1)
    assert.equal(cancelRun.sf.read().cliPrompted, true)
  })

  test('First-launch prompt: skipped when already installed, pointing elsewhere, or not yet in Applications (and asks once moved)', async () => {
    for (const current of [{ state: 'ours' as const }, { state: 'other' as const, points: '/opt/homebrew/bin/loopctl' }, { state: 'other' as const }]) {
      const { sf } = freshSettings()
      const outcome = await maybePromptCliInstall({
        location: macPackaged,
        inApplications: true,
        settings: sf,
        strings: enStrings,
        checkLink: async () => current,
        showDialog: async () => {
          throw new Error('should not prompt')
        },
      })
      assert.equal(outcome, current.state === 'ours' ? 'skipped_already_installed' : 'skipped_points_elsewhere')
    }

    const { sf } = freshSettings()
    assert.equal(
      await maybePromptCliInstall({
        location: macPackaged,
        inApplications: false,
        settings: sf,
        strings: enStrings,
        checkLink: async () => ({ state: 'missing' }),
        showDialog: async () => {
          throw new Error('should not prompt outside Applications')
        },
      }),
      'skipped_not_in_applications',
    )
    assert.equal(sf.read().cliPrompted, undefined)

    let prompted = 0
    assert.equal(
      await maybePromptCliInstall({
        location: macPackaged,
        inApplications: true,
        settings: sf,
        strings: enStrings,
        checkLink: async () => ({ state: 'missing' }),
        showDialog: async () => ({ response: (prompted++, 1) }),
      }),
      'later',
    )
    assert.equal(prompted, 1)
  })

  test('First-launch prompt: upgrade case asks once when missing (preserving existing settings) and skips when already installed', async () => {
    const { file, sf } = freshSettings({ port: 8780, externalPort: 8781 })
    assert.equal(
      await maybePromptCliInstall({
        location: macPackaged,
        inApplications: true,
        settings: sf,
        strings: enStrings,
        checkLink: async () => ({ state: 'missing' }),
        showDialog: async () => ({ response: 1 }),
      }),
      'later',
    )
    assert.deepEqual(new SettingsFile(file).read(), { port: 8780, externalPort: 8781, cliPrompted: true })

    const existingInstalled = freshSettings({ port: 8780, externalPort: 8781 })
    assert.equal(
      await maybePromptCliInstall({
        location: macPackaged,
        inApplications: true,
        settings: existingInstalled.sf,
        strings: enStrings,
        checkLink: async () => ({ state: 'ours' }),
        showDialog: async () => {
          throw new Error('should not prompt')
        },
      }),
      'skipped_already_installed',
    )
  })

  test('First-launch prompt: Windows mode prompts once when not on PATH, adds to user PATH without admin, and skips when already present or unreadable', async () => {
    const { file, sf } = freshSettings()
    const shown: CliDialogMessage[] = []
    const added: string[] = []
    assert.equal(
      await maybePromptCliInstall({
        location: winPackaged,
        inApplications: false,
        settings: sf,
        strings: enStrings,
        readUserPath: async () => 'C:\\Go\\bin',
        addToUserPath: async (targetDir) => {
          added.push(targetDir)
          return 'done'
        },
        showDialog: async (options) => {
          shown.push(options)
          return { response: 0 }
        },
      }),
      'installed',
    )
    assert.deepEqual(added, [winPackaged.binDir])
    assert.equal(shown.length, 2)
    assert.match(shown[0]!.detail, /no administrator/i)
    assert.equal(shown[1]?.message, 'loopctl was added to PATH')
    assert.match(shown[1]!.detail, /External Identity Connection…/)
    assert.equal(new SettingsFile(file).read().cliPrompted, true)

    assert.equal(
      await maybePromptCliInstall({
        location: winPackaged,
        inApplications: false,
        settings: freshSettings().sf,
        strings: enStrings,
        readUserPath: async () => `C:\\Go\\bin;${winPackaged.binDir}`,
        showDialog: async () => {
          throw new Error('should not prompt')
        },
      }),
      'skipped_already_installed',
    )

    const errored = freshSettings()
    assert.equal(
      await maybePromptCliInstall({
        location: winPackaged,
        inApplications: false,
        settings: errored.sf,
        strings: enStrings,
        readUserPath: async () => new Error('access denied'),
        showDialog: async () => {
          throw new Error('should not prompt')
        },
      }),
      'skipped_error',
    )
    assert.equal(errored.sf.read().cliPrompted, undefined)

    // Chinese prompt copy carries the expected button labels and next-step hint.
    const zhShown: CliDialogMessage[] = []
    await maybePromptCliInstall({
      location: macPackaged,
      inApplications: true,
      settings: freshSettings().sf,
      strings: () => stringsFor('zh-CN'),
      checkLink: async () => ({ state: 'missing' }),
      installLink: async () => 'done',
      showDialog: async (options) => {
        zhShown.push(options)
        return { response: 0 }
      },
    })
    assert.deepEqual(zhShown[0]?.buttons, ['安装', '以后再说']) // check-english: zh (asserts Chinese prompt button labels)
    assert.match(zhShown[1]!.detail, /外部身份连接方式…/) // check-english: zh (asserts Chinese next-step wording)
  })

  test('First-show ordering runs the git check to completion before loopctl so dialogs never stack', async () => {
    const events: string[] = []
    let finishGit!: () => void
    const gitDone = new Promise<void>((resolve) => {
      finishGit = resolve
    })
    const run = runFirstShowChecks({
      checkGit: async () => {
        events.push('git:start')
        await gitDone
        events.push('git:end')
      },
      checkCli: async () => {
        events.push('cli:start')
        events.push('cli:end')
      },
    })
    await Promise.resolve()
    assert.deepEqual(events, ['git:start'])
    finishGit()
    await run
    assert.deepEqual(events, ['git:start', 'git:end', 'cli:start', 'cli:end'])

    const closedEvents: string[] = []
    let alive = true
    await runFirstShowChecks({
      checkGit: async () => {
        closedEvents.push('git')
        alive = false
      },
      checkCli: async () => {
        closedEvents.push('cli')
      },
      isAlive: () => alive,
    })
    assert.deepEqual(closedEvents, ['git'])
  })

  test('IPC handlers reject untrusted callers, install directly when missing, confirm before replacing, and handle moveFirst/unavailable/failure/Windows', async () => {
    let called = false
    const untrustedStatus = cliStatusHandler({
      trusted: () => false,
      getStatus: async () => {
        called = true
        return { state: 'not_installed', mode: 'symlink', location: cliLink, target: macPackaged.target, externalAvailable: true }
      },
    })
    const untrustedInstall = cliInstallHandler({
      trusted: () => false,
      location: macPackaged,
      settings: freshSettings().sf,
      strings: enStrings,
      getStatus: async () => {
        called = true
        return { state: 'not_installed', mode: 'symlink', location: cliLink, target: macPackaged.target, externalAvailable: true }
      },
      confirmReplace: async () => ({ response: 0 }),
    })
    await assert.rejects(untrustedStatus({}))
    await assert.rejects(untrustedInstall({}))
    assert.equal(called, false)

    const { sf } = freshSettings()
    let currentState: { state: 'missing' } | { state: 'ours' } | { state: 'other'; points?: string } = { state: 'missing' }
    let inApplications = true
    const confirms: CliDialogMessage[] = []
    const getStatus = () => readCliStatus({ location: macPackaged, inApplications, externalAvailable: true, checkLink: async () => currentState })

    const install = cliInstallHandler({
      trusted: () => true,
      location: macPackaged,
      settings: sf,
      strings: enStrings,
      getStatus,
      confirmReplace: async (options) => {
        confirms.push(options)
        return { response: confirms.length === 1 ? 1 : 0 }
      },
      installLink: async () => {
        currentState = { state: 'ours' }
        return 'done'
      },
    })

    // moveFirst refuses without installing.
    inApplications = false
    assert.equal((await install({})).outcome, 'move_first')
    inApplications = true

    // Missing installs directly with no confirmation dialog.
    const installed = await install({})
    assert.equal(installed.outcome, 'installed')
    assert.equal(installed.status.state, 'installed')
    assert.equal(confirms.length, 0)
    assert.equal(sf.read().cliPrompted, true)

    // Already installed does nothing.
    assert.equal((await install({})).outcome, 'already_installed')

    // Pointing elsewhere confirms first; first click cancels, second replaces.
    currentState = { state: 'other', points: '/opt/homebrew/bin/loopctl' }
    assert.equal((await install({})).outcome, 'cancelled')
    assert.equal(confirms.length, 1)
    assert.equal((await install({})).outcome, 'installed')
    assert.equal(confirms.length, 2)

    // Failure returns failed + error message.
    currentState = { state: 'missing' }
    const failing = cliInstallHandler({
      trusted: () => true,
      location: macPackaged,
      settings: freshSettings().sf,
      strings: enStrings,
      getStatus,
      confirmReplace: async () => ({ response: 0 }),
      installLink: async () => new Error('permission denied'),
    })
    const failed = await failing({})
    assert.equal(failed.outcome, 'failed')
    assert.equal(failed.error, 'permission denied')

    // Windows PATH install via IPC.
    const winSettings = freshSettings()
    let userPath = 'C:\\Go\\bin'
    const winInstall = cliInstallHandler({
      trusted: () => true,
      location: winPackaged,
      settings: winSettings.sf,
      strings: enStrings,
      getStatus: () => readCliStatus({ location: winPackaged, inApplications: false, externalAvailable: true, readUserPath: async () => userPath }),
      confirmReplace: async () => {
        throw new Error('Windows PATH install should not confirm replace')
      },
      addToUserPath: async (targetDir) => {
        userPath = `${userPath};${targetDir}`
        return 'done'
      },
    })
    const winResult = await winInstall({})
    assert.equal(winResult.outcome, 'installed')
    assert.equal(winResult.status.state, 'installed')
    assert.equal(winSettings.sf.read().cliPrompted, true)
  })
})

describe('Checking for updates', () => {
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

  // The packaging config's publish is written into app-update.yml and becomes the provider's options. Reading the config requires a release
  // bundle directory, so one is provided temporarily while reading.
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

  // Uses electron-updater's real GitHub provider to read a fake releases.atom (tags from newest to oldest): each release only has
  // latest-mac.yml and latest.yml, and every other file is a 404. The updater settings come from UpdateChecker, the provider options from the
  // packaging config.
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
    // The provider only uses the updater's allowPrerelease, channel and current version (semver accepts strings too).
    const provider = new GitHubProvider(publish(), { ...updater, currentVersion: current, fullChangelog: false } as unknown as AppUpdater, {
      executor: executor as unknown as ProviderRuntimeOptions['executor'],
      platform,
      isUseMultipleRangeRequest: true,
    })
    const { version } = await provider.getLatestVersion()
    return { version, fetched }
  }

  test('Checks releases and prereleases, sets no channel, never downloads or installs by itself, never downgrades; a version is announced only once', () => {
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

  test('A failed check is only logged', async () => {
    const updater = fake()
    const notes: string[] = []
    updater.fail = new Error('Cannot find latest-mac.yml')
    await new UpdateChecker(updater, (n) => notes.push(n)).check()
    assert.deepEqual(notes, ['Update check failed: Cannot find latest-mac.yml'])
  })

  test('With a release installed, the latest is found whether it is a prerelease or a release', async () => {
    // For a pre-release, rc-mac.yml is looked up first, falling back to latest-mac.yml.
    assert.deepEqual(await latestRelease('0.2.0', ['v0.3.0-rc.1', 'v0.2.1', 'v0.2.0']), {
      version: '0.3.0-rc.1',
      fetched: ['.atom', '/download/v0.3.0-rc.1/rc-mac.yml', '/download/v0.3.0-rc.1/latest-mac.yml'],
    })
    assert.deepEqual(await latestRelease('0.2.0', ['v0.2.1', 'v0.2.1-rc.1', 'v0.2.0']), {
      version: '0.2.1',
      fetched: ['.atom', '/download/v0.2.1/latest-mac.yml'],
    })
  })

  test('Windows reads latest.yml and likewise only points to the download page (the build config sets no channel and no auto install for win)', async () => {
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

  test('With a new version, the menu bar gets an item for the download page', () => {
    const items = trayItems('running', 'http://127.0.0.1:8780', undefined, stringsFor('en-US'), true, '0.2.0')
    assert.deepEqual(items[2], { label: 'Version 0.2.0 is available — Download…', enabled: true, action: 'update' })
  })
})
