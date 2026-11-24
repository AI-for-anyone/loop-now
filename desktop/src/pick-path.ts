// The page asks for a folder or file on this computer (window.loopNowDesktop.pickPath, see preload.ts): the shell shows the system's own
// dialog and returns the full path, which a browser never gives a page. The server always runs on the same computer as the desktop shell,
// so the path is valid for it too. Kept free of Electron so the checks run in unit tests; the wiring is in dialogs.ts.
import { execFile } from 'node:child_process'
import { readFile, stat } from 'node:fs/promises'
import { join } from 'node:path'
import { promisify } from 'node:util'
import { pathOf, type PlatformId } from './platform'

export type PathKind = 'directory' | 'file'
export type PickPathRequest = { kind: PathKind; defaultPath?: string }

// What the system dialog is asked to show (a subset of Electron's OpenDialogOptions) and what it answers.
export type PathDialogOptions = { properties: Array<'openDirectory' | 'createDirectory' | 'openFile' | 'showHiddenFiles'>; defaultPath?: string }
export type PathDialogResult = { canceled: boolean; filePaths: string[] }

export const pickPathChannel = 'loop-now:pick-path'

// Longer than any path the systems accept (macOS 1024, Linux 4096, Windows 32767 only with the \\?\ prefix, which the dialog doesn't take).
export const maxPathLength = 4096

const kinds: readonly PathKind[] = ['directory', 'file']

// undefined when the request is malformed. The page passes the input's current value as defaultPath; it is used only when it is an
// absolute path on this system (a bare command name or a half-typed path just opens the dialog at its usual place).
export function parsePickPath(value: unknown, platform: PlatformId): PickPathRequest | undefined {
  if (!value || typeof value !== 'object') return undefined
  const v = value as Record<string, unknown>
  if (!kinds.includes(v.kind as PathKind)) return undefined
  const kind = v.kind as PathKind
  if (v.defaultPath === undefined || v.defaultPath === null) return { kind }
  if (typeof v.defaultPath !== 'string' || v.defaultPath.length > maxPathLength || v.defaultPath.includes('\0')) return undefined
  const defaultPath = v.defaultPath.trim()
  return defaultPath && pathOf(platform).isAbsolute(defaultPath) ? { kind, defaultPath } : { kind }
}

export function dialogOptions(request: PickPathRequest, platform: PlatformId): PathDialogOptions {
  // createDirectory: the “New Folder” button in the macOS dialog (Windows always has one). A file is usually a program, and on macOS
  // many live in hidden places (~/.local/bin) or ones Finder hides (/opt/homebrew), so the file dialog shows hidden files there instead of
  // leaving it to ⌘⇧. (Command-Shift-Period).
  const properties: PathDialogOptions['properties'] =
    request.kind === 'directory' ? ['openDirectory', 'createDirectory'] : platform === 'darwin' ? ['openFile', 'showHiddenFiles'] : ['openFile']
  return request.defaultPath ? { properties, defaultPath: request.defaultPath } : { properties }
}

type Options<E> = {
  // Whether the call comes from the console page that the server serves in the window.
  trusted: (event: E) => boolean
  show: (options: PathDialogOptions) => Promise<PathDialogResult>
  platform: PlatformId
  // Turns a chosen file into the path to fill in (appExecutable in the shell); a chosen folder is returned as is.
  resolveFile?: (path: string) => Promise<string>
}

// The handler for ipcMain.handle: a refused call rejects the page's promise without showing anything; cancelling resolves to null.
export function pickPathHandler<E>(o: Options<E>): (event: E, value: unknown) => Promise<string | null> {
  return async (event, value) => {
    if (!o.trusted(event)) throw new Error('Refused: the call does not come from the console page')
    const request = parsePickPath(value, o.platform)
    if (!request) throw new Error('Refused: malformed request')
    const result = await o.show(dialogOptions(request, o.platform))
    const path = result.canceled ? undefined : result.filePaths[0]
    if (!path) return null
    return request.kind === 'file' && o.resolveFile ? o.resolveFile(path) : path
  }
}

const run = promisify(execFile)

// The macOS file dialog treats an .app bundle as one file, so choosing a program there gives the bundle, which can't be run as a command.
// A chosen .app stands for the program inside it: Contents/MacOS/<CFBundleExecutable from Contents/Info.plist>. When that can't be read,
// names a path outside Contents/MacOS, or isn't there, the path is kept as chosen, without an error (the server then says it can't run
// it). Anything else, and any path on another system, is kept as is.
export async function appExecutable(path: string, platform: PlatformId): Promise<string> {
  const app = path.replace(/\/+$/, '')
  if (platform !== 'darwin' || !/\.app$/i.test(app)) return path
  try {
    const name = await bundleExecutable(join(app, 'Contents', 'Info.plist'))
    if (!name || name.includes('/') || name === '.' || name === '..') return path
    const executable = join(app, 'Contents', 'MacOS', name)
    return (await stat(executable)).isFile() ? executable : path
  } catch {
    return path
  }
}

// CFBundleExecutable from an Info.plist: an XML one is read directly; a binary one (bplist00) is turned into XML by the system's plutil.
async function bundleExecutable(plist: string): Promise<string | undefined> {
  const data = await readFile(plist)
  const xml = data.subarray(0, 8).toString('latin1') === 'bplist00' ? (await run('plutil', ['-convert', 'xml1', '-o', '-', plist], { timeout: 5_000 })).stdout : data.toString('utf8')
  const match = /<key>\s*CFBundleExecutable\s*<\/key>\s*<string>([^<]*)<\/string>/.exec(xml)
  return match ? decodeXml(match[1].trim()) : undefined
}

const entities: Record<string, string> = { lt: '<', gt: '>', quot: '"', apos: "'", amp: '&' }
const decodeXml = (text: string) =>
  text.replace(/&(#x[0-9a-f]+|#\d+|lt|gt|quot|apos|amp);/gi, (whole, entity: string) => {
    if (entity[0] !== '#') return entities[entity.toLowerCase()] ?? whole
    const code = entity[1]?.toLowerCase() === 'x' ? parseInt(entity.slice(2), 16) : parseInt(entity.slice(1), 10)
    return Number.isFinite(code) && code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : whole
  })
