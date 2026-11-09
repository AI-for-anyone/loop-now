// Loop Now 桌面壳：拉起并守护 loop-server，窗口加载它在同一端口提供的控制台，菜单栏图标显示状态。
// 壳只做壳，不含业务逻辑。
import { app, clipboard, dialog, powerMonitor, session, shell, type BrowserWindow } from 'electron'
import { autoUpdater } from 'electron-updater'
import { mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { Badge } from './badge'
import { RotatingLog } from './log'
import { setAppMenu } from './menu'
import { handleNotifications } from './notifications'
import { choosePort, portOf } from './port'
import { addToUserPath, cliLink, installLink, linkState, readUserPath, userPathHas } from './cli'
import { externalCertPath, loopctlConfigCommand } from './external'
import { gitUsable, installGit } from './git'
import { cliBinary, cliInstallMode, current as platform, serverBinary } from './platform'
import { defaultProfile, overridesFromFile, profileArg, profileEnv, profileFromFile, profileSummary, resolveProfile, type Profile } from './profile'
import { readyTimeoutMs, ServerProcess, type ServerState, type StartFailure } from './server'
import { SettingsFile } from './settings'
import { strings } from './strings'
import { StatusTray } from './tray'
import { releasePage, UpdateChecker } from './updates'
import { MainWindow, type StatusView } from './window'

// 配置要在 app 就绪前定下：userData 决定设置、浏览器存储与单实例锁的位置。打包后的 app 什么都没选时用默认配置；
// 开发时必须明确选一个，开发时的默认配置就是装好的 app 自己在用的那一套。desktop.json 还可以为配置指定目录。
let profile: Profile | undefined
let profileError: string | undefined
const desktopFile = join(app.getPath('userData'), 'desktop.json')
const chosen = profileArg(process.argv, process.env) ?? profileFromFile(desktopFile)
const requested = chosen ?? (app.isPackaged ? defaultProfile : undefined)
if (requested !== undefined) {
  try {
    profile = resolveProfile(requested, undefined, overridesFromFile(desktopFile, requested))
    mkdirSync(profile.shellDir, { recursive: true })
    app.setPath('userData', profile.shellDir)
  } catch (err) {
    profileError = (err as Error).message
  }
}

// 后台启动（--hidden）：只出菜单栏图标，不开窗口、不占 Dock，等人从菜单栏、Dock 或再次打开 app 时才显示窗口。
const background = process.argv.includes('--hidden')

if (!app.requestSingleInstanceLock()) app.quit()
else run()

function run(): void {
  const userData = app.getPath('userData')
  const logDir = profile?.logDir ?? app.getPath('logs')
  const shellLog = new RotatingLog(join(logDir, 'shell.log'))
  const serverLog = new RotatingLog(join(logDir, 'server.log'))
  const settings = new SettingsFile(join(userData, 'settings.json'))
  const externalTLSDir = join(userData, 'tls')
  const note = (text: string) => shellLog.line(text)
  // 每次启动的第一行：用的哪个配置、交给服务端的三个目录。
  const which = profile ? profileSummary(profile) : profileError ? `配置不可用（${profileError}）` : '没有选配置'
  note(`壳启动：${which}；Electron ${process.versions.electron}${background ? '，后台启动' : ''}`)

  let quitting = false // 已确认退出：关窗口不再只是隐藏
  let confirming = false

  // 打包后服务端在 app 资源的 server/ 下；开发时用 LOOP_NOW_DESKTOP_SERVER 指定（服务端以自己所在目录为 cwd 启动，所以转成绝对路径）。
  const devBin = process.env.LOOP_NOW_DESKTOP_SERVER
  const bin = app.isPackaged ? join(process.resourcesPath, 'server', serverBinary(platform)) : devBin ? resolve(devBin) : undefined
  // 起不了服务端的情形（在 app 就绪后调用，语言要到那时才确定）。没有配置只会发生在开发时没选的情况下。
  const blocked = (): StatusView | undefined => {
    const s = strings().page
    if (profileError) return { title: s.badProfileTitle, body: profileError }
    if (!bin) return { title: s.noServerTitle, body: s.noServerBody }
    if (!profile) return { title: s.noProfileTitle, body: s.noProfileBody }
    return undefined
  }

  const server = new ServerProcess({
    bin: bin ?? '',
    readyFile: join(userData, 'run', 'server-ready.json'),
    stampFile: join(userData, 'run', 'server-stamp.json'),
    env: profile ? { ...process.env, ...profileEnv(profile) } : process.env,
    log: serverLog,
    note,
    // 第一次交给系统选，之后沿用记住的端口（见 port.ts）。
    choosePort: () => choosePort(settings.read().port ?? 0),
    // 对外 TLS 端口（外部身份与 loopctl）：同样记住端口；自签证书放在壳的 userData/tls，由服务端维护。
    external: { tlsDir: externalTLSDir, choosePort: () => choosePort(settings.read().externalPort ?? 0) },
  })
  const window = new MainWindow({
    // 默认配置的窗口标题不带名字。
    profile: profile?.label === defaultProfile ? undefined : profile?.label,
    background,
    onFirstShow: (win) => void checkGit(win),
    quitting: () => quitting,
    note,
    onAction: (action) => (action === 'retry' ? void server.restart() : void shell.openPath(logDir)),
  })
  let tray: StatusTray | undefined
  const badge = new Badge(() => window.browserWindow)
  const updates = new UpdateChecker(autoUpdater, note)
  updates.on('available', (version) => {
    note(`有新版本 ${version}`)
    tray?.update(server.state, server.url, server.externalUrl)
  })

  const startingView = (): StatusView => ({ title: strings().page.startingTitle, body: strings().page.startingBody, busy: true })
  const failureView = (title: string, body: string): StatusView => {
    const s = strings()
    const full = profile ? `${body}\n${s.page.profileRoot(profile.root)}` : body
    return { title, body: full, log: serverLog.tail(40), actions: { retry: s.page.retry, logs: s.page.logs } }
  }
  const failureBody = (failure: StartFailure | undefined): string => {
    const s = strings().page
    switch (failure?.kind) {
      case 'spawn':
        return s.spawn(bin ?? '', failure.message)
      case 'config':
        return s.config
      case 'locked':
        return s.locked
      case 'timeout':
        return s.timeout(readyTimeoutMs / 1000)
      case 'exited':
        return s.exited(failure.signal ? s.exitSignal(failure.signal) : s.exitCode(failure.code))
      default:
        return ''
    }
  }

  // 第一次显示窗口时查一次 git（服务端用它拉取仓库）；不可用就提示一句，可以直接打开系统的安装框（Windows 打开 git-scm.com）。不打包 git。
  async function checkGit(win: BrowserWindow): Promise<void> {
    if (await gitUsable()) return
    note(platform === 'win32' ? 'git 不可用：PATH 里没有 git.exe' : 'git 不可用：没有安装命令行工具')
    const s = strings().git
    const { response } = await dialog.showMessageBox(win, {
      type: 'warning',
      message: s.message,
      detail: platform === 'win32' ? s.detailWindows : s.detail,
      buttons: [platform === 'win32' ? s.installWindows : s.install, s.later],
      defaultId: 0,
      cancelId: 1,
    })
    if (response === 0) installGit()
  }

  // 托盘"安装命令行工具"。macOS：/usr/local/bin/loopctl 链到包内固定的 Resources/bin/loopctl（打包时链到当前版本的二进制）。
  // Windows：把 resources\bin（里面是复制出来的 loopctl.exe）加进用户 PATH（取舍见 platform.ts 的 cliInstallMode）。
  // 由人从菜单里触发，确认框和系统的管理员密码框都在这时出现。
  async function installCli(): Promise<void> {
    const s = strings().cli
    const info = (message: string, detail: string, type: 'info' | 'warning' = 'info') => dialog.showMessageBox({ type, message, detail })
    if (cliInstallMode(platform) === 'path') return installCliOnPath(info)
    if (!app.isInApplicationsFolder()) return void (await info(s.moveFirst, s.moveFirstDetail, 'warning'))
    const target = join(process.resourcesPath, 'bin', cliBinary(platform))
    const current = await linkState(cliLink, target)
    if (current.state === 'ours') return void (await info(s.already, `${cliLink} → ${target}\n\n${s.external}`))
    const ask =
      current.state === 'other'
        ? { message: s.replace(cliLink), detail: `${s.replaceDetail(current.points ?? cliLink)}\n\n${s.external}`, button: s.replaceButton }
        : { message: s.confirm, detail: `${s.confirmDetail(cliLink)}\n\n${s.external}`, button: s.install }
    app.focus({ steal: true })
    const { response } = await dialog.showMessageBox({ type: 'question', message: ask.message, detail: ask.detail, buttons: [ask.button, s.cancel], defaultId: 0, cancelId: 1 })
    if (response !== 0) return
    const result = await installLink(target)
    if (result === 'cancelled') return
    if (result instanceof Error) {
      note(`安装 loopctl 失败：${result.message}`)
      return void (await info(s.failed, result.message, 'warning'))
    }
    note(`已安装 loopctl：${cliLink} → ${target}`)
    await info(s.done, `${cliLink} → ${target}\n\n${s.external}`)
  }

  async function installCliOnPath(info: (message: string, detail: string, type?: 'info' | 'warning') => Promise<unknown>): Promise<void> {
    const s = strings().cli
    const dir = join(process.resourcesPath, 'bin')
    const current = await readUserPath()
    if (current instanceof Error) {
      note(`读不到用户 PATH：${current.message}`)
      return void (await info(s.failed, current.message, 'warning'))
    }
    if (userPathHas(current, dir)) return void (await info(s.pathAlready, `${s.pathAlreadyDetail(dir)}\n\n${s.external}`))
    app.focus({ steal: true })
    const { response } = await dialog.showMessageBox({ type: 'question', message: s.pathConfirm, detail: `${s.pathConfirmDetail(dir)}\n\n${s.external}`, buttons: [s.add, s.cancel], defaultId: 0, cancelId: 1 })
    if (response !== 0) return
    const result = await addToUserPath(dir)
    if (result instanceof Error) {
      note(`把 loopctl 加进 PATH 失败：${result.message}`)
      return void (await info(s.failed, result.message, 'warning'))
    }
    note(`已把 ${dir} 加进用户 PATH`)
    await info(s.pathDone, `${s.pathDoneDetail(dir)}\n\n${s.external}`)
  }

  // 托盘"外部身份连接方式…"：对外 TLS 端口的地址、自签证书的位置和把令牌配进 loopctl 的命令，可以一键复制命令。
  // 菜单项只在服务端就绪且开了对外端口时可点，这里仍防一手。
  async function showExternal(): Promise<void> {
    const url = server.externalUrl
    if (!url) return
    const s = strings().external
    const caFile = externalCertPath(externalTLSDir)
    const command = loopctlConfigCommand(url, caFile)
    app.focus({ steal: true })
    const { response } = await dialog.showMessageBox({ type: 'info', message: s.title, detail: s.detail(url, caFile, command), buttons: [s.copy, s.close], defaultId: 0, cancelId: 1 })
    if (response === 0) clipboard.writeText(command)
  }

  let previous: ServerState = server.state
  server.on('state', (state) => {
    note(`状态：${previous} → ${state}`)
    tray?.update(state, server.url, server.externalUrl)
    // 崩溃后的自动重启保留控制台页面（它会自己重连）；其余启动显示状态页。
    if (state === 'starting' && previous !== 'restarting') window.showStatus(startingView())
    if (state === 'failed') {
      window.showStatus(failureView(strings().page.failedTitle, failureBody(server.failure)))
      window.attention()
    }
    if (state === 'crashed') {
      window.showStatus(failureView(strings().page.crashedTitle, strings().page.crashedBody))
      window.attention()
    }
    previous = state
  })
  server.on('ready', (url) => {
    const port = portOf(url)
    const externalPort = server.externalUrl ? portOf(server.externalUrl) : undefined
    const remembered = settings.read()
    const patch = {
      ...(port !== undefined && port !== remembered.port ? { port } : {}),
      ...(externalPort !== undefined && externalPort !== remembered.externalPort ? { externalPort } : {}),
    }
    if (Object.keys(patch).length > 0) settings.update(patch)
    tray?.update(server.state, url, server.externalUrl)
    window.showConsole(url)
  })

  // 退出：停掉服务端（它会停掉所有 Agent）再退出。只有人从托盘或 Cmd+Q（Windows 只有托盘：关窗口只是收进托盘）选"退出"时 confirm 为 true，先问一句；
  // 信号、关机注销、Dock 菜单等其他途径都经 before-quit 直接退出，不弹框（Electron 自己处理 SIGTERM 时也走 before-quit，
  // 和我们的信号处理谁先到不固定，所以 before-quit 一律不问）。macOS 上没有父窗口的确认框会阻塞主线程，期间到的信号等人答完再处理。
  async function quit(confirm: boolean): Promise<void> {
    if (confirming || quitting) return
    confirming = true
    try {
      if (confirm && server.alive) {
        const s = strings().quit
        app.focus({ steal: true })
        const { response } = await dialog.showMessageBox({
          type: 'question',
          message: s.message,
          detail: s.detail,
          buttons: [s.confirm, s.cancel],
          defaultId: 0,
          cancelId: 1,
        })
        if (response !== 0) return
      }
      quitting = true
      await server.stop()
      note('退出')
      // 放到下一轮再退：没有服务端要停时上面的 await 只隔一个微任务，还在 Electron 分发 before-quit 的调用里，
      // 这时再调 app.quit() 会被丢掉，进程不退出（收到 SIGTERM、启动失败后退出时都会遇到）。
      setImmediate(() => app.quit())
    } finally {
      confirming = false
    }
  }

  app.on('before-quit', (event) => {
    if (quitting) return
    event.preventDefault()
    void quit(false)
  })
  for (const signal of ['SIGINT', 'SIGTERM'] as const) process.on(signal, () => void quit(false))
  app.on('second-instance', () => window.show())
  app.on('activate', () => window.show())
  // 关窗口只是隐藏，不退出。
  app.on('window-all-closed', () => undefined)

  void app.whenReady().then(() => {
    if (background) app.dock?.hide()
    // 开发时 Electron.app 自带的是 Electron 的图标；打包后用 app 自己的图标。
    if (!app.isPackaged) app.dock?.setIcon(join(__dirname, '..', 'assets', 'icon.png'))
    setAppMenu(() => void quit(true))
    handleNotifications({ trusted: (event) => window.trusts(event), open: (path) => window.navigate(path), badge: (count) => badge.set(count) })
    session.defaultSession.setPermissionRequestHandler((_contents, permission, callback) =>
      callback(permission === 'clipboard-sanitized-write' || permission === 'fullscreen'),
    )
    // 关机、重启或注销：先推迟系统关机，停掉服务端再退出。类型声明里没有参数，实际传入可 preventDefault 的事件。
    powerMonitor.on('shutdown', ((event: Electron.Event) => {
      event.preventDefault()
      void quit(false)
    }) as () => void)
    tray = new StatusTray(
      {
        update: () => updates.available && void shell.openExternal(releasePage(updates.available)),
        open: () => window.show(),
        browser: () => server.url && void shell.openExternal(server.url),
        logs: () => void shell.openPath(logDir),
        cli: () => void installCli(),
        external: () => void showExternal(),
        restart: () => void server.restart(),
        quit: () => void quit(true),
      },
      profile,
      () => updates.available,
    )
    tray.update(server.state, server.url, server.externalUrl)
    if (app.isPackaged) updates.start()
    const view = blocked()
    if (view) {
      window.showStatus(view)
      return
    }
    void server.start()
  })
}
