import { app, BrowserWindow, screen, shell, type IpcMainEvent } from 'electron'
import { join } from 'node:path'
import { classifyNavigation, handOffOnce } from './links'
import { current, windowChrome, type PlatformId } from './platform'

// What the shell's built-in status page (starting, failed to start) shows; the text is already in the chosen language.
export type StatusView = {
  title: string
  body: string
  log?: string
  actions?: { retry: string; logs: string }
  busy?: boolean
}

export type StatusAction = 'retry' | 'logs'

type Options = {
  // The name of a separate profile, shown after the window title so the window is not confused with the default profile's.
  profile?: string
  // Background start: no window and no Dock icon until the user first asks to see the window.
  background: boolean
  // Called when the window is shown for the first time.
  onFirstShow?: (win: BrowserWindow) => void
  // Closing the window really closes it only once quitting is confirmed; otherwise it just hides and the server keeps running.
  quitting: () => boolean
  onAction: (action: StatusAction) => void
  note: (text: string) => void
  platform?: PlatformId
}

const statusPage = join(__dirname, '..', 'static', 'status.html')

// The single main window: loads the console the server serves, and shows the status page until the server is ready.
export class MainWindow {
  private win: BrowserWindow | undefined
  // The console address currently loaded; undefined while the status page is shown.
  private consoleURL: string | undefined
  // Whether the user has opened the window yet (false before the first show in a background start).
  private opened: boolean

  constructor(private readonly o: Options) {
    this.opened = !o.background
  }

  get showingConsole(): boolean {
    return this.consoleURL !== undefined
  }

  get browserWindow(): BrowserWindow | undefined {
    return this.win && !this.win.isDestroyed() ? this.win : undefined
  }

  // The user asks to see the window (menu bar, Dock, opening the app again).
  show(): void {
    this.opened = true
    void app.dock?.show()
    const win = this.ensure()
    if (win.isMinimized()) win.restore()
    win.show()
    win.focus()
  }

  // When the user's attention is needed (failed start, repeated crashes): bring the window to the front only if the user has opened it
  // before; in a background start where it has not been opened yet, the state shows only in the menu bar.
  attention(): void {
    if (this.opened) this.show()
  }

  showStatus(view: StatusView): void {
    const win = this.ensure()
    this.consoleURL = undefined
    const query: Record<string, string> = { title: view.title, body: view.body }
    if (view.log) query.log = view.log
    if (view.actions) Object.assign(query, view.actions)
    if (view.busy) query.busy = '1'
    void win.loadFile(statusPage, { query })
  }

  // Whether a call the page sends through preload comes from the console the server serves in the window (main frame, same origin), not
  // from the status page or anywhere else.
  trusts(event: IpcMainEvent): boolean {
    const frame = event.senderFrame
    if (!this.win || this.win.isDestroyed() || event.sender !== this.win.webContents || !frame || frame.parent || !this.consoleURL) return false
    try {
      return new URL(frame.url).origin === new URL(this.consoleURL).origin
    } catch {
      return false
    }
  }

  // A system notification was clicked: show the window and have the console navigate to path with its own router.
  navigate(path: string): void {
    this.show()
    if (this.consoleURL !== undefined) this.win?.webContents.send('loop-now:navigate', path)
  }

  showConsole(url: string): void {
    const win = this.ensure()
    if (this.consoleURL === url) return
    this.consoleURL = url
    win.loadURL(url).catch((err: Error) => this.o.note(`Console failed to load: ${err.message}`))
  }

  private ensure(): BrowserWindow {
    if (this.win && !this.win.isDestroyed()) return this.win
    const area = screen.getPrimaryDisplay().workAreaSize
    const win = new BrowserWindow({
      width: Math.min(1440, area.width),
      height: Math.min(900, area.height),
      minWidth: 390,
      minHeight: 600,
      show: false,
      title: this.o.profile ? `Loop Now · ${this.o.profile}` : 'Loop Now',
      // No system title bar on macOS: the page extends to the top of the window and the system clips the corners to rounded ones. The
      // traffic lights sit in the middle of the 48px drag strip at the top of the page, left-aligned with the logo in the console's left
      // sidebar (web/src/app/Sidebar.tsx, static/status.html). Windows uses the system title bar (see platform.ts).
      ...windowChrome(this.o.platform ?? current),
      // In development, the Windows window icon comes from the exe (Electron's); once packaged, the exe carries the app's icon. It is given
      // again here so the taskbar and the title bar both use it.
      icon: join(__dirname, '..', 'assets', 'icon.png'),
      backgroundColor: '#FBF9F4',
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
        // Keeps the console's event stream working while the window is hidden (system notifications rely on it).
        backgroundThrottling: false,
        preload: join(__dirname, 'preload.js'),
        spellcheck: false,
      },
    })
    win.once('ready-to-show', () => this.opened && win.show())
    win.once('show', () => this.o.onFirstShow?.(win))
    win.on('close', (event) => {
      if (this.o.quitting()) return
      event.preventDefault()
      win.hide()
    })
    win.on('page-title-updated', (event, title) => {
      if (!this.o.profile) return
      event.preventDefault()
      win.setTitle(`${title} · ${this.o.profile}`)
    })

    const contents = win.webContents
    contents.setWindowOpenHandler(({ url }) => {
      if (url === 'about:blank') return { action: 'allow', overrideBrowserWindowOptions: { show: false } }
      this.openExternal(url)
      return { action: 'deny' }
    })
    contents.on('did-create-window', (child) => this.handOffWhenNavigated(child))
    contents.on('will-navigate', (event) => {
      if (event.url.startsWith('file:')) {
        // The buttons on the status page are links carrying an action.
        event.preventDefault()
        const action = new URL(event.url).searchParams.get('action')
        if (action === 'retry' || action === 'logs') this.o.onAction(action)
        return
      }
      const origin = this.consoleURL === undefined ? undefined : new URL(this.consoleURL).origin
      if (classifyNavigation(event.url, origin) === 'app') return
      event.preventDefault()
      this.openExternal(event.url)
    })
    this.win = win
    return win
  }

  // Authorization login first opens a blank popup synchronously, then navigates it to the https authorization address
  // (web/src/features/agent-config/authorizationWindow.ts).
  // The shell does not log in inside its own windows: as soon as the popup starts navigating, the address is handed to the system browser
  // (only once, see handOffOnce) and the popup is closed; the page does not judge success or failure by whether the popup closed.
  private handOffWhenNavigated(child: BrowserWindow): void {
    const handOff = handOffOnce((url) => {
      this.openExternal(url)
      setImmediate(() => child.isDestroyed() || child.destroy())
    })
    child.webContents.setWindowOpenHandler(({ url }) => {
      this.openExternal(url)
      return { action: 'deny' }
    })
    child.webContents.on('will-navigate', (event) => {
      event.preventDefault()
      handOff(event.url)
    })
    child.webContents.on('did-start-navigation', (event) => {
      if (event.isMainFrame) handOff(event.url)
    })
  }

  private openExternal(url: string): void {
    if (classifyNavigation(url) !== 'external') return this.o.note(`Not opening this address: ${url.slice(0, 200)}`)
    shell.openExternal(url).catch((err: Error) => this.o.note(`The system browser could not open the address: ${err.message}`))
  }
}
