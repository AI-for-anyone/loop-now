import { readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { basename, isAbsolute, join, normalize } from 'node:path'

// 隔离配置：用 --profile=<名字或绝对路径>、LOOP_NOW_DESKTOP_PROFILE，或默认 userData 下 desktop.json 的 profile 选择（依次优先）。选了之后壳的设置、日志，
// 服务端的数据目录、上下文目录与工作根都放到这个配置自己的位置，覆盖环境里的同名变量，
// 线上实例还在用默认目录（~/.loop_now、/Users/Shared/loop-now）时也能放心试。
// 不选时是默认配置：服务端用自己的默认目录，壳用 Electron 的默认位置。
export type Profile = {
  // 显示在窗口标题与菜单栏里的名字：配置名，或绝对路径的最后一段。
  label: string
  // 这个配置的根目录（完整路径只出现在失败页正文与壳的日志里）。
  root: string
  // 壳的 userData（设置、就绪文件、浏览器存储、单实例锁）。
  shellDir: string
  logDir: string
  dataDir: string
  contextDir: string
  // 工作根必须在 HOME 之外。
  workRoot: string
}

const namePattern = /^[a-z0-9][a-z0-9_-]{0,31}$/

export function profileArg(argv: string[], env: NodeJS.ProcessEnv): string | undefined {
  for (const arg of argv) {
    if (arg.startsWith('--profile=')) return arg.slice('--profile='.length)
  }
  return env.LOOP_NOW_DESKTOP_PROFILE || undefined
}

// 从 Finder、Dock 打开或登录时启动带不了参数，就读这个文件：{"profile": "<名字或绝对路径>"}。读不到或没写时为空。
export function profileFromFile(file: string): string | undefined {
  try {
    const value = (JSON.parse(readFileSync(file, 'utf8')) as { profile?: unknown }).profile
    return typeof value === 'string' && value !== '' ? value : undefined
  } catch {
    return undefined
  }
}

// 名字：放在 ~/Library/Application Support/Loop Now Profiles/<名字>，工作根在 /Users/Shared/loop-now-profiles/<名字>。
// 绝对路径：所有东西都放在这个目录下（工作根是其中的 work，目录本身须在 HOME 之外），用于临时目录里联调。
export function resolveProfile(value: string, home = homedir()): Profile {
  if (isAbsolute(value)) {
    const root = normalize(value).replace(/(.)\/+$/, '$1')
    return {
      label: basename(root) || root,
      root,
      shellDir: join(root, 'shell'),
      logDir: join(root, 'logs'),
      dataDir: join(root, 'data'),
      contextDir: join(root, 'context'),
      workRoot: join(root, 'work'),
    }
  }
  if (!namePattern.test(value)) throw new Error(`配置名只能用小写字母、数字、下划线与连字符，最长 32 个字符：${value}`)
  const root = join(home, 'Library', 'Application Support', 'Loop Now Profiles', value)
  return {
    label: value,
    root,
    shellDir: join(root, 'shell'),
    logDir: join(root, 'logs'),
    dataDir: join(root, 'data'),
    contextDir: join(root, 'context'),
    workRoot: join('/Users/Shared/loop-now-profiles', value),
  }
}

// 交给服务端的目录变量。
export function profileEnv(profile: Profile): NodeJS.ProcessEnv {
  return { LOOP_NOW_DATA_DIR: profile.dataDir, LOOP_NOW_CONTEXT_DIR: profile.contextDir, LOOP_NOW_WORK_ROOT: profile.workRoot }
}
