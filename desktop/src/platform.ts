import { posix, win32 } from 'node:path'

// 平台差异集中在这里：壳的其余模块按 PlatformId 取值，不直接看 process.platform（单测据此注入 win32）。
// Linux 暂时按 macOS 之外的"类 Unix"处理：没有打包目标，只保证开发时能跑。
export type PlatformId = 'darwin' | 'win32' | 'linux'

export const current: PlatformId = process.platform === 'win32' ? 'win32' : process.platform === 'darwin' ? 'darwin' : 'linux'

export const isWindows = (p: PlatformId) => p === 'win32'

// 路径 API：Windows 的配置目录用反斜杠拼，其余用斜杠；单测在 macOS 上也能算出 Windows 的路径。
export const pathOf = (p: PlatformId) => (p === 'win32' ? win32 : posix)

// 随包服务端在 Resources/server（Windows 是 resources\server）下的文件名。
export const serverBinary = (p: PlatformId) => (p === 'win32' ? 'loop-server.exe' : 'loop-server')

// 随包 loopctl 在 Resources/bin 下的文件名：macOS 是指向 loopctl-dist 的链接，Windows 是复制出来的 exe（见 scripts/after-pack.js）。
export const cliBinary = (p: PlatformId) => (p === 'win32' ? 'loopctl.exe' : 'loopctl')

// 托盘图标：macOS 用 Template 图（按菜单栏深浅自动着色），Windows 用 .ico（任务栏通知区按 DPI 取尺寸）。
export const trayIcon = (p: PlatformId) => (p === 'win32' ? 'tray.ico' : 'trayTemplate.png')

// 窗口标题栏：macOS 藏起系统标题栏，红绿灯放进页面顶部 48px 的拖动区（web/src/app/Sidebar.tsx、static/status.html）；
// Windows 用系统默认标题栏（最小化、最大化、关闭按钮都在那里），页面不留拖动区。
export const windowChrome = (p: PlatformId): { titleBarStyle?: 'hiddenInset'; trafficLightPosition?: { x: number; y: number } } =>
  p === 'darwin' ? { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 18, y: 16 } } : {}

// 未读数：macOS 显示 Dock 角标；Windows 没有 Dock，任务栏按钮上放一个圆点 overlay，数字变多时闪一下任务栏。
export const badgeStyle = (p: PlatformId): 'dock' | 'overlay' => (p === 'win32' ? 'overlay' : 'dock')

// "安装命令行工具"：macOS 在 /usr/local/bin 建链接（需要管理员密码）；Windows 把随包的 bin 目录加进当前用户的 PATH
// （安装目录按用户固定在 %LOCALAPPDATA%\Programs\Loop Now，升级原地覆盖，所以加一次就够；不需要管理员，也不依赖符号链接权限）。
export const cliInstallMode = (p: PlatformId): 'symlink' | 'path' => (p === 'win32' ? 'path' : 'symlink')

// Windows 没有命令行工具安装框，提示去 git-scm.com 下载。
export const gitDownloadPage = 'https://git-scm.com/download/win'

// Windows 的工作根：服务端的 PrepareWorkRoot 拒绝 %USERPROFILE% 之内的工作根（%LOCALAPPDATA% 也在其中），它自己的默认是
// %ProgramData%\loop-now（ProgramData 相当于 /Users/Shared，普通用户能在里面建目录，服务端把它改成只有自己能读写）。
// 桌面版照 macOS 的 /Users/Shared/loop-now-desktop/<配置名>，放在 %ProgramData%\loop-now-desktop\<配置名>。
export const windowsWorkRootBase = (programData: string) => win32.join(programData, 'loop-now-desktop')
export const defaultProgramData = 'C:\\ProgramData'
