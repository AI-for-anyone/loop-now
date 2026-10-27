// 选择的文件及其相对路径。目录上传时路径取 webkitRelativePath，包含所选目录名。
export interface PickedFile {
  path: string
  file: File
}

export function pickedFiles(files: FileList | File[]): PickedFile[] {
  return Array.from(files).map((file) => ({ path: (file.webkitRelativePath || file.name).replace(/^\/+/, ''), file }))
}

// 按并发上限依次执行任务，返回与输入顺序一致的结果；一个任务失败不影响其他任务。
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

// 把相对路径拼到上传资料来源中的目标目录下。
export function joinPath(dir: string, path: string): string {
  const base = dir.replace(/^\/+|\/+$/g, '')
  return base ? `${base}/${path}` : path
}
