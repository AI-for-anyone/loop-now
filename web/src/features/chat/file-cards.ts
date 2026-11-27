import { extensionOf, nameOf } from '@/features/folders/folder-source'
import { classifyInlineCodePath } from './local-files'
import { isReplyImagePath } from './local-images'

export type FileCardKind =
  | 'webpage'
  | 'pdf'
  | 'markdown'
  | 'text'
  | 'document'
  | 'table'
  | 'code'
  | 'archive'
  | 'file'

export type FileCardGlyph = 'code' | 'pdf' | 'doc' | 'table' | 'file'

export interface FileCardAppearance {
  kind: FileCardKind
  color: string
  glyph: FileCardGlyph
}

const WEBPAGE_EXTS = new Set(['html', 'htm'])
const MARKDOWN_EXTS = new Set(['md', 'markdown', 'mdx'])
const TEXT_EXTS = new Set(['txt', 'log'])
const DOCUMENT_EXTS = new Set(['doc', 'docx', 'rtf', 'tex', 'odt', 'pages', 'ppt', 'pptx', 'key'])
const TABLE_EXTS = new Set(['csv', 'tsv', 'xls', 'xlsx', 'ods', 'numbers'])
const ARCHIVE_EXTS = new Set(['zip', 'gz', 'tgz', 'tar', 'bz2', 'xz', '7z', 'rar', 'zst'])
const CODE_EXTS = new Set([
  'js', 'mjs', 'cjs', 'jsx', 'ts', 'tsx',
  'css', 'scss', 'sass', 'less',
  'json', 'jsonc', 'jsonl', 'xml', 'yaml', 'yml', 'toml', 'ini', 'cfg', 'conf', 'env', 'properties',
  'go', 'py', 'rs', 'rb', 'java', 'kt', 'swift', 'c', 'h', 'cc', 'cpp', 'hpp', 'cs', 'php',
  'sh', 'bash', 'zsh', 'ps1', 'bat', 'sql', 'graphql', 'gql', 'proto', 'diff', 'patch', 'wasm', 'map', 'lock',
])

export function classifyFileCardKind(path: string): FileCardKind {
  const ext = extensionOf(nameOf(path))
  if (!ext) return 'file'
  if (WEBPAGE_EXTS.has(ext)) return 'webpage'
  if (ext === 'pdf') return 'pdf'
  if (MARKDOWN_EXTS.has(ext)) return 'markdown'
  if (TEXT_EXTS.has(ext)) return 'text'
  if (DOCUMENT_EXTS.has(ext)) return 'document'
  if (TABLE_EXTS.has(ext)) return 'table'
  if (ARCHIVE_EXTS.has(ext)) return 'archive'
  if (CODE_EXTS.has(ext)) return 'code'
  return 'file'
}

const APPEARANCE_BY_KIND: Record<FileCardKind, { color: string; glyph: FileCardGlyph }> = {
  webpage: { color: '#7c3aed', glyph: 'code' },
  pdf: { color: '#ef4444', glyph: 'pdf' },
  markdown: { color: '#2563eb', glyph: 'doc' },
  text: { color: '#2563eb', glyph: 'doc' },
  document: { color: '#2563eb', glyph: 'doc' },
  table: { color: '#10a37f', glyph: 'table' },
  code: { color: '#475569', glyph: 'code' },
  archive: { color: '#64748b', glyph: 'file' },
  file: { color: '#64748b', glyph: 'file' },
}

export function fileCardAppearance(path: string): FileCardAppearance {
  const kind = classifyFileCardKind(path)
  return { kind, ...APPEARANCE_BY_KIND[kind] }
}

function formatDecimal(value: number): string {
  if (value >= 100) return String(Math.round(value))
  return value.toFixed(1).replace(/\.0$/, '')
}

// Human-friendly file sizes for reply file cards: B, KB (whole number), MB / GB (up to 1 decimal, dropping .0).
export function formatFileCardSize(bytes: number): string {
  const clamped = Number.isFinite(bytes) && bytes > 0 ? bytes : 0
  if (clamped < 1024) return `${Math.round(clamped)} B`
  const kb = Math.round(clamped / 1024)
  if (kb < 1024) return `${kb} KB`
  const mb = clamped / (1024 * 1024)
  if (Number(mb.toFixed(1)) < 1024) return `${formatDecimal(mb)} MB`
  const gb = clamped / (1024 * 1024 * 1024)
  return `${formatDecimal(gb)} GB`
}

export type FencedPathCandidate =
  | { kind: 'none' }
  | { kind: 'file'; relativePath: string; certain: boolean }

// Detect a fenced code block whose entire content is one path to a non-image file inside the Agent or task node folder.
// Multi-line blocks, shell commands/flags, mermaid diagrams, directories, reply images, outside paths, and URLs stay code blocks.
export function classifyFencedPath(
  rawText: string,
  roots: readonly string[],
  rootsReady = true,
  language?: string | null,
): FencedPathCandidate {
  if (language?.toLowerCase() === 'mermaid') return { kind: 'none' }
  const trimmed = rawText.trim()
  if (!trimmed || /[\r\n]/.test(trimmed)) return { kind: 'none' }
  if (/["'`<>|*{}();&=]/.test(trimmed)) return { kind: 'none' }
  if (!/^file:\/\//i.test(trimmed) && /\?/.test(trimmed)) return { kind: 'none' }

  const classified = classifyInlineCodePath(trimmed, roots, rootsReady)
  if (classified.kind !== 'local' || classified.trailingSlash) return { kind: 'none' }
  if (isReplyImagePath(classified.relativePath)) return { kind: 'none' }
  if (/\s--?[A-Za-z0-9]/.test(classified.relativePath)) return { kind: 'none' }

  return { kind: 'file', relativePath: classified.relativePath, certain: classified.certain }
}
