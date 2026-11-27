import { useEffect, useState } from 'react'
import { extensionOf, nameOf } from '@/features/folders/folder-source'
import { blobToImageUrl } from '@/features/images/blob-url'

// Default preview limit (20 MiB, matching LOOP_NOW_TEXT_EDIT_MAX on the server) until Context.limits loads.
export const DEFAULT_IMAGE_PREVIEW_MAX_BYTES = 20 << 20

export interface ReplyFolderContext {
  // Absolute path(s) of the Agent's (or task instance's) folder on this computer: FolderLocation.path and FolderLocation.realPath.
  roots: readonly string[]
  // True once the folder location has resolved (or was provided statically); while false, absolute paths wait before marking outside.
  rootsReady?: boolean
  // Folder content API URL for a validated folder-relative path (raw bytes, same checks as the Folder tab).
  contentURL: (relativePath: string) => string
  // Sandboxed raw file URL for opening one file in a new browser tab.
  rawURL?: (relativePath: string) => string
  // Console URL that opens a directory in the Agent's Folder tab; omitted for task node conversations, which have no Folder tab.
  folderLink?: (relativePath: string) => string
  // Check whether a folder-relative path is a directory; omitted when entries cannot be listed (e.g. task node folders).
  checkDirectory?: (relativePath: string, signal?: AbortSignal) => Promise<'dir' | 'not-dir' | 'missing'>
  // Maximum file size in bytes allowed for preview (Context.limits.textEdit.maxBytes).
  maxBytes: number
}

const MIME_BY_EXT: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
}

export function mimeTypeForImagePath(path: string): string | null {
  return MIME_BY_EXT[extensionOf(nameOf(path))] ?? null
}

export function isReplyImagePath(path: string): boolean {
  return mimeTypeForImagePath(path) !== null
}

// Resolve a folder-relative path's . and .. segments; returns 'root' for '.' / './', a POSIX relative path when inside, or null
// when empty, containing NUL, absolute, or escaping above the root.
export function normalizeRelativeOrRoot(raw: string): string | 'root' | null {
  if (!raw || raw.includes('\0')) return null
  const unified = raw.replace(/\\/g, '/')
  if (unified.startsWith('/') || /^[a-zA-Z]:/.test(unified)) return null
  const stack: string[] = []
  let sawDot = false
  for (const part of unified.split('/')) {
    if (part === '') continue
    if (part === '.') {
      sawDot = true
      continue
    }
    if (part === '..') {
      if (stack.length === 0) return null
      stack.pop()
      sawDot = true
    } else {
      stack.push(part)
    }
  }
  if (stack.length > 0) return stack.join('/')
  return sawDot ? 'root' : null
}

// Resolve a folder-relative path's . and .. segments; returns null when empty, root, containing NUL, or escaping above the root.
export function normalizeRelativePath(raw: string): string | null {
  const result = normalizeRelativeOrRoot(raw)
  return result && result !== 'root' ? result : null
}

