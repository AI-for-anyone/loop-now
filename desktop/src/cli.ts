import { execFile } from 'node:child_process'
import { existsSync } from 'node:fs'
import { lstat, mkdir, readlink, rm, symlink } from 'node:fs/promises'
import { dirname } from 'node:path'
import { cliBinary, cliInstallMode, pathOf, type PlatformId } from './platform'
import type { SettingsFile } from './settings'
import { errorText, LocalizedError, type Strings } from './strings'

// macOS: the install location on PATH (the default macOS PATH includes /usr/local/bin), linked to the fixed Resources/bin/loopctl inside the app bundle.
export const cliLink = '/usr/local/bin/loopctl'

export const cliStatusChannel = 'loop-now:cli-status'
export const cliInstallChannel = 'loop-now:cli-install'

export type CliInstallMode = 'symlink' | 'path'
export type LinkState = { state: 'missing' } | { state: 'ours' } | { state: 'other'; points?: string }

export type CliLocation = {
  // Whether installing loopctl is available in this run (always true when packaged; in dev only with the dev-only link override).
  available: boolean
  // Development-only override targeting a temporary link path; never true in packaged builds.
  devOverride: boolean
  mode: CliInstallMode
  // macOS: symlink path (/usr/local/bin/loopctl when packaged); Windows: bundled bin directory.
  link: string
  // Full path to the bundled loopctl executable.
  target: string
  // Directory holding the bundled loopctl executable (Resources/bin or resources\bin).
  binDir: string
}

export type CliStatus =
  | {
      state: 'installed'
      mode: CliInstallMode
      location: string
      target: string
      externalAvailable: boolean
    }
  | {
      state: 'not_installed'
      mode: CliInstallMode
      location: string
      target: string
      moveFirst?: boolean
      externalAvailable: boolean
    }
  | {
      state: 'other'
      mode: 'symlink'
      location: string
      target: string
      points?: string
      moveFirst?: boolean
      externalAvailable: boolean
    }
  | {
      state: 'unavailable'
      mode: CliInstallMode
      reason: 'not_packaged' | 'error'
      error?: string
      externalAvailable: boolean
    }

export type CliInstallOutcome = 'installed' | 'already_installed' | 'cancelled' | 'move_first' | 'unavailable' | 'failed'
export type CliInstallResult = {
  outcome: CliInstallOutcome
  status: CliStatus
  error?: string
}

export type CliDialogMessage = {
  type: 'info' | 'warning' | 'question'
  message: string
  detail: string
  buttons?: string[]
  defaultId?: number
  cancelId?: number
}

export type ResolveCliLocationOptions = {
  packaged: boolean
  platform: PlatformId
  resourcesPath: string
  serverBin?: string
  env?: NodeJS.ProcessEnv
  exists?: (file: string) => boolean
}

// Resolves where loopctl is bundled and where it installs.
// Packaged builds always use the fixed bundle path and /usr/local/bin/loopctl (or resources\bin on Windows) and never honor dev env vars.
// Unpackaged development runs disable CLI installation unless LOOP_NOW_DESKTOP_CLI_LINK points to an absolute path for safe local testing.
export function resolveCliLocation(o: ResolveCliLocationOptions): CliLocation {
  const mode = cliInstallMode(o.platform)
  const pathApi = pathOf(o.platform)
  const binName = cliBinary(o.platform)
  const defaultBinDir = pathApi.join(o.resourcesPath, 'bin')
  const defaultTarget = pathApi.join(defaultBinDir, binName)

  if (o.packaged) {
    return {
      available: true,
      devOverride: false,
      mode,
      link: mode === 'path' ? defaultBinDir : cliLink,
      target: defaultTarget,
      binDir: defaultBinDir,
    }
  }

  const env = o.env ?? {}
  const overrideLink = env.LOOP_NOW_DESKTOP_CLI_LINK?.trim()
  if (mode === 'symlink' && overrideLink && pathApi.isAbsolute(overrideLink)) {
    const checkExists = o.exists ?? existsSync
    const explicitTarget = env.LOOP_NOW_DESKTOP_CLI_TARGET?.trim()
    let target = defaultTarget
    if (explicitTarget) {
      target = pathApi.resolve(explicitTarget)
    } else if (o.serverBin) {
      const serverDir = pathApi.dirname(o.serverBin)
      const besideServer = pathApi.join(serverDir, binName)
      const siblingBin = pathApi.join(serverDir, '..', 'bin', binName)
      target = checkExists(besideServer) ? besideServer : checkExists(siblingBin) ? siblingBin : besideServer
    }
    return {
      available: true,
      devOverride: true,
      mode,
      link: overrideLink,
      target,
      binDir: pathApi.dirname(target),
    }
  }

  return {
    available: false,
    devOverride: false,
    mode,
    link: mode === 'path' ? defaultBinDir : cliLink,
    target: defaultTarget,
    binDir: defaultBinDir,
  }
}

