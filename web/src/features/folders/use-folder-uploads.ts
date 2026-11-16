import { useCallback, useState } from 'react'
import { config } from '@/config'
import { t } from '@/i18n'
import { ApiError } from '@/api/client'
import { describeError } from '@/lib/errors'
import { runPool } from '@/lib/upload'
import { joinPath, parentOf, type FolderSource, type UploadItem } from './folder-source'

export interface UploadRow {
  id: string
  // The full path relative to the folder root.
  path: string
  size: number
  loaded: number
  state: 'queued' | 'uploading' | 'done' | 'failed'
  error: string | null
}

// Upload a batch of files to a folder: upload them in turn up to the concurrency limit, recording each one's progress; one failure doesn't
// affect the others.
// When all are done, onDone receives the affected folders (the target folder and the subfolders the upload brought in).
// Files over maxUpload (Context.limits.maxFolderUpload) are not sent and are recorded as failed right away.
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

// Dropped files and folders: folders are read level by level via webkitGetAsEntry, and paths include subfolders.
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