export function decodePercent(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

// Unwrap a file:// URL into a filesystem path (handling file:///..., file://localhost/..., Windows file:///C:/..., and percent-encoding).
export function stripFileUrl(raw: string): string | null {
  const match = /^file:\/\/(?:localhost)?(\/.*)$/i.exec(raw)
  if (!match) return null
  let path = decodePercent(match[1]!.replace(/[?#].*$/, ''))
  if (/^\/[a-zA-Z]:\//.test(path)) path = path.slice(1)
  return path
}

interface NormalizedAbsolute {
  // Normalized path with / separators, no trailing slash, and . / .. resolved.
  normalized: string
  // Match key: same as normalized on POSIX; drive letter lowercased on Windows.
  key: string
}

function normalizeAbsoluteForMatch(raw: string): NormalizedAbsolute | null {
  if (!raw || raw.includes('\0')) return null
  const unified = raw.replace(/\\/g, '/')
  let prefix: string
  let keyPrefix: string
  let rest: string
  const win = /^([a-zA-Z]:)(\/.*)$/.exec(unified)
  if (win) {
    prefix = win[1]!
    keyPrefix = prefix.toLowerCase()
    rest = win[2]!
  } else if (unified.startsWith('/')) {
    prefix = ''
    keyPrefix = ''
    rest = unified
  } else {
    return null
  }
  const stack: string[] = []
  for (const part of rest.split('/')) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      stack.pop()
    } else {
      stack.push(part)
    }
  }
  if (stack.length === 0) return null
  const suffix = '/' + stack.join('/')
  return { normalized: prefix + suffix, key: keyPrefix + suffix }
}

// If raw is an absolute path (or file:// URL) inside or equal to one of roots, return its normalized folder-relative POSIX path
// (or 'root' when equal to a root itself); else null.
export function resolveAbsoluteOrRoot(raw: string, roots: readonly string[]): string | 'root' | null {
  const trimmed = raw.trim()
  if (!trimmed || roots.length === 0) return null
  const unwrapped = stripFileUrl(trimmed) ?? trimmed
  const candidate = normalizeAbsoluteForMatch(unwrapped)
  if (!candidate) return null

  const normalizedRoots = roots
    .map(normalizeAbsoluteForMatch)
    .filter((item): item is NormalizedAbsolute => item !== null)
    .sort((a, b) => b.key.length - a.key.length)

  for (const root of normalizedRoots) {
    if (candidate.key === root.key) return 'root'
    if (!candidate.key.startsWith(`${root.key}/`)) continue
    const rel = candidate.normalized.slice(root.normalized.length + 1)
    const clean = normalizeRelativePath(rel)
    if (clean) return clean
  }
  return null
}

// If raw is an absolute path (or file:// URL) strictly inside one of roots, return its normalized folder-relative POSIX path; else null.
export function resolveAbsoluteInsideRoots(raw: string, roots: readonly string[]): string | null {
  const result = resolveAbsoluteOrRoot(raw, roots)
  return result && result !== 'root' ? result : null
}

export function resolveAbsoluteLocalImage(raw: string, roots: readonly string[]): string | null {
  const rel = resolveAbsoluteInsideRoots(raw, roots)
  return rel && isReplyImagePath(rel) ? rel : null
}

export type MarkdownImageSource =
  | { kind: 'external' }
  | { kind: 'local'; relativePath: string }
  | { kind: 'blocked' }

// Decide how a Markdown image's src should be handled when rendering inside an Agent or task node reply:
// - http(s): / data: and other non-file URI schemes stay on task #10's normal path (urlTransform blocks javascript: etc.).
// - file://, absolute paths, and relative paths resolve only to image files inside the Agent's folder; outside paths or unsupported
//   extensions become 'blocked' so the browser never makes a request for them and no thumbnail is shown.
export function classifyMarkdownImageSource(rawSrc: string, roots: readonly string[]): MarkdownImageSource {
  const src = rawSrc.trim()
  if (!src) return { kind: 'external' }
  if (/^(?:https?:|data:)/i.test(src)) return { kind: 'external' }
  if (/^file:\/\//i.test(src)) {
    const rel = resolveAbsoluteLocalImage(src, roots)
    return rel ? { kind: 'local', relativePath: rel } : { kind: 'blocked' }
  }
  const windowsAbs = /^[a-zA-Z]:[/\\]/.test(src)
  if (!windowsAbs && /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(src)) return { kind: 'external' }

  const decoded = decodePercent(src)
  if (src.startsWith('/') || windowsAbs) {
    const rel = resolveAbsoluteLocalImage(decoded, roots) ?? resolveAbsoluteLocalImage(src, roots)
    return rel ? { kind: 'local', relativePath: rel } : { kind: 'blocked' }
  }

  const rel = normalizeRelativePath(decoded) ?? normalizeRelativePath(src)
  if (rel && isReplyImagePath(rel)) return { kind: 'local', relativePath: rel }
  return { kind: 'blocked' }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export function rootPattern(root: string): string | null {
  const norm = normalizeAbsoluteForMatch(root)
  if (!norm) return null
  const body = escapeRegExp(norm.normalized).replace(/\//g, '[/\\\\]').replace(/ /g, '(?: |%20)')
  if (/^[a-zA-Z]:/.test(norm.normalized)) {
    const drive = norm.normalized[0]!
    const driveClass = `[${drive.toLowerCase()}${drive.toUpperCase()}]`
    return `(?:file://(?:localhost)?/)?${driveClass}${body.slice(1)}`
  }
  return `(?:file://(?:localhost)?)?${body}`
}

export type BareTextPart =
  | { kind: 'text'; value: string }
  | { kind: 'image'; raw: string; relativePath: string }

// Full-width / CJK punctuation that terminates a bare path (matches remark-http-links.ts):
// comma, period, semicolon, colon, exclamation, question, ideographic comma, brackets, quotes.
export const CJK_PUNCT = '\\uFF0C\\u3002\\uFF1B\\uFF1A\\uFF01\\uFF1F\\u3001\\uFF08\\uFF09\\u3010\\u3011\\u300A\\u300B\\u201C\\u201D\\u2018\\u2019'

// Split a plain text node around bare absolute paths (or bare file:// paths) that point to supported image files inside roots.
// Matching by the known folder roots supports spaces in the root directory itself (e.g. "Application Support/Loop Now") while
// rejecting paths that sit outside the folder or continue into another extension (e.g. .png.bak).
export function splitBareLocalImagePaths(text: string, roots: readonly string[]): BareTextPart[] {
  if (!text || roots.length === 0) return [{ kind: 'text', value: text }]
  const patterns = [...new Set(
    [...roots]
      .sort((a, b) => b.length - a.length)
      .map(rootPattern)
      .filter((pattern): pattern is string => pattern !== null),
  )]
  if (patterns.length === 0) return [{ kind: 'text', value: text }]

  const regex = new RegExp(
    `(?:${patterns.join('|')})[/\\\\][^\\s"'\`<>()[\\]{}${CJK_PUNCT}]+?\\.(?:png|jpe?g|gif|webp|svg)(?=$|[\\s,;!?\\)\\]}'"\`<>${CJK_PUNCT}]|\\.(?![A-Za-z0-9_/-])|:(?![A-Za-z0-9_/-]))`,
    'gi',
  )

  const parts: BareTextPart[] = []
  let last = 0
  for (const match of text.matchAll(regex)) {
    const start = match.index
    const raw = match[0]!
    if (start > 0 && /[A-Za-z0-9_./\\-]/.test(text[start - 1]!)) continue
    const relativePath = resolveAbsoluteLocalImage(raw, roots)
    if (!relativePath) continue
    if (start > last) parts.push({ kind: 'text', value: text.slice(last, start) })
    parts.push({ kind: 'image', raw, relativePath })
    last = start + raw.length
  }
  if (parts.length === 0) return [{ kind: 'text', value: text }]
  if (last < text.length) parts.push({ kind: 'text', value: text.slice(last) })
  return parts
}

// Fetch image bytes through the folder content endpoint, enforcing maxBytes before buffering large files, and wrap them in a Blob URL
// with the image's MIME type (so SVG renders in <img> even though the folder endpoint sends application/octet-stream).
export async function loadLocalFolderImage(
  url: string,
  relativePath: string,
  maxBytes: number,
  signal?: AbortSignal,
): Promise<{ url: string; revoke: () => void } | null> {
  const mime = mimeTypeForImagePath(relativePath)
  if (!mime) return null
  let response: Response
  try {
    response = await fetch(url, { signal })
  } catch {
    return null
  }
  if (!response.ok) return null

  const headerLength = response.headers.get('content-length')
  if (headerLength !== null) {
    const size = Number(headerLength)
    if (Number.isFinite(size) && size > maxBytes) {
      void response.body?.cancel().catch(() => {})
      return null
    }
  }

  let blob: Blob
  if (response.body && typeof response.body.getReader === 'function') {
    const reader = response.body.getReader()
    const chunks: Uint8Array<ArrayBuffer>[] = []
    let total = 0
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        if (!value) continue
        total += value.byteLength
        if (total > maxBytes) {
          void reader.cancel().catch(() => {})
          return null
        }
        chunks.push(new Uint8Array(value))
      }
    } catch {
      return null
    }
    if (total === 0) return null
    blob = new Blob(chunks, { type: mime })
  } else {
    let buffer: ArrayBuffer
    try {
      buffer = await response.arrayBuffer()
    } catch {
      return null
    }
    if (buffer.byteLength === 0 || buffer.byteLength > maxBytes) return null
    blob = new Blob([buffer], { type: mime })
  }

  if (signal?.aborted) return null
  try {
    const result = await blobToImageUrl(blob)
    if (signal?.aborted) {
      result.revoke()
      return null
    }
    return result
  } catch {
    return null
  }
}

export function useLocalFolderImage(folder: ReplyFolderContext | undefined, relativePath: string) {
  const contentURL = folder ? folder.contentURL(relativePath) : null
  const maxBytes = folder?.maxBytes ?? DEFAULT_IMAGE_PREVIEW_MAX_BYTES
  const key = contentURL ? `${contentURL}\0${relativePath}\0${maxBytes}` : ''
  const [state, setState] = useState<{ key: string; status: 'ready' | 'error'; src: string | null } | null>(null)

  useEffect(() => {
    if (!contentURL || !key) return
    const controller = new AbortController()
    let revoke: (() => void) | null = null
    void loadLocalFolderImage(contentURL, relativePath, maxBytes, controller.signal).then((loaded) => {
      if (controller.signal.aborted) {
        loaded?.revoke()
        return
      }
      if (!loaded) {
        setState({ key, status: 'error', src: null })
        return
      }
      revoke = loaded.revoke
      setState({ key, status: 'ready', src: loaded.url })
    })
    return () => {
      controller.abort()
      revoke?.()
    }
  }, [contentURL, relativePath, maxBytes, key])

  if (!key) return { status: 'error' as const, src: null, contentURL: null }
  if (state?.key !== key) return { status: 'loading' as const, src: null, contentURL }
  return { status: state.status, src: state.src, contentURL }
}

export interface LoadedLocalImage {
  relativePath: string
  src: string
  contentURL: string
  alt: string
}

export function useLocalFolderImages(folder: ReplyFolderContext | undefined, paths: readonly string[]): LoadedLocalImage[] {
  const maxBytes = folder?.maxBytes ?? DEFAULT_IMAGE_PREVIEW_MAX_BYTES
  const entries = folder ? paths.map((relativePath) => ({ relativePath, contentURL: folder.contentURL(relativePath) })) : []
  const batchKey = entries.map((entry) => `${entry.relativePath}\0${entry.contentURL}`).join('\n') + `\n#${maxBytes}`
  const [loaded, setLoaded] = useState<{ batchKey: string; urls: Record<string, string> }>({ batchKey: '', urls: {} })

  useEffect(() => {
    if (!folder || paths.length === 0) return
    const controller = new AbortController()
    const revokers: (() => void)[] = []
    for (const relativePath of paths) {
      const contentURL = folder.contentURL(relativePath)
      void loadLocalFolderImage(contentURL, relativePath, maxBytes, controller.signal).then((result) => {
        if (controller.signal.aborted) {
          result?.revoke()
          return
        }
        if (!result) return
        revokers.push(result.revoke)
        setLoaded((prev) => ({
          batchKey,
          urls: prev.batchKey === batchKey ? { ...prev.urls, [relativePath]: result.url } : { [relativePath]: result.url },
        }))
      })
    }
    return () => {
      controller.abort()
      for (const revoke of revokers) revoke()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [batchKey])

  if (loaded.batchKey !== batchKey) return []
  return entries.flatMap((entry) => {
    const src = loaded.urls[entry.relativePath]
    return src ? [{ ...entry, src, alt: nameOf(entry.relativePath) }] : []
  })
}
