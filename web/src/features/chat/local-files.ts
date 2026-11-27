import { useEffect, useState } from 'react'
import { extensionOf, nameOf } from '@/features/folders/folder-source'
import {
  CJK_PUNCT,
  decodePercent,
  normalizeRelativeOrRoot,
  resolveAbsoluteOrRoot,
  rootPattern,
  stripFileUrl,
  type ReplyFolderContext,
} from './local-images'

// Common file extensions used to recognize relative file paths in inline code and bare text without matching domain names
// (example.com) or version numbers (v0.3.5).
const KNOWN_FILE_EXTENSIONS = new Set([
  'html', 'htm', 'css', 'scss', 'sass', 'less',
  'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx',
  'json', 'jsonc', 'jsonl', 'map',
  'md', 'markdown', 'mdx', 'txt', 'csv', 'tsv', 'log', 'xml',
  'yaml', 'yml', 'toml', 'ini', 'cfg', 'conf', 'env', 'properties',
  'svg', 'png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'ico', 'bmp',
  'woff', 'woff2', 'ttf', 'otf',
  'mp4', 'webm', 'mp3', 'wav', 'ogg', 'm4a',
  'pdf', 'wasm', 'zip', 'gz', 'tgz', 'tar',
  'go', 'py', 'rs', 'rb', 'java', 'kt', 'swift', 'c', 'h', 'cc', 'cpp', 'hpp', 'cs', 'php',
  'sh', 'bash', 'zsh', 'ps1', 'bat', 'sql', 'graphql', 'gql', 'proto', 'diff', 'patch', 'lock',
  'tex', 'rtf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx',
])

const KNOWN_FILE_EXT_PATTERN = [...KNOWN_FILE_EXTENSIONS].join('|')

// Top-level POSIX directories used to recognize outside filesystem paths in inline code and bare text without mistaking slash
// commands (/model, /compact) or console/API routes (/api/..., /p/...) for local paths.
const OUTSIDE_POSIX_ROOTS = new Set([
  'etc', 'tmp', 'var', 'usr', 'bin', 'sbin', 'home', 'Users', 'private', 'opt',
  'Library', 'Applications', 'Volumes', 'System', 'mnt', 'media', 'srv', 'root',
  'proc', 'sys', 'dev', 'work', 'workspace',
])

const OUTSIDE_POSIX_ROOT_PATTERN = [...OUTSIDE_POSIX_ROOTS].join('|')

export type LocalLinkTarget =
  | { kind: 'external' }
  | { kind: 'local'; relativePath: string; trailingSlash: boolean; suffix: string }
  | { kind: 'local-dir'; relativePath: string }
  | { kind: 'outside' }
  | { kind: 'pending-root' }

export type InlineCodePath =
  | { kind: 'none' }
  | { kind: 'local'; relativePath: string; trailingSlash: boolean; certain: boolean }
  | { kind: 'local-dir'; relativePath: string }
  | { kind: 'outside' }
  | { kind: 'pending-root' }

export type BareLocalPart =
  | { kind: 'text'; value: string }
  | { kind: 'local'; raw: string; relativePath: string; trailingSlash: boolean; suffix: string; certain: boolean }
  | { kind: 'local-dir'; raw: string; relativePath: string }
  | { kind: 'outside'; raw: string }
  | { kind: 'pending-root'; raw: string }

function splitPathAndSuffix(value: string): { pathPart: string; suffix: string } {
  const q = value.indexOf('?')
  const h = value.indexOf('#')
  const cut = q >= 0 && h >= 0 ? Math.min(q, h) : q >= 0 ? q : h
  if (cut < 0) return { pathPart: value, suffix: '' }
  return { pathPart: value.slice(0, cut), suffix: value.slice(cut) }
}

function hasKnownFileExtension(path: string): boolean {
  const ext = extensionOf(nameOf(path))
  return ext !== '' && KNOWN_FILE_EXTENSIONS.has(ext)
}

