// Loop Now 桌面壳：拉起并守护 loop-server，窗口加载它在同一端口提供的控制台，菜单栏图标显示状态。
// 壳只做壳，不含业务逻辑。
import { app, dialog, powerMonitor, session, shell } from 'electron'
import { mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { RotatingLog } from './log'
import { setAppMenu } from './menu'
import { choosePort, defaultPort, portOf } from './port'
import { profileArg, profileEnv, resolveProfile, type Profile } from './profile'
import { readyTimeoutMs, ServerProcess, type ServerState, type StartFailure } from './server'
import { SettingsFile } from './settings'
import { strings } from './strings'
import { StatusTray } from './tray'
import { MainWindow, type StatusView } from './window'

// 隔离配置要在 app 就绪前定下：userData 决定设置、浏览器存储与单实例锁的位置。
let profile: Profile | undefined
let profileError: string | undefined
const requested = profileArg(process.argv, process.env)
if (requested !== undefined) {
  try {
    profile = resolveProfile(requested)
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
  const note = (text: string) => shellLog.line(text)

  let quitting = false // 已确认退出：关窗口不再只是隐藏
  let confirming = false

  // 打包后服务端在 app 资源的 server/ 下；开发时用 LOOP_NOW_DESKTOP_SERVER 指定（服务端以自己所在目录为 cwd 启动，所以转成绝对路径）。
  const devBin = process.env.LOOP_NOW_DESKTOP_SERVER
  const bin = app.isPackaged ? join(process.resourcesPath, 'server', 'loop-server') : devBin ? resolve(devBin) : undefined
  // 起不了服务端的情形（在 app 就绪后调用，语言要到那时才确定）。开发时必须用隔离配置：服务端的默认目录正是线上实例在用的。
  const blocked = (): StatusView | undefined => {
    const s = strings().page
    if (profileError) return { title: s.badProfileTitle, body: profileError }
    if (!bin) return { title: s.noServerTitle, body: s.noServerBody }
    if (!app.isPackaged && !profile) return { title: s.noProfileTitle, body: s.noProfileBody }
    return undefined
  }

  const server = new ServerProcess({
    bin: bin ?? '',
    readyFile: join(userData, 'run', 'server-ready.json'),
    env: profile ? { ...process.env, ...profileEnv(profile) } : process.env,
    log: serverLog,
    note,
    // 默认配置第一次试 8780；隔离配置第一次就交给系统选，免得占住线上实例的端口。
    choosePort: () => choosePort(settings.read().port ?? (profile ? 0 : defaultPort)),
  })
  const window = new MainWindow({
    profile: profile?.label,
    background,
    quitting: () => quitting,
    note,
    onAction: (action) => (action === 'retry' ? void server.restart() : void shell.openPath(logDir)),
  })
  let tray: StatusTray | undefined

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

  let previous: ServerState = server.state
  server.on('state', (state) => {
    note(`状态：${previous} → ${state}`)
    tray?.update(state, server.url)
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
    if (port !== undefined && port !== settings.read().port) settings.update({ port })
    tray?.update(server.state, url)
    window.showConsole(url)
  })

  // 退出：停掉服务端（它会停掉所有 Agent）再退出。只有人从托盘或 Cmd+Q 选"退出"时 confirm 为 true，先问一句；
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
    note(`壳启动：Electron ${process.versions.electron}${profile ? `，隔离配置 ${profile.root}` : ''}${background ? '，后台启动' : ''}`)
    if (background) app.dock?.hide()
    // 开发时 Electron.app 自带的是 Electron 的图标；打包后用 app 自己的图标。
    if (!app.isPackaged) app.dock?.setIcon(join(__dirname, '..', 'assets', 'icon.png'))
    setAppMenu(() => void quit(true))
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
        open: () => window.show(),
        browser: () => server.url && void shell.openExternal(server.url),
        logs: () => void shell.openPath(logDir),
        restart: () => void server.restart(),
        quit: () => void quit(true),
      },
      profile?.label,
    )
    tray.update(server.state, server.url)
    const view = blocked()
    if (view) {
      window.showStatus(view)
      return
    }
    void server.start()
  })
}
