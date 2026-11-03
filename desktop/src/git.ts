import { execFile } from 'node:child_process'

// 跑一个命令，只看是否成功。
export type Probe = (file: string, args: string[]) => Promise<boolean>

const probe: Probe = (file, args) => new Promise((resolve) => execFile(file, args, { timeout: 10_000 }, (err) => resolve(!err)))

// 服务端用的 git 是否真能用。macOS 没装命令行工具时 /usr/bin/git 只是占位，一运行就弹系统的安装框；
// xcode-select -p 不会弹框，它失败就说明既没有命令行工具也没有 Xcode，这时不去碰 git。
export async function gitUsable(run: Probe = probe): Promise<boolean> {
  if (!(await run('/usr/bin/xcode-select', ['-p']))) return false
  return run('/usr/bin/git', ['--version'])
}

// 打开系统的"安装命令行工具"对话框。
export function installCommandLineTools(): void {
  execFile('/usr/bin/xcode-select', ['--install'], () => undefined)
}