// What link currently is: missing, already pointing to target, or some other file/link.
export async function linkState(link: string, target: string): Promise<LinkState> {
  try {
    if (!(await lstat(link)).isSymbolicLink()) return { state: 'other' }
    const points = await readlink(link)
    return points === target ? { state: 'ours' } : { state: 'other', points }
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === 'ENOENT' ? { state: 'missing' } : { state: 'other' }
  }
}

// AppleScript that creates /usr/local/bin (it may be missing on Apple silicon Macs) and the link with administrator privileges.
// Paths are first escaped as AppleScript strings, then passed to the shell via quoted form.
export function installScript(target: string, link = cliLink): string {
  const string = (value: string) => `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  return `do shell script "mkdir -p " & quoted form of ${string(dirname(link))} & " && ln -sfn " & quoted form of ${string(target)} & " " & quoted form of ${string(link)} with administrator privileges`
}

// Shows the system's administrator password prompt and creates the link. 'cancelled' when the user cancels.
export function installLink(target: string, link = cliLink): Promise<'done' | 'cancelled' | Error> {
  return new Promise((resolve) => {
    execFile('/usr/bin/osascript', ['-e', installScript(target, link)], (err, _stdout, stderr) => {
      if (!err) return resolve('done')
      // osascript's -128 means "User canceled."
      resolve(/-128/.test(stderr) ? 'cancelled' : new Error(stderr.trim() || err.message))
    })
  })
}

// Development-only non-elevated symlink creation for testing against a user-writable temporary folder (never used in packaged builds).
export async function installLinkDirect(target: string, link: string): Promise<'done' | Error> {
  try {
    await mkdir(dirname(link), { recursive: true })
    await rm(link, { force: true })
    await symlink(target, link)
    return 'done'
  } catch (err) {
    return err instanceof Error ? err : new Error(String(err))
  }
}

// Windows: add the bundled bin directory to the current user's PATH (registry HKCU\Environment); the system PATH is left alone and no
// administrator is needed.
// Uses [Environment]::SetEnvironmentVariable rather than setx: setx truncates the value to 1024 characters, which would break the user's PATH.
// After the write, Windows broadcasts the environment change, so new terminals see it; terminals already open have to be reopened.

// Whether the user PATH contains this directory (case-insensitive, trailing backslashes ignored).
export function userPathHas(userPath: string, dir: string): boolean {
  const norm = (v: string) => v.trim().replace(/[\\/]+$/, '').toLowerCase()
  return userPath.split(';').some((entry) => entry !== '' && norm(entry) === norm(dir))
}

// PowerShell single-quoted string: only single quotes need to be doubled.
const psQuote = (value: string) => `'${value.replace(/'/g, "''")}'`

export const readUserPathScript = `[Environment]::GetEnvironmentVariable('Path', 'User')`

// Appends the directory if it is missing and leaves PATH alone if it is there. Outputs the resulting user PATH.
export function addToUserPathScript(dir: string): string {
  return [
    `$dir = ${psQuote(dir)}`,
    `$path = [Environment]::GetEnvironmentVariable('Path', 'User')`,
    `if ($null -eq $path) { $path = '' }`,
    `$parts = $path -split ';' | Where-Object { $_ -ne '' }`,
    `if (-not ($parts | Where-Object { $_.TrimEnd('\\') -ieq $dir.TrimEnd('\\') })) { $path = (($parts + $dir) -join ';') }`,
    `[Environment]::SetEnvironmentVariable('Path', $path, 'User')`,
    `$path`,
  ].join('; ')
}

function powershell(script: string): Promise<string | Error> {
  return new Promise((resolve) => {
    execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script], { windowsHide: true, timeout: 30_000 }, (err, stdout, stderr) => {
      resolve(err ? new Error(String(stderr).trim() || err.message) : String(stdout).trim())
    })
  })
}

export async function readUserPath(): Promise<string | Error> {
  return powershell(readUserPathScript)
}

// A PowerShell failure comes back as its stderr; a write that did not take is a LocalizedError, shown in the failure dialog.
export async function addToUserPath(dir: string): Promise<'done' | Error> {
  const result = await powershell(addToUserPathScript(dir))
  if (result instanceof Error) return result
  return userPathHas(result, dir) ? 'done' : new LocalizedError((s) => s.cli.pathMissing(dir))
}

export type ReadCliStatusDeps = {
  location: CliLocation
  inApplications: boolean
  externalAvailable: boolean
  checkLink?: (link: string, target: string) => Promise<LinkState>
  readUserPath?: () => Promise<string | Error>
}

