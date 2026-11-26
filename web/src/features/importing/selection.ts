import type { Schemas } from '@/api/client'

type Folder = Schemas['ImportFolder']
type Conversation = Schemas['ImportConversation']

// One import takes at most this many conversations, and this many bytes of their records, per folder (the server refuses the whole
// import past either); the newest are offered first.
export const MAX_PER_FOLDER = 100
export const MAX_BYTES_PER_FOLDER = 4 * 1024 ** 3

// The picked conversations of each folder, by folder path.
export type Selection = ReadonlyMap<string, ReadonlySet<string>>

// A conversation can be imported unless it already was (it links to its Agent instead) or the scan says why not.
export const importable = (conversation: Conversation) => conversation.agentId === null && conversation.skipReason === null

// The conversations of a folder that a check on the folder picks: the newest importable ones, up to the limits of one import.
export function pickable(folder: Folder): Conversation[] {
  const picked: Conversation[] = []
  let bytes = 0
  for (const conversation of folder.conversations.filter(importable)) {
    if (picked.length >= MAX_PER_FOLDER || bytes + conversation.sizeBytes > MAX_BYTES_PER_FOLDER) break
    picked.push(conversation)
    bytes += conversation.sizeBytes
  }
  return picked
}

// Whether one more conversation still fits in a folder's import.
export function fits(selection: Selection, folder: Folder, sessionId: string): boolean {
  const picked = selection.get(folder.path) ?? new Set<string>()
  const bytes = folder.conversations.filter((c) => picked.has(c.sessionId) || c.sessionId === sessionId).reduce((sum, c) => sum + c.sizeBytes, 0)
  return picked.size < MAX_PER_FOLDER && bytes <= MAX_BYTES_PER_FOLDER
}

// What the page starts with (T3): every suggested folder checked, with its importable conversations.
export function initialSelection(folders: Folder[]): Selection {
  return new Map(folders.filter((folder) => folder.preChecked && pickable(folder).length > 0).map((folder) => [folder.path, new Set(pickable(folder).map((c) => c.sessionId))]))
}

export function withFolder(selection: Selection, folder: Folder, checked: boolean): Selection {
  const next = new Map(selection)
  if (checked && pickable(folder).length > 0) next.set(folder.path, new Set(pickable(folder).map((c) => c.sessionId)))
  else next.delete(folder.path)
  return next
}

export function withConversation(selection: Selection, folder: Folder, sessionId: string, checked: boolean): Selection {
  const current = new Set(selection.get(folder.path) ?? [])
  if (checked) {
    if (!fits(selection, folder, sessionId)) return selection
    current.add(sessionId)
  } else current.delete(sessionId)
  const next = new Map(selection)
  if (current.size > 0) next.set(folder.path, current)
  else next.delete(folder.path)
  return next
}

// A folder's check: all of what a check would pick, some, or none.
export function folderCheck(selection: Selection, folder: Folder): boolean | 'indeterminate' {
  const picked = selection.get(folder.path)?.size ?? 0
  if (picked === 0) return false
  return picked >= pickable(folder).length ? true : 'indeterminate'
}

export function counts(selection: Selection): { folders: number; conversations: number } {
  let conversations = 0
  selection.forEach((ids) => (conversations += ids.size))
  return { folders: selection.size, conversations }
}

// The import request: each folder with something picked, its conversations newest first; projectId overrides where they go (a
// project's own entry imports into that project), null lets the server use the project already binding the folder, else a new one.
export function importBody(scanId: string, folders: Folder[], selection: Selection, projectId: string | null = null): Schemas['ConversationImportInput'] {
  return {
    scanId,
    folders: folders.flatMap((folder) => {
      const picked = selection.get(folder.path)
      if (!picked || picked.size === 0) return []
      const sessionIds = folder.conversations.filter((c) => picked.has(c.sessionId)).map((c) => c.sessionId).slice(0, MAX_PER_FOLDER)
      return [{ path: folder.path, projectId: projectId ?? folder.projectId, sessionIds }]
    }),
  }
}

// A git remote as owner/name (GitHub, GitLab and the like, over https or ssh); null for anything else.
export function repositoryName(remote: string | null): string | null {
  if (!remote) return null
  const match = /^(?:[a-z+]+:\/\/)?(?:[^@/]+@)?[^/:]+[:/]+(.+?)(?:\.git)?\/?$/i.exec(remote.trim())
  const parts = match?.[1].split('/').filter(Boolean) ?? []
  return parts.length >= 2 ? `${parts.at(-2)}/${parts.at(-1)}` : null
}

export const baseName = (path: string) => path.replace(/\/+$/, '').split('/').at(-1) || path

export interface FolderGroup {
  key: string
  // owner/name for a remote, else the main checkout's folder name; null for “Other folders”.
  name: string | null
  remote: string | null
  folders: Folder[]
}

// Git repositories first, grouped by remote (clones of one remote share a group), in the order the scan gives (newest first); folders
// outside git last, under “Other folders”.
export function groupFolders(folders: Folder[]): FolderGroup[] {
  const groups = new Map<string, FolderGroup>()
  const other: Folder[] = []
  for (const folder of folders) {
    if (!folder.git) {
      other.push(folder)
      continue
    }
    const remote = folder.repository?.remote ?? null
    const key = remote ? `remote:${repositoryName(remote) ?? remote}` : `path:${folder.repository?.mainCheckout ?? folder.path}`
    const group = groups.get(key) ?? { key, name: repositoryName(remote) ?? baseName(folder.repository?.mainCheckout ?? folder.path), remote, folders: [] }
    group.folders.push(folder)
    groups.set(key, group)
  }
  return [...groups.values(), ...(other.length > 0 ? [{ key: 'other', name: null, remote: null, folders: other }] : [])]
}
