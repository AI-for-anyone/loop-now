import { execFile } from 'node:child_process'
import { lstat, readlink } from 'node:fs/promises'
import { dirname } from 'node:path'

// 装到 PATH 上的位置（macOS 默认的 PATH 含 /usr/local/bin）。
export const cliLink = '/usr/local/bin/loopctl'

export type LinkState = { state: 'missing' } | { state: 'ours' } | { state: 'other'; points?: string }

// link 现在是什么：不存在、已经指向 target，或别的文件／链接。
export async function linkState(link: string, target: string): Promise<LinkState> {
  try {
    if (!(await lstat(link)).isSymbolicLink()) return { state: 'other' }
    const points = await readlink(link)
    return points === target ? { state: 'ours' } : { state: 'other', points }
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === 'ENOENT' ? { state: 'missing' } : { state: 'other' }
  }
}

// 用管理员权限建 /usr/local/bin（Apple 芯片的 Mac 上可能没有）与链接的 AppleScript。
// 路径先按 AppleScript 字符串转义，再用 quoted form 交给 shell。
export function installScript(target: string, link = cliLink): string {
  const string = (value: string) => `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  return `do shell script "mkdir -p " & quoted form of ${string(dirname(link))} & " && ln -sfn " & quoted form of ${string(target)} & " " & quoted form of ${string(link)} with administrator privileges`
}

// 弹出系统的管理员密码框并建链接。人取消时为 'cancelled'。
export function installLink(target: string): Promise<'done' | 'cancelled' | Error> {
  return new Promise((resolve) => {
    execFile('/usr/bin/osascript', ['-e', installScript(target)], (err, _stdout, stderr) => {
      if (!err) return resolve('done')
      // osascript 的 -128 是"用户已取消"。
      resolve(/-128/.test(stderr) ? 'cancelled' : new Error(stderr.trim() || err.message))
    })
  })
}
