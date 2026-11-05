import { readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { current, pathOf, windowsWorkRootBase, type PlatformId } from './platform'

// 配置：用 --profile=<名字或绝对路径>、LOOP_NOW_DESKTOP_PROFILE，或 ~/Library/Application Support/Loop Now/desktop.json
// （Windows：%APPDATA%\Loop Now\desktop.json，即 Electron 的 userData）的 profile 选择
// （依次优先），都没选时是名为 default 的默认配置。壳的设置、日志，服务端的数据目录、上下文目录与工作根都放在配置自己的位置，
// 显式交给服务端、覆盖环境里的同名变量：桌面版不使用服务端的默认目录（~/.loop_now、/Users/Shared/loop-now），
// 那是单独部署的服务端在用的；除非 desktop.json 为这个配置明确指定了目录（见 overridesFromFile）。
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
  // desktop.json 指定、换掉了默认位置的目录（日志首行与托盘据此标出）。
  overrides: DirOverrides
}

// desktop.json 里能为配置指定的目录。
export const overridableDirs = ['workRoot', 'dataDir', 'contextDir'] as const
export type DirOverrides = Partial<Record<(typeof overridableDirs)[number], string>>

const namePattern = /^[a-z0-9][a-z0-9_-]{0,31}$/
// 去掉末尾的分隔符（Windows 两种都算），盘符根与 / 保留。
const trimSlashes = (path: string, p: PlatformId) => pathOf(p).normalize(path).replace(/([^:/\\])[/\\]+$/, '$1')

export const defaultProfile = 'default'

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

// desktop.json 的 profiles.<配置> 为这个配置指定目录，用于从源码安装迁移过来时沿用原来的工作根等：
// {"profiles": {"default": {"workRoot": "/Users/Shared/loop-now"}}}。键是 --profile 里写的名字或绝对路径，只认 workRoot、dataDir、
// contextDir，值是绝对路径。文件读不到、不是 JSON 或没有这一项时不指定；这一项写错时报错，不悄悄用回默认目录。
export function overridesFromFile(file: string, profile: string, p: PlatformId = current): DirOverrides {
  let profiles: unknown
  try {
    profiles = (JSON.parse(readFileSync(file, 'utf8')) as { profiles?: unknown } | null)?.profiles
  } catch {
    return {}
  }
  if (typeof profiles !== 'object' || profiles === null || !Object.hasOwn(profiles, profile)) return {}
  const entry = (profiles as Record<string, unknown>)[profile]
  const where = `desktop.json 的 profiles.${profile}`
  if (typeof entry !== 'object' || entry === null || Array.isArray(entry)) throw new Error(`${where} 应是对象`)
  const overrides: DirOverrides = {}
  for (const [key, value] of Object.entries(entry)) {
    const dir = overridableDirs.find((name) => name === key)
    if (!dir) throw new Error(`${where} 只能写 ${overridableDirs.join('、')}，不认识 ${key}`)
    if (typeof value !== 'string' || !pathOf(p).isAbsolute(value)) throw new Error(`${where}.${key} 应是绝对路径：${JSON.stringify(value)}`)
    overrides[dir] = trimSlashes(value, p)
  }
  return overrides
}

// 名字：macOS 放在 ~/Library/Application Support/Loop Now/<名字>（壳的文件在其中的 shell），日志在 ~/Library/Logs/Loop Now/<名字>，
// 工作根在 /Users/Shared/loop-now-desktop/<名字>（服务端要求工作根在 HOME 之外）。
// Windows 放在 %LOCALAPPDATA%\Loop Now\<名字>（shell、data、context、logs 都在其中），工作根见 platform.ts 的 windowsWorkRootBase。
// 绝对路径：所有东西都放在这个目录下（工作根是其中的 work，目录本身须在 HOME 之外），用于临时目录里联调。
// overrides（desktop.json 指定的目录）换掉对应的默认位置。
export function resolveProfile(value: string, home = homedir(), overrides: DirOverrides = {}, p: PlatformId = current, env: NodeJS.ProcessEnv = process.env): Profile {
  const { join, basename, isAbsolute } = pathOf(p)
  if (isAbsolute(value)) {
    const root = trimSlashes(value, p)
    return {
      label: basename(root) || root,
      root,
      shellDir: join(root, 'shell'),
      logDir: join(root, 'logs'),
      dataDir: join(root, 'data'),
      contextDir: join(root, 'context'),
      workRoot: join(root, 'work'),
      ...overrides,
      overrides,
    }
  }
  if (!namePattern.test(value)) throw new Error(`配置名只能用小写字母、数字、下划线与连字符，最长 32 个字符：${value}`)
  if (p === 'win32') {
    const localAppData = env.LOCALAPPDATA || join(home, 'AppData', 'Local')
    const root = join(localAppData, 'Loop Now', value)
    return {
      label: value,
      root,
      shellDir: join(root, 'shell'),
      logDir: join(root, 'logs'),
      dataDir: join(root, 'data'),
      contextDir: join(root, 'context'),
      workRoot: join(windowsWorkRootBase(localAppData), value),
      ...overrides,
      overrides,
    }
  }
  const root = join(home, 'Library', 'Application Support', 'Loop Now', value)
  return {
    label: value,
    root,
    shellDir: join(root, 'shell'),
    logDir: join(home, 'Library', 'Logs', 'Loop Now', value),
    dataDir: join(root, 'data'),
    contextDir: join(root, 'context'),
    workRoot: join('/Users/Shared/loop-now-desktop', value),
    ...overrides,
    overrides,
  }
}

// 交给服务端的目录变量。
export function profileEnv(profile: Profile): NodeJS.ProcessEnv {
  return { LOOP_NOW_DATA_DIR: profile.dataDir, LOOP_NOW_CONTEXT_DIR: profile.contextDir, LOOP_NOW_WORK_ROOT: profile.workRoot }
}

// 壳日志每次启动的第一行里的配置部分：配置名与交给服务端的三个目录，desktop.json 指定的标出来。
export function profileSummary(profile: Profile): string {
  const dir = (path: string, key: keyof DirOverrides) => (profile.overrides[key] ? `${path}（desktop.json 指定）` : path)
  return `配置 ${profile.label}，数据目录 ${dir(profile.dataDir, 'dataDir')}，上下文目录 ${dir(profile.contextDir, 'contextDir')}，工作根 ${dir(profile.workRoot, 'workRoot')}`
}
