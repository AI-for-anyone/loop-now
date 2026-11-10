import { execFile } from 'node:child_process'
import { accessSync, constants } from 'node:fs'
import { shell } from 'electron'
import { current, gitDownloadPage, pathOf, type PlatformId } from './platform'

// 跑一个命令，只看是否成功。
export type Probe = (file: string, args: string[]) => Promise<boolean>

const probe: Probe = (file, args) => new Promise((resolve) => execFile(file, args, { timeout: 10_000, windowsHide: true }, (err) => resolve(!err)))

// 在 PATH 里找可执行文件（Windows 用）：按目录顺序找第一个存在的 name。
export function findInPath(name: string, pathEnv: string | undefined, p: PlatformId = current, exists: (file: string) => boolean = fileExists): string | undefined {
  const { delimiter, join } = pathOf(p)
  for (const dir of (pathEnv ?? '').split(delimiter)) {
    if (dir === '') continue
    const file = join(dir, name)
    if (exists(file)) return file
  }
  return undefined
}

function fileExists(file: string): boolean {
  try {
    accessSync(file, constants.F_OK)
    return true
  } catch {
    return false
  }
}

// 服务端用的 git 是否真能用。
// macOS 没装命令行工具时 /usr/bin/git 只是占位，一运行就弹系统的安装框；xcode-select -p 不会弹框，它失败就说明既没有命令行工具
// 也没有 Xcode，这时不去碰 git。
// Windows 没有占位的 git：PATH 里找到 git.exe（Git for Windows 装好后在 PATH 里）再跑 git --version；找不到就是没装。
export async function gitUsable(run: Probe = probe, p: PlatformId = current, env: NodeJS.ProcessEnv = process.env, exists?: (file: string) => boolean): Promise<boolean> {
  if (p === 'win32') {
    const git = findInPath('git.exe', env.PATH ?? env.Path, p, exists)
    return git !== undefined && run(git, ['--version'])
  }
  if (!(await run('/usr/bin/xcode-select', ['-p']))) return false
  return run('/usr/bin/git', ['--version'])
}

// 引导安装：macOS 打开系统的"安装命令行工具"对话框；Windows 打开 git-scm.com 的下载页。
export function installGit(p: PlatformId = current): void {
  if (p === 'win32') {
    void shell.openExternal(gitDownloadPage)
    return
  }
  execFile('/usr/bin/xcode-select', ['--install'], () => undefined)
}