export async function readCliStatus(deps: ReadCliStatusDeps): Promise<CliStatus> {
  const { location, inApplications, externalAvailable } = deps
  if (!location.available) {
    return { state: 'unavailable', mode: location.mode, reason: 'not_packaged', externalAvailable }
  }
  if (location.mode === 'path') {
    const current = await (deps.readUserPath ?? readUserPath)()
    if (current instanceof Error) {
      return { state: 'unavailable', mode: 'path', reason: 'error', error: current.message, externalAvailable }
    }
    return userPathHas(current, location.binDir)
      ? { state: 'installed', mode: 'path', location: location.binDir, target: location.target, externalAvailable }
      : { state: 'not_installed', mode: 'path', location: location.binDir, target: location.target, externalAvailable }
  }
  const current = await (deps.checkLink ?? linkState)(location.link, location.target)
  const moveFirst = !location.devOverride && !inApplications ? true : undefined
  if (current.state === 'ours') {
    return { state: 'installed', mode: 'symlink', location: location.link, target: location.target, externalAvailable }
  }
  if (current.state === 'other') {
    return {
      state: 'other',
      mode: 'symlink',
      location: location.link,
      target: location.target,
      ...(current.points !== undefined ? { points: current.points } : {}),
      ...(moveFirst ? { moveFirst } : {}),
      externalAvailable,
    }
  }
  return {
    state: 'not_installed',
    mode: 'symlink',
    location: location.link,
    target: location.target,
    ...(moveFirst ? { moveFirst } : {}),
    externalAvailable,
  }
}

export type PromptCliOutcome =
  | 'skipped_unavailable'
  | 'skipped_already_prompted'
  | 'skipped_not_in_applications'
  | 'skipped_already_installed'
  | 'skipped_points_elsewhere'
  | 'skipped_error'
  | 'interrupted'
  | 'later'
  | 'installed'
  | 'cancelled'
  | 'failed'

export type PromptCliDeps = {
  location: CliLocation
  inApplications: boolean
  settings: Pick<SettingsFile, 'read' | 'update'>
  strings: () => Strings
  showDialog: (options: CliDialogMessage) => Promise<{ response: number }>
  // Returns false when the prompt was dismissed because the window closed or the app started quitting (no user choice was made).
  answered?: () => boolean
  checkLink?: (link: string, target: string) => Promise<LinkState>
  installLink?: (target: string, link: string) => Promise<'done' | 'cancelled' | Error>
  readUserPath?: () => Promise<string | Error>
  addToUserPath?: (dir: string) => Promise<'done' | Error>
  note?: (text: string) => void
}

// Offers loopctl installation once on first launch when it is missing for this user.
// Never nags after Later or Install, never asks when already installed or pointing elsewhere, and waits until the macOS app is in Applications.
export async function maybePromptCliInstall(deps: PromptCliDeps): Promise<PromptCliOutcome> {
  const { location, inApplications, settings, strings, showDialog, answered, note } = deps
  if (!location.available) return 'skipped_unavailable'
  if (settings.read().cliPrompted) return 'skipped_already_prompted'
  if (location.mode === 'symlink' && !location.devOverride && !inApplications) return 'skipped_not_in_applications'

  if (location.mode === 'path') {
    const current = await (deps.readUserPath ?? readUserPath)()
    if (current instanceof Error) {
      note?.(`Cannot read the user PATH: ${current.message}`)
      return 'skipped_error'
    }
    if (userPathHas(current, location.binDir)) return 'skipped_already_installed'
  } else {
    const current = await (deps.checkLink ?? linkState)(location.link, location.target)
    if (current.state === 'ours') return 'skipped_already_installed'
    if (current.state === 'other') return 'skipped_points_elsewhere'
  }

  if (answered && !answered()) return 'interrupted'
  const s = strings().cli
  const detail = location.mode === 'path' ? s.firstPromptDetailWindows(location.binDir) : s.firstPromptDetail(location.link)
  const { response } = await showDialog({
    type: 'question',
    message: s.firstPromptTitle,
    detail,
    buttons: [s.install, s.later],
    defaultId: 0,
    cancelId: 1,
  })
  // When the app quits or the window closes while the dialog is open, Electron resolves showMessageBox with cancelId; do not count that as Later.
  if (answered && !answered()) return 'interrupted'
  settings.update({ cliPrompted: true })
  if (response !== 0) return 'later'

  if (location.mode === 'path') {
    const result = await (deps.addToUserPath ?? addToUserPath)(location.binDir)
    if (result instanceof Error) {
      note?.(`Adding loopctl to PATH failed: ${result.message}`)
      await showDialog({ type: 'warning', message: s.failed, detail: errorText(result, strings()) })
      return 'failed'
    }
    note?.(`Added ${location.binDir} to the user PATH`)
    await showDialog({ type: 'info', message: s.pathDone, detail: `${s.pathDoneDetail(location.binDir)}\n\n${s.external}` })
    return 'installed'
  }

  const runInstall = deps.installLink ?? (location.devOverride ? installLinkDirect : installLink)
  const result = await runInstall(location.target, location.link)
  if (result === 'cancelled') return 'cancelled'
  if (result instanceof Error) {
    note?.(`Installing loopctl failed: ${result.message}`)
    await showDialog({ type: 'warning', message: s.failed, detail: result.message })
    return 'failed'
  }
  note?.(`Installed loopctl: ${location.link} → ${location.target}`)
  await showDialog({ type: 'info', message: s.done, detail: `${location.link} → ${location.target}\n\n${s.external}` })
  return 'installed'
}

