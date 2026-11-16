import { readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { current, defaultProgramData, pathOf, windowsWorkRootBase, type PlatformId } from './platform'
import { LocalizedError } from './strings'

// Profiles: chosen with --profile=<name or absolute path>, LOOP_NOW_DESKTOP_PROFILE, or the profile field of
// ~/Library/Application Support/Loop Now/desktop.json (Windows: %APPDATA%\Loop Now\desktop.json, i.e. Electron's userData)
// (in that order of precedence); when none is chosen, it is the default profile named default. The shell's settings and logs, and the
// server's data directory, context directory and work root all live in the profile's own location and are handed to the server
// explicitly, overriding same-named variables in the environment: the desktop app does not use the server's default directories
// (~/.loop_now, /Users/Shared/loop-now), which belong to a standalone server deployment, unless desktop.json explicitly specifies
// directories for this profile (see overridesFromFile).
export type Profile = {
  // The name shown in the window title and the menu bar: the profile name, or the last segment of the absolute path.
  label: string
  // This profile's root directory (the full path only appears in the failure page body and the shell log).
  root: string
  // The shell's userData (settings, ready file, browser storage, single-instance lock).
  shellDir: string
  logDir: string
  dataDir: string
  contextDir: string
  // The work root must be outside HOME.
  workRoot: string
  // Directories specified by desktop.json that replace the default locations (the first log line and the tray mark them based on this).
  overrides: DirOverrides
}

// The directories desktop.json can specify for a profile.
export const overridableDirs = ['workRoot', 'dataDir', 'contextDir'] as const
export type DirOverrides = Partial<Record<(typeof overridableDirs)[number], string>>

const namePattern = /^[a-z0-9][a-z0-9_-]{0,31}$/
// Strips trailing separators (both kinds count on Windows); a drive root and / are kept.
const trimSlashes = (path: string, p: PlatformId) => pathOf(p).normalize(path).replace(/([^:/\\])[/\\]+$/, '$1')

export const defaultProfile = 'default'

export function profileArg(argv: string[], env: NodeJS.ProcessEnv): string | undefined {
  for (const arg of argv) {
    if (arg.startsWith('--profile=')) return arg.slice('--profile='.length)
  }
  return env.LOOP_NOW_DESKTOP_PROFILE || undefined
}

// Opening from Finder or the Dock, or starting at login, cannot pass arguments, so this file is read: {"profile": "<name or absolute path>"}.
// Undefined when it cannot be read or does not set one.
export function profileFromFile(file: string): string | undefined {
  try {
    const value = (JSON.parse(readFileSync(file, 'utf8')) as { profile?: unknown }).profile
    return typeof value === 'string' && value !== '' ? value : undefined
  } catch {
    return undefined
  }
}

// profiles.<profile> in desktop.json specifies directories for this profile, used to keep the original work root etc. when migrating from
// a source install: {"profiles": {"default": {"workRoot": "/Users/Shared/loop-now"}}}. The key is the name or absolute path given to
// --profile; only workRoot, dataDir and contextDir are accepted, and the values are absolute paths. Nothing is specified when the file cannot
// be read, is not JSON or has no such entry; a malformed entry is an error rather than a silent fallback to the default directories
// (a LocalizedError: it is thrown before the app is ready, and main.ts shows it on the status page).
export function overridesFromFile(file: string, profile: string, p: PlatformId = current): DirOverrides {
  let profiles: unknown
  try {
    profiles = (JSON.parse(readFileSync(file, 'utf8')) as { profiles?: unknown } | null)?.profiles
  } catch {
    return {}
  }
  if (typeof profiles !== 'object' || profiles === null || !Object.hasOwn(profiles, profile)) return {}
  const entry = (profiles as Record<string, unknown>)[profile]
  if (typeof entry !== 'object' || entry === null || Array.isArray(entry)) throw new LocalizedError((s) => s.profile.notObject(profile))
  const overrides: DirOverrides = {}
  for (const [key, value] of Object.entries(entry)) {
    const dir = overridableDirs.find((name) => name === key)
    if (!dir) throw new LocalizedError((s) => s.profile.unknownKey(profile, overridableDirs, key))
    if (typeof value !== 'string' || !pathOf(p).isAbsolute(value)) throw new LocalizedError((s) => s.profile.notAbsolute(profile, key, JSON.stringify(value)))
    overrides[dir] = trimSlashes(value, p)
  }
  return overrides
}

// A name: on macOS it goes in ~/Library/Application Support/Loop Now/<name> (the shell's files in its shell subdirectory), logs in
// ~/Library/Logs/Loop Now/<name>, and the work root in /Users/Shared/loop-now-desktop/<name> (the server requires the work root to be outside HOME).
// On Windows it goes in %LOCALAPPDATA%\Loop Now\<name> (shell, data, context and logs all inside it), and the work root in
// %ProgramData%\loop-now-desktop\<name> (%LOCALAPPDATA% is inside %USERPROFILE%, which the server does not accept; see windowsWorkRootBase in platform.ts).
// An absolute path: everything goes under that directory (the work root is its work subdirectory, and the directory itself must be outside
// HOME), for integration testing in a temporary directory.
// overrides (directories specified by desktop.json) replace the corresponding default locations. A malformed name throws a LocalizedError.
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
  if (!namePattern.test(value)) throw new LocalizedError((s) => s.profile.badName(value))
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
      workRoot: join(windowsWorkRootBase(env.ProgramData || defaultProgramData), value),
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

// The directory variables handed to the server.
export function profileEnv(profile: Profile): NodeJS.ProcessEnv {
  return { LOOP_NOW_DATA_DIR: profile.dataDir, LOOP_NOW_CONTEXT_DIR: profile.contextDir, LOOP_NOW_WORK_ROOT: profile.workRoot }
}

// The profile part of the first shell log line on every start: the profile name and the three directories handed to the server, with those
// specified by desktop.json marked.
export function profileSummary(profile: Profile): string {
  const dir = (path: string, key: keyof DirOverrides) => (profile.overrides[key] ? `${path} (set in desktop.json)` : path)
  return `profile ${profile.label}, data directory ${dir(profile.dataDir, 'dataDir')}, context directory ${dir(profile.contextDir, 'contextDir')}, work root ${dir(profile.workRoot, 'workRoot')}`
}
