import { zipSync } from 'fflate'
import type { PickedFile } from './upload'

// The relative path inside the directory: strip the outermost directory name so that SKILL.md sits at the archive root.
function innerPath(path: string): string {
  return path.includes('/') ? path.slice(path.indexOf('/') + 1) : path
}

// When unpacking a Skill, the server ignores the __MACOSX/ and .DS_Store that macOS generates (judged by the path inside the archive).
function ignoredBySkillServer(inner: string): boolean {
  return inner.startsWith('__MACOSX/') || inner.split('/').at(-1) === '.DS_Store'
}

// Drop the entries the server would ignore, leaving only the files that actually count toward the limits and go into the archive.
export function skillFiles(files: PickedFile[]): PickedFile[] {
  return files.filter(({ path }) => !ignoredBySkillServer(innerPath(path)))
}

// Pack the chosen Skill directory into a zip: strip the outermost directory name and leave out the entries the server would ignore.
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
