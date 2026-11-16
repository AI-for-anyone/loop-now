// A chosen file and its relative path. For directory uploads the path is webkitRelativePath, which includes the chosen directory's name.
export interface PickedFile {
  path: string
  file: File
}

export function pickedFiles(files: FileList | File[]): PickedFile[] {
  return Array.from(files).map((file) => ({ path: (file.webkitRelativePath || file.name).replace(/^\/+/, ''), file }))
}

// Run tasks in turn within the concurrency limit and return results in the same order as the input; one failed task does not affect the
// others.
export async function runPool<T, R>(items: T[], limit: number, task: (item: T) => Promise<R>): Promise<PromiseSettledResult<R>[]> {
  const results: PromiseSettledResult<R>[] = new Array(items.length)
  let next = 0
  const worker = async () => {
    while (next < items.length) {
      const index = next++
      try {
        results[index] = { status: 'fulfilled', value: await task(items[index]) }
      } catch (reason) {
        results[index] = { status: 'rejected', reason }
      }
    }
  }
  await Promise.all(Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, worker))
  return results
}

// Join a relative path under the target directory in the source the files are uploaded to.
export function joinPath(dir: string, path: string): string {
  const base = dir.replace(/^\/+|\/+$/g, '')
  return base ? `${base}/${path}` : path
}