// Runs first-show checks sequentially (git first, then loopctl) so two native dialogs never stack.
export async function runFirstShowChecks(deps: {
  checkGit: () => Promise<void>
  checkCli: () => Promise<void>
  isAlive?: () => boolean
}): Promise<void> {
  await deps.checkGit()
  if (deps.isAlive && !deps.isAlive()) return
  await deps.checkCli()
}

export function cliStatusHandler<E>(o: { trusted: (event: E) => boolean; getStatus: () => Promise<CliStatus> }): (event: E) => Promise<CliStatus> {
  return async (event) => {
    if (!o.trusted(event)) throw new Error('Refused: the call does not come from the console page')
    return o.getStatus()
  }
}

export type CliInstallHandlerOptions<E> = {
  trusted: (event: E) => boolean
  location: CliLocation
  settings: Pick<SettingsFile, 'read' | 'update'>
  strings: () => Strings
  getStatus: () => Promise<CliStatus>
  confirmReplace: (options: CliDialogMessage) => Promise<{ response: number }>
  installLink?: (target: string, link: string) => Promise<'done' | 'cancelled' | Error>
  addToUserPath?: (dir: string) => Promise<'done' | Error>
  note?: (text: string) => void
}

// IPC handler for installing loopctl from Global settings. Installing when missing runs directly (the user already clicked Install);
// replacing an existing file or foreign symlink confirms first.
export function cliInstallHandler<E>(o: CliInstallHandlerOptions<E>): (event: E) => Promise<CliInstallResult> {
  let inflight: Promise<CliInstallResult> | undefined
  const run = async (): Promise<CliInstallResult> => {
    const status = await o.getStatus()
    if (status.state === 'unavailable') {
      return { outcome: 'unavailable', status, ...(status.error ? { error: status.error } : {}) }
    }
    if (status.state === 'installed') {
      return { outcome: 'already_installed', status }
    }
    if (status.moveFirst) {
      return { outcome: 'move_first', status }
    }
    if (status.state === 'other') {
      const s = o.strings().cli
      const { response } = await o.confirmReplace({
        type: 'question',
        message: s.replace(status.location),
        detail: `${s.replaceDetail(status.points ?? status.location)}\n\n${s.external}`,
        buttons: [s.replaceButton, s.cancel],
        defaultId: 0,
        cancelId: 1,
      })
      if (response !== 0) return { outcome: 'cancelled', status }
    }

    if (o.location.mode === 'path') {
      const result = await (o.addToUserPath ?? addToUserPath)(o.location.binDir)
      const nextStatus = await o.getStatus()
      if (result instanceof Error) {
        o.note?.(`Adding loopctl to PATH failed: ${result.message}`)
        return { outcome: 'failed', error: errorText(result, o.strings()), status: nextStatus }
      }
      o.settings.update({ cliPrompted: true })
      o.note?.(`Added ${o.location.binDir} to the user PATH`)
      return { outcome: 'installed', status: nextStatus }
    }

    const runInstall = o.installLink ?? (o.location.devOverride ? installLinkDirect : installLink)
    const result = await runInstall(o.location.target, o.location.link)
    const nextStatus = await o.getStatus()
    if (result === 'cancelled') return { outcome: 'cancelled', status: nextStatus }
    if (result instanceof Error) {
      o.note?.(`Installing loopctl failed: ${result.message}`)
      return { outcome: 'failed', error: result.message, status: nextStatus }
    }
    o.settings.update({ cliPrompted: true })
    o.note?.(`Installed loopctl: ${o.location.link} → ${o.location.target}`)
    return { outcome: 'installed', status: nextStatus }
  }

  return async (event) => {
    if (!o.trusted(event)) throw new Error('Refused: the call does not come from the console page')
    if (inflight) return inflight
    inflight = run().finally(() => {
      inflight = undefined
    })
    return inflight
  }
}
