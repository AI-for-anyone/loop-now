import { useInfiniteQuery, useQuery, type QueryClient } from '@tanstack/react-query'
import { decodeText, folderKeys, parentOf, type FolderEntry, type FolderSource, type TextFormat } from './folder-source'

// The contents of one level: read only when expanded or opened, page by page.
export function useFolderList(source: FolderSource, path: string, enabled = true) {
  return useInfiniteQuery({
    queryKey: folderKeys.list(source.key, path),
    queryFn: ({ pageParam }) => source.list(path, pageParam),
    initialPageParam: null as string | null,
    getNextPageParam: (page) => page.nextCursor ?? undefined,
    enabled,
  })
}

export type FileText = { kind: 'text'; text: string; format: TextFormat } | { kind: 'binary' }

// File content: after reading the raw content, the client decides whether it is text. Callers first decide by size whether to read it.
export function useFolderFile(source: FolderSource, path: string, enabled = true) {
  return useQuery({
    queryKey: folderKeys.file(source.key, path),
    queryFn: async (): Promise<FileText> => {
      const decoded = decodeText(await (await source.read(path)).arrayBuffer())
      return decoded === null ? { kind: 'binary' } : { kind: 'text', ...decoded }
    },
    enabled,
  })
}

// Recent Git pulls (newest first): re-read periodically while any pull is queued or running; the status survives a page reload.
export function useGitPulls(source: FolderSource) {
  return useQuery({
    queryKey: folderKeys.git(source.key),
    queryFn: () => source.git!.list(),
    enabled: source.git !== undefined,
    refetchInterval: (query) => (query.state.data?.some((pull) => pull.status === 'queued' || pull.status === 'running') ? 1500 : false),
  })
}

// After a change, re-read the affected folders (only that level, not their subfolders).
export function refreshDirs(client: QueryClient, source: FolderSource, ...paths: string[]) {
  return Promise.all([...new Set(paths)].map((path) => client.invalidateQueries({ queryKey: folderKeys.list(source.key, path), exact: true })))
}
export const refreshParent = (client: QueryClient, source: FolderSource, path: string) => refreshDirs(client, source, parentOf(path))

// The currently selected item: the root has no entry; a file carries its info from the list (size, modified time, type).
export type Selection = { path: string; type: FolderEntry['type']; entry: FolderEntry | null }
