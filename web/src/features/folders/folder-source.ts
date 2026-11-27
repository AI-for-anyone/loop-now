import type { Schemas } from '@/api/client'

// Data sources for the file manager: the Agent's folder, the shared project folder and the shared global folder are each a FolderSource,
// backed respectively by the three API groups /agents/{agentId}/folder, /projects/{projectId}/context-folder and /context-folder.
// All paths are relative to the folder root and separated by /; the root is the empty string.

export type FolderEntry = Schemas['FolderEntry']
export type FolderPage = Schemas['FolderEntryPage']
export type GitPull = Schemas['FolderGitPull']
export type GitPullInput = Omit<Schemas['FolderGitPullInput'], 'actionId'>

export interface UploadItem {
  // Path relative to the upload's target folder; includes subfolders when a folder is uploaded.
  path: string
  file: File
}

export interface FolderSource {
  // Namespace in the query cache, so different folders don't affect each other.
  key: string
  // The contents of one level: folders first, then by name; read page by page, never listing everything at once.
  list: (path: string, cursor: string | null) => Promise<FolderPage>
  // The file's raw content; the client decides whether it is text.
  read: (path: string) => Promise<Blob>
  // Write a file, creating parent folders automatically; with create set to true, an existing file returns a conflict.
  write: (path: string, body: Blob | string, create: boolean) => Promise<FolderEntry>
  mkdir: (path: string) => Promise<FolderEntry>
  // Fails if the target already exists; never overwrites.
  move: (from: string, to: string) => Promise<FolderEntry>
  // Deletes immediately and can't be restored; a folder goes together with everything in it.
  remove: (path: string) => Promise<void>
  upload: (target: string, item: UploadItem, onProgress: (loaded: number) => void) => Promise<void>
  // URL of the file's content, used for downloads and image previews.
  contentURL: (path: string) => string
  // Sandboxed URL that opens a file as itself in a new browser tab; only Agent folders have a raw view.
  rawURL?: (path: string) => string
  // Only an Agent's folder can pull from Git. list returns the recent pulls, newest first.
  git?: {
    start: (input: GitPullInput) => Promise<GitPull>
    list: () => Promise<GitPull[]>
  }
}

export const folderKeys = {
  all: (key: string) => ['folder', key] as const,
  list: (key: string, path: string) => ['folder', key, 'list', path] as const,
  lists: (key: string) => ['folder', key, 'list'] as const,
  file: (key: string, path: string) => ['folder', key, 'file', path] as const,
  git: (key: string) => ['folder', key, 'git'] as const,
}

// The entry file at the root of each folder: the Agent reads it first every time it starts Pi.
export const ENTRY_FILE = 'AGENTS.md'

export const joinPath = (dir: string, name: string) => (dir ? `${dir}/${name}` : name)
export const parentOf = (path: string) => (path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')
export const nameOf = (path: string) => path.slice(path.lastIndexOf('/') + 1)
export const segmentsOf = (path: string) => (path ? path.split('/') : [])

// Whether a symlink's target, taken literally, lands outside the folder: an absolute path, or .. going above the root. The server
// doesn't follow such links.
export function linksOutside(path: string, target: string) {
  if (target.startsWith('/')) return true
  const stack = segmentsOf(parentOf(path))
  for (const part of target.split('/')) {
    if (part === '' || part === '.') continue
    if (part !== '..') stack.push(part)
    else if (!stack.pop()) return true
  }
  return false
}
// Every ancestor folder from the root down to path (excluding the root and path itself), opened level by level when expanding the tree.
export const ancestorsOf = (path: string) => segmentsOf(parentOf(path)).map((_, index, all) => all.slice(0, index + 1).join('/'))
// Whether path is dir itself or lies under dir.
export const within = (path: string, dir: string) => path === dir || path.startsWith(`${dir}/`)

export const isEntryFile = (path: string) => path === ENTRY_FILE
export const extensionOf = (name: string) => (name.includes('.') && !name.startsWith('.') ? name.slice(name.lastIndexOf('.') + 1).toLowerCase() : '')

// Name validation: must not be empty, must not contain / or \, must not be . or ..; returns null when valid.
export function nameProblem(name: string): 'empty' | 'slash' | 'dots' | null {
  if (name.trim() === '') return 'empty'
  if (name.includes('/') || name.includes('\\')) return 'slash'
  if (name.trim() === '.' || name.trim() === '..') return 'dots'
  return null
}

// How a move's target folder is written: relative to the root, with leading and trailing / removed; returns null if it has a . or .. segment.
export function folderPath(value: string): string | null {
  const path = value.trim().replace(/^\/+|\/+$/g, '')
  return segmentsOf(path).some((part) => part === '.' || part === '..' || part === '') ? null : path
}

const LANGUAGES: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  json: 'json', yaml: 'yaml', yml: 'yaml', md: 'markdown', sh: 'bash', bash: 'bash', zsh: 'bash', py: 'python',
  css: 'css', html: 'xml', xml: 'xml', svg: 'xml', sql: 'sql', toml: 'ini', ini: 'ini', mod: 'go',
}
// Language for code highlighting; unknown extensions are shown as plain text.
export const languageOf = (path: string) => LANGUAGES[extensionOf(nameOf(path))] ?? null
export const isMarkdown = (path: string) => ['md', 'markdown'].includes(extensionOf(nameOf(path)))
export const isImage = (path: string) => ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(extensionOf(nameOf(path)))

// Conditions for online editing (Context.limits.textEdit): the extension is on the list and the size is within the limit. A text file
// without an extension (such as Makefile) is also treated as not on the list.
export type TextEditLimits = Schemas['Limits']['textEdit']
export const canEdit = (path: string, size: number | null, limits: TextEditLimits | undefined) =>
  !!limits && size !== null && size <= limits.maxBytes && limits.extensions.includes(`.${extensionOf(nameOf(path))}`)

// A text file's original format: on save, the UTF-8 BOM and CRLF line endings are written back as they were (the text box always uses LF).
export interface TextFormat {
  bom: boolean
  crlf: boolean
}
export const PLAIN_TEXT: TextFormat = { bom: false, crlf: false }

// Whether the content read is treated as text: no NUL in the first 8 KiB, and it decodes as UTF-8. For text, returns the content with the
// BOM removed and line endings normalized to LF, along with the original format.
export function decodeText(bytes: ArrayBuffer): { text: string; format: TextFormat } | null {
  const head = new Uint8Array(bytes, 0, Math.min(bytes.byteLength, 8192))
  if (head.includes(0)) return null
  let text: string
  try {
    text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes)
  } catch {
    return null
  }
  const bom = text.startsWith('\uFEFF')
  if (bom) text = text.slice(1)
  const crlf = text.includes('\r\n')
  return { text: crlf ? text.replace(/\r\n/g, '\n') : text, format: { bom, crlf } }
}

export const encodeText = (text: string, format: TextFormat) => (format.bom ? '\uFEFF' : '') + (format.crlf ? text.replace(/\n/g, '\r\n') : text)
