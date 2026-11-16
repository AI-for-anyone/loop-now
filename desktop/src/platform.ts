import { posix, win32 } from 'node:path'

// Platform differences are collected here: the rest of the shell picks values by PlatformId and never looks at process.platform directly
// (unit tests rely on this to inject win32).
// For now Linux is treated as a "Unix-like" other than macOS: there is no packaging target, it is only guaranteed to run in development.
export type PlatformId = 'darwin' | 'win32' | 'linux'

export const current: PlatformId = process.platform === 'win32' ? 'win32' : process.platform === 'darwin' ? 'darwin' : 'linux'

export const isWindows = (p: PlatformId) => p === 'win32'

// Path API: Windows profile directories are joined with backslashes, everything else with slashes; unit tests can compute Windows paths
// even on macOS.
export const pathOf = (p: PlatformId) => (p === 'win32' ? win32 : posix)

// File name of the bundled server under Resources/server (resources\server on Windows).
export const serverBinary = (p: PlatformId) => (p === 'win32' ? 'loop-server.exe' : 'loop-server')

// File name of the bundled loopctl under Resources/bin: on macOS a link into loopctl-dist, on Windows a copied exe (see scripts/after-pack.js).
export const cliBinary = (p: PlatformId) => (p === 'win32' ? 'loopctl.exe' : 'loopctl')

// Tray icon: a Template image on macOS (tinted automatically for a light or dark menu bar), an .ico on Windows (the taskbar notification
// area picks the size by DPI).
export const trayIcon = (p: PlatformId) => (p === 'win32' ? 'tray.ico' : 'trayTemplate.png')

// Window title bar: macOS hides the system title bar and puts the traffic lights into the 48px drag area at the top of the page
// (web/src/app/Sidebar.tsx, static/status.html); Windows uses the default system title bar (minimize, maximize and close are all there),
// and the page leaves no drag area.
export const windowChrome = (p: PlatformId): { titleBarStyle?: 'hiddenInset'; trafficLightPosition?: { x: number; y: number } } =>
  p === 'darwin' ? { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 18, y: 16 } } : {}

// Unread count: a Dock badge on macOS; Windows has no Dock, so a dot overlay goes on the taskbar button, and the taskbar flashes once when
// the number grows.
export const badgeStyle = (p: PlatformId): 'dock' | 'overlay' => (p === 'win32' ? 'overlay' : 'dock')

// "安装命令行工具" (Install Command Line Tool…): macOS creates a link in /usr/local/bin (needs the administrator password); Windows adds the
// bundled bin directory to the current user's PATH (the install directory is fixed per user at %LOCALAPPDATA%\Programs\Loop Now and upgrades
// overwrite it in place, so adding it once is enough; no administrator needed, and no dependence on symlink privileges).
export const cliInstallMode = (p: PlatformId): 'symlink' | 'path' => (p === 'win32' ? 'path' : 'symlink')

// Windows has no install dialog for command line tools, so the user is pointed to git-scm.com to download Git.
export const gitDownloadPage = 'https://git-scm.com/download/win'

// Windows work root: the server's PrepareWorkRoot rejects work roots inside %USERPROFILE% (%LOCALAPPDATA% is inside it too); the server's
// own default is %ProgramData%\loop-now (ProgramData is the counterpart of /Users/Shared: regular users can create directories in it, and the
// server changes the permissions so only it can read and write). Mirroring macOS's /Users/Shared/loop-now-desktop/<profile-name>, the desktop
// app uses %ProgramData%\loop-now-desktop\<profile-name>.
export const windowsWorkRootBase = (programData: string) => win32.join(programData, 'loop-now-desktop')
export const defaultProgramData = 'C:\\ProgramData'
