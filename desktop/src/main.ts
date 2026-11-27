// Loop Now desktop shell: launches and supervises loop-server, the window loads the console it serves on the same port, and the menu bar
// icon shows the state.
// The shell is only a shell; it contains no business logic.
import { app, clipboard, dialog, ipcMain, nativeTheme, powerMonitor, session, shell, type BrowserWindow } from 'electron'
import { autoUpdater } from 'electron-updater'
import { mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { nativeThemeSource, resolveWindowAppearance } from './appearance'
import { Badge } from './badge'
import { parseAppearance, type DesktopAppearance } from './bridge'
import { handlePathPicker } from './dialogs'
import { RotatingLog } from './log'
import { setAppMenu } from './menu'
import { handleNotifications } from './notifications'
import { choosePort, portOf } from './port'
import {
  addToUserPath,
  cliInstallChannel,
  cliInstallHandler,
  cliStatusChannel,
  cliStatusHandler,
  installLink,
  installLinkDirect,
  linkState,
  maybePromptCliInstall,
  readCliStatus,
  readUserPath,
  resolveCliLocation,
  runFirstShowChecks,
  userPathHas,
  type CliDialogMessage,
} from './cli'
import { externalCertPath, loopctlConfigCommand } from './external'
import { gitUsable, installGit } from './git'
import { current as platform, serverBinary } from './platform'
import { defaultProfile, overridesFromFile, profileArg, profileEnv, profileFromFile, profileSummary, resolveProfile, type Profile } from './profile'
import { readyTimeoutMs, ServerProcess, type ServerState, type StartFailure } from './server'
import { SettingsFile } from './settings'
import { errorText, strings } from './strings'
import { StatusTray } from './tray'
import { releasePage, UpdateChecker } from './updates'
import { MainWindow, type StatusView } from './window'

// The profile has to be settled before the app is ready: userData decides where settings, browser storage and the single-instance lock live.
// The packaged app uses the default profile when nothing is chosen; in development one must be chosen explicitly, because the default profile
// in development is the very one the installed app itself uses. desktop.json can also specify directories for a profile.
let profile: Profile | undefined
// Shown on the status page once the language is known (a LocalizedError from profile.ts, or a system error such as mkdir failing).
let profileError: Error | undefined
const desktopFile = join(app.getPath('userData'), 'desktop.json')
const chosen = profileArg(process.argv, process.env) ?? profileFromFile(desktopFile)
const requested = chosen ?? (app.isPackaged ? defaultProfile : undefined)
if (requested !== undefined) {
  try {
    profile = resolveProfile(requested, undefined, overridesFromFile(desktopFile, requested))
    mkdirSync(profile.shellDir, { recursive: true })
    app.setPath('userData', profile.shellDir)
  } catch (err) {
    profileError = err as Error
  }
}

// Background start (--hidden): only the menu bar icon appears, with no window and no Dock icon; the window shows only once the user opens it
// from the menu bar or the Dock, or opens the app again.
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
  // The first line of every start: which profile is used and the three directories handed to the server. An unusable profile is logged with
  // the error's message, which is English (this runs before the app is ready, when the UI language is not known yet).
  const which = profile ? profileSummary(profile) : profileError ? `profile unusable (${profileError.message})` : 'no profile chosen'
  note(`Shell started: ${which}; Electron ${process.versions.electron}${background ? ', background start' : ''}`)

  let quitting = false // quit confirmed: closing the window no longer just hides it
  let confirming = false

  // When packaged, the server is under server/ in the app resources; in development LOOP_NOW_DESKTOP_SERVER points to it (the server starts
  // with its own directory as cwd, so the path is made absolute).
  const devBin = process.env.LOOP_NOW_DESKTOP_SERVER
  const bin = app.isPackaged ? join(process.resourcesPath, 'server', serverBinary(platform)) : devBin ? resolve(devBin) : undefined
  const cliLocation = resolveCliLocation({
    packaged: app.isPackaged,
    platform,
    resourcesPath: process.resourcesPath,
    serverBin: bin,
    env: process.env,
  })
  const inApplications = () => (platform === 'darwin' ? app.isInApplicationsFolder() : true)
  // Cases where the server cannot be started (called after the app is ready, because the language is only known then). A missing profile
  // only happens in development when none was chosen.
  const blocked = (): StatusView | undefined => {
    const s = strings().page
    if (profileError) return { title: s.badProfileTitle, body: errorText(profileError, strings()) }
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
    // The system picks the first time; after that the remembered port is reused (see port.ts).
    choosePort: () => choosePort(settings.read().port ?? 0),
    // External TLS port (external identities and loopctl): its port is remembered too; the self-signed certificate lives in the shell's
    // userData/tls and is maintained by the server.
    external: { tlsDir: externalTLSDir, choosePort: () => choosePort(settings.read().externalPort ?? 0) },
  })
  let currentAppearance: DesktopAppearance | undefined = parseAppearance(settings.read().appearance)
  nativeTheme.themeSource = nativeThemeSource(currentAppearance)
  const window = new MainWindow({
    // The window title for the default profile carries no name.
    profile: profile?.label === defaultProfile ? undefined : profile?.label,
    background,
    appearance: resolveWindowAppearance(currentAppearance, nativeTheme.shouldUseDarkColors),
    onFirstShow: (win) =>
      void runFirstShowChecks({
        checkGit: () => checkGit(win),
        checkCli: async () => {
          if (blocked()) return
          await maybePromptCliInstall({
            location: cliLocation,
            inApplications: inApplications(),
            settings,
            strings,
            showDialog: (options) => dialog.showMessageBox(win, options),
            note,
          })
        },
        isAlive: () => !win.isDestroyed(),
      }),
    quitting: () => quitting,
    note,
    onAction: (action) => (action === 'retry' ? void server.restart() : void shell.openPath(logDir)),
  })
  let tray: StatusTray | undefined
  const badge = new Badge(() => window.browserWindow)
  const updates = new UpdateChecker(autoUpdater, note)
  updates.on('available', (version) => {
    note(`New version available: ${version}`)
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

  // Checks git once when the window is first shown (the server uses it to fetch repositories); if it is unusable, shows a short notice that
  // can open the system install dialog directly (git-scm.com on Windows). git is not bundled.
  async function checkGit(win: BrowserWindow): Promise<void> {
    if (await gitUsable()) return
    note(platform === 'win32' ? 'git unusable: no git.exe on PATH' : 'git unusable: command line tools not installed')
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

  // Tray "安装命令行工具" (Install Command Line Tool…). macOS: link /usr/local/bin/loopctl to the fixed Resources/bin/loopctl inside the
  // bundle (which, at packaging time, is linked to the current version's binary).
  // Windows: add resources\bin (holding the copied loopctl.exe) to the user PATH (for the trade-offs see cliInstallMode in platform.ts).
  // Triggered by the user from the menu; both the confirmation dialog and the system's administrator password prompt appear at that point.
  async function installCli(): Promise<void> {
    if (!cliLocation.available) return
    const s = strings().cli
    const info = (message: string, detail: string, type: 'info' | 'warning' = 'info') => dialog.showMessageBox({ type, message, detail })
    if (cliLocation.mode === 'path') return installCliOnPath(info)
    if (!cliLocation.devOverride && !inApplications()) return void (await info(s.moveFirst, s.moveFirstDetail, 'warning'))
    const { link, target } = cliLocation
    const current = await linkState(link, target)
    if (current.state === 'ours') return void (await info(s.already, `${link} → ${target}\n\n${s.external}`))
    const ask =
      current.state === 'other'
        ? { message: s.replace(link), detail: `${s.replaceDetail(current.points ?? link)}\n\n${s.external}`, button: s.replaceButton }
        : { message: s.confirm, detail: `${s.confirmDetail(link)}\n\n${s.external}`, button: s.install }
    app.focus({ steal: true })
    const { response } = await dialog.showMessageBox({ type: 'question', message: ask.message, detail: ask.detail, buttons: [ask.button, s.cancel], defaultId: 0, cancelId: 1 })
    if (response !== 0) return
    const result = cliLocation.devOverride ? await installLinkDirect(target, link) : await installLink(target, link)
    if (result === 'cancelled') return
    if (result instanceof Error) {
      note(`Installing loopctl failed: ${result.message}`)
      return void (await info(s.failed, result.message, 'warning'))
    }
    settings.update({ cliPrompted: true })
    note(`Installed loopctl: ${link} → ${target}`)
    await info(s.done, `${link} → ${target}\n\n${s.external}`)
  }

  async function installCliOnPath(info: (message: string, detail: string, type?: 'info' | 'warning') => Promise<unknown>): Promise<void> {
    const s = strings().cli
    const dir = cliLocation.binDir
    const current = await readUserPath()
    if (current instanceof Error) {
      note(`Cannot read the user PATH: ${current.message}`)
      return void (await info(s.failed, current.message, 'warning'))
    }
    if (userPathHas(current, dir)) return void (await info(s.pathAlready, `${s.pathAlreadyDetail(dir)}\n\n${s.external}`))
    app.focus({ steal: true })
    const { response } = await dialog.showMessageBox({ type: 'question', message: s.pathConfirm, detail: `${s.pathConfirmDetail(dir)}\n\n${s.external}`, buttons: [s.add, s.cancel], defaultId: 0, cancelId: 1 })
    if (response !== 0) return
    const result = await addToUserPath(dir)
    if (result instanceof Error) {
      note(`Adding loopctl to PATH failed: ${result.message}`)
      return void (await info(s.failed, errorText(result, strings()), 'warning'))
    }
    settings.update({ cliPrompted: true })
    note(`Added ${dir} to the user PATH`)
    await info(s.pathDone, `${s.pathDoneDetail(dir)}\n\n${s.external}`)
  }

  // Tray "外部身份连接方式…" (External Identity Connection…): the external TLS port's address, the location of the self-signed certificate
  // and the command that configures the token into loopctl, with one click to copy the command.
  // The menu item is only enabled when the server is ready and the external port is open; still guarded here just in case.
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
    note(`State: ${previous} → ${state}`)
    tray?.update(state, server.url, server.externalUrl)
    // An automatic restart after a crash keeps the console page (it reconnects by itself); every other start shows the status page.
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

  // Quit: stop the server (which stops all Agents), then quit. confirm is true, and the user is asked first, only when the user chooses "退出"
  // (Quit) from the tray or with Cmd+Q (Windows only has the tray: closing the window just tucks it into the tray);
  // every other path (signals, shutdown/log off, the Dock menu, …) quits directly through before-quit without a dialog (Electron's own SIGTERM
  // handling also goes through before-quit, and whether it or our signal handler comes first is not fixed, so before-quit never asks). On macOS
  // a confirmation dialog without a parent window blocks the main thread; signals arriving meanwhile are handled after the user answers.
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
      note('Quitting')
      // Quit on the next tick: when there is no server to stop, the await above is only one microtask away and still inside Electron's
      // before-quit dispatch, where calling app.quit() again is dropped and the process does not exit (happens both on SIGTERM and when
      // quitting after a failed start).
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
  // Closing the window only hides it; the app does not quit.
  app.on('window-all-closed', () => undefined)

  void app.whenReady().then(() => {
    if (background) app.dock?.hide()
    // In development, Electron.app carries Electron's icon; the packaged app uses its own icon.
    if (!app.isPackaged) app.dock?.setIcon(join(__dirname, '..', 'assets', 'icon.png'))
    setAppMenu(() => void quit(true))
    handleNotifications({ trusted: (event) => window.trusts(event), open: (path) => window.navigate(path), badge: (count) => badge.set(count) })
    handlePathPicker({ trusted: (event) => window.trusts(event), window: () => window.browserWindow, platform })
    const getCliStatus = () =>
      readCliStatus({
        location: cliLocation,
        inApplications: inApplications(),
        externalAvailable: (server.state === 'running' || server.state === 'degraded') && server.url !== undefined && server.externalUrl !== undefined,
      })
    ipcMain.handle(cliStatusChannel, cliStatusHandler({ trusted: (event) => window.trusts(event), getStatus: getCliStatus }))
    ipcMain.handle(
      cliInstallChannel,
      cliInstallHandler({
        trusted: (event) => window.trusts(event),
        location: cliLocation,
        settings,
        strings,
        getStatus: getCliStatus,
        confirmReplace: (options: CliDialogMessage) => {
          const win = window.browserWindow
          return win ? dialog.showMessageBox(win, options) : dialog.showMessageBox(options)
        },
        note,
      }),
    )
    ipcMain.on('loop-now:appearance', (event, raw) => {
      if (!window.trusts(event)) return
      const appearance = parseAppearance(raw)
      if (!appearance) return
      currentAppearance = appearance
      nativeTheme.themeSource = nativeThemeSource(appearance)
      window.setAppearance(resolveWindowAppearance(appearance, nativeTheme.shouldUseDarkColors))
      settings.update({ appearance })
    })
    nativeTheme.on('updated', () => {
      window.setAppearance(resolveWindowAppearance(currentAppearance, nativeTheme.shouldUseDarkColors))
    })
    session.defaultSession.setPermissionRequestHandler((_contents, permission, callback) =>
      callback(permission === 'clipboard-sanitized-write' || permission === 'fullscreen'),
    )
    // Shutdown, restart or log off: hold off the system shutdown first, stop the server, then quit. The type declaration has no parameter,
    // but an event that supports preventDefault is actually passed in.
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
      platform,
      cliLocation.available,
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