function looksLikeOutsidePosixPath(value: string): boolean {
  if (!value.startsWith('/') || value.startsWith('//')) return false
  if (/[\r\n"'`<>|*?{}();=,:]/.test(value)) return false
  const trimmed = value.replace(/\/+$/, '')
  const segments = trimmed.split('/').filter(Boolean)
  if (segments.length < 2) return false
  const first = segments[0]!
  if (first === 'api' || first === 'p' || first === 'agent') return false
  if (OUTSIDE_POSIX_ROOTS.has(first)) {
    // Allow spaces only under OS user/app directories that commonly contain spaces (e.g. Application Support).
    if (/\s/.test(value) && !['Users', 'Library', 'Applications', 'Volumes', 'home', 'mnt', 'media'].includes(first)) {
      return false
    }
    return true
  }
  if (/\s/.test(value)) return false
  return hasKnownFileExtension(trimmed)
}

// Classify a Markdown link's href inside an Agent or task node reply:
// - http(s), mailto, in-page anchors (#...), and other non-file URI schemes stay on Markdown's normal link path
//   (react-markdown's urlTransform still blocks dangerous schemes such as javascript:).
// - file://, POSIX/Windows absolute paths, and relative paths are resolved against the folder roots; paths inside the folder
//   become 'local' (or 'local-dir' for the folder root itself), while paths outside or escaping via '..' become 'outside' so they
//   never navigate the console.
export function classifyMarkdownLinkHref(
  rawHref: string,
  roots: readonly string[],
  rootsReady = true,
): LocalLinkTarget {
  const href = rawHref.trim()
  if (!href || href.startsWith('#')) return { kind: 'external' }
  if (/^(?:https?:\/\/|mailto:)/i.test(href)) return { kind: 'external' }

  if (/^file:\/\//i.test(href)) {
    if (!rootsReady && roots.length === 0) return { kind: 'pending-root' }
    const { pathPart, suffix } = splitPathAndSuffix(href)
    const unwrapped = stripFileUrl(pathPart)
    if (!unwrapped) return { kind: 'outside' }
    const trailingSlash = /[/\\]$/.test(unwrapped)
    const resolved = resolveAbsoluteOrRoot(unwrapped, roots)
    if (resolved === 'root') return { kind: 'local-dir', relativePath: '' }
    if (resolved) return { kind: 'local', relativePath: resolved, trailingSlash, suffix }
    return { kind: 'outside' }
  }

  const windowsAbs = /^[a-zA-Z]:[/\\]/.test(href)
  if (!windowsAbs && /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(href)) return { kind: 'external' }

  const { pathPart, suffix } = splitPathAndSuffix(href)
  const decoded = decodePercent(pathPart)
  const trailingSlash = /[/\\]$/.test(pathPart)

  if (href.startsWith('/') || windowsAbs) {
    if (!rootsReady && roots.length === 0) return { kind: 'pending-root' }
    const resolved = resolveAbsoluteOrRoot(decoded, roots) ?? resolveAbsoluteOrRoot(pathPart, roots)
    if (resolved === 'root') return { kind: 'local-dir', relativePath: '' }
    if (resolved) return { kind: 'local', relativePath: resolved, trailingSlash, suffix }
    return { kind: 'outside' }
  }

  const rel = normalizeRelativeOrRoot(decoded) ?? normalizeRelativeOrRoot(pathPart)
  if (rel === 'root') return { kind: 'local-dir', relativePath: '' }
  if (rel) return { kind: 'local', relativePath: rel, trailingSlash, suffix }
  return { kind: 'outside' }
}

// Classify an inline-code token (`...`) inside a reply:
// - Absolute paths and file:// URLs inside roots are certain local paths (missing shows a tooltip).
// - Explicit relative paths starting with './' are certain local paths.
// - Other plausible relative paths (nested paths or known file extensions) are uncertain local candidates: they become clickable
//   links only when the file/directory actually exists, and stay untouched plain code on 404.
// - Outside absolute paths, file:// URLs, Windows paths, and '../' escapes become 'outside'.
export function classifyInlineCodePath(
  rawCode: string,
  roots: readonly string[],
  rootsReady = true,
): InlineCodePath {
  const text = rawCode.trim()
  if (!text || /[\r\n]/.test(text)) return { kind: 'none' }

  if (/^file:\/\//i.test(text)) {
    if (!rootsReady && roots.length === 0) return { kind: 'pending-root' }
    const { pathPart } = splitPathAndSuffix(text)
    const unwrapped = stripFileUrl(pathPart)
    if (!unwrapped) return { kind: 'outside' }
    const trailingSlash = /[/\\]$/.test(unwrapped)
    const resolved = resolveAbsoluteOrRoot(unwrapped, roots)
    if (resolved === 'root') return { kind: 'local-dir', relativePath: '' }
    if (resolved) return { kind: 'local', relativePath: resolved, trailingSlash, certain: true }
    return { kind: 'outside' }
  }

  const windowsAbs = /^[a-zA-Z]:[/\\]/.test(text)
  if (!windowsAbs && /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(text)) return { kind: 'none' }

  if (windowsAbs) {
    if (/["'`<>|*?]/.test(text)) return { kind: 'none' }
    if (!rootsReady && roots.length === 0) return { kind: 'pending-root' }
    const trailingSlash = /[/\\]$/.test(text)
    const resolved = resolveAbsoluteOrRoot(text, roots)
    if (resolved === 'root') return { kind: 'local-dir', relativePath: '' }
    if (resolved) return { kind: 'local', relativePath: resolved, trailingSlash, certain: true }
    return { kind: 'outside' }
  }

  if (text.startsWith('/')) {
    const trailingSlash = /\/$/.test(text)
    if (roots.length > 0) {
      const resolved = resolveAbsoluteOrRoot(text, roots)
      if (resolved === 'root') return { kind: 'local-dir', relativePath: '' }
      if (resolved) return { kind: 'local', relativePath: resolved, trailingSlash, certain: true }
    }
    if (!rootsReady && roots.length === 0 && looksLikeOutsidePosixPath(text)) return { kind: 'pending-root' }
    return looksLikeOutsidePosixPath(text) ? { kind: 'outside' } : { kind: 'none' }
  }

  if (/^(?:\.\.[/\\])+/.test(text)) {
    if (/[\s"'`<>|*?{}();=,:]/.test(text)) return { kind: 'none' }
    const rel = normalizeRelativeOrRoot(text)
    return rel === null ? { kind: 'outside' } : { kind: 'none' }
  }

  if (/["'`<>|*?{}();=,:#@]/.test(text) || /^[~$!-]/.test(text)) return { kind: 'none' }

  const trailingSlash = /[/\\]$/.test(text)
  if (/^\.[/\\]/.test(text)) {
    if (/\s/.test(text) && !hasKnownFileExtension(text)) return { kind: 'none' }
    const rel = normalizeRelativeOrRoot(text)
    if (rel === 'root') return { kind: 'local-dir', relativePath: '' }
    if (rel) return { kind: 'local', relativePath: rel, trailingSlash, certain: true }
    return { kind: 'outside' }
  }

  if (/\s/.test(text) || text.startsWith('.')) return { kind: 'none' }
  const hasSlash = text.includes('/') || text.includes('\\')
  if (!hasSlash && !hasKnownFileExtension(text)) return { kind: 'none' }
  // Skip short all-caps word pairs like GET/POST or ON/OFF.
  if (hasSlash && !trailingSlash && !hasKnownFileExtension(text) && /^[A-Z0-9_-]+\/[A-Z0-9_-]+$/.test(text)) {
    return { kind: 'none' }
  }
  const rel = normalizeRelativeOrRoot(text)
  if (rel === 'root') return { kind: 'local-dir', relativePath: '' }
  if (rel) return { kind: 'local', relativePath: rel, trailingSlash, certain: false }
  return text.includes('..') ? { kind: 'outside' } : { kind: 'none' }
}

const TRAILING_PUNCT = /[.,;!?]+$/

function trimBareRaw(raw: string): string {
  let trimmed = raw.replace(TRAILING_PUNCT, '')
  if (trimmed.endsWith(':') && !/^[a-zA-Z]:$/.test(trimmed)) trimmed = trimmed.slice(0, -1)
  return trimmed
}

// Split a plain text node around bare local paths (inside roots, outside absolute/file:///../ paths, and ASCII relative file paths
// with known extensions). Matching known roots first preserves spaces in the root directory itself (e.g. Application Support).
export function splitBareLocalPaths(
  text: string,
  roots: readonly string[],
  rootsReady = true,
): BareLocalPart[] {
  if (!text) return [{ kind: 'text', value: text }]

  const rootPatterns = [...new Set(
    [...roots]
      .sort((a, b) => b.length - a.length)
      .map(rootPattern)
      .filter((pattern): pattern is string => pattern !== null),
  )]

  const branches: string[] = []
  if (rootPatterns.length > 0) {
    branches.push(
      `(?:${rootPatterns.join('|')})(?:[/\\\\][^\\s"'\`<>()[\\]{}${CJK_PUNCT}]*|(?=$|[\\s,;!?\\)\\]}'"\`<>:${CJK_PUNCT}]|\\.(?![A-Za-z0-9_/-])))`,
    )
  }
  // Bare file:// URLs, Windows absolute paths, POSIX outside paths, relative ../ escapes, and ASCII relative file paths.
  branches.push(`file://(?:localhost)?/[^\\s"'\`<>()[\\]{}${CJK_PUNCT}]+`)
  branches.push(`[A-Za-z]:[/\\\\][^\\s"'\`<>()[\\]{}${CJK_PUNCT}]+`)
  branches.push(`/(?:${OUTSIDE_POSIX_ROOT_PATTERN})/[^\\s"'\`<>()[\\]{}${CJK_PUNCT}]+`)
  branches.push(`(?:\\.\\.[/\\\\])+[A-Za-z0-9._~+/-]+`)
  branches.push(`(?:\\./)?(?:[A-Za-z0-9._~+-]+/)*[A-Za-z0-9._~+-]+\\.(?:${KNOWN_FILE_EXT_PATTERN})(?=$|[\\s,;!?\\)\\]}'"\`<>:${CJK_PUNCT}]|\\.(?![A-Za-z0-9_/-]))`)

  const regex = new RegExp(branches.join('|'), 'gi')
  const parts: BareLocalPart[] = []
  let last = 0

  for (const match of text.matchAll(regex)) {
    const start = match.index
    const raw = trimBareRaw(match[0]!)
    if (!raw) continue

    // Check left boundary: do not match inside a longer word, email, URL, or path prefix.
    if (start > 0) {
      const prev = text[start - 1]!
      if (raw.startsWith('/') || /^[A-Za-z]:/.test(raw) || raw.startsWith('../') || raw.startsWith('..\\')) {
        if (/[A-Za-z0-9_./\\:-]/.test(prev)) continue
      } else if (/^file:\/\//i.test(raw)) {
        if (/[A-Za-z0-9_./\\-]/.test(prev)) continue
      } else if (/[A-Za-z0-9_./\\:@-]/.test(prev)) {
        continue
      }
    }

    let part: BareLocalPart | null = null
    if (/^file:\/\//i.test(raw) || raw.startsWith('/') || /^[A-Za-z]:[/\\]/.test(raw)) {
      const { pathPart, suffix } = /^file:\/\//i.test(raw) ? splitPathAndSuffix(raw) : { pathPart: raw, suffix: '' }
      const unwrapped = /^file:\/\//i.test(raw) ? stripFileUrl(pathPart) : pathPart
      if (!unwrapped) {
        part = { kind: 'outside', raw }
      } else {
        const trailingSlash = /[/\\]$/.test(unwrapped)
        const resolved = resolveAbsoluteOrRoot(unwrapped, roots)
        if (resolved === 'root') {
          part = { kind: 'local-dir', raw, relativePath: '' }
        } else if (resolved) {
          part = { kind: 'local', raw, relativePath: resolved, trailingSlash, suffix, certain: true }
        } else if (!rootsReady && roots.length === 0) {
          part = { kind: 'pending-root', raw }
        } else {
          part = { kind: 'outside', raw }
        }
      }
    } else if (/^(?:\.\.[/\\])+/.test(raw)) {
      if (normalizeRelativeOrRoot(raw) === null) part = { kind: 'outside', raw }
    } else {
      const rel = normalizeRelativeOrRoot(raw)
      if (rel && rel !== 'root') {
        part = { kind: 'local', raw, relativePath: rel, trailingSlash: false, suffix: '', certain: raw.startsWith('./') }
      }
    }

    if (!part) continue
    if (start > last) parts.push({ kind: 'text', value: text.slice(last, start) })
    parts.push(part)
    last = start + raw.length
  }

  if (parts.length === 0) return [{ kind: 'text', value: text }]
  if (last < text.length) parts.push({ kind: 'text', value: text.slice(last) })
  return parts
}

export type LocalEntryStatus =
  | { kind: 'file' }
  | { kind: 'dir' }
  | { kind: 'missing' }
  | { kind: 'too-large' }
  | { kind: 'outside' }
  | { kind: 'unavailable' }

// Probe whether a folder-relative path exists as a readable file, a directory, is missing, is over the preview limit, or escapes
// the folder via a symlink. Uses HEAD on the raw endpoint (no file bytes transferred) and falls back to checkDirectory on 422.
export async function probeLocalFolderEntry(
  folder: ReplyFolderContext,
  relativePath: string,
  trailingSlash: boolean,
  signal?: AbortSignal,
): Promise<LocalEntryStatus> {
  if (relativePath === '') return { kind: 'dir' }

  if (trailingSlash && folder.checkDirectory) {
    const dirState = await folder.checkDirectory(relativePath, signal)
    if (dirState === 'dir') return { kind: 'dir' }
    if (dirState === 'missing') return { kind: 'missing' }
    return { kind: 'unavailable' }
  }

  if (!folder.rawURL) return { kind: 'unavailable' }

  let response: Response
  try {
    response = await fetch(folder.rawURL(relativePath), { method: 'HEAD', signal })
  } catch {
    return { kind: 'unavailable' }
  }

  if (response.ok) return { kind: 'file' }
  if (response.status === 404) return { kind: 'missing' }
  if (response.status === 413) return { kind: 'too-large' }
  if (response.status === 422) {
    if (folder.checkDirectory) {
      const dirState = await folder.checkDirectory(relativePath, signal)
      if (dirState === 'dir') return { kind: 'dir' }
      if (dirState === 'missing') return { kind: 'missing' }
      return { kind: 'outside' }
    }
    return trailingSlash ? { kind: 'dir' } : { kind: 'outside' }
  }
  return { kind: 'unavailable' }
}

// Per-Markdown probe cache: deduplicates repeated paths inside one reply and aborts in-flight checks when the reply unmounts.
export interface LocalProbeStore {
  probe: (folder: ReplyFolderContext, relativePath: string, trailingSlash: boolean) => Promise<LocalEntryStatus>
  peek: (key: string) => LocalEntryStatus | undefined
  keyOf: (folder: ReplyFolderContext, relativePath: string, trailingSlash: boolean) => string
  activate: () => void
  abort: () => void
}

export function createLocalProbeStore(): LocalProbeStore {
  let controller = new AbortController()
  const inflight = new Map<string, Promise<LocalEntryStatus>>()
  const settled = new Map<string, LocalEntryStatus>()

  const keyOf = (folder: ReplyFolderContext, relativePath: string, trailingSlash: boolean) =>
    `${folder.rawURL?.(relativePath) ?? ''}\0${relativePath}\0${trailingSlash ? '1' : '0'}\0${folder.checkDirectory ? '1' : '0'}`

  return {
    keyOf,
    peek: (key) => settled.get(key),
    probe: (folder, relativePath, trailingSlash) => {
      const key = keyOf(folder, relativePath, trailingSlash)
      const done = settled.get(key)
      if (done) return Promise.resolve(done)
      const pending = inflight.get(key)
      if (pending) return pending
      const active = controller
      const run = probeLocalFolderEntry(folder, relativePath, trailingSlash, active.signal).then((result) => {
        if (!active.signal.aborted) settled.set(key, result)
        if (inflight.get(key) === run) inflight.delete(key)
        return result
      })
      inflight.set(key, run)
      return run
    },
    activate: () => {
      if (controller.signal.aborted) controller = new AbortController()
    },
    abort: () => {
      controller.abort()
      inflight.clear()
    },
  }
}

export function useLocalEntryStatus(
  folder: ReplyFolderContext | undefined,
  relativePath: string,
  trailingSlash: boolean,
  streaming: boolean,
  store: LocalProbeStore | null,
): 'loading' | LocalEntryStatus {
  const key = folder && !streaming ? (store ? store.keyOf(folder, relativePath, trailingSlash) : `${folder.rawURL?.(relativePath) ?? ''}\0${relativePath}\0${trailingSlash ? '1' : '0'}`) : ''
  const cached = key && store ? store.peek(key) : undefined
  const [state, setState] = useState<{ key: string; status: LocalEntryStatus } | null>(null)

  useEffect(() => {
    if (!folder || streaming || !key || cached) return
    let active = true
    const controller = store ? undefined : new AbortController()
    const run = store
      ? store.probe(folder, relativePath, trailingSlash)
      : probeLocalFolderEntry(folder, relativePath, trailingSlash, controller!.signal)
    void run.then((status) => {
      if (active) setState({ key, status })
    })
    return () => {
      active = false
      controller?.abort()
    }
  }, [folder, relativePath, trailingSlash, streaming, store, key, cached])

  if (!folder) return { kind: 'unavailable' }
  if (streaming) return 'loading'
  if (cached) return cached
  if (state?.key !== key) return 'loading'
  return state.status
}
