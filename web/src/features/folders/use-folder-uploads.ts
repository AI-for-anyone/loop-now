import { useCallback, useState } from 'react'
import { config } from '@/config'
import { t } from '@/i18n'
import { ApiError } from '@/api/client'
import { describeError } from '@/lib/errors'
import { runPool } from '@/lib/upload'
import { joinPath, parentOf, type FolderSource, type UploadItem } from './folder-source'

export interface UploadRow {
  id: string
  // 相对文件夹根目录的完整路径。
  path: string
  size: number
  loaded: number
  state: 'queued' | 'uploading' | 'done' | 'failed'
  error: string | null
}

// 上传一批文件到某个文件夹：按并发上限依次上传，逐个记下进度；一个失败不影响其他。
// 全部结束后 onDone 收到受影响的文件夹（目标文件夹及上传带出的子文件夹）。
// 超过 maxUpload（Context.limits.maxFolderUpload）的文件不发出，直接记为失败。
export function useFolderUploads(source: FolderSource, maxUpload: number | undefined, onDone: (dirs: string[]) => void) {
  const [rows, setRows] = useState<UploadRow[]>([])
  const patch = (id: string, value: Partial<UploadRow>) => setRows((old) => old.map((row) => (row.id === id ? { ...row, ...value } : row)))
  const start = useCallback(async (target: string, items: UploadItem[]) => {
    const batch = items.map((item) => ({ item, row: { id: crypto.randomUUID(), path: joinPath(target, item.path), size: item.file.size, loaded: 0, state: 'queued' as const, error: null } }))
    setRows((old) => [...old.filter((row) => row.state === 'uploading' || row.state === 'queued'), ...batch.map((entry) => entry.row)])
    await runPool(batch, config.uploadConcurrency, async ({ item, row }) => {
      if (maxUpload !== undefined && item.file.size > maxUpload) { patch(row.id, { state: 'failed', error: t.folders.uploads.tooLarge(maxUpload) }); return }
      patch(row.id, { state: 'uploading' })
      try {
        await source.upload(target, item, (loaded) => patch(row.id, { loaded }))
        patch(row.id, { state: 'done', loaded: row.size })
      } catch (error) {
        patch(row.id, { state: 'failed', error: error instanceof ApiError ? describeError(error) : error instanceof Error ? error.message : String(error) })
      }
    })
    onDone([target, ...batch.map((entry) => parentOf(entry.row.path))])
  }, [source, maxUpload, onDone])
  const clear = useCallback(() => setRows((old) => old.filter((row) => row.state === 'uploading' || row.state === 'queued')), [])
  return { rows, start, clear }
}

// 拖入的文件与文件夹：文件夹按 webkitGetAsEntry 逐级读出，路径带子目录。
export async function droppedItems(transfer: DataTransfer): Promise<UploadItem[]> {
  const entries = Array.from(transfer.items).map((item) => item.webkitGetAsEntry?.()).filter((entry): entry is FileSystemEntry => !!entry)
  if (entries.length === 0) return Array.from(transfer.files).map((file) => ({ path: file.name, file }))
  const out: UploadItem[] = []
  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject))
      out.push({ path: prefix + entry.name, file })
      return
    }
    const reader = (entry as FileSystemDirectoryEntry).createReader()
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject))
      if (batch.length === 0) break
      for (const child of batch) await walk(child, `${prefix}${entry.name}/`)
    }
  }
  for (const entry of entries) await walk(entry, '')
  return out
}
