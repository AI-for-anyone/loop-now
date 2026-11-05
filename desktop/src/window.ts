import { app, BrowserWindow, screen, shell, type IpcMainEvent } from 'electron'
import { join } from 'node:path'
import { classifyNavigation, handOffOnce } from './links'

// 壳自带的状态页（启动中、启动失败）显示的内容；文字已按语言选好。
export type StatusView = {
  title: string
  body: string
  log?: string
  actions?: { retry: string; logs: string }
  busy?: boolean
}

export type StatusAction = 'retry' | 'logs'

type Options = {
  // 隔离配置的名字，显示在窗口标题后面，免得和默认配置的窗口混淆。
  profile?: string
  // 后台启动：人第一次要求显示之前不显示窗口，也不占 Dock。
  background: boolean
  // 窗口第一次显示出来时调用。
  onFirstShow?: (win: BrowserWindow) => void
  // 已确认退出时关窗口才真正关闭，否则只是隐藏，服务端照常运行。
  quitting: () => boolean
  onAction: (action: StatusAction) => void
  note: (text: string) => void
}

const statusPage = join(__dirname, '..', 'static', 'status.html')

// 唯一的主窗口：加载服务端提供的控制台，就绪前显示状态页。
export class MainWindow {
  private win: BrowserWindow | undefined
  // 当前加载的控制台地址；显示状态页时为空。
  private consoleURL: string | undefined
  // 人是否已经打开过窗口（后台启动时第一次 show 之前为 false）。
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

  // 人要求显示窗口（菜单栏、Dock、再次打开 app）。
  show(): void {
    this.opened = true
    void app.dock?.show()
    const win = this.ensure()
    if (win.isMinimized()) win.restore()
    win.show()
    win.focus()
  }

  // 需要人注意时（启动失败、多次崩溃）：人打开过窗口才把它拿到前面；后台启动且还没打开过时只在菜单栏里显示状态。
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

  // 页面经 preload 发来的调用是否来自窗口里服务端提供的控制台（主框架、同源），不是状态页或别处。
  trusts(event: IpcMainEvent): boolean {
    const frame = event.senderFrame
    if (!this.win || this.win.isDestroyed() || event.sender !== this.win.webContents || !frame || frame.parent || !this.consoleURL) return false
    try {
      return new URL(frame.url).origin === new URL(this.consoleURL).origin
    } catch {
      return false
    }
  }

  // 点了系统通知：显示窗口，让控制台在自己的路由里跳到 path。
  navigate(path: string): void {
    this.show()
    if (this.consoleURL !== undefined) this.win?.webContents.send('loop-now:navigate', path)
  }

  showConsole(url: string): void {
    const win = this.ensure()
    if (this.consoleURL === url) return
    this.consoleURL = url
    win.loadURL(url).catch((err: Error) => this.o.note(`控制台加载失败：${err.message}`))
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
      // 不要系统标题栏：页面铺到窗口顶部，四角由系统圆角裁切。红绿灯放在页面顶部 48px 那条拖动区的正中，
      // 与控制台左侧栏的标志左对齐（web/src/app/Sidebar.tsx、static/status.html）。
      titleBarStyle: 'hiddenInset',
      trafficLightPosition: { x: 18, y: 16 },
      backgroundColor: '#FBF9F4',
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
        // 窗口隐藏时控制台的事件流照常工作（系统通知靠它）。
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
        // 状态页上的按钮是带 action 的链接。
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

  // 授权登录先同步开一个空白弹窗，再把它导航到 https 的授权地址（web/src/features/agent-config/authorizationWindow.ts）。
  // 壳不在自己的窗口里登录：弹窗一开始导航，就把地址交给系统浏览器（只交一次，见 handOffOnce）并关掉它；页面不以弹窗是否关闭判断成败。
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
    if (classifyNavigation(url) !== 'external') return this.o.note(`不打开这个地址：${url.slice(0, 200)}`)
    shell.openExternal(url).catch((err: Error) => this.o.note(`系统浏览器打不开地址：${err.message}`))
  }
}
