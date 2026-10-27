import { useInfiniteQuery, useQuery, type QueryClient } from '@tanstack/react-query'
import { decodeText, folderKeys, parentOf, type FolderEntry, type FolderSource, type TextFormat } from './folder-source'

// 一层的内容：展开或打开时才读取，按页读取。
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

// 文件内容：读取原始内容后由客户端判断是否文本。调用方先按大小决定要不要读。
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

// 最近的 Git 拉取（新的在前）：有排队或进行中的拉取时定时重新读取，刷新页面后状态仍在。
export function useGitPulls(source: FolderSource) {
  return useQuery({
    queryKey: folderKeys.git(source.key),
    queryFn: () => source.git!.list(),
    enabled: source.git !== undefined,
    refetchInterval: (query) => (query.state.data?.some((pull) => pull.status === 'queued' || pull.status === 'running') ? 1500 : false),
  })
}

// 改动后重新读取受影响的文件夹（只读这一级，不连带子文件夹）。
export function refreshDirs(client: QueryClient, source: FolderSource, ...paths: string[]) {
  return Promise.all([...new Set(paths)].map((path) => client.invalidateQueries({ queryKey: folderKeys.list(source.key, path), exact: true })))
}
export const refreshParent = (client: QueryClient, source: FolderSource, path: string) => refreshDirs(client, source, parentOf(path))

// 当前选中的一项：根目录没有 entry；文件带上列表里的信息（大小、修改时间、类型）。
export type Selection = { path: string; type: FolderEntry['type']; entry: FolderEntry | null }
