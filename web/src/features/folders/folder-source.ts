import type { Schemas } from '@/api/client'

// 文件管理器的数据来源：Agent 的文件夹、项目共享文件夹与全局共享文件夹各是一个 FolderSource，
// 分别对应 /agents/{agentId}/folder、/projects/{projectId}/context-folder 与 /context-folder 三组接口。
// 路径都相对文件夹根目录，用 / 分隔，根目录为空字符串。

export type FolderEntry = Schemas['FolderEntry']
export type FolderPage = Schemas['FolderEntryPage']
export type GitPull = Schemas['FolderGitPull']
export type GitPullInput = Omit<Schemas['FolderGitPullInput'], 'actionId'>

export interface UploadItem {
  // 相对上传目标文件夹的路径；上传文件夹时带子目录。
  path: string
  file: File
}

export interface FolderSource {
  // 查询缓存的命名空间，不同文件夹互不影响。
  key: string
  // 一层的内容：文件夹在前，再按名称；分页读取，不一次列出全部。
  list: (path: string, cursor: string | null) => Promise<FolderPage>
  // 文件的原始内容；是否文本由客户端判断。
  read: (path: string) => Promise<Blob>
  // 写入文件，父文件夹自动创建；create 为 true 时文件已存在返回冲突。
  write: (path: string, body: Blob | string, create: boolean) => Promise<FolderEntry>
  mkdir: (path: string) => Promise<FolderEntry>
  // 目标已存在时失败，不覆盖。
  move: (from: string, to: string) => Promise<FolderEntry>
  // 立即删除，不能恢复；文件夹连同其中的全部内容。
  remove: (path: string) => Promise<void>
  upload: (target: string, item: UploadItem, onProgress: (loaded: number) => void) => Promise<void>
  // 文件内容的地址：下载与图片预览用。
  contentURL: (path: string) => string
  // 只有 Agent 的文件夹能从 Git 拉取。list 为最近的拉取，新的在前。
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

// 每个文件夹根目录下的入口文件：Agent 每次启动 Pi 时先读它。
export const ENTRY_FILE = 'AGENTS.md'

export const joinPath = (dir: string, name: string) => (dir ? `${dir}/${name}` : name)
export const parentOf = (path: string) => (path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '')
export const nameOf = (path: string) => path.slice(path.lastIndexOf('/') + 1)
export const segmentsOf = (path: string) => (path ? path.split('/') : [])

// 软链接的目标按字面是否落在文件夹之外：绝对路径，或 .. 越过了根目录。服务端不跟随这样的链接。
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
// 从根目录到 path 的每一级上级文件夹（不含根目录与 path 本身），展开树时逐级打开。
export const ancestorsOf = (path: string) => segmentsOf(parentOf(path)).map((_, index, all) => all.slice(0, index + 1).join('/'))
// path 是否就是 dir 或在 dir 之下。
export const within = (path: string, dir: string) => path === dir || path.startsWith(`${dir}/`)

export const isEntryFile = (path: string) => path === ENTRY_FILE
export const extensionOf = (name: string) => (name.includes('.') && !name.startsWith('.') ? name.slice(name.lastIndexOf('.') + 1).toLowerCase() : '')

// 名称校验：不能为空，不能含 / 或 \，不能是 . 或 ..；合法时返回 null。
export function nameProblem(name: string): 'empty' | 'slash' | 'dots' | null {
  if (name.trim() === '') return 'empty'
  if (name.includes('/') || name.includes('\\')) return 'slash'
  if (name.trim() === '.' || name.trim() === '..') return 'dots'
  return null
}

// 移动目标文件夹的写法：相对根目录，去掉首尾的 /；含 . 或 .. 段时返回 null。
export function folderPath(value: string): string | null {
  const path = value.trim().replace(/^\/+|\/+$/g, '')
  return segmentsOf(path).some((part) => part === '.' || part === '..' || part === '') ? null : path
}

const LANGUAGES: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  json: 'json', yaml: 'yaml', yml: 'yaml', md: 'markdown', sh: 'bash', bash: 'bash', zsh: 'bash', py: 'python',
  css: 'css', html: 'xml', xml: 'xml', svg: 'xml', sql: 'sql', toml: 'ini', ini: 'ini', mod: 'go',
}
// 代码高亮用的语言；不认识的扩展名按纯文本显示。
export const languageOf = (path: string) => LANGUAGES[extensionOf(nameOf(path))] ?? null
export const isMarkdown = (path: string) => ['md', 'markdown'].includes(extensionOf(nameOf(path)))
export const isImage = (path: string) => ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(extensionOf(nameOf(path)))

// 在线编辑的条件（Context.limits.textEdit）：扩展名在名单里、大小不超过上限。没有扩展名的文本文件（如 Makefile）也按名单外处理。
export type TextEditLimits = Schemas['Limits']['textEdit']
export const canEdit = (path: string, size: number | null, limits: TextEditLimits | undefined) =>
  !!limits && size !== null && size <= limits.maxBytes && limits.extensions.includes(`.${extensionOf(nameOf(path))}`)

// 文本文件原有的格式：保存时照原样写回 UTF-8 BOM 与 CRLF 换行（文本框里一律是 LF）。
export interface TextFormat {
  bom: boolean
  crlf: boolean
}
export const PLAIN_TEXT: TextFormat = { bom: false, crlf: false }

// 读到的内容是否当作文本：前 8 KiB 里没有 NUL，且能按 UTF-8 解码；是文本时返回去掉 BOM、换行统一为 LF 的内容与原有格式。
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
