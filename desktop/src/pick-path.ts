// The page asks for a folder or file on this computer (window.loopNowDesktop.pickPath, see preload.ts): the shell shows the system's own
// dialog and returns the full path, which a browser never gives a page. The server always runs on the same computer as the desktop shell,
// so the path is valid for it too. Kept free of Electron so the checks run in unit tests; the wiring is in dialogs.ts.
import { pathOf, type PlatformId } from './platform'

export type PathKind = 'directory' | 'file'
export type PickPathRequest = { kind: PathKind; defaultPath?: string }

// What the system dialog is asked to show (a subset of Electron's OpenDialogOptions) and what it answers.
export type PathDialogOptions = { properties: Array<'openDirectory' | 'createDirectory' | 'openFile'>; defaultPath?: string }
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

export function dialogOptions(request: PickPathRequest): PathDialogOptions {
  // createDirectory: the “New Folder” button in the macOS dialog (Windows always has one).
  const properties: PathDialogOptions['properties'] = request.kind === 'directory' ? ['openDirectory', 'createDirectory'] : ['openFile']
  return request.defaultPath ? { properties, defaultPath: request.defaultPath } : { properties }
}

type Options<E> = {
  // Whether the call comes from the console page that the server serves in the window.
  trusted: (event: E) => boolean
  show: (options: PathDialogOptions) => Promise<PathDialogResult>
  platform: PlatformId
}

// The handler for ipcMain.handle: a refused call rejects the page's promise without showing anything; cancelling resolves to null.
export function pickPathHandler<E>(o: Options<E>): (event: E, value: unknown) => Promise<string | null> {
  return async (event, value) => {
    if (!o.trusted(event)) throw new Error('Refused: the call does not come from the console page')
    const request = parsePickPath(value, o.platform)
    if (!request) throw new Error('Refused: malformed request')
    const result = await o.show(dialogOptions(request))
    return result.canceled ? null : (result.filePaths[0] ?? null)
  }
}
