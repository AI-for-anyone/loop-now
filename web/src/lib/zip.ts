import { zipSync } from 'fflate'
import type { PickedFile } from './upload'

// 目录内的相对路径：去掉最外层的目录名，使 SKILL.md 位于压缩包根目录。
function innerPath(path: string): string {
  return path.includes('/') ? path.slice(path.indexOf('/') + 1) : path
}

// 服务端解压 Skill 时忽略 macOS 生成的 __MACOSX/ 与 .DS_Store（按压缩包内的路径判断）。
function ignoredBySkillServer(inner: string): boolean {
  return inner.startsWith('__MACOSX/') || inner.split('/').at(-1) === '.DS_Store'
}

// 去掉服务端会忽略的条目，只留下实际计入上限并放进压缩包的文件。
export function skillFiles(files: PickedFile[]): PickedFile[] {
  return files.filter(({ path }) => !ignoredBySkillServer(innerPath(path)))
}

// 把选择的 Skill 目录打包成 zip：去掉最外层的目录名，并排除服务端会忽略的条目。
export async function zipDirectory(files: PickedFile[]): Promise<Blob> {
  const entries: Record<string, Uint8Array> = {}
  for (const { path, file } of skillFiles(files)) {
    entries[innerPath(path)] = new Uint8Array(await file.arrayBuffer())
  }
  const zipped = zipSync(entries)
  return new Blob([zipped.buffer as ArrayBuffer], { type: 'application/zip' })
}

export function totalSize(files: PickedFile[]): number {
  return files.reduce((sum, { file }) => sum + file.size, 0)
}
