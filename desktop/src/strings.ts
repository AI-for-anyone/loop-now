import { app } from 'electron'
import type { ServerState } from './server'

// 壳自己的界面文字，按系统首选语言选中文或英文。控制台的语言由控制台自己决定。
const zhCN = {
  state: {
    stopped: '已停止',
    starting: '正在启动…',
    running: '运行中',
    degraded: '数据库不可用',
    restarting: '正在重启…',
    failed: '启动失败',
    crashed: '已停止：多次崩溃',
    stopping: '正在停止…',
  } satisfies Record<ServerState, string>,
  tray: {
    profile: (name: string) => `配置：${name}`,
    defaultProfile: '默认',
    open: '打开窗口',
    browser: '在默认浏览器打开',
    logs: '查看日志',
    cli: '安装命令行工具…',
    update: (version: string) => `有新版本 ${version}，去下载…`,
    restart: '重启服务',
    quit: '退出 Loop Now',
  },
  cli: {
    moveFirst: '先把 Loop Now 移到"应用程序"文件夹',
    moveFirstDetail: '命令行工具链接到 app 里自带的 loopctl；app 留在下载目录或磁盘映像里时，这个链接会失效。',
    confirm: '安装 loopctl 命令行工具？',
    confirmDetail: (link: string) => `会在 ${link} 建一个指向 Loop Now 自带 loopctl 的链接，需要输入管理员密码。`,
    replace: (link: string) => `${link} 已经存在`,
    replaceDetail: (points: string) => `它现在指向 ${points}。要换成 Loop Now 自带的 loopctl 吗？需要输入管理员密码。`,
    install: '安装',
    replaceButton: '替换',
    cancel: '取消',
    done: '已安装 loopctl',
    already: 'loopctl 已经安装',
    failed: '没能安装 loopctl',
    external: 'loopctl 以外部身份连接服务端的对外 TLS 端口；桌面版目前还不能开启这个端口。',
  },
  quit: {
    message: '退出 Loop Now？',
    detail: '会停掉服务端和所有正在运行的 Agent。',
    confirm: '停止并退出',
    cancel: '取消',
  },
  git: {
    message: '需要 Git',
    detail: 'Loop Now 用 Git 拉取和提交代码，但这台 Mac 还没有安装命令行工具（/usr/bin/git 只是占位）。装好后重新打开 Loop Now。',
    install: '安装命令行工具',
    later: '以后再说',
  },
  page: {
    startingTitle: '正在启动 Loop Now',
    startingBody: '服务端就绪后会自动打开控制台。',
    failedTitle: '服务端没能启动',
    crashedTitle: '服务端多次崩溃，已停止重启',
    crashedBody: '5 分钟内崩溃超过 3 次。查看日志找到原因后，可以重试。',
    spawn: (bin: string, message: string) => `无法运行 ${bin}：${message}`,
    config: '服务端配置有误，详见下面的日志。',
    locked: '数据目录或工作根被另一个 Loop Now 服务端占用。先停掉它，或用 --profile 换一个配置。',
    exited: (exit: string) => `服务端在就绪前退出（${exit}）。`,
    exitCode: (code: number | null) => `退出码 ${code}`,
    exitSignal: (signal: string) => `信号 ${signal}`,
    timeout: (seconds: number) => `服务端 ${seconds} 秒内没有就绪。`,
    profileRoot: (root: string) => `配置目录：${root}`,
    noServerTitle: '找不到服务端',
    noServerBody: '开发模式下用 LOOP_NOW_DESKTOP_SERVER 指向 loop-server。',
    noProfileTitle: '开发模式需要选一个配置',
    noProfileBody: '用 --profile=<名字或临时目录> 或 LOOP_NOW_DESKTOP_PROFILE 启动，免得和装好的 Loop Now 共用默认配置。',
    badProfileTitle: '配置名不可用',
    retry: '重试',
    logs: '打开日志',
  },
}

const enUS: typeof zhCN = {
  state: {
    stopped: 'Stopped',
    starting: 'Starting…',
    running: 'Running',
    degraded: 'Database unavailable',
    restarting: 'Restarting…',
    failed: 'Failed to start',
    crashed: 'Stopped after repeated crashes',
    stopping: 'Stopping…',
  },
  tray: {
    profile: (name) => `Profile: ${name}`,
    defaultProfile: 'default',
    open: 'Open Window',
    browser: 'Open in Browser',
    logs: 'Show Logs',
    cli: 'Install Command Line Tool…',
    update: (version) => `Version ${version} is available — Download…`,
    restart: 'Restart Server',
    quit: 'Quit Loop Now',
  },
  cli: {
    moveFirst: 'Move Loop Now to the Applications folder first',
    moveFirstDetail: 'The command line tool links to the loopctl inside the app; while the app stays in Downloads or on a disk image, that link would break.',
    confirm: 'Install the loopctl command line tool?',
    confirmDetail: (link) => `This creates ${link}, a link to the loopctl that comes with Loop Now. You will be asked for an administrator password.`,
    replace: (link) => `${link} already exists`,
    replaceDetail: (points) => `It currently points to ${points}. Replace it with the loopctl that comes with Loop Now? You will be asked for an administrator password.`,
    install: 'Install',
    replaceButton: 'Replace',
    cancel: 'Cancel',
    done: 'loopctl is installed',
    already: 'loopctl is already installed',
    failed: 'Could not install loopctl',
    external: 'loopctl connects to the server’s external TLS port as an external identity; the desktop app cannot turn that port on yet.',
  },
  quit: {
    message: 'Quit Loop Now?',
    detail: 'This stops the server and every running Agent.',
    confirm: 'Stop and Quit',
    cancel: 'Cancel',
  },
  git: {
    message: 'Git is required',
    detail: 'Loop Now uses Git to fetch and commit code, but the command line tools are not installed on this Mac (/usr/bin/git is only a placeholder). Reopen Loop Now once they are installed.',
    install: 'Install Command Line Tools',
    later: 'Not Now',
  },
  page: {
    startingTitle: 'Starting Loop Now',
    startingBody: 'The console opens as soon as the server is ready.',
    failedTitle: 'The server did not start',
    crashedTitle: 'The server kept crashing, so it was not restarted',
    crashedBody: 'It crashed more than 3 times in 5 minutes. Check the logs, then try again.',
    spawn: (bin, message) => `Could not run ${bin}: ${message}`,
    config: 'The server configuration is invalid; see the log below.',
    locked: 'Another Loop Now server is using this profile’s data directory or work root. Stop it, or use --profile to pick another profile.',
    exited: (exit) => `The server exited before it was ready (${exit}).`,
    exitCode: (code) => `exit code ${code}`,
    exitSignal: (signal) => `signal ${signal}`,
    timeout: (seconds) => `The server was not ready within ${seconds} seconds.`,
    profileRoot: (root) => `Profile directory: ${root}`,
    noServerTitle: 'Server not found',
    noServerBody: 'In development, point LOOP_NOW_DESKTOP_SERVER at loop-server.',
    noProfileTitle: 'Development needs a profile',
    noProfileBody: 'Start with --profile=<name or temporary directory> or LOOP_NOW_DESKTOP_PROFILE so the installed Loop Now keeps its default profile to itself.',
    badProfileTitle: 'Unusable profile name',
    retry: 'Try Again',
    logs: 'Open Logs',
  },
}

export type Strings = typeof zhCN

export function stringsFor(language: string): Strings {
  return language.toLowerCase().startsWith('zh') ? zhCN : enUS
}

export function strings(): Strings {
  return stringsFor(app.getPreferredSystemLanguages()[0] ?? app.getLocale())
}
